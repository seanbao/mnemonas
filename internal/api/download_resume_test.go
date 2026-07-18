package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestServer_DownloadFileIdentityIsStableForGETAndHEAD(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()
	content := []byte("current snapshot bytes")
	const filePath = "/download-identity.bin"

	if err := fs.WriteFile(ctx, filePath, bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", filePath, err)
	}
	info, err := fs.StatMetadata(ctx, filePath)
	if err != nil {
		t.Fatalf("StatMetadata(%s) error: %v", filePath, err)
	}
	if info.DeleteIdentityToken == "" {
		t.Skip("download identity is unavailable on this platform")
	}

	tests := []struct {
		method   string
		wantBody []byte
	}{
		{method: http.MethodGet, wantBody: content},
		{method: http.MethodHead, wantBody: nil},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/api/v1/download/download-identity.bin", nil)
			rec := httptest.NewRecorder()

			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s download status = %d, want %d; body=%s", test.method, rec.Code, http.StatusOK, rec.Body.String())
			}
			if got := rec.Header().Get(downloadIdentityHeader); got != info.DeleteIdentityToken {
				t.Fatalf("%s download identity = %q, want %q", test.method, got, info.DeleteIdentityToken)
			}
			if got := rec.Header().Get("ETag"); got != fmt.Sprintf(`W/"%s"`, info.DeleteIdentityToken) {
				t.Fatalf("%s ETag = %q, want weak identity ETag", test.method, got)
			}
			if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(content)) {
				t.Fatalf("%s Content-Length = %q, want %d", test.method, got, len(content))
			}
			if got := rec.Body.Bytes(); !bytes.Equal(got, test.wantBody) {
				t.Fatalf("%s body = %q, want %q", test.method, got, test.wantBody)
			}
		})
	}
}

func TestServer_DownloadFileIdentityPreconditionSupportsRangeGETAndHEAD(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()
	content := []byte("0123456789")
	const filePath = "/download-range-identity.bin"

	if err := fs.WriteFile(ctx, filePath, bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", filePath, err)
	}

	identityReq := httptest.NewRequest(http.MethodHead, "/api/v1/download/download-range-identity.bin", nil)
	identityRec := httptest.NewRecorder()
	server.Router().ServeHTTP(identityRec, identityReq)
	if identityRec.Code != http.StatusOK {
		t.Fatalf("identity HEAD status = %d, want %d", identityRec.Code, http.StatusOK)
	}
	identity := identityRec.Header().Get(downloadIdentityHeader)
	if identity == "" {
		t.Skip("download identity is unavailable on this platform")
	}

	tests := []struct {
		method           string
		wantStatus       int
		wantBody         []byte
		wantContentRange string
		wantLength       string
	}{
		{
			method:           http.MethodGet,
			wantStatus:       http.StatusPartialContent,
			wantBody:         []byte("2345"),
			wantContentRange: "bytes 2-5/10",
			wantLength:       "4",
		},
		{
			method:     http.MethodHead,
			wantStatus: http.StatusOK,
			wantBody:   nil,
			wantLength: "10",
		},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/api/v1/download/download-range-identity.bin", nil)
			req.Header.Set("Range", "bytes=2-5")
			req.Header.Set(downloadIdentityPreconditionHeader, identity)
			rec := httptest.NewRecorder()

			server.Router().ServeHTTP(rec, req)

			if rec.Code != test.wantStatus {
				t.Fatalf("%s range status = %d, want %d; body=%s", test.method, rec.Code, test.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get(downloadIdentityHeader); got != identity {
				t.Fatalf("%s range identity = %q, want %q", test.method, got, identity)
			}
			if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
				t.Fatalf("%s Accept-Ranges = %q, want bytes", test.method, got)
			}
			if got := rec.Header().Get("Content-Range"); got != test.wantContentRange {
				t.Fatalf("%s Content-Range = %q, want %q", test.method, got, test.wantContentRange)
			}
			if got := rec.Header().Get("Content-Length"); got != test.wantLength {
				t.Fatalf("%s Content-Length = %q, want %q", test.method, got, test.wantLength)
			}
			if got := rec.Body.Bytes(); !bytes.Equal(got, test.wantBody) {
				t.Fatalf("%s range body = %q, want %q", test.method, got, test.wantBody)
			}
		})
	}
}

type mutateDownloadOnWriteWriter struct {
	http.ResponseWriter
	mutate      func() error
	mutated     bool
	mutationErr error
}

func (w *mutateDownloadOnWriteWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *mutateDownloadOnWriteWriter) Write(p []byte) (int, error) {
	if !w.mutated {
		w.mutated = true
		w.mutationErr = w.mutate()
	}
	return w.ResponseWriter.Write(p)
}

func TestServer_DownloadFileAbortsCompleteResponseWhenOpenFileChangesDuringStreaming(t *testing.T) {
	server, fs, root := setupTestServer(t)
	ctx := context.Background()
	const filePath = "/download-stream-change.bin"
	original := bytes.Repeat([]byte("a"), downloadIntegrityTailBytes*8)
	replacement := bytes.Repeat([]byte("b"), len(original))

	if err := fs.WriteFile(ctx, filePath, bytes.NewReader(original)); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", filePath, err)
	}
	hostPath := filepath.Join(root, "files", "download-stream-change.bin")
	recorder := httptest.NewRecorder()
	writer := &mutateDownloadOnWriteWriter{
		ResponseWriter: recorder,
		mutate: func() error {
			return os.WriteFile(hostPath, replacement, 0o644)
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/download-stream-change.bin", nil)

	server.Router().ServeHTTP(writer, req)

	if writer.mutationErr != nil {
		t.Fatalf("mutate open download file: %v", writer.mutationErr)
	}
	if !writer.mutated {
		t.Fatal("download response never reached the mutation writer")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("download status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(len(original)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(original))
	}
	if got := recorder.Body.Len(); got == 0 || got >= len(original) {
		t.Fatalf("streamed body length = %d, want a non-empty truncated response below %d", got, len(original))
	}
	_, total := server.activity.List(10, 0, "", "")
	if total != 0 {
		t.Fatalf("download activity count = %d, want 0 after integrity abort", total)
	}
}

func TestServer_DownloadFileIntegrityAbortIsObservedAsUnexpectedEOF(t *testing.T) {
	server, fs, root := setupTestServer(t)
	ctx := context.Background()
	const filePath = "/download-stream-network-change.bin"
	original := bytes.Repeat([]byte("a"), downloadIntegrityTailBytes*8)
	replacement := bytes.Repeat([]byte("b"), len(original))

	if err := fs.WriteFile(ctx, filePath, bytes.NewReader(original)); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", filePath, err)
	}
	hostPath := filepath.Join(root, "files", "download-stream-network-change.bin")
	mutationResult := make(chan error, 1)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &mutateDownloadOnWriteWriter{
			ResponseWriter: w,
			mutate: func() error {
				return os.WriteFile(hostPath, replacement, 0o644)
			},
		}
		server.Router().ServeHTTP(writer, r)
		if !writer.mutated && writer.mutationErr == nil {
			writer.mutationErr = errors.New("download response never reached the mutation writer")
		}
		mutationResult <- writer.mutationErr
	}))
	t.Cleanup(httpServer.Close)

	response, err := httpServer.Client().Get(httpServer.URL + "/api/v1/download/download-stream-network-change.bin")
	if err != nil {
		t.Fatalf("GET changed download: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if mutationErr := <-mutationResult; mutationErr != nil {
		t.Fatalf("mutate open download file: %v", mutationErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if response.ContentLength != int64(len(original)) {
		t.Fatalf("Content-Length = %d, want %d", response.ContentLength, len(original))
	}
	if !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadAll() error = %v, want %v", readErr, io.ErrUnexpectedEOF)
	}
	if closeErr != nil {
		t.Fatalf("response body Close() error: %v", closeErr)
	}
	if got := len(body); got == 0 || got >= len(original) {
		t.Fatalf("received body length = %d, want a non-empty truncated response below %d", got, len(original))
	}
}

func TestDownloadIntegrityResponseWriterCommitsBoundedTail(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newDownloadIntegrityResponseWriter(recorder)
	content := bytes.Repeat([]byte("stable"), downloadIntegrityTailBytes)

	for offset := 0; offset < len(content); {
		end := min(offset+8191, len(content))
		n, err := writer.Write(content[offset:end])
		if err != nil {
			t.Fatalf("Write(%d:%d) error: %v", offset, end, err)
		}
		if n != end-offset {
			t.Fatalf("Write(%d:%d) = %d, want %d", offset, end, n, end-offset)
		}
		if len(writer.pending) > downloadIntegrityTailBytes {
			t.Fatalf("pending tail = %d bytes, want at most %d", len(writer.pending), downloadIntegrityTailBytes)
		}
		offset = end
	}
	if got := recorder.Body.Len(); got != len(content)-downloadIntegrityTailBytes {
		t.Fatalf("pre-commit body length = %d, want %d", got, len(content)-downloadIntegrityTailBytes)
	}
	if err := writer.commit(); err != nil {
		t.Fatalf("commit() error: %v", err)
	}
	if got := recorder.Body.Bytes(); !bytes.Equal(got, content) {
		t.Fatalf("committed body length = %d, want exact %d-byte content", len(got), len(content))
	}
	if len(writer.pending) != 0 {
		t.Fatalf("pending tail after commit = %d, want 0", len(writer.pending))
	}
}

func TestDownloadIdentityPreconditionMatchesRejectsAmbiguousValues(t *testing.T) {
	const identity = "identity-token"
	tests := []struct {
		name           string
		actualIdentity string
		values         []string
		want           bool
	}{
		{name: "absent", actualIdentity: identity, want: true},
		{name: "match", actualIdentity: identity, values: []string{identity}, want: true},
		{name: "mismatch", actualIdentity: identity, values: []string{"other"}, want: false},
		{name: "empty", actualIdentity: identity, values: []string{""}, want: false},
		{name: "repeated", actualIdentity: identity, values: []string{identity, identity}, want: false},
		{name: "comma folded", actualIdentity: identity, values: []string{identity + "," + identity}, want: false},
		{name: "unsupported platform absent", want: true},
		{name: "unsupported platform conditional", values: []string{identity}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/download/file.bin", nil)
			for _, value := range test.values {
				req.Header.Add(downloadIdentityPreconditionHeader, value)
			}
			if got := downloadIdentityPreconditionMatches(req, test.actualIdentity); got != test.want {
				t.Fatalf("downloadIdentityPreconditionMatches() = %t, want %t; values=%q", got, test.want, test.values)
			}
		})
	}
}

func TestServer_DownloadFileIdentityPreconditionRejectsChangedFileWithoutBody(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()
	const filePath = "/download-changed-identity.bin"

	if err := fs.WriteFile(ctx, filePath, bytes.NewReader([]byte("old-content"))); err != nil {
		t.Fatalf("WriteFile(%s) initial error: %v", filePath, err)
	}
	initialReq := httptest.NewRequest(http.MethodHead, "/api/v1/download/download-changed-identity.bin", nil)
	initialRec := httptest.NewRecorder()
	server.Router().ServeHTTP(initialRec, initialReq)
	if initialRec.Code != http.StatusOK {
		t.Fatalf("initial HEAD status = %d, want %d", initialRec.Code, http.StatusOK)
	}
	oldIdentity := initialRec.Header().Get(downloadIdentityHeader)
	if oldIdentity == "" {
		t.Skip("download identity is unavailable on this platform")
	}

	if err := fs.WriteFile(ctx, filePath, bytes.NewReader([]byte("new-content"))); err != nil {
		t.Fatalf("WriteFile(%s) replacement error: %v", filePath, err)
	}
	currentReq := httptest.NewRequest(http.MethodHead, "/api/v1/download/download-changed-identity.bin", nil)
	currentRec := httptest.NewRecorder()
	server.Router().ServeHTTP(currentRec, currentReq)
	if currentRec.Code != http.StatusOK {
		t.Fatalf("current HEAD status = %d, want %d", currentRec.Code, http.StatusOK)
	}
	currentIdentity := currentRec.Header().Get(downloadIdentityHeader)
	if currentIdentity == "" || currentIdentity == oldIdentity {
		t.Fatalf("replacement identity = %q, want non-empty value different from %q", currentIdentity, oldIdentity)
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/download/download-changed-identity.bin", nil)
			req.Header.Set("Range", "bytes=4-")
			req.Header.Set(downloadIdentityPreconditionHeader, oldIdentity)
			rec := httptest.NewRecorder()

			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusPreconditionFailed {
				t.Fatalf("%s stale identity status = %d, want %d; body=%s", method, rec.Code, http.StatusPreconditionFailed, rec.Body.String())
			}
			if got := rec.Header().Get(downloadIdentityHeader); got != currentIdentity {
				t.Fatalf("%s stale response identity = %q, want current identity %q", method, got, currentIdentity)
			}
			if got := rec.Header().Get("Content-Length"); got != "0" {
				t.Fatalf("%s stale response Content-Length = %q, want 0", method, got)
			}
			if got := rec.Header().Get("Content-Range"); got != "" {
				t.Fatalf("%s stale response Content-Range = %q, want empty", method, got)
			}
			if rec.Body.Len() != 0 {
				t.Fatalf("%s stale response body length = %d, want 0", method, rec.Body.Len())
			}
		})
	}
}

func TestServer_DownloadArchiveHEADIsRejectedWithoutBody(t *testing.T) {
	server, _, _ := setupTestServer(t)
	req := httptest.NewRequest(http.MethodHead, "/api/v1/download/archive-target?archive=zip", nil)
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("archive HEAD status = %d, want %d; body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("archive HEAD Allow = %q, want %q", got, http.MethodGet)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("archive HEAD body length = %d, want 0", rec.Body.Len())
	}
}
