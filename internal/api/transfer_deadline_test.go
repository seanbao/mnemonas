package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/storage"
)

type apiDeadlineRecordingWriter struct {
	header         http.Header
	body           bytes.Buffer
	readDeadlines  []time.Time
	writeDeadlines []time.Time
	statusCode     int
}

type apiBlockingUploadReader struct {
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
	sent        bool
}

func newAPIBlockingUploadReader() *apiBlockingUploadReader {
	return &apiBlockingUploadReader{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *apiBlockingUploadReader) Read(p []byte) (int, error) {
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

func (r *apiBlockingUploadReader) Release() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func (w *apiDeadlineRecordingWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *apiDeadlineRecordingWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *apiDeadlineRecordingWriter) Write(p []byte) (int, error) {
	return w.body.Write(p)
}

func (w *apiDeadlineRecordingWriter) SetReadDeadline(deadline time.Time) error {
	w.readDeadlines = append(w.readDeadlines, deadline)
	return nil
}

func (w *apiDeadlineRecordingWriter) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadlines = append(w.writeDeadlines, deadline)
	return nil
}

func TestDownloadHandlersRefreshExpiredDeadlineBeforeFirstResponse(t *testing.T) {
	current := time.Now()
	cfg := config.Default()
	cfg.Server.WriteTimeout = time.Minute
	cfg.Share.Enabled = false
	server := &Server{config: cfg}

	tests := []struct {
		name    string
		method  string
		target  string
		handler http.HandlerFunc
	}{
		{name: "authenticated file", method: http.MethodGet, target: "/api/v1/download", handler: server.handleDownloadFile},
		{name: "public ticket", method: http.MethodPost, target: "/api/v1/public/shares/share-id/download-ticket", handler: server.handleCreateDownloadTicket},
		{name: "public root", method: http.MethodGet, target: "/api/v1/public/shares/share-id/download", handler: server.handleDownloadShare},
		{name: "public file", method: http.MethodGet, target: "/api/v1/public/shares/share-id/download/file.txt", handler: server.handleDownloadShareFile},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &apiDeadlineRecordingWriter{}
			request := httptest.NewRequest(test.method, test.target, nil)

			test.handler(writer, request)

			if writer.statusCode == 0 {
				t.Fatal("handler did not write a response")
			}
			if len(writer.writeDeadlines) == 0 {
				t.Fatal("handler did not refresh the write deadline")
			}
			if got := writer.writeDeadlines[len(writer.writeDeadlines)-1]; !got.After(current) {
				t.Fatalf("last deadline = %v, want after %v", got, current)
			}
		})
	}
}

func TestUploadHandlerRefreshesReadAndWriteDeadlines(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	if err := fs.Mkdir(context.Background(), "/upload"); err != nil {
		t.Fatalf("Mkdir(/upload) error = %v", err)
	}
	cfg := server.currentConfig()
	cfg.Server.ReadTimeout = time.Minute
	cfg.Server.WriteTimeout = 2 * time.Minute
	server.storeConfig(cfg)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload/file.txt", strings.NewReader("content"))
	writer := &apiDeadlineRecordingWriter{}
	server.handleUploadFile(writer, request)

	if writer.statusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d; body=%s", writer.statusCode, http.StatusCreated, writer.body.String())
	}
	if len(writer.readDeadlines) == 0 {
		t.Fatal("upload did not refresh the read deadline")
	}
	if len(writer.writeDeadlines) == 0 {
		t.Fatal("upload did not refresh the write deadline")
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

func TestRespondStreamedWriteStateError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantRetry  string
		wantCode   string
	}{
		{
			name:       "staging capacity",
			err:        errors.Join(errors.New("stage upload"), storage.ErrWriteBusy),
			wantStatus: http.StatusTooManyRequests,
			wantRetry:  "1",
			wantCode:   ErrCodeWriteBusy,
		},
		{
			name:       "target conflict",
			err:        errors.Join(errors.New("publish upload"), storage.ErrWriteConflict),
			wantStatus: http.StatusConflict,
			wantCode:   ErrCodeConflict,
		},
		{
			name:       "write recovery required",
			err:        errors.Join(errors.New("rollback upload"), storage.ErrWriteRecoveryRequired),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   ErrCodeServiceUnavail,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			if handled := respondStreamedWriteStateError(writer, test.err); !handled {
				t.Fatal("respondStreamedWriteStateError() = false, want true")
			}
			if writer.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", writer.Code, test.wantStatus, writer.Body.String())
			}
			if got := writer.Header().Get("Retry-After"); got != test.wantRetry {
				t.Fatalf("Retry-After = %q, want %q", got, test.wantRetry)
			}
			if !strings.Contains(writer.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("body = %s, want code %s", writer.Body.String(), test.wantCode)
			}
		})
	}

	writer := httptest.NewRecorder()
	if handled := respondStreamedWriteStateError(writer, errors.New("other")); handled {
		t.Fatal("unrecognized error was handled")
	}
}

func TestUploadHandlerReturnsBusyWhenWriteStagingIsFull(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	if err := fs.Mkdir(context.Background(), "/upload"); err != nil {
		t.Fatalf("Mkdir(/upload) error = %v", err)
	}

	const admittedUploads = 4
	readers := make([]*apiBlockingUploadReader, admittedUploads)
	results := make(chan *httptest.ResponseRecorder, admittedUploads)
	for i := range readers {
		readers[i] = newAPIBlockingUploadReader()
		t.Cleanup(readers[i].Release)
		request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/files/upload/admitted-%d.bin", i), readers[i])
		writer := httptest.NewRecorder()
		go func() {
			server.handleUploadFile(writer, request)
			results <- writer
		}()
		<-readers[i].started
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload/rejected.bin", strings.NewReader("rejected"))
	writer := httptest.NewRecorder()
	server.handleUploadFile(writer, request)

	if writer.Code != http.StatusTooManyRequests {
		t.Fatalf("staging-full upload status = %d, want %d; body=%s", writer.Code, http.StatusTooManyRequests, writer.Body.String())
	}
	if got := writer.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	if !strings.Contains(writer.Body.String(), `"code":"`+ErrCodeWriteBusy+`"`) {
		t.Fatalf("body = %s, want code %s", writer.Body.String(), ErrCodeWriteBusy)
	}

	for _, reader := range readers {
		reader.Release()
	}
	for range readers {
		result := <-results
		if result.Code != http.StatusCreated {
			t.Fatalf("admitted upload status = %d, want %d; body=%s", result.Code, http.StatusCreated, result.Body.String())
		}
	}
}

func TestUploadHandlerReturnsConflictWhenTargetChangesDuringBodyRead(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/upload"); err != nil {
		t.Fatalf("Mkdir(/upload) error = %v", err)
	}
	if err := fs.WriteFile(ctx, "/upload/file.txt", strings.NewReader("original")); err != nil {
		t.Fatalf("initial WriteFile() error = %v", err)
	}

	reader := newAPIBlockingUploadReader()
	t.Cleanup(reader.Release)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload/file.txt", reader)
	writer := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.handleUploadFile(writer, request)
		close(done)
	}()
	<-reader.started

	if err := fs.WriteFile(ctx, "/upload/file.txt", strings.NewReader("newer")); err != nil {
		t.Fatalf("concurrent WriteFile() error = %v", err)
	}
	reader.Release()
	<-done

	if writer.Code != http.StatusConflict {
		t.Fatalf("conflicting upload status = %d, want %d; body=%s", writer.Code, http.StatusConflict, writer.Body.String())
	}
	if !strings.Contains(writer.Body.String(), `"code":"`+ErrCodeConflict+`"`) {
		t.Fatalf("body = %s, want code %s", writer.Body.String(), ErrCodeConflict)
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
