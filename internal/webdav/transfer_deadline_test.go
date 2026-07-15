package webdav

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/storage"
)

type webDAVDeadlineRecordingWriter struct {
	header         http.Header
	body           bytes.Buffer
	readDeadlines  []time.Time
	writeDeadlines []time.Time
	statusCode     int
}

type webDAVBlockingUploadReader struct {
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
	sent        bool
}

type webDAVBlockingDataEOFReader struct {
	data        []byte
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
	sent        bool
}

func newWebDAVBlockingDataEOFReader(data string) *webDAVBlockingDataEOFReader {
	return &webDAVBlockingDataEOFReader{
		data:    []byte(data),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *webDAVBlockingDataEOFReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.startOnce.Do(func() { close(r.started) })
	<-r.release
	r.sent = true
	n := copy(p, r.data)
	return n, io.EOF
}

func (r *webDAVBlockingDataEOFReader) Release() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func newWebDAVBlockingUploadReader() *webDAVBlockingUploadReader {
	return &webDAVBlockingUploadReader{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *webDAVBlockingUploadReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.startOnce.Do(func() { close(r.started) })
	<-r.release
	if len(p) == 0 {
		return 0, nil
	}
	r.sent = true
	p[0] = 'x'
	return 1, nil
}

func (r *webDAVBlockingUploadReader) Release() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func (w *webDAVDeadlineRecordingWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *webDAVDeadlineRecordingWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *webDAVDeadlineRecordingWriter) Write(p []byte) (int, error) {
	return w.body.Write(p)
}

func (w *webDAVDeadlineRecordingWriter) SetReadDeadline(deadline time.Time) error {
	w.readDeadlines = append(w.readDeadlines, deadline)
	return nil
}

func (w *webDAVDeadlineRecordingWriter) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadlines = append(w.writeDeadlines, deadline)
	return nil
}

func TestHandlerTransferIdleDeadlinesWrapPutBodyAndResponse(t *testing.T) {
	handler := NewHandler(Config{
		AuthType:     "none",
		ReadTimeout:  time.Minute,
		WriteTimeout: 2 * time.Minute,
	})
	t.Cleanup(handler.Close)
	request := httptest.NewRequest(http.MethodPut, "/dav/file.txt", strings.NewReader("content"))
	writer := &webDAVDeadlineRecordingWriter{}

	wrappedWriter := handler.withTransferIdleDeadlines(writer, request)
	if got, err := io.ReadAll(request.Body); err != nil || string(got) != "content" {
		t.Fatalf("ReadAll(request body) = %q, %v; want content, nil", got, err)
	}
	wrappedWriter.WriteHeader(http.StatusCreated)

	if len(writer.readDeadlines) == 0 {
		t.Fatal("PUT body did not refresh the read deadline")
	}
	if len(writer.writeDeadlines) == 0 {
		t.Fatal("response did not refresh the write deadline")
	}
	if got := writer.writeDeadlines[0]; !got.IsZero() {
		t.Fatalf("initial write deadline = %v, want cleared deadline", got)
	}
	now := time.Now()
	if got := writer.readDeadlines[0]; got.Before(now.Add(50*time.Second)) || got.After(now.Add(70*time.Second)) {
		t.Fatalf("first read deadline = %v, want approximately one minute from now", got)
	}
	if got := writer.writeDeadlines[len(writer.writeDeadlines)-1]; got.Before(now.Add(110*time.Second)) || got.After(now.Add(130*time.Second)) {
		t.Fatalf("last write deadline = %v, want approximately two minutes from now", got)
	}
}

func TestHandlerTransferIdleDeadlinesLeaveNonPutBodyOnServerDeadline(t *testing.T) {
	handler := NewHandler(Config{
		AuthType:     "none",
		ReadTimeout:  time.Minute,
		WriteTimeout: time.Minute,
	})
	t.Cleanup(handler.Close)
	request := httptest.NewRequest("PROPFIND", "/dav/", strings.NewReader("body"))
	writer := &webDAVDeadlineRecordingWriter{}

	handler.withTransferIdleDeadlines(writer, request)
	if _, err := io.ReadAll(request.Body); err != nil {
		t.Fatalf("ReadAll(request body) error = %v", err)
	}
	if len(writer.readDeadlines) != 0 {
		t.Fatalf("PROPFIND read deadlines = %v, want none", writer.readDeadlines)
	}
}

func TestHandlerServeHTTPRefreshesWriteDeadlineBeforeResponse(t *testing.T) {
	handler := NewHandler(Config{
		Prefix:       "/dav",
		AuthType:     "none",
		WriteTimeout: time.Minute,
	})
	t.Cleanup(handler.Close)
	request := httptest.NewRequest("OPTIONS", "/dav/", nil)
	writer := &webDAVDeadlineRecordingWriter{}

	handler.ServeHTTP(writer, request)

	if writer.statusCode != http.StatusOK {
		t.Fatalf("OPTIONS status = %d, want %d", writer.statusCode, http.StatusOK)
	}
	if len(writer.writeDeadlines) == 0 {
		t.Fatal("OPTIONS response did not refresh the write deadline")
	}
}

func TestHandlerMapsWriteConcurrencyErrors(t *testing.T) {
	handler := NewHandler(Config{AuthType: "none"})
	t.Cleanup(handler.Close)
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantRetry  string
		wantBody   string
	}{
		{
			name:       "staging capacity",
			err:        errors.Join(errors.New("stage upload"), storage.ErrWriteBusy),
			wantStatus: http.StatusServiceUnavailable,
			wantRetry:  "1",
			wantBody:   "write capacity is busy, retry later",
		},
		{
			name:       "target conflict",
			err:        errors.Join(errors.New("publish upload"), storage.ErrWriteConflict),
			wantStatus: http.StatusConflict,
			wantBody:   "resource changed during write",
		},
		{
			name:       "write recovery required",
			err:        errors.Join(errors.New("rollback upload"), storage.ErrWriteRecoveryRequired),
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "storage write recovery is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			handler.handleError(writer, test.err)

			if writer.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", writer.Code, test.wantStatus, writer.Body.String())
			}
			if got := writer.Header().Get("Retry-After"); got != test.wantRetry {
				t.Fatalf("Retry-After = %q, want %q", got, test.wantRetry)
			}
			if !strings.Contains(writer.Body.String(), test.wantBody) {
				t.Fatalf("body = %q, want substring %q", writer.Body.String(), test.wantBody)
			}
		})
	}
}

func TestHandlerPutReturnsBusyWhenWriteStagingIsFull(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	if err := fs.Mkdir(t.Context(), "/upload"); err != nil {
		t.Fatalf("Mkdir(/upload) error = %v", err)
	}

	const admittedUploads = 4
	ctx := t.Context()
	readers := make([]*webDAVBlockingUploadReader, admittedUploads)
	results := make(chan error, admittedUploads)
	for i := range readers {
		readers[i] = newWebDAVBlockingUploadReader()
		t.Cleanup(readers[i].Release)
		go func() {
			results <- fs.WriteFile(ctx, "/upload/admitted-"+string(rune('a'+i))+".bin", readers[i])
		}()
		<-readers[i].started
	}

	request := httptest.NewRequest(http.MethodPut, "/dav/upload/rejected.bin", strings.NewReader("rejected"))
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, request)

	if writer.Code != http.StatusServiceUnavailable {
		t.Fatalf("staging-full PUT status = %d, want %d; body=%s", writer.Code, http.StatusServiceUnavailable, writer.Body.String())
	}
	if got := writer.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}

	for _, reader := range readers {
		reader.Release()
	}
	for range readers {
		if err := <-results; err != nil {
			t.Fatalf("admitted WriteFile() error = %v", err)
		}
	}
}

func TestHandlerPutReturnsConflictWhenTargetChangesDuringBodyRead(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := t.Context()
	if err := fs.Mkdir(ctx, "/upload"); err != nil {
		t.Fatalf("Mkdir(/upload) error = %v", err)
	}
	if err := fs.WriteFile(ctx, "/upload/file.txt", strings.NewReader("original")); err != nil {
		t.Fatalf("initial WriteFile() error = %v", err)
	}

	reader := newWebDAVBlockingUploadReader()
	t.Cleanup(reader.Release)
	request := httptest.NewRequest(http.MethodPut, "/dav/upload/file.txt", reader)
	writer := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(writer, request)
		close(done)
	}()
	<-reader.started

	if err := fs.WriteFile(ctx, "/upload/file.txt", strings.NewReader("newer")); err != nil {
		t.Fatalf("concurrent WriteFile() error = %v", err)
	}
	reader.Release()
	<-done

	if writer.Code != http.StatusConflict {
		t.Fatalf("conflicting PUT status = %d, want %d; body=%s", writer.Code, http.StatusConflict, writer.Body.String())
	}
	if !strings.Contains(writer.Body.String(), "resource changed during write") {
		t.Fatalf("conflicting PUT body = %q", writer.Body.String())
	}
	stored, err := fs.OpenFile(ctx, "/upload/file.txt")
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer stored.Close()
	data, err := io.ReadAll(stored)
	if err != nil {
		t.Fatalf("ReadAll(stored file) error = %v", err)
	}
	if got := string(data); got != "newer" {
		t.Fatalf("stored content = %q, want newer", got)
	}
}

func TestHandlerPutDoesNotHoldHierarchyLockWhileReadingBody(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := t.Context()
	if err := fs.Mkdir(ctx, "/upload"); err != nil {
		t.Fatalf("Mkdir(/upload) error = %v", err)
	}
	if err := fs.WriteFile(ctx, "/upload/file.txt", strings.NewReader("original")); err != nil {
		t.Fatalf("initial WriteFile() error = %v", err)
	}

	reader := newWebDAVBlockingUploadReader()
	t.Cleanup(reader.Release)
	putRequest := httptest.NewRequest(http.MethodPut, "/dav/upload/file.txt", reader)
	putWriter := httptest.NewRecorder()
	putDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(putWriter, putRequest)
		close(putDone)
	}()
	<-reader.started

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/dav/upload/file.txt", nil)
	deleteWriter := httptest.NewRecorder()
	deleteDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(deleteWriter, deleteRequest)
		close(deleteDone)
	}()

	select {
	case <-deleteDone:
		if deleteWriter.Code != http.StatusNoContent {
			t.Fatalf("concurrent DELETE status = %d, want %d; body=%s", deleteWriter.Code, http.StatusNoContent, deleteWriter.Body.String())
		}
	case <-time.After(2 * time.Second):
		reader.Release()
		<-putDone
		<-deleteDone
		t.Fatal("concurrent DELETE remained blocked while PUT waited for request body")
	}

	reader.Release()
	<-putDone
	if putWriter.Code != http.StatusConflict {
		t.Fatalf("PUT after concurrent DELETE status = %d, want %d; body=%s", putWriter.Code, http.StatusConflict, putWriter.Body.String())
	}
}

func TestHandlerPutRechecksDAVLockWhenReaderReturnsDataAndEOF(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := t.Context()
	if err := fs.Mkdir(ctx, "/upload"); err != nil {
		t.Fatalf("Mkdir(/upload) error = %v", err)
	}
	if err := fs.WriteFile(ctx, "/upload/file.txt", strings.NewReader("original")); err != nil {
		t.Fatalf("initial WriteFile() error = %v", err)
	}

	reader := newWebDAVBlockingDataEOFReader("replacement")
	t.Cleanup(reader.Release)
	request := httptest.NewRequest(http.MethodPut, "/dav/upload/file.txt", reader)
	writer := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(writer, request)
		close(done)
	}()
	<-reader.started

	handler.locksMu.Lock()
	handler.locks["/upload/file.txt"] = webdavLock{
		token:     "opaquelocktoken:concurrent",
		depth:     webdavLockDepthZero,
		expiresAt: time.Now().Add(time.Hour),
	}
	handler.locksMu.Unlock()

	reader.Release()
	<-done

	if writer.Code != http.StatusLocked {
		t.Fatalf("PUT after concurrent DAV lock status = %d, want %d; body=%s", writer.Code, http.StatusLocked, writer.Body.String())
	}
	stored, err := fs.OpenFile(ctx, "/upload/file.txt")
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer stored.Close()
	data, err := io.ReadAll(stored)
	if err != nil {
		t.Fatalf("ReadAll(stored file) error = %v", err)
	}
	if got := string(data); got != "original" {
		t.Fatalf("stored content = %q, want original", got)
	}
}

func TestHandlerPutCommitBoundaryPreservesDepthZeroNamespaceSemantics(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := t.Context()
	if err := fs.Mkdir(ctx, "/locked-parent"); err != nil {
		t.Fatalf("Mkdir(/locked-parent) error = %v", err)
	}
	if err := fs.WriteFile(ctx, "/locked-parent/existing.txt", strings.NewReader("original")); err != nil {
		t.Fatalf("WriteFile(existing) error = %v", err)
	}
	handler.locksMu.Lock()
	handler.locks["/locked-parent"] = webdavLock{
		token:     "opaquelocktoken:depth-zero-parent",
		depth:     webdavLockDepthZero,
		expiresAt: time.Now().Add(time.Hour),
	}
	handler.locksMu.Unlock()

	reader := newWebDAVBlockingDataEOFReader("replacement")
	t.Cleanup(reader.Release)
	request := httptest.NewRequest(http.MethodPut, "/dav/locked-parent/existing.txt", reader)
	writer := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(writer, request)
		close(done)
	}()
	<-reader.started

	reader.Release()
	<-done

	if writer.Code != http.StatusNoContent {
		t.Fatalf("PUT existing child under depth-zero collection lock status = %d, want %d; body=%s", writer.Code, http.StatusNoContent, writer.Body.String())
	}
	stored, err := fs.OpenFile(ctx, "/locked-parent/existing.txt")
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer stored.Close()
	data, err := io.ReadAll(stored)
	if err != nil {
		t.Fatalf("ReadAll(stored file) error = %v", err)
	}
	if got := string(data); got != "replacement" {
		t.Fatalf("stored content = %q, want replacement", got)
	}
}

func TestHandlerPutRechecksWritePermissionAtCommitBoundary(t *testing.T) {
	handler, fs := setupUsersModeHandler(t, map[string]webDAVTestCredential{
		"alice": {
			password: "password123",
			identity: UserIdentity{
				Role:    webDAVRoleUser,
				HomeDir: "/users/alice",
			},
		},
	})
	ctx := t.Context()
	for _, directory := range []string{"/users", "/users/alice"} {
		if err := fs.Mkdir(ctx, directory); err != nil {
			t.Fatalf("Mkdir(%s) error = %v", directory, err)
		}
	}
	if err := fs.WriteFile(ctx, "/users/alice/file.txt", strings.NewReader("original")); err != nil {
		t.Fatalf("initial WriteFile() error = %v", err)
	}

	reader := newWebDAVBlockingDataEOFReader("replacement")
	t.Cleanup(reader.Release)
	request := httptest.NewRequest(http.MethodPut, "/dav/file.txt", reader)
	setWebDAVTestBasicAuth(request, "alice")
	writer := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(writer, request)
		close(done)
	}()
	<-reader.started

	handler.userAuthenticator = func(context.Context, string, string) (*UserIdentity, error) {
		return &UserIdentity{
			Username: "alice",
			Role:     webDAVRoleGuest,
			HomeDir:  "/users/alice",
		}, nil
	}
	reader.Release()
	<-done

	if writer.Code != http.StatusForbidden {
		t.Fatalf("PUT after permission revocation status = %d, want %d; body=%s", writer.Code, http.StatusForbidden, writer.Body.String())
	}
	stored, err := fs.OpenFile(ctx, "/users/alice/file.txt")
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer stored.Close()
	data, err := io.ReadAll(stored)
	if err != nil {
		t.Fatalf("ReadAll(stored file) error = %v", err)
	}
	if got := string(data); got != "original" {
		t.Fatalf("stored content = %q, want original", got)
	}
}

func TestHandlerUnknownLengthPutReservesInChunks(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := t.Context()
	if err := fs.Mkdir(ctx, "/team"); err != nil {
		t.Fatalf("Mkdir(/team) error = %v", err)
	}
	handler.directoryQuotas = []DirectoryQuota{{
		Path:       "/team",
		QuotaBytes: 2 * webDAVQuotaGrowthChunk,
	}}

	slowReader := newWebDAVBlockingUploadReader()
	t.Cleanup(slowReader.Release)
	slowRequest := httptest.NewRequest(http.MethodPut, "/dav/team/slow.bin", slowReader)
	slowRequest.ContentLength = -1
	slowWriter := httptest.NewRecorder()
	slowDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(slowWriter, slowRequest)
		close(slowDone)
	}()
	<-slowReader.started

	fastRequest := httptest.NewRequest(http.MethodPut, "/dav/team/fast.bin", strings.NewReader("y"))
	fastRequest.ContentLength = webDAVQuotaGrowthChunk / 2
	fastWriter := httptest.NewRecorder()
	fastDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(fastWriter, fastRequest)
		close(fastDone)
	}()

	select {
	case <-fastDone:
		if fastWriter.Code != http.StatusCreated {
			t.Fatalf("concurrent known-length PUT status = %d, want %d; body=%s", fastWriter.Code, http.StatusCreated, fastWriter.Body.String())
		}
	case <-time.After(2 * time.Second):
		slowReader.Release()
		<-slowDone
		<-fastDone
		t.Fatal("unknown-length PUT reserved the entire quota or kept the hierarchy lock")
	}

	slowReader.Release()
	<-slowDone
	if slowWriter.Code != http.StatusCreated {
		t.Fatalf("unknown-length PUT status = %d, want %d; body=%s", slowWriter.Code, http.StatusCreated, slowWriter.Body.String())
	}
}

func TestHandlerCanceledUnknownLengthPutReleasesReservations(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := t.Context()
	if err := fs.Mkdir(ctx, "/team"); err != nil {
		t.Fatalf("Mkdir(/team) error = %v", err)
	}
	handler.directoryQuotas = []DirectoryQuota{{
		Path:       "/team",
		QuotaBytes: webDAVQuotaGrowthChunk,
	}}

	slowReader := newWebDAVBlockingUploadReader()
	t.Cleanup(slowReader.Release)
	requestContext, cancel := context.WithCancel(ctx)
	slowRequest := httptest.NewRequest(http.MethodPut, "/dav/team/canceled.bin", slowReader).WithContext(requestContext)
	slowRequest.ContentLength = -1
	slowWriter := httptest.NewRecorder()
	slowDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(slowWriter, slowRequest)
		close(slowDone)
	}()
	<-slowReader.started

	cancel()
	slowReader.Release()
	select {
	case <-slowDone:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled unknown-length PUT did not terminate")
	}

	retryRequest := httptest.NewRequest(http.MethodPut, "/dav/team/retry.bin", strings.NewReader("z"))
	retryRequest.ContentLength = webDAVQuotaGrowthChunk
	retryWriter := httptest.NewRecorder()
	handler.ServeHTTP(retryWriter, retryRequest)
	if retryWriter.Code != http.StatusCreated {
		t.Fatalf("PUT after canceled reservation status = %d, want %d; body=%s", retryWriter.Code, http.StatusCreated, retryWriter.Body.String())
	}
}
