package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/requestip"
)

type cookieSecurityFixture struct {
	store      *UserStore
	manager    *TokenManager
	handler    *Handler
	middleware *Middleware
	userA      *User
	userB      *User
}

func newCookieSecurityFixture(t *testing.T) *cookieSecurityFixture {
	t.Helper()
	store, _, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("create user store: %v", err)
	}
	userA, err := store.Create("cookie-user-a", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("create cookie user A: %v", err)
	}
	userB, err := store.Create("cookie-user-b", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("create cookie user B: %v", err)
	}
	manager := NewTokenManager("cookie-security-fixture-secret", 15*time.Minute, 24*time.Hour)
	return &cookieSecurityFixture{
		store:      store,
		manager:    manager,
		handler:    NewHandler(store, manager),
		middleware: NewMiddleware(store, manager),
		userA:      userA,
		userB:      userB,
	}
}

func (fixture *cookieSecurityFixture) router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", fixture.handler.HandleLogin)
	mux.HandleFunc("/api/v1/auth/refresh", fixture.handler.HandleRefresh)
	mux.Handle("/api/v1/auth/logout", fixture.middleware.RequireAuth(http.HandlerFunc(fixture.handler.HandleLogout)))
	mux.Handle("/api/v1/auth/download-session", fixture.middleware.RequireAuth(http.HandlerFunc(fixture.handler.HandleCreateDownloadSession)))
	mux.Handle("/api/v1/auth/me", fixture.middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUserFromContext(r.Context())
		if user == nil {
			writeError(w, http.StatusUnauthorized, "not authenticated", "NOT_AUTHENTICATED")
			return
		}
		writeSuccess(w, http.StatusOK, map[string]string{"username": user.Username}, "")
	})))
	mux.Handle("/api/v1/download/", fixture.middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	return mux
}

func doCookieLogin(t *testing.T, client *http.Client, baseURL, username string) *http.Response {
	t.Helper()
	body, err := json.Marshal(LoginRequest{Username: username, Password: "password123"})
	if err != nil {
		t.Fatalf("marshal login request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create login request: %v", err)
	}
	req.Header.Set(sessionModeHeader, sessionModeCookie)
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("perform login request: %v", err)
	}
	return response
}

func doCookieRequest(t *testing.T, client *http.Client, method, target string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("perform %s request: %v", method, err)
	}
	return response
}

func requireResponseStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if response.StatusCode != want {
		t.Fatalf("response status = %d, want %d: %s", response.StatusCode, want, body)
	}
}

func TestHTTPSCookieSessionLifecycleUsesHostCookies(t *testing.T) {
	fixture := newCookieSecurityFixture(t)
	server := httptest.NewTLSServer(fixture.router())
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := server.Client()
	client.Jar = jar

	loginResponse := doCookieLogin(t, client, server.URL, fixture.userA.Username)
	loginCookies := loginResponse.Cookies()
	requireResponseStatus(t, loginResponse, http.StatusOK)
	wantLoginCookies := map[string]bool{
		HTTPSAccessSessionCookieName:  true,
		HTTPSRefreshSessionCookieName: true,
	}
	for _, cookie := range loginCookies {
		if !wantLoginCookies[cookie.Name] {
			t.Fatalf("unexpected HTTPS login cookie %q", cookie.Name)
		}
		if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" {
			t.Fatalf("HTTPS login cookie has unsafe attributes: %+v", cookie)
		}
		delete(wantLoginCookies, cookie.Name)
	}
	if len(wantLoginCookies) != 0 {
		t.Fatalf("missing HTTPS login cookies: %v", wantLoginCookies)
	}

	requireResponseStatus(t, doCookieRequest(t, client, http.MethodGet, server.URL+"/api/v1/auth/me"), http.StatusOK)
	downloadSessionResponse := doCookieRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/download-session")
	downloadCookies := downloadSessionResponse.Cookies()
	requireResponseStatus(t, downloadSessionResponse, http.StatusOK)
	if len(downloadCookies) != 1 {
		t.Fatalf("download-session cookies = %d, want 1", len(downloadCookies))
	}
	downloadCookie := downloadCookies[0]
	if downloadCookie.Name != HTTPSDownloadSessionCookieName || !downloadCookie.Secure || !downloadCookie.HttpOnly || downloadCookie.Path != "/" || downloadCookie.Domain != "" {
		t.Fatalf("HTTPS download cookie has unsafe attributes: %+v", downloadCookie)
	}
	requireResponseStatus(t, doCookieRequest(t, client, http.MethodGet, server.URL+"/api/v1/download/test.bin"), http.StatusNoContent)
	requireResponseStatus(t, doCookieRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/refresh"), http.StatusOK)

	logoutResponse := doCookieRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/logout")
	logoutCookies := logoutResponse.Cookies()
	requireResponseStatus(t, logoutResponse, http.StatusOK)
	wantCleared := map[string]bool{
		HTTPSAccessSessionCookieName:   true,
		HTTPSRefreshSessionCookieName:  true,
		HTTPSDownloadSessionCookieName: true,
	}
	for _, cookie := range logoutCookies {
		if !wantCleared[cookie.Name] {
			t.Fatalf("unexpected HTTPS clearing cookie %q", cookie.Name)
		}
		if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != -1 {
			t.Fatalf("HTTPS clearing cookie has unsafe attributes: %+v", cookie)
		}
		delete(wantCleared, cookie.Name)
	}
	if len(wantCleared) != 0 {
		t.Fatalf("missing HTTPS clearing cookies: %v", wantCleared)
	}
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	if cookies := jar.Cookies(baseURL); len(cookies) != 0 {
		t.Fatalf("cookie jar retained cookies after logout: %+v", cookies)
	}
	requireResponseStatus(t, doCookieRequest(t, client, http.MethodGet, server.URL+"/api/v1/auth/me"), http.StatusUnauthorized)
}

func TestLocalHTTPCookieSessionKeepsNarrowLegacyNames(t *testing.T) {
	fixture := newCookieSecurityFixture(t)
	server := httptest.NewServer(fixture.router())
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := server.Client()
	client.Jar = jar

	loginResponse := doCookieLogin(t, client, server.URL, fixture.userA.Username)
	loginCookies := loginResponse.Cookies()
	requireResponseStatus(t, loginResponse, http.StatusOK)
	wantPaths := map[string]string{
		AccessSessionCookieName:  sessionCookiePath,
		RefreshSessionCookieName: refreshSessionCookiePath,
	}
	for _, cookie := range loginCookies {
		wantPath, ok := wantPaths[cookie.Name]
		if !ok {
			t.Fatalf("unexpected local HTTP login cookie %q", cookie.Name)
		}
		if cookie.Secure || !cookie.HttpOnly || cookie.Path != wantPath || cookie.Domain != "" {
			t.Fatalf("local HTTP login cookie attributes are incorrect: %+v", cookie)
		}
		delete(wantPaths, cookie.Name)
	}
	if len(wantPaths) != 0 {
		t.Fatalf("missing local HTTP login cookies: %v", wantPaths)
	}

	requireResponseStatus(t, doCookieRequest(t, client, http.MethodGet, server.URL+"/api/v1/auth/me"), http.StatusOK)
	downloadSessionResponse := doCookieRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/download-session")
	downloadCookies := downloadSessionResponse.Cookies()
	requireResponseStatus(t, downloadSessionResponse, http.StatusOK)
	if len(downloadCookies) != 1 || downloadCookies[0].Name != DownloadSessionCookieName || downloadCookies[0].Path != downloadSessionCookiePath || downloadCookies[0].Secure {
		t.Fatalf("local HTTP download cookie attributes are incorrect: %+v", downloadCookies)
	}

	outsideURL, err := url.Parse(server.URL + "/outside")
	if err != nil {
		t.Fatalf("parse outside URL: %v", err)
	}
	if cookies := jar.Cookies(outsideURL); len(cookies) != 0 {
		t.Fatalf("narrow local cookies escaped /api/v1: %+v", cookies)
	}
}

func TestTrustedProxyHTTPSUsesHostCookieNamesAndParser(t *testing.T) {
	fixture := newCookieSecurityFixture(t)
	originalHops := requestip.TrustedProxyHops()
	requestip.SetTrustedProxyHops(1)
	defer requestip.SetTrustedProxyHops(originalHops)

	body, err := json.Marshal(LoginRequest{Username: fixture.userA.Username, Password: "password123"})
	if err != nil {
		t.Fatalf("marshal login request: %v", err)
	}
	loginReq := httptest.NewRequest(http.MethodPost, "http://nas.example.test/api/v1/auth/login", bytes.NewReader(body))
	loginReq.RemoteAddr = "127.0.0.1:1234"
	loginReq.Header.Set("X-Forwarded-Proto", "https")
	loginReq.Header.Set(sessionModeHeader, sessionModeCookie)
	loginRec := httptest.NewRecorder()
	fixture.handler.HandleLogin(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("trusted proxy login status = %d: %s", loginRec.Code, loginRec.Body.String())
	}
	var accessCookie *http.Cookie
	for _, cookie := range loginRec.Result().Cookies() {
		if cookie.Name == HTTPSAccessSessionCookieName {
			accessCookie = cookie
		}
		if !cookie.Secure || cookie.Path != "/" || cookie.Domain != "" || !strings.HasPrefix(cookie.Name, "__Host-") {
			t.Fatalf("trusted proxy cookie has unsafe attributes: %+v", cookie)
		}
	}
	if accessCookie == nil {
		t.Fatal("trusted proxy login did not issue __Host- access cookie")
	}

	called := false
	protected := fixture.middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if user := GetUserFromContext(r.Context()); user == nil || user.ID != fixture.userA.ID {
			t.Fatalf("trusted proxy middleware user = %#v, want user A", user)
		}
	}))
	protectedReq := httptest.NewRequest(http.MethodGet, "http://nas.example.test/api/v1/auth/me", nil)
	protectedReq.RemoteAddr = "127.0.0.1:1234"
	protectedReq.Header.Set("X-Forwarded-Proto", "https")
	protectedReq.AddCookie(accessCookie)
	protectedRec := httptest.NewRecorder()
	protected.ServeHTTP(protectedRec, protectedReq)
	if !called || protectedRec.Code != http.StatusOK {
		t.Fatalf("trusted proxy __Host- cookie was not accepted: status=%d body=%s", protectedRec.Code, protectedRec.Body.String())
	}
}

func TestCookieAuthenticationRejectsCrossAccountAmbiguity(t *testing.T) {
	fixture := newCookieSecurityFixture(t)
	pairA, err := fixture.manager.GenerateTokenPair(fixture.userA)
	if err != nil {
		t.Fatalf("generate token pair A: %v", err)
	}
	pairB, err := fixture.manager.GenerateTokenPair(fixture.userB)
	if err != nil {
		t.Fatalf("generate token pair B: %v", err)
	}

	protected := fixture.middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUserFromContext(r.Context())
		if user == nil {
			t.Fatal("authenticated request has no user")
		}
		w.Header().Set("X-Test-User", user.ID)
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, header := range []string{
		HTTPSAccessSessionCookieName + "=" + pairA.AccessToken + "; " + HTTPSAccessSessionCookieName + "=" + pairB.AccessToken,
		HTTPSAccessSessionCookieName + "=" + pairB.AccessToken + "; " + HTTPSAccessSessionCookieName + "=" + pairA.AccessToken,
	} {
		req := httptest.NewRequest(http.MethodGet, "https://nas.example.test/api/v1/auth/me", nil)
		req.Header.Set("Cookie", header)
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("cross-account duplicate access cookies returned %d, want 401", rec.Code)
		}
		if got := rec.Header().Get("X-Test-User"); got != "" {
			t.Fatalf("ambiguous cookie selected user %q", got)
		}
	}

	for _, header := range []string{
		AccessSessionCookieName + "=" + pairB.AccessToken + "; " + HTTPSAccessSessionCookieName + "=" + pairA.AccessToken,
		HTTPSAccessSessionCookieName + "=" + pairA.AccessToken + "; " + AccessSessionCookieName + "=" + pairB.AccessToken,
	} {
		ordinaryReq := httptest.NewRequest(http.MethodGet, "https://nas.example.test/api/v1/auth/me", nil)
		ordinaryReq.Header.Set("Cookie", header)
		ordinaryRec := httptest.NewRecorder()
		protected.ServeHTTP(ordinaryRec, ordinaryReq)
		if ordinaryRec.Code != http.StatusNoContent || ordinaryRec.Header().Get("X-Test-User") != fixture.userA.ID {
			t.Fatalf("ordinary Domain-capable cookie affected HTTPS identity: status=%d user=%q", ordinaryRec.Code, ordinaryRec.Header().Get("X-Test-User"))
		}
	}

	httpReq := httptest.NewRequest(http.MethodGet, "http://nas.example.test/api/v1/auth/me", nil)
	httpReq.AddCookie(&http.Cookie{Name: HTTPSAccessSessionCookieName, Value: pairA.AccessToken})
	httpRec := httptest.NewRecorder()
	protected.ServeHTTP(httpRec, httpReq)
	if httpRec.Code != http.StatusUnauthorized {
		t.Fatalf("local HTTP accepted HTTPS-only cookie: status=%d", httpRec.Code)
	}

	authorizationReq := httptest.NewRequest(http.MethodGet, "https://nas.example.test/api/v1/auth/me", nil)
	authorizationReq.Header.Set("Authorization", "Bearer "+pairB.AccessToken)
	authorizationReq.Header.Set("Cookie", HTTPSAccessSessionCookieName+"="+pairA.AccessToken+"; "+HTTPSAccessSessionCookieName+"="+pairB.AccessToken)
	authorizationRec := httptest.NewRecorder()
	protected.ServeHTTP(authorizationRec, authorizationReq)
	if authorizationRec.Code != http.StatusNoContent || authorizationRec.Header().Get("X-Test-User") != fixture.userB.ID {
		t.Fatalf("Authorization client was affected by cookies: status=%d user=%q", authorizationRec.Code, authorizationRec.Header().Get("X-Test-User"))
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "https://nas.example.test/api/v1/download/test.bin", nil)
	downloadReq.Header.Set("Cookie", HTTPSAccessSessionCookieName+"="+pairA.AccessToken+"; "+HTTPSDownloadSessionCookieName+"="+pairB.AccessToken)
	downloadRec := httptest.NewRecorder()
	protected.ServeHTTP(downloadRec, downloadReq)
	if downloadRec.Code != http.StatusUnauthorized {
		t.Fatalf("cross-account access/download cookies returned %d, want 401", downloadRec.Code)
	}
	duplicateDownloadReq := httptest.NewRequest(http.MethodGet, "https://nas.example.test/api/v1/download/test.bin", nil)
	duplicateDownloadReq.Header.Set("Cookie", HTTPSDownloadSessionCookieName+"="+pairA.AccessToken+"; "+HTTPSDownloadSessionCookieName+"="+pairB.AccessToken)
	duplicateDownloadRec := httptest.NewRecorder()
	protected.ServeHTTP(duplicateDownloadRec, duplicateDownloadReq)
	if duplicateDownloadRec.Code != http.StatusUnauthorized {
		t.Fatalf("cross-account duplicate download cookies returned %d, want 401", duplicateDownloadRec.Code)
	}

	for _, header := range []string{
		HTTPSRefreshSessionCookieName + "=" + pairA.RefreshToken + "; " + HTTPSRefreshSessionCookieName + "=" + pairB.RefreshToken,
		HTTPSRefreshSessionCookieName + "=" + pairB.RefreshToken + "; " + HTTPSRefreshSessionCookieName + "=" + pairA.RefreshToken,
	} {
		refreshReq := httptest.NewRequest(http.MethodPost, "https://nas.example.test/api/v1/auth/refresh", nil)
		refreshReq.Header.Set("Cookie", header)
		refreshRec := httptest.NewRecorder()
		fixture.handler.HandleRefresh(refreshRec, refreshReq)
		if refreshRec.Code != http.StatusUnauthorized {
			t.Fatalf("cross-account duplicate refresh cookies returned %d, want 401: %s", refreshRec.Code, refreshRec.Body.String())
		}
		cleared := map[string]bool{}
		for _, cookie := range refreshRec.Result().Cookies() {
			if cookie.MaxAge == -1 {
				cleared[cookie.Name] = true
			}
		}
		for _, name := range []string{HTTPSAccessSessionCookieName, HTTPSRefreshSessionCookieName, HTTPSDownloadSessionCookieName} {
			if !cleared[name] {
				t.Fatalf("ambiguous HTTPS refresh did not clear %s: %+v", name, refreshRec.Result().Cookies())
			}
		}
	}
	ordinaryRefreshReq := httptest.NewRequest(http.MethodPost, "https://nas.example.test/api/v1/auth/refresh", nil)
	ordinaryRefreshReq.AddCookie(&http.Cookie{Name: RefreshSessionCookieName, Value: pairA.RefreshToken})
	ordinaryRefreshRec := httptest.NewRecorder()
	fixture.handler.HandleRefresh(ordinaryRefreshRec, ordinaryRefreshReq)
	if ordinaryRefreshRec.Code != http.StatusBadRequest {
		t.Fatalf("HTTPS refresh accepted ordinary cookie name: status=%d body=%s", ordinaryRefreshRec.Code, ordinaryRefreshRec.Body.String())
	}

	duplicateLogoutReq := httptest.NewRequest(http.MethodPost, "https://nas.example.test/api/v1/auth/logout", nil)
	duplicateLogoutReq.Header.Set("Cookie", HTTPSRefreshSessionCookieName+"="+pairA.RefreshToken+"; "+HTTPSRefreshSessionCookieName+"="+pairB.RefreshToken)
	duplicateLogoutRec := httptest.NewRecorder()
	fixture.handler.HandleLogout(duplicateLogoutRec, duplicateLogoutReq)
	if duplicateLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("cross-account duplicate logout cookies returned %d, want 401: %s", duplicateLogoutRec.Code, duplicateLogoutRec.Body.String())
	}
	bodyLogout, err := json.Marshal(RefreshRequest{RefreshToken: pairA.RefreshToken})
	if err != nil {
		t.Fatalf("marshal logout body: %v", err)
	}
	bodyLogoutReq := httptest.NewRequest(http.MethodPost, "https://nas.example.test/api/v1/auth/logout", bytes.NewReader(bodyLogout))
	bodyLogoutReq.Header.Set("Cookie", HTTPSRefreshSessionCookieName+"="+pairA.RefreshToken+"; "+HTTPSRefreshSessionCookieName+"="+pairB.RefreshToken)
	bodyLogoutRec := httptest.NewRecorder()
	fixture.handler.HandleLogout(bodyLogoutRec, bodyLogoutReq)
	if bodyLogoutRec.Code != http.StatusOK {
		t.Fatalf("JSON logout client was affected by cookies: status=%d body=%s", bodyLogoutRec.Code, bodyLogoutRec.Body.String())
	}

	refreshBody, err := json.Marshal(RefreshRequest{RefreshToken: pairB.RefreshToken})
	if err != nil {
		t.Fatalf("marshal refresh body: %v", err)
	}
	bodyRefreshReq := httptest.NewRequest(http.MethodPost, "https://nas.example.test/api/v1/auth/refresh", bytes.NewReader(refreshBody))
	bodyRefreshReq.Header.Set("Cookie", HTTPSRefreshSessionCookieName+"="+pairA.RefreshToken+"; "+HTTPSRefreshSessionCookieName+"="+pairB.RefreshToken)
	bodyRefreshRec := httptest.NewRecorder()
	fixture.handler.HandleRefresh(bodyRefreshRec, bodyRefreshReq)
	if bodyRefreshRec.Code != http.StatusOK {
		t.Fatalf("JSON refresh client was affected by cookies: status=%d body=%s", bodyRefreshRec.Code, bodyRefreshRec.Body.String())
	}
	var refreshEnvelope struct {
		Data LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(bodyRefreshRec.Body.Bytes(), &refreshEnvelope); err != nil {
		t.Fatalf("decode JSON refresh response: %v", err)
	}
	if refreshEnvelope.Data.AccessToken == "" || refreshEnvelope.Data.RefreshToken == "" {
		t.Fatalf("JSON refresh client did not receive bearer tokens: %s", bodyRefreshRec.Body.String())
	}
}
