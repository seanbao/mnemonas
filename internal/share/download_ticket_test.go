package share

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/storage"
)

var fixedDownloadTicketTestKey = bytes.Repeat([]byte{0x5a}, downloadTicketKeySize)

const fixedDownloadTicketClientNonce = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func downloadTicketBodyForTest(body string) []byte {
	return downloadTicketBodyWithNonceForTest(body, fixedDownloadTicketClientNonce)
}

func downloadTicketBodyWithNonceForTest(body, nonce string) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		panic(err)
	}
	encodedNonce, err := json.Marshal(nonce)
	if err != nil {
		panic(err)
	}
	fields["client_nonce"] = encodedNonce
	encoded, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return encoded
}

type downloadTicketTestIssue struct {
	response DownloadTicketResponse
	cookie   *http.Cookie
	all      []*http.Cookie
}

type errorReader struct {
	err error
}

type mutableDownloadTicketOwnerStore struct {
	user *auth.User
}

func (s *mutableDownloadTicketOwnerStore) GetByID(id string) (*auth.User, error) {
	if s == nil || s.user == nil || s.user.ID != id {
		return nil, auth.ErrUserNotFound
	}
	return cloneDownloadTicketOwner(s.user), nil
}

func (s *mutableDownloadTicketOwnerStore) GetByUsername(username string) (*auth.User, error) {
	if s == nil || s.user == nil || s.user.Username != username {
		return nil, auth.ErrUserNotFound
	}
	return cloneDownloadTicketOwner(s.user), nil
}

func cloneDownloadTicketOwner(owner *auth.User) *auth.User {
	if owner == nil {
		return nil
	}
	cloned := *owner
	cloned.Groups = append([]string(nil), owner.Groups...)
	return &cloned
}

type fullWriteErrorResponseWriter struct {
	header http.Header
	err    error
}

func (w *fullWriteErrorResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*fullWriteErrorResponseWriter) WriteHeader(int) {}

func (w *fullWriteErrorResponseWriter) Write(payload []byte) (int, error) {
	return len(payload), w.err
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func newDownloadTicketTestHandler(t *testing.T, maxAccess int64) (*ShareStore, *Share, *Handler, *fakeShareFS) {
	t.Helper()
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "owner-1",
		MaxAccess: maxAccess,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	fs := &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			share.Path: {Path: share.Path, Name: "report.pdf", Size: 6},
		},
		openByPath: map[string]FileReader{
			share.Path: &readSeekCloser{Reader: bytes.NewReader([]byte("abcdef"))},
		},
	}
	handler := NewHandler(store, fs)
	if err := handler.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatalf("SetDownloadTicketSigningKey() error: %v", err)
	}
	return store, share, handler, fs
}

func issueDownloadTicketForTest(t *testing.T, handler *Handler, shareID, routePrefix, body string) downloadTicketTestIssue {
	return issueDownloadTicketWithNonceForTest(t, handler, shareID, routePrefix, body, fixedDownloadTicketClientNonce)
}

func issueDownloadTicketWithNonceForTest(t *testing.T, handler *Handler, shareID, routePrefix, body, nonce string) downloadTicketTestIssue {
	t.Helper()
	route := routePrefix + shareID + "/download-ticket"
	request := newRouteRequest(http.MethodPost, route, shareID, downloadTicketBodyWithNonceForTest(body, nonce))
	recorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("CreateDownloadTicket() status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), `"success"`) {
		t.Fatalf("ticket response must be raw JSON, got %s", recorder.Body.String())
	}
	var response DownloadTicketResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode ticket response: %v", err)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(response.Ticket)
	if err != nil || len(sealed) != downloadTicketGrantSize || len(response.Ticket) > 256 {
		t.Fatalf("ticket length/encoding = %d/%d, err=%v", len(response.Ticket), len(sealed), err)
	}
	if _, err := time.Parse(time.RFC3339, response.ExpiresAt); err != nil {
		t.Fatalf("expires_at = %q, want RFC3339: %v", response.ExpiresAt, err)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("ticket cookie count = %d, want 1", len(cookies))
	}
	return downloadTicketTestIssue{response: response, cookie: cookies[0], all: cookies}
}

func downloadRequestWithTicket(shareID, requestPath string, issue downloadTicketTestIssue) *http.Request {
	separator := "?"
	if strings.Contains(requestPath, "?") {
		separator = "&"
	}
	request := newRouteRequest(http.MethodGet, requestPath+separator+"ticket="+issue.response.Ticket, shareID, nil)
	request.AddCookie(issue.cookie)
	return request
}

func responseErrorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error payload: %#v", payload)
	}
	code, _ := errorPayload["code"].(string)
	return code
}

func TestCreateDownloadTicket_RawResponseAndStableBinderCookie(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 1)
	request := newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`))
	request.TLS = &tls.ConnectionState{}
	recorder := httptest.NewRecorder()

	handler.CreateDownloadTicket(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response DownloadTicketResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Ticket == "" || response.ExpiresAt == "" {
		t.Fatalf("response = %#v, want ticket and expires_at", response)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if !strings.HasPrefix(cookie.Name, downloadTicketSecureCookiePrefix) || len(strings.TrimPrefix(cookie.Name, downloadTicketSecureCookiePrefix)) != downloadTicketBinderIDSize*2 || cookie.Path != downloadTicketCookiePath {
		t.Fatalf("binder cookie name/path = %q/%q", cookie.Name, cookie.Path)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie security attributes = %#v", cookie)
	}
	binder, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(binder) != downloadTicketBindingSize {
		t.Fatalf("cookie binder length = %d, err=%v", len(binder), err)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(response.Ticket)
	if err != nil || len(sealed) != downloadTicketGrantSize || len(response.Ticket) > 256 {
		t.Fatalf("signed ticket length = %d/%d, err=%v", len(response.Ticket), len(sealed), err)
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("access_count = %d, want 1", current.AccessCount)
	}
}

func TestCreateDownloadTicket_BinderCookieMaxAgeUsesResponseTime(t *testing.T) {
	_, share, handler, _ := newDownloadTicketTestHandler(t, 0)
	issuedAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	responseTime := issuedAt.Add(10 * time.Minute)
	clockCalls := 0
	handler.downloadTicketNow = func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			return issuedAt
		}
		return responseTime
	}
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	wantExpires := issuedAt.Add(defaultDownloadTicketTTL)
	if !issue.cookie.Expires.Equal(wantExpires) {
		t.Fatalf("cookie Expires = %v, want %v", issue.cookie.Expires, wantExpires)
	}
	wantMaxAge := int(wantExpires.Sub(responseTime) / time.Second)
	if issue.cookie.MaxAge != wantMaxAge {
		t.Fatalf("cookie MaxAge = %d, want %d", issue.cookie.MaxAge, wantMaxAge)
	}
}

func TestDownloadTicket_RejectsForgedOrDuplicateBinderCookies(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 0)
	valid := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	downloadRequest := downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", valid)
	downloadRequest.AddCookie(valid.cookie)
	downloadRecorder := httptest.NewRecorder()
	handler.DownloadShare(downloadRecorder, downloadRequest)
	if downloadRecorder.Code != http.StatusUnauthorized || responseErrorCode(t, downloadRecorder) != "DOWNLOAD_TICKET_INVALID" {
		t.Fatalf("duplicate binder download status/body = %d/%s", downloadRecorder.Code, downloadRecorder.Body.String())
	}
	forged := *valid.cookie
	forged.Value = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, downloadTicketBindingSize))
	forgedRequest := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?ticket="+valid.response.Ticket, share.ID, nil)
	forgedRequest.AddCookie(&forged)
	forgedRecorder := httptest.NewRecorder()
	handler.DownloadShare(forgedRecorder, forgedRequest)
	if forgedRecorder.Code != http.StatusUnauthorized || responseErrorCode(t, forgedRecorder) != "DOWNLOAD_TICKET_INVALID" {
		t.Fatalf("forged binder download status/body = %d/%s", forgedRecorder.Code, forgedRecorder.Body.String())
	}
	current, _ := store.Get(share.ID)
	if current.AccessCount != 1 {
		t.Fatalf("access_count = %d, want only the valid ticket reservation", current.AccessCount)
	}
}

func TestCreateDownloadTicket_ReusesStableBinderForSameClientNonce(t *testing.T) {
	store, share, handler, fs := newDownloadTicketTestHandler(t, 0)
	first := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)

	request := newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`))
	request.AddCookie(first.cookie)
	recorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second ticket status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	var secondResponse DownloadTicketResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &secondResponse); err != nil {
		t.Fatal(err)
	}
	secondCookies := recorder.Result().Cookies()
	if len(secondCookies) != 1 || !strings.HasPrefix(secondCookies[0].Name, downloadTicketCookiePrefix) {
		t.Fatalf("second binder cookies = %#v", secondCookies)
	}
	if secondCookies[0].Name != first.cookie.Name || secondCookies[0].Value != first.cookie.Value {
		t.Fatalf("stable binder changed from %q/%q to %q/%q", first.cookie.Name, first.cookie.Value, secondCookies[0].Name, secondCookies[0].Value)
	}
	if secondResponse.Ticket == first.response.Ticket {
		t.Fatal("parallel ticket reused signed grant")
	}

	second := downloadTicketTestIssue{response: secondResponse, cookie: secondCookies[0]}
	for _, issue := range []downloadTicketTestIssue{first, second} {
		fs.openByPath[share.Path] = &readSeekCloser{Reader: bytes.NewReader([]byte("abcdef"))}
		downloadRecorder := httptest.NewRecorder()
		handler.DownloadShare(downloadRecorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
		if downloadRecorder.Code != http.StatusOK || downloadRecorder.Body.String() != "abcdef" {
			t.Fatalf("stable-binder ticket download = %d/%q", downloadRecorder.Code, downloadRecorder.Body.String())
		}
	}
	current, _ := store.Get(share.ID)
	if current.AccessCount != 2 {
		t.Fatalf("access_count = %d, want 2", current.AccessCount)
	}
}

func TestCreateDownloadTicket_ConcurrentFirstTicketsRemainUsable(t *testing.T) {
	store, share, handler, fs := newDownloadTicketTestHandler(t, 0)
	const requests = defaultDownloadTicketConcurrent
	type result struct {
		response DownloadTicketResponse
		cookie   *http.Cookie
		status   int
		body     string
	}
	results := make(chan result, requests)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < requests; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			recorder := httptest.NewRecorder()
			handler.CreateDownloadTicket(recorder, newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`)))
			issued := result{status: recorder.Code, body: recorder.Body.String()}
			if recorder.Code == http.StatusOK {
				_ = json.Unmarshal(recorder.Body.Bytes(), &issued.response)
				cookies := recorder.Result().Cookies()
				if len(cookies) == 1 {
					issued.cookie = cookies[0]
				}
			}
			results <- issued
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	issues := make([]downloadTicketTestIssue, 0, requests)
	seenTickets := make(map[string]struct{}, requests)
	var stableCookie *http.Cookie
	for issued := range results {
		if issued.status != http.StatusOK || issued.response.Ticket == "" || issued.cookie == nil {
			t.Fatalf("concurrent issue = status %d body %s cookie %#v", issued.status, issued.body, issued.cookie)
		}
		if _, exists := seenTickets[issued.response.Ticket]; exists {
			t.Fatal("concurrent issuance reused a signed ticket")
		}
		seenTickets[issued.response.Ticket] = struct{}{}
		if stableCookie == nil {
			stableCookie = issued.cookie
		} else if issued.cookie.Name != stableCookie.Name || issued.cookie.Value != stableCookie.Value {
			t.Fatalf("same nonce produced different concurrent binders: %q/%q and %q/%q", stableCookie.Name, stableCookie.Value, issued.cookie.Name, issued.cookie.Value)
		}
		issues = append(issues, downloadTicketTestIssue{response: issued.response, cookie: issued.cookie})
	}
	for _, issue := range issues {
		fs.openByPath[share.Path] = &readSeekCloser{Reader: bytes.NewReader([]byte("abcdef"))}
		recorder := httptest.NewRecorder()
		handler.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
		if recorder.Code != http.StatusOK || recorder.Body.String() != "abcdef" {
			t.Fatalf("concurrent ticket download = %d/%q", recorder.Code, recorder.Body.String())
		}
	}
	current, _ := store.Get(share.ID)
	if current.AccessCount != requests {
		t.Fatalf("access_count = %d, want %d", current.AccessCount, requests)
	}
}

func TestCreateDownloadTicket_DifferentClientNoncesRemainIndependentAndUsable(t *testing.T) {
	store, share, handler, fs := newDownloadTicketTestHandler(t, 0)
	secondNonce := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, downloadTicketClientNonceSize))
	first := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	second := issueDownloadTicketWithNonceForTest(t, handler, share.ID, "/s/", `{}`, secondNonce)
	if first.cookie.Name == second.cookie.Name || first.cookie.Value == second.cookie.Value {
		t.Fatalf("different nonces shared binder name/value: %q/%q and %q/%q", first.cookie.Name, first.cookie.Value, second.cookie.Name, second.cookie.Value)
	}
	for _, issue := range []downloadTicketTestIssue{first, second} {
		fs.openByPath[share.Path] = &readSeekCloser{Reader: bytes.NewReader([]byte("abcdef"))}
		recorder := httptest.NewRecorder()
		handler.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
		if recorder.Code != http.StatusOK || recorder.Body.String() != "abcdef" {
			t.Fatalf("different-nonce ticket download = %d/%q", recorder.Code, recorder.Body.String())
		}
	}
	current, _ := store.Get(share.ID)
	if current.AccessCount != 2 {
		t.Fatalf("access_count = %d, want 2", current.AccessCount)
	}
}

func TestCreateDownloadTicket_BinderDerivationIsShareScoped(t *testing.T) {
	store, shareA, handler, fs := newDownloadTicketTestHandler(t, 0)
	shareB, err := store.Create(CreateShareOptions{
		Path:      "/docs/other.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "owner-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	fs.statInfoByPath[shareB.Path] = &storage.FileInfo{Path: shareB.Path, Name: "other.pdf", Size: 5}
	fs.openByPath[shareB.Path] = &readSeekCloser{Reader: bytes.NewReader([]byte("other"))}
	issueA := issueDownloadTicketForTest(t, handler, shareA.ID, "/s/", `{}`)
	issueB := issueDownloadTicketForTest(t, handler, shareB.ID, "/s/", `{}`)
	if issueA.cookie.Name == issueB.cookie.Name || issueA.cookie.Value == issueB.cookie.Value {
		t.Fatalf("same nonce was not share-scoped: %q/%q and %q/%q", issueA.cookie.Name, issueA.cookie.Value, issueB.cookie.Name, issueB.cookie.Value)
	}

	forged := newRouteRequest(http.MethodGet, "/s/"+shareB.ID+"/download?ticket="+issueB.response.Ticket, shareB.ID, nil)
	forged.AddCookie(issueA.cookie)
	forgedRecorder := httptest.NewRecorder()
	handler.DownloadShare(forgedRecorder, forged)
	if forgedRecorder.Code != http.StatusUnauthorized || responseErrorCode(t, forgedRecorder) != "DOWNLOAD_TICKET_INVALID" {
		t.Fatalf("cross-share binder status/body = %d/%s", forgedRecorder.Code, forgedRecorder.Body.String())
	}

	validRecorder := httptest.NewRecorder()
	handler.DownloadShare(validRecorder, downloadRequestWithTicket(shareB.ID, "/s/"+shareB.ID+"/download", issueB))
	if validRecorder.Code != http.StatusOK || validRecorder.Body.String() != "other" {
		t.Fatalf("share-scoped valid ticket = %d/%q", validRecorder.Code, validRecorder.Body.String())
	}
}

func TestCreateDownloadTicket_CookieAndIssuanceLimitsRejectBeforeFilesystemIO(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*Handler, *Share, *http.Request)
	}{
		{
			name: "cookie soft bound",
			setup: func(_ *Handler, _ *Share, request *http.Request) {
				for index := 0; index < maxDownloadTicketCookies; index++ {
					var id [downloadTicketBinderIDSize]byte
					binary.BigEndian.PutUint64(id[downloadTicketBinderIDSize-8:], uint64(index+1))
					request.AddCookie(&http.Cookie{Name: downloadTicketCookiePrefix + hex.EncodeToString(id[:]), Value: "malformed"})
				}
			},
		},
		{
			name: "https counts http and secure cookie namespaces",
			setup: func(_ *Handler, _ *Share, request *http.Request) {
				request.TLS = &tls.ConnectionState{}
				for index := 0; index < maxDownloadTicketCookies; index++ {
					var id [downloadTicketBinderIDSize]byte
					binary.BigEndian.PutUint64(id[downloadTicketBinderIDSize-8:], uint64(index+1))
					prefix := downloadTicketCookiePrefix
					if index >= maxDownloadTicketCookies/2 {
						prefix = downloadTicketSecureCookiePrefix
					}
					request.AddCookie(&http.Cookie{Name: prefix + hex.EncodeToString(id[:]), Value: "malformed"})
				}
			},
		},
		{
			name: "existing target binder value is invalid",
			setup: func(handler *Handler, share *Share, request *http.Request) {
				nonce, err := decodeDownloadTicketClientNonce(json.RawMessage(`"` + fixedDownloadTicketClientNonce + `"`))
				if err != nil {
					panic(err)
				}
				binderID, _, err := deriveDownloadTicketBinder(handler.currentDownloadTicketKey(), share.ID, nonce)
				if err != nil {
					panic(err)
				}
				request.AddCookie(&http.Cookie{Name: downloadTicketBinderCookieName(request, binderID), Value: "malformed"})
			},
		},
		{
			name: "existing target binder is duplicated",
			setup: func(handler *Handler, share *Share, request *http.Request) {
				nonce, err := decodeDownloadTicketClientNonce(json.RawMessage(`"` + fixedDownloadTicketClientNonce + `"`))
				if err != nil {
					panic(err)
				}
				binderID, binding, err := deriveDownloadTicketBinder(handler.currentDownloadTicketKey(), share.ID, nonce)
				if err != nil {
					panic(err)
				}
				cookie := &http.Cookie{Name: downloadTicketBinderCookieName(request, binderID), Value: base64.RawURLEncoding.EncodeToString(binding[:])}
				request.AddCookie(cookie)
				request.AddCookie(cookie)
			},
		},
		{
			name: "issuance concurrency",
			setup: func(handler *Handler, _ *Share, _ *http.Request) {
				for index := 0; index < cap(handler.downloadTicketGate); index++ {
					handler.downloadTicketGate <- struct{}{}
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, share, handler, fs := newDownloadTicketTestHandler(t, 0)
			handler.downloadTicketRandom = errorReader{err: errors.New("random must not be read")}
			statCalls := 0
			fs.beforeStat = func(string) error {
				statCalls++
				return nil
			}
			request := newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`))
			test.setup(handler, share, request)
			t.Cleanup(func() {
				for len(handler.downloadTicketGate) > 0 {
					<-handler.downloadTicketGate
				}
			})
			recorder := httptest.NewRecorder()
			handler.CreateDownloadTicket(recorder, request)
			if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "1" || responseErrorCode(t, recorder) != "DOWNLOAD_TICKET_RATE_LIMITED" {
				t.Fatalf("status/headers/body = %d/%v/%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			if statCalls != 0 {
				t.Fatalf("filesystem calls = %d, want 0", statCalls)
			}
			current, _ := store.Get(share.ID)
			if current.AccessCount != 0 {
				t.Fatalf("access_count = %d, want 0", current.AccessCount)
			}
		})
	}
}

func TestCreateDownloadTicket_FullCookieSetRefreshesValidBinderButRejectsNewNonce(t *testing.T) {
	store, share, handler, fs := newDownloadTicketTestHandler(t, 0)
	first := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	secondNonce := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, downloadTicketClientNonceSize))
	decodedSecond, err := decodeDownloadTicketClientNonce(json.RawMessage(`"` + secondNonce + `"`))
	if err != nil {
		t.Fatal(err)
	}
	secondBinderID, _, err := deriveDownloadTicketBinder(handler.currentDownloadTicketKey(), share.ID, decodedSecond)
	if err != nil {
		t.Fatal(err)
	}
	secondCookieName := downloadTicketBinderCookieName(httptest.NewRequest(http.MethodPost, "/", nil), secondBinderID)

	cookies := []*http.Cookie{first.cookie}
	for index := uint64(1); len(cookies) < maxDownloadTicketCookies; index++ {
		var id [downloadTicketBinderIDSize]byte
		binary.BigEndian.PutUint64(id[downloadTicketBinderIDSize-8:], index)
		name := downloadTicketCookiePrefix + hex.EncodeToString(id[:])
		if name == first.cookie.Name || name == secondCookieName {
			continue
		}
		cookies = append(cookies, &http.Cookie{Name: name, Value: "malformed"})
	}

	statCalls := 0
	fs.beforeStat = func(string) error {
		statCalls++
		return nil
	}
	refreshRequest := newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`))
	for _, cookie := range cookies {
		refreshRequest.AddCookie(cookie)
	}
	refreshRecorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(refreshRecorder, refreshRequest)
	if refreshRecorder.Code != http.StatusOK {
		t.Fatalf("valid full-set refresh status/body = %d/%s", refreshRecorder.Code, refreshRecorder.Body.String())
	}
	if statCalls == 0 {
		t.Fatal("valid full-set refresh did not reach filesystem preflight")
	}
	refreshedCookies := refreshRecorder.Result().Cookies()
	if len(refreshedCookies) != 1 || refreshedCookies[0].Name != first.cookie.Name || refreshedCookies[0].Value != first.cookie.Value {
		t.Fatalf("refreshed binder = %#v, want same name/value as %#v", refreshedCookies, first.cookie)
	}

	statCalls = 0
	newNonceRequest := newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyWithNonceForTest(`{}`, secondNonce))
	for _, cookie := range cookies {
		newNonceRequest.AddCookie(cookie)
	}
	newNonceRecorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(newNonceRecorder, newNonceRequest)
	if newNonceRecorder.Code != http.StatusTooManyRequests || responseErrorCode(t, newNonceRecorder) != "DOWNLOAD_TICKET_RATE_LIMITED" {
		t.Fatalf("new nonce at capacity status/body = %d/%s", newNonceRecorder.Code, newNonceRecorder.Body.String())
	}
	if statCalls != 0 {
		t.Fatalf("new nonce filesystem calls = %d, want 0", statCalls)
	}
	current, _ := store.Get(share.ID)
	if current.AccessCount != 2 {
		t.Fatalf("access_count = %d, want two successful issuances", current.AccessCount)
	}
}

func TestPublicArchiveGateRejectsTicketPreflightAndDownloadWithoutFilesystemIO(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	folder, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "owner-1"})
	if err != nil {
		t.Fatal(err)
	}
	statCalls := 0
	fs := &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{"/docs": {Path: "/docs", Name: "docs", IsDir: true, Mode: os.ModeDir}},
		dirItemsByPath: map[string][]*storage.FileInfo{"/docs": {}},
		beforeStat: func(string) error {
			statCalls++
			return nil
		},
	}
	handler := NewHandler(store, fs)
	if err := handler.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatal(err)
	}

	issue := issueDownloadTicketForTest(t, handler, folder.ID, "/s/", `{"archive":"zip"}`)
	statCalls = 0
	for index := 0; index < cap(handler.publicArchiveGate); index++ {
		handler.publicArchiveGate <- struct{}{}
	}
	t.Cleanup(func() {
		for len(handler.publicArchiveGate) > 0 {
			<-handler.publicArchiveGate
		}
	})

	ticketRecorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(ticketRecorder, newRouteRequest(http.MethodPost, "/s/"+folder.ID+"/download-ticket", folder.ID, downloadTicketBodyForTest(`{"archive":"zip"}`)))
	if ticketRecorder.Code != http.StatusTooManyRequests || ticketRecorder.Header().Get("Retry-After") != "1" || responseErrorCode(t, ticketRecorder) != "DOWNLOAD_TICKET_RATE_LIMITED" {
		t.Fatalf("preflight gate status/headers/body = %d/%v/%s", ticketRecorder.Code, ticketRecorder.Header(), ticketRecorder.Body.String())
	}
	if statCalls != 0 {
		t.Fatalf("preflight filesystem calls = %d, want 0", statCalls)
	}

	downloadRecorder := httptest.NewRecorder()
	handler.DownloadShare(downloadRecorder, downloadRequestWithTicket(folder.ID, "/s/"+folder.ID+"/download?archive=zip", issue))
	if downloadRecorder.Code != http.StatusTooManyRequests || downloadRecorder.Header().Get("Retry-After") != "1" || responseErrorCode(t, downloadRecorder) != "DOWNLOAD_TICKET_RATE_LIMITED" {
		t.Fatalf("download gate status/headers/body = %d/%v/%s", downloadRecorder.Code, downloadRecorder.Header(), downloadRecorder.Body.String())
	}
	if statCalls != 0 {
		t.Fatalf("download filesystem calls = %d, want 0", statCalls)
	}
}

func TestCreateDownloadTicket_ArchiveUsesBoundedDirectoryRead(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	folder, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "owner-1"})
	if err != nil {
		t.Fatal(err)
	}
	children := make([]*storage.FileInfo, maxShareArchiveEntries+1)
	for index := range children {
		name := fmt.Sprintf("file-%05d.bin", index)
		children[index] = &storage.FileInfo{Path: path.Join("/docs", name), Name: name, Mode: 0}
	}
	fs := &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{"/docs": {Path: "/docs", Name: "docs", IsDir: true, Mode: os.ModeDir}},
		dirItemsByPath: map[string][]*storage.FileInfo{"/docs": children},
	}
	handler := NewHandler(store, fs)
	if err := handler.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(recorder, newRouteRequest(http.MethodPost, "/s/"+folder.ID+"/download-ticket", folder.ID, downloadTicketBodyForTest(`{"archive":"zip"}`)))
	if recorder.Code != http.StatusRequestEntityTooLarge || responseErrorCode(t, recorder) != "ARCHIVE_TOO_MANY_ENTRIES" {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	if len(fs.readDirLimits) != 1 || fs.readDirLimits[0] > maxShareArchiveEntries {
		t.Fatalf("bounded read limits = %v, want one request no larger than remaining+1", fs.readDirLimits)
	}
	current, _ := store.Get(folder.ID)
	if current.AccessCount != 0 {
		t.Fatalf("access_count = %d, want 0", current.AccessCount)
	}
}

func TestCreateDownloadTicket_ArchiveReservesDiscoveredBudgetAcrossDepth(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	folder, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "owner-1"})
	if err != nil {
		t.Fatal(err)
	}
	rootChildren := make([]*storage.FileInfo, 0, 5001)
	rootChildren = append(rootChildren, &storage.FileInfo{Path: "/docs/000-deep", Name: "000-deep", IsDir: true, Mode: os.ModeDir})
	for index := 0; index < 5000; index++ {
		name := fmt.Sprintf("sibling-%05d.bin", index)
		rootChildren = append(rootChildren, &storage.FileInfo{Path: path.Join("/docs", name), Name: name})
	}
	deepChildren := make([]*storage.FileInfo, 5000)
	for index := range deepChildren {
		name := fmt.Sprintf("nested-%05d.bin", index)
		deepChildren[index] = &storage.FileInfo{Path: path.Join("/docs/000-deep", name), Name: name}
	}
	fs := &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{"/docs": {Path: "/docs", Name: "docs", IsDir: true, Mode: os.ModeDir}},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs":          rootChildren,
			"/docs/000-deep": deepChildren,
		},
	}
	handler := NewHandler(store, fs)
	if err := handler.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(recorder, newRouteRequest(http.MethodPost, "/s/"+folder.ID+"/download-ticket", folder.ID, downloadTicketBodyForTest(`{"archive":"zip"}`)))
	if recorder.Code != http.StatusRequestEntityTooLarge || responseErrorCode(t, recorder) != "ARCHIVE_TOO_MANY_ENTRIES" {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	wantLimits := []int{maxShareArchiveEntries, 4999}
	if len(fs.readDirLimits) != len(wantLimits) {
		t.Fatalf("bounded read limits = %v, want %v", fs.readDirLimits, wantLimits)
	}
	for index := range wantLimits {
		if fs.readDirLimits[index] != wantLimits[index] {
			t.Fatalf("bounded read limits = %v, want %v", fs.readDirLimits, wantLimits)
		}
	}
	if fs.readDirReturned > maxShareArchiveEntries+1 {
		t.Fatalf("returned metadata = %d, want <= %d", fs.readDirReturned, maxShareArchiveEntries+1)
	}
}

func TestShareStore_DeleteDoesNotRetainDownloadTicketRevisionTombstones(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 32; index++ {
		created, err := store.Create(CreateShareOptions{Path: "/docs/report.pdf", Type: ShareTypeFile, CreatedBy: "owner-1"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Delete(created.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.ticketRevisions) != 0 {
		t.Fatalf("in-memory ticket revision tombstones = %d, want 0", len(store.ticketRevisions))
	}
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeShareStoreFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.TicketRevisions) != 0 {
		t.Fatalf("persisted ticket revision tombstones = %d, want 0", len(stored.TicketRevisions))
	}
}

func TestShareStore_LoadRebuildsExactDownloadTicketRevisionMap(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	live, err := store.Create(CreateShareOptions{Path: "/docs/report.pdf", Type: ShareTypeFile, CreatedBy: "owner-1"})
	if err != nil {
		t.Fatal(err)
	}
	preserved, err := store.Create(CreateShareOptions{Path: "/docs/preserved.pdf", Type: ShareTypeFile, CreatedBy: "owner-1"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeShareStoreFile(data)
	if err != nil {
		t.Fatal(err)
	}
	invalid := copyShare(live)
	invalid.ID = "invalid/share-id"
	stored.Shares = append(stored.Shares, invalid)
	stored.TicketRevisions = map[string]uint64{
		live.ID:            0,
		preserved.ID:       7,
		invalid.ID:         7,
		"unknown-share-id": 9,
	}
	encoded, err := json.Marshal(&stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeShareStoreFile(storePath, encoded); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewShareStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.ticketRevisions) != 2 || reopened.ticketRevisions[live.ID] != 1 || reopened.ticketRevisions[preserved.ID] != 7 {
		t.Fatalf("rebuilt revisions = %#v, want %q:1 and %q:7", reopened.ticketRevisions, live.ID, preserved.ID)
	}
	current, err := reopened.Get(live.ID)
	if err != nil || current.ticketRevision != 1 {
		t.Fatalf("live share revision = (%d, %v), want (1, nil)", current.ticketRevision, err)
	}

	persistedData, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := decodeShareStoreFile(persistedData)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Shares) != 2 || len(persisted.TicketRevisions) != 2 || persisted.TicketRevisions[live.ID] != 1 || persisted.TicketRevisions[preserved.ID] != 7 {
		t.Fatalf("rewritten store shares/revisions = %d/%#v", len(persisted.Shares), persisted.TicketRevisions)
	}
	reopenedAgain, err := NewShareStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopenedAgain.ticketRevisions) != 2 || reopenedAgain.ticketRevisions[live.ID] != 1 || reopenedAgain.ticketRevisions[preserved.ID] != 7 {
		t.Fatalf("second reopen revisions = %#v", reopenedAgain.ticketRevisions)
	}
}

func TestDownloadTicket_DeleteThenInternalRestoreDoesNotReviveOldGrant(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 0)
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	original, err := store.Get(share.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(share.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RestoreShares([]*Share{original}); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	handler.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
	if recorder.Code != http.StatusUnauthorized || responseErrorCode(t, recorder) != "DOWNLOAD_TICKET_STALE" {
		t.Fatalf("delete/restore status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestDownloadTicket_TrashDisableRollbackRemainsStaleAcrossRestart(t *testing.T) {
	store, share, handler, fs := newDownloadTicketTestHandler(t, 0)
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	original, err := store.Get(share.ID)
	if err != nil {
		t.Fatal(err)
	}
	const operationID = "0123456789abcdef0123456789abcdef"
	if err := store.ApplyTrashDeleteOperation(operationID, []*Share{original}, false); err != nil {
		t.Fatal(err)
	}
	if err := store.RollbackTrashDeleteOperation(operationID); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewShareStore(store.filePath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewHandler(reopened, fs)
	if err := restarted.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	restarted.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
	if recorder.Code != http.StatusUnauthorized || responseErrorCode(t, recorder) != "DOWNLOAD_TICKET_STALE" {
		t.Fatalf("trash rollback status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestDownloadTicket_TrashRestoreRemainsStaleAcrossRestart(t *testing.T) {
	store, share, handler, fs := newDownloadTicketTestHandler(t, 0)
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	original, err := store.Get(share.ID)
	if err != nil {
		t.Fatal(err)
	}
	const deleteOperationID = "1123456789abcdef0123456789abcdef"
	const restoreOperationID = "2123456789abcdef0123456789abcdef"
	if err := store.ApplyTrashDeleteOperation(deleteOperationID, []*Share{original}, true); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTrashDeleteOperation(deleteOperationID); err != nil {
		t.Fatal(err)
	}
	relocated := copyShare(original)
	relocated.Enabled = true
	if err := store.ApplyTrashRestoreOperation(restoreOperationID, deleteOperationID, []*Share{original}, []*Share{relocated}); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewShareStore(store.filePath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewHandler(reopened, fs)
	if err := restarted.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	restarted.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
	if recorder.Code != http.StatusUnauthorized || responseErrorCode(t, recorder) != "DOWNLOAD_TICKET_STALE" {
		t.Fatalf("trash restore status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateDownloadTicket_StrictRequestValidationDoesNotReserve(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 0)
	tests := [][]byte{
		[]byte(`{}`),
		[]byte(`{"client_nonce":null}`),
		[]byte(`{"client_nonce":7}`),
		[]byte(`{"client_nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}`),
		[]byte(`{"client_nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB"}`),
		downloadTicketBodyWithNonceForTest(`{}`, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, downloadTicketClientNonceSize-1))),
		downloadTicketBodyWithNonceForTest(`{}`, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, downloadTicketClientNonceSize+1))),
		[]byte(`{"client_nonce":"short"}`),
		downloadTicketBodyForTest(`{"unknown":true}`),
		downloadTicketBodyForTest(`{"path":null}`),
		downloadTicketBodyForTest(`{"archive":null}`),
		downloadTicketBodyForTest(`{"archive":""}`),
		downloadTicketBodyForTest(`{"archive":"tar"}`),
		downloadTicketBodyForTest(`{"path":7}`),
	}
	for _, body := range tests {
		request := newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, body)
		recorder := httptest.NewRecorder()
		handler.CreateDownloadTicket(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d, want 400", string(body), recorder.Code)
		}
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("invalid requests access_count = %d, want 0", current.AccessCount)
	}
}

func TestCreateDownloadTicket_RandomAndEncodingFailuresDoNotReserve(t *testing.T) {
	t.Run("random", func(t *testing.T) {
		store, share, handler, _ := newDownloadTicketTestHandler(t, 0)
		handler.downloadTicketRandom = errorReader{err: errors.New("entropy unavailable")}
		recorder := httptest.NewRecorder()
		handler.CreateDownloadTicket(recorder, newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`)))
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", recorder.Code)
		}
		current, _ := store.Get(share.ID)
		if current.AccessCount != 0 {
			t.Fatalf("access_count = %d, want 0", current.AccessCount)
		}
	})

	t.Run("encoding", func(t *testing.T) {
		store, share, handler, _ := newDownloadTicketTestHandler(t, 0)
		originalMarshal := marshalShareJSON
		marshalShareJSON = func(any) ([]byte, error) { return nil, errors.New("encode failed") }
		defer func() { marshalShareJSON = originalMarshal }()
		recorder := httptest.NewRecorder()
		handler.CreateDownloadTicket(recorder, newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`)))
		current, _ := store.Get(share.ID)
		if current.AccessCount != 0 {
			t.Fatalf("access_count = %d, want 0", current.AccessCount)
		}
	})
}

func TestCreateDownloadTicket_FirstResponseWriteFailureKeepsPublishedReservation(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 1)
	writer := &failFirstWriteResponseWriter{}

	handler.CreateDownloadTicket(writer, newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`)))

	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("access_count = %d, want fail-closed reservation 1", current.AccessCount)
	}
}

func TestCreateDownloadTicket_PublishedCapabilityWriteErrorKeepsReservation(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 1)
	writer := &fullWriteErrorResponseWriter{err: errors.New("connection closed after write")}

	handler.CreateDownloadTicket(writer, newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`)))

	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("access_count = %d, want fail-closed reservation 1", current.AccessCount)
	}
}

func TestCreateDownloadTicket_MaxAccessOneIsAtomic(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 1)
	const requests = 16
	start := make(chan struct{})
	statuses := make(chan int, requests)
	var wait sync.WaitGroup
	for index := 0; index < requests; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for attempts := 0; attempts < 100; attempts++ {
				recorder := httptest.NewRecorder()
				handler.CreateDownloadTicket(recorder, newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`)))
				if recorder.Code == http.StatusTooManyRequests {
					time.Sleep(time.Millisecond)
					continue
				}
				statuses <- recorder.Code
				return
			}
			statuses <- http.StatusTooManyRequests
		}()
	}
	close(start)
	wait.Wait()
	close(statuses)
	successes := 0
	for status := range statuses {
		if status == http.StatusOK {
			successes++
		} else if status != http.StatusGone && status != http.StatusConflict {
			t.Fatalf("unexpected status %d", status)
		}
	}
	if successes != 1 {
		t.Fatalf("successful tickets = %d, want 1", successes)
	}
	current, _ := store.Get(share.ID)
	if current.AccessCount != 1 {
		t.Fatalf("access_count = %d, want 1", current.AccessCount)
	}
}

func TestCreateDownloadTicket_ArchiveRejectsNonRegularSnapshotBeforeReserve(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	share, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "owner-1", MaxAccess: 1})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	fs := &fakeShareSnapshotFS{
		fakeShareFS: &fakeShareFS{
			statInfoByPath: map[string]*storage.FileInfo{"/docs": {Path: "/docs", Name: "docs", IsDir: true}},
			dirItemsByPath: map[string][]*storage.FileInfo{"/docs": {{Path: "/docs/device", Name: "device", Size: 1}}},
		},
		snapshotByPath: map[string]fakeShareSnapshot{"/docs/device": {err: storage.ErrNotRegular}},
	}
	handler := NewHandler(store, fs)
	if err := handler.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(recorder, newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{"archive":"zip"}`)))
	if recorder.Code != http.StatusConflict || responseErrorCode(t, recorder) != "ARCHIVE_ENTRY_NOT_REGULAR" {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	current, _ := store.Get(share.ID)
	if current.AccessCount != 0 {
		t.Fatalf("access_count = %d, want 0", current.AccessCount)
	}
}

func TestCreateDownloadTicket_RejectsNonRegularFileInfoModeBeforeReserve(t *testing.T) {
	store, share, handler, fs := newDownloadTicketTestHandler(t, 1)
	fs.statInfoByPath[share.Path].Mode = os.ModeSymlink | 0o777
	recorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(recorder, newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`)))
	if recorder.Code != http.StatusConflict || responseErrorCode(t, recorder) != "FILE_NOT_REGULAR" {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	current, _ := store.Get(share.ID)
	if current.AccessCount != 0 {
		t.Fatalf("access_count = %d, want 0", current.AccessCount)
	}
}

func TestDownloadTicket_DisableEnableABARemainsStaleAcrossRestart(t *testing.T) {
	store, share, handler, fs := newDownloadTicketTestHandler(t, 0)
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	for _, enabled := range []bool{false, true} {
		if err := store.Update(share.ID, func(current *Share) error {
			current.Enabled = enabled
			return nil
		}); err != nil {
			t.Fatalf("Update(enabled=%t) error: %v", enabled, err)
		}
	}

	reopened, err := NewShareStore(store.filePath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	current, err := reopened.Get(share.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	if bytes.Contains(encoded, []byte("ticketRevision")) || bytes.Contains(encoded, []byte("ticket_revision")) {
		t.Fatalf("public share JSON leaked ticket revision: %s", encoded)
	}

	restarted := NewHandler(reopened, fs)
	if err := restarted.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	restarted.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
	if recorder.Code != http.StatusUnauthorized || responseErrorCode(t, recorder) != "DOWNLOAD_TICKET_STALE" {
		t.Fatalf("ABA ticket status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestDownloadTicket_OwnerDisableEnableABARemainsStale(t *testing.T) {
	_, share, handler, _ := newDownloadTicketTestHandler(t, 0)
	owners := &mutableDownloadTicketOwnerStore{user: &auth.User{
		ID:                share.CreatedBy,
		Username:          "owner",
		Role:              auth.RoleUser,
		HomeDir:           "/home/owner",
		UpdatedAt:         time.Now().UTC().Truncate(time.Second),
		CredentialVersion: 1,
	}}
	handler.SetUserStore(owners)
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	owners.user.Disabled = true
	owners.user.UpdatedAt = owners.user.UpdatedAt.Add(time.Second)
	owners.user.Disabled = false
	owners.user.UpdatedAt = owners.user.UpdatedAt.Add(time.Second)

	recorder := httptest.NewRecorder()
	handler.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
	if recorder.Code != http.StatusUnauthorized || responseErrorCode(t, recorder) != "DOWNLOAD_TICKET_STALE" {
		t.Fatalf("owner ABA ticket status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestDownloadTicket_FileBecomesNonRegularAfterIssue(t *testing.T) {
	_, share, handler, fs := newDownloadTicketTestHandler(t, 0)
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	fs.openErrByPath = map[string]error{share.Path: storage.ErrNotRegular}

	recorder := httptest.NewRecorder()
	handler.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
	if recorder.Code != http.StatusConflict || responseErrorCode(t, recorder) != "FILE_NOT_REGULAR" {
		t.Fatalf("non-regular race status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestDownloadTicket_TargetBindingPrecedesPathAuthorization(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	folder, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "owner-1"})
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeShareFS{statInfoByPath: map[string]*storage.FileInfo{
		"/docs/allowed.txt": {Path: "/docs/allowed.txt", Name: "allowed.txt", Size: 1},
	}}
	handler := NewHandler(store, fs)
	if err := handler.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatal(err)
	}
	issue := issueDownloadTicketForTest(t, handler, folder.ID, "/s/", `{"path":"allowed.txt"}`)
	authorizerCalls := 0
	handler.SetPathAccessAuthorizer(func(context.Context, *Share, string) error {
		authorizerCalls++
		return storage.ErrNotFound
	})

	request := downloadRequestWithTicket(folder.ID, "/s/"+folder.ID+"/download/hidden.txt", issue)
	recorder := httptest.NewRecorder()
	handler.DownloadShareFile(recorder, request)
	if recorder.Code != http.StatusUnauthorized || responseErrorCode(t, recorder) != "DOWNLOAD_TICKET_INVALID" {
		t.Fatalf("wrong target status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	if authorizerCalls != 0 {
		t.Fatalf("path authorizer calls = %d, want 0 before target validation", authorizerCalls)
	}
}

func TestDownloadTicket_MissingInvalidExpiredAndDuplicateCodes(t *testing.T) {
	_, share, handler, _ := newDownloadTicketTestHandler(t, 1)
	now := time.Now().UTC().Truncate(time.Second)
	handler.downloadTicketNow = func() time.Time { return now }
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)

	tests := []struct {
		name     string
		request  func() *http.Request
		wantCode string
		status   int
	}{
		{
			name: "missing",
			request: func() *http.Request {
				return newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
			},
			wantCode: "DOWNLOAD_TICKET_REQUIRED",
			status:   http.StatusUnauthorized,
		},
		{
			name: "invalid",
			request: func() *http.Request {
				request := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?ticket="+strings.Repeat("0", 32), share.ID, nil)
				request.AddCookie(issue.cookie)
				return request
			},
			wantCode: "DOWNLOAD_TICKET_INVALID",
			status:   http.StatusUnauthorized,
		},
		{
			name: "duplicate",
			request: func() *http.Request {
				request := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?ticket="+issue.response.Ticket+"&ticket="+issue.response.Ticket, share.ID, nil)
				request.AddCookie(issue.cookie)
				return request
			},
			wantCode: "DOWNLOAD_TICKET_REQUIRED",
			status:   http.StatusUnauthorized,
		},
		{
			name: "expired",
			request: func() *http.Request {
				handler.downloadTicketNow = func() time.Time { return now.Add(defaultDownloadTicketTTL + time.Second) }
				return downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue)
			},
			wantCode: "DOWNLOAD_TICKET_EXPIRED",
			status:   http.StatusGone,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler.downloadTicketNow = func() time.Time { return now }
			recorder := httptest.NewRecorder()
			handler.DownloadShare(recorder, test.request())
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			if code := responseErrorCode(t, recorder); code != test.wantCode {
				t.Fatalf("code = %q, want %q", code, test.wantCode)
			}
		})
	}
}

func TestDownloadTicket_SameTicketSupportsRangeResumeAtLimit(t *testing.T) {
	store, share, handler, fs := newDownloadTicketTestHandler(t, 1)
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)

	for _, test := range []struct {
		rangeHeader string
		wantBody    string
	}{
		{rangeHeader: "bytes=0-2", wantBody: "abc"},
		{rangeHeader: "bytes=3-5", wantBody: "def"},
	} {
		fs.openByPath[share.Path] = &readSeekCloser{Reader: bytes.NewReader([]byte("abcdef"))}
		request := downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue)
		request.Header.Set("Range", test.rangeHeader)
		recorder := httptest.NewRecorder()
		handler.DownloadShare(recorder, request)
		if recorder.Code != http.StatusPartialContent || recorder.Body.String() != test.wantBody {
			t.Fatalf("range %q status/body = %d/%q", test.rangeHeader, recorder.Code, recorder.Body.String())
		}
	}
	current, _ := store.Get(share.ID)
	if current.AccessCount != 1 {
		t.Fatalf("access_count = %d, want 1", current.AccessCount)
	}
}

func TestDownloadTicket_SurvivesHandlerAndStoreRestart(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	share, err := store.Create(CreateShareOptions{Path: "/docs/report.pdf", Type: ShareTypeFile, CreatedBy: "owner-1", MaxAccess: 1})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	newFS := func() *fakeShareFS {
		return &fakeShareFS{
			statInfoByPath: map[string]*storage.FileInfo{share.Path: {Path: share.Path, Name: "report.pdf", Size: 6}},
			openByPath:     map[string]FileReader{share.Path: &readSeekCloser{Reader: bytes.NewReader([]byte("abcdef"))}},
		}
	}
	first := NewHandler(store, newFS())
	if err := first.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatal(err)
	}
	issue := issueDownloadTicketForTest(t, first, share.ID, "/s/", `{}`)

	reopenedStore, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	second := NewHandler(reopenedStore, newFS())
	if err := second.SetDownloadTicketSigningKey(fixedDownloadTicketTestKey); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	second.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "abcdef" {
		t.Fatalf("restart download status/body = %d/%q", recorder.Code, recorder.Body.String())
	}
}

func TestDownloadTicket_StateAndTargetBinding(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 0)
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	if err := store.Update(share.ID, func(current *Share) error {
		current.MaxAccess = 5
		return nil
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
	if recorder.Code != http.StatusUnauthorized || responseErrorCode(t, recorder) != "DOWNLOAD_TICKET_STALE" {
		t.Fatalf("state change status/body = %d/%s", recorder.Code, recorder.Body.String())
	}

	store2, folder, handler2, _ := newDownloadTicketTestHandler(t, 0)
	_ = store2
	issue2 := issueDownloadTicketForTest(t, handler2, folder.ID, "/s/", `{}`)
	wrongTarget := newRouteRequest(http.MethodGet, "/s/"+folder.ID+"/download?archive=zip&ticket="+issue2.response.Ticket, folder.ID, nil)
	wrongTarget.AddCookie(issue2.cookie)
	wrongRecorder := httptest.NewRecorder()
	handler2.DownloadShare(wrongRecorder, wrongTarget)
	if wrongRecorder.Code != http.StatusUnauthorized || responseErrorCode(t, wrongRecorder) != "DOWNLOAD_TICKET_INVALID" {
		t.Fatalf("target mismatch status/body = %d/%s", wrongRecorder.Code, wrongRecorder.Body.String())
	}
}

func TestDownloadTicket_ExplicitDisableUsesShareTerminalError(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 0)
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	if err := store.Update(share.ID, func(current *Share) error {
		current.Enabled = false
		return nil
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
	if recorder.Code != http.StatusGone || responseErrorCode(t, recorder) != "SHARE_DISABLED" {
		t.Fatalf("disabled share status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestDownloadTicket_TTLIsCappedByShareExpiryAndServerClock(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 0)
	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(30 * time.Minute)
	if err := store.Update(share.ID, func(current *Share) error {
		current.ExpiresAt = &expires
		return nil
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	handler.downloadTicketNow = func() time.Time { return now }
	issue := issueDownloadTicketForTest(t, handler, share.ID, "/s/", `{}`)
	if issue.response.ExpiresAt != expires.Format(time.RFC3339) {
		t.Fatalf("expires_at = %q, want %q", issue.response.ExpiresAt, expires.Format(time.RFC3339))
	}
	handler.downloadTicketNow = func() time.Time { return expires }
	recorder := httptest.NewRecorder()
	handler.DownloadShare(recorder, downloadRequestWithTicket(share.ID, "/s/"+share.ID+"/download", issue))
	if recorder.Code != http.StatusGone || responseErrorCode(t, recorder) != "DOWNLOAD_TICKET_EXPIRED" {
		t.Fatalf("expiry status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestDownloadTicket_GrantContainsOnlyFixedSizeHashes(t *testing.T) {
	store, share, handler, _ := newDownloadTicketTestHandler(t, 0)
	passwordHash := strings.Repeat("sensitive-password-hash", 4)
	if err := store.Update(share.ID, func(current *Share) error {
		current.PasswordHash = passwordHash
		return nil
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	share, _ = store.Get(share.ID)
	request := newRouteRequest(http.MethodPost, "/s/"+share.ID+"/download-ticket", share.ID, downloadTicketBodyForTest(`{}`))
	request.AddCookie(&http.Cookie{Name: shareAccessCookieName(share.ID), Value: handler.shareAccessToken(share)})
	recorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response DownloadTicketResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(response.Ticket)
	if err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	if len(sealed) != downloadTicketGrantSize {
		t.Fatalf("grant size = %d, want %d", len(sealed), downloadTicketGrantSize)
	}
	if len(response.Ticket) > 256 {
		t.Fatalf("grant encoding length = %d, want <= 256", len(response.Ticket))
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(fixedDownloadTicketClientNonce)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range [][]byte{[]byte(share.Path), []byte(passwordHash), []byte(fixedDownloadTicketClientNonce), nonceBytes} {
		if bytes.Contains(sealed, secret) {
			t.Fatalf("grant leaked raw value %q", string(secret))
		}
	}
}

func TestDownloadTicketSigningKeyRejectsShortKeys(t *testing.T) {
	_, _, handler, _ := newDownloadTicketTestHandler(t, 0)
	if err := handler.SetDownloadTicketSigningKey([]byte("short")); err == nil {
		t.Fatal("SetDownloadTicketSigningKey() accepted a short key")
	}
}

func TestDownloadTicketPublicRoutesIncludeTicketEndpoint(t *testing.T) {
	_, share, handler, _ := newDownloadTicketTestHandler(t, 0)
	router := chi.NewRouter()
	handler.PublicRoutes(router)
	request := httptest.NewRequest(http.MethodPost, "/"+share.ID+"/download-ticket", bytes.NewReader(downloadTicketBodyForTest(`{}`)))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ticket endpoint status = %d", recorder.Code)
	}
}

var _ io.Reader = errorReader{}
