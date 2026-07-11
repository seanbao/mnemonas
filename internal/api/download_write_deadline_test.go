package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
)

type downloadDeadlineTestClock struct {
	now time.Time
}

func (c *downloadDeadlineTestClock) Now() time.Time {
	return c.now
}

func (c *downloadDeadlineTestClock) Advance(elapsed time.Duration) {
	c.now = c.now.Add(elapsed)
}

type downloadDeadlineRecordingWriter struct {
	header        http.Header
	body          bytes.Buffer
	clock         *downloadDeadlineTestClock
	deadline      time.Time
	deadlines     []time.Time
	statusCode    int
	flushes       int
	writeDuration time.Duration
}

func (w *downloadDeadlineRecordingWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *downloadDeadlineRecordingWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *downloadDeadlineRecordingWriter) Write(p []byte) (int, error) {
	if w.writeDuration > 0 {
		w.clock.Advance(w.writeDuration)
	}
	if !w.deadline.IsZero() && !w.clock.Now().Before(w.deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	return w.body.Write(p)
}

func (w *downloadDeadlineRecordingWriter) Flush() {
	w.flushes++
}

func (w *downloadDeadlineRecordingWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestDownloadWriteDeadlineResponseWriterRefreshesBeforeOperations(t *testing.T) {
	start := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	clock := &downloadDeadlineTestClock{now: start}
	underlying := &downloadDeadlineRecordingWriter{clock: clock}
	wrapped := newDownloadWriteDeadlineResponseWriter(underlying, time.Minute)
	deadlineWriter, ok := wrapped.(*downloadWriteDeadlineResponseWriter)
	if !ok {
		t.Fatalf("newDownloadWriteDeadlineResponseWriter() type = %T", wrapped)
	}
	deadlineWriter.now = clock.Now

	wrapped.WriteHeader(http.StatusPartialContent)
	clock.Advance(10 * time.Second)
	if _, err := wrapped.Write([]byte("chunk")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	clock.Advance(20 * time.Second)
	if err := http.NewResponseController(wrapped).Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	wantDeadlines := []time.Time{
		start.Add(time.Minute),
		start.Add(70 * time.Second),
		start.Add(90 * time.Second),
	}
	if len(underlying.deadlines) != len(wantDeadlines) {
		t.Fatalf("deadline count = %d, want %d", len(underlying.deadlines), len(wantDeadlines))
	}
	for i, want := range wantDeadlines {
		if got := underlying.deadlines[i]; !got.Equal(want) {
			t.Errorf("deadline[%d] = %v, want %v", i, got, want)
		}
	}
	if underlying.statusCode != http.StatusPartialContent {
		t.Errorf("status code = %d, want %d", underlying.statusCode, http.StatusPartialContent)
	}
	if got := underlying.body.String(); got != "chunk" {
		t.Errorf("body = %q, want chunk", got)
	}
	if underlying.flushes != 1 {
		t.Errorf("flush count = %d, want 1", underlying.flushes)
	}
}

func TestDownloadWriteDeadlineResponseWriterRefreshesAcrossSlowTotalDuration(t *testing.T) {
	start := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	clock := &downloadDeadlineTestClock{now: start}
	underlying := &downloadDeadlineRecordingWriter{clock: clock}
	wrapped := newDownloadWriteDeadlineResponseWriter(underlying, time.Minute)
	wrapped.(*downloadWriteDeadlineResponseWriter).now = clock.Now

	for i := 0; i < 4; i++ {
		if i > 0 {
			clock.Advance(45 * time.Second)
		}
		if _, err := wrapped.Write([]byte{byte('a' + i)}); err != nil {
			t.Fatalf("Write(%d) after %v error = %v", i, clock.Now().Sub(start), err)
		}
	}

	if elapsed := clock.Now().Sub(start); elapsed <= time.Minute {
		t.Fatalf("elapsed duration = %v, want greater than one timeout", elapsed)
	}
	if got := underlying.body.String(); got != "abcd" {
		t.Fatalf("body = %q, want abcd", got)
	}
	if got := underlying.deadlines[len(underlying.deadlines)-1]; !got.Equal(clock.Now().Add(time.Minute)) {
		t.Fatalf("last deadline = %v, want %v", got, clock.Now().Add(time.Minute))
	}
}

func TestDownloadWriteDeadlineResponseWriterKeepsSingleWriteBounded(t *testing.T) {
	start := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	clock := &downloadDeadlineTestClock{now: start}
	underlying := &downloadDeadlineRecordingWriter{
		clock:         clock,
		writeDuration: time.Minute + time.Second,
	}
	wrapped := newDownloadWriteDeadlineResponseWriter(underlying, time.Minute)
	wrapped.(*downloadWriteDeadlineResponseWriter).now = clock.Now

	n, err := wrapped.Write([]byte("slow chunk"))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Write() error = %v, want deadline exceeded", err)
	}
	if n != 0 {
		t.Errorf("Write() bytes = %d, want 0", n)
	}
	if len(underlying.deadlines) != 1 || !underlying.deadlines[0].Equal(start.Add(time.Minute)) {
		t.Fatalf("deadlines = %v, want [%v]", underlying.deadlines, start.Add(time.Minute))
	}
}

func TestDownloadWriteDeadlineResponseWriterAllowsUnsupportedDeadlineController(t *testing.T) {
	underlying := httptest.NewRecorder()
	wrapped := newDownloadWriteDeadlineResponseWriter(underlying, time.Minute)

	wrapped.WriteHeader(http.StatusCreated)
	if _, err := wrapped.Write([]byte("download")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := http.NewResponseController(wrapped).Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if underlying.Code != http.StatusCreated {
		t.Errorf("status code = %d, want %d", underlying.Code, http.StatusCreated)
	}
	if got := underlying.Body.String(); got != "download" {
		t.Errorf("body = %q, want download", got)
	}
	if !underlying.Flushed {
		t.Error("response was not flushed")
	}
}

func TestDownloadWriteDeadlineResponseWriterUnwrapsWithoutReaderFrom(t *testing.T) {
	underlying := httptest.NewRecorder()
	wrapped := newDownloadWriteDeadlineResponseWriter(underlying, time.Minute)
	unwrapper, ok := wrapped.(interface{ Unwrap() http.ResponseWriter })
	if !ok {
		t.Fatalf("wrapped writer %T does not implement Unwrap", wrapped)
	}
	if got := unwrapper.Unwrap(); got != underlying {
		t.Fatalf("Unwrap() = %T %p, want %T %p", got, got, underlying, underlying)
	}
	if _, ok := wrapped.(io.ReaderFrom); ok {
		t.Fatalf("wrapped writer %T unexpectedly implements io.ReaderFrom", wrapped)
	}
}

func TestDownloadWriteDeadlineResponseWriterDisabledTimeoutKeepsWriter(t *testing.T) {
	underlying := httptest.NewRecorder()
	if got := newDownloadWriteDeadlineResponseWriter(underlying, 0); got != underlying {
		t.Fatalf("zero timeout writer = %T %p, want original %T %p", got, got, underlying, underlying)
	}
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
			clock := &downloadDeadlineTestClock{now: current}
			writer := &downloadDeadlineRecordingWriter{
				clock:    clock,
				deadline: current.Add(-time.Second),
			}
			request := httptest.NewRequest(test.method, test.target, nil)

			test.handler(writer, request)

			if writer.statusCode == 0 {
				t.Fatal("handler did not write a response")
			}
			if len(writer.deadlines) == 0 {
				t.Fatal("handler did not refresh the expired write deadline")
			}
			if got := writer.deadlines[len(writer.deadlines)-1]; !got.After(current) {
				t.Fatalf("last deadline = %v, want after %v", got, current)
			}
		})
	}
}
