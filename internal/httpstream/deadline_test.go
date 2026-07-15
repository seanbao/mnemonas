package httpstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

type deadlineTestClock struct {
	now time.Time
}

func (c *deadlineTestClock) Now() time.Time {
	return c.now
}

func (c *deadlineTestClock) Advance(elapsed time.Duration) {
	c.now = c.now.Add(elapsed)
}

type deadlineRecordingWriter struct {
	header         http.Header
	body           bytes.Buffer
	clock          *deadlineTestClock
	readDeadline   time.Time
	writeDeadline  time.Time
	readDeadlines  []time.Time
	writeDeadlines []time.Time
	statusCode     int
	flushes        int
	writeDuration  time.Duration
}

func (w *deadlineRecordingWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *deadlineRecordingWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *deadlineRecordingWriter) Write(p []byte) (int, error) {
	if w.writeDuration > 0 {
		w.clock.Advance(w.writeDuration)
	}
	if !w.writeDeadline.IsZero() && !w.clock.Now().Before(w.writeDeadline) {
		return 0, os.ErrDeadlineExceeded
	}
	return w.body.Write(p)
}

func (w *deadlineRecordingWriter) Flush() {
	w.flushes++
}

func (w *deadlineRecordingWriter) SetReadDeadline(deadline time.Time) error {
	w.readDeadline = deadline
	w.readDeadlines = append(w.readDeadlines, deadline)
	return nil
}

func (w *deadlineRecordingWriter) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadline = deadline
	w.writeDeadlines = append(w.writeDeadlines, deadline)
	return nil
}

type deadlineRecordingBody struct {
	reader       *bytes.Reader
	writer       *deadlineRecordingWriter
	clock        *deadlineTestClock
	readDuration time.Duration
	closed       bool
}

func (b *deadlineRecordingBody) Read(p []byte) (int, error) {
	if b.readDuration > 0 {
		b.clock.Advance(b.readDuration)
	}
	if !b.writer.readDeadline.IsZero() && !b.clock.Now().Before(b.writer.readDeadline) {
		return 0, os.ErrDeadlineExceeded
	}
	return b.reader.Read(p)
}

func (b *deadlineRecordingBody) Close() error {
	b.closed = true
	return nil
}

func TestWriteIdleDeadlineResponseWriterRefreshesBeforeOperations(t *testing.T) {
	start := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	clock := &deadlineTestClock{now: start}
	underlying := &deadlineRecordingWriter{clock: clock}
	wrapped := NewWriteIdleDeadlineResponseWriter(underlying, time.Minute)
	deadlineWriter, ok := wrapped.(*writeIdleDeadlineResponseWriter)
	if !ok {
		t.Fatalf("NewWriteIdleDeadlineResponseWriter() type = %T", wrapped)
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
		{},
		start.Add(time.Minute),
		start.Add(70 * time.Second),
		start.Add(90 * time.Second),
	}
	if len(underlying.writeDeadlines) != len(wantDeadlines) {
		t.Fatalf("deadline count = %d, want %d", len(underlying.writeDeadlines), len(wantDeadlines))
	}
	for i, want := range wantDeadlines {
		if got := underlying.writeDeadlines[i]; !got.Equal(want) {
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

func TestWriteIdleDeadlineResponseWriterKeepsSingleWriteBounded(t *testing.T) {
	start := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	clock := &deadlineTestClock{now: start}
	underlying := &deadlineRecordingWriter{
		clock:         clock,
		writeDuration: time.Minute + time.Second,
	}
	wrapped := NewWriteIdleDeadlineResponseWriter(underlying, time.Minute)
	wrapped.(*writeIdleDeadlineResponseWriter).now = clock.Now

	n, err := wrapped.Write([]byte("slow chunk"))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Write() error = %v, want deadline exceeded", err)
	}
	if n != 0 {
		t.Errorf("Write() bytes = %d, want 0", n)
	}
}

func TestReadIdleDeadlineBodyRefreshesBeforeReads(t *testing.T) {
	start := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	clock := &deadlineTestClock{now: start}
	writer := &deadlineRecordingWriter{clock: clock}
	underlying := &deadlineRecordingBody{
		reader: bytes.NewReader([]byte("ab")),
		writer: writer,
		clock:  clock,
	}
	wrapped := NewReadIdleDeadlineBody(underlying, writer, time.Minute)
	deadlineBody, ok := wrapped.(*readIdleDeadlineBody)
	if !ok {
		t.Fatalf("NewReadIdleDeadlineBody() type = %T", wrapped)
	}
	deadlineBody.now = clock.Now

	one := make([]byte, 1)
	if _, err := wrapped.Read(one); err != nil {
		t.Fatalf("first Read() error = %v", err)
	}
	clock.Advance(45 * time.Second)
	if _, err := wrapped.Read(one); err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if err := wrapped.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	wantDeadlines := []time.Time{start.Add(time.Minute), start.Add(105 * time.Second)}
	if len(writer.readDeadlines) != len(wantDeadlines) {
		t.Fatalf("deadline count = %d, want %d", len(writer.readDeadlines), len(wantDeadlines))
	}
	for i, want := range wantDeadlines {
		if got := writer.readDeadlines[i]; !got.Equal(want) {
			t.Errorf("deadline[%d] = %v, want %v", i, got, want)
		}
	}
	if !underlying.closed {
		t.Error("underlying body was not closed")
	}
}

func TestReadIdleDeadlineBodyKeepsSingleReadBounded(t *testing.T) {
	start := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	clock := &deadlineTestClock{now: start}
	writer := &deadlineRecordingWriter{clock: clock}
	underlying := &deadlineRecordingBody{
		reader:       bytes.NewReader([]byte("a")),
		writer:       writer,
		clock:        clock,
		readDuration: time.Minute + time.Second,
	}
	wrapped := NewReadIdleDeadlineBody(underlying, writer, time.Minute)
	wrapped.(*readIdleDeadlineBody).now = clock.Now

	n, err := wrapped.Read(make([]byte, 1))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read() error = %v, want deadline exceeded", err)
	}
	if n != 0 {
		t.Errorf("Read() bytes = %d, want 0", n)
	}
}

func TestIdleDeadlineWrappersAllowUnsupportedController(t *testing.T) {
	writer := httptest.NewRecorder()
	wrappedWriter := NewWriteIdleDeadlineResponseWriter(writer, time.Minute)
	wrappedBody := NewReadIdleDeadlineBody(io.NopCloser(bytes.NewReader([]byte("upload"))), wrappedWriter, time.Minute)

	if got, err := io.ReadAll(wrappedBody); err != nil || string(got) != "upload" {
		t.Fatalf("ReadAll() = %q, %v; want upload, nil", got, err)
	}
	wrappedWriter.WriteHeader(http.StatusCreated)
	if _, err := wrappedWriter.Write([]byte("download")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := http.NewResponseController(wrappedWriter).Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
}

func TestIdleDeadlineWrappersDisabledKeepOriginalValues(t *testing.T) {
	writer := httptest.NewRecorder()
	body := io.NopCloser(bytes.NewReader(nil))
	if got := NewWriteIdleDeadlineResponseWriter(writer, 0); got != writer {
		t.Fatalf("zero timeout writer = %T %p, want original %T %p", got, got, writer, writer)
	}
	if got := NewReadIdleDeadlineBody(body, writer, 0); got != body {
		t.Fatalf("zero timeout body = %T %p, want original %T %p", got, got, body, body)
	}
}

func TestWriteIdleDeadlineResponseWriterUnwrapsWithoutReaderFrom(t *testing.T) {
	underlying := httptest.NewRecorder()
	wrapped := NewWriteIdleDeadlineResponseWriter(underlying, time.Minute)
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

func startDeadlineTestServer(t *testing.T, server *http.Server) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Serve() error = %v", err)
		}
	})
	return "http://" + listener.Addr().String()
}

func startHTTP2DeadlineTestServer(t *testing.T, handler http.Handler, readTimeout, writeTimeout time.Duration) (*httptest.Server, *http.Client) {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.Config.ReadTimeout = readTimeout
	server.Config.WriteTimeout = writeTimeout
	server.StartTLS()
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 4 * time.Second
	return server, client
}

func TestWriteIdleDeadlineAllowsLateFirstResponseOverHTTP2(t *testing.T) {
	const timeout = 300 * time.Millisecond
	server, client := startHTTP2DeadlineTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w = NewWriteIdleDeadlineResponseWriter(w, timeout)
		time.Sleep(2 * timeout)
		_, _ = io.WriteString(w, "late response")
	}), 2*time.Second, timeout)

	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response) error = %v", err)
	}
	if response.ProtoMajor != 2 {
		t.Fatalf("response protocol = %s, want HTTP/2", response.Proto)
	}
	if got := string(data); got != "late response" {
		t.Fatalf("response body = %q, want late response", got)
	}
}

func TestIdleDeadlinesAllowContinuousHTTP2UploadBeyondServerTimeouts(t *testing.T) {
	const timeout = 500 * time.Millisecond
	server, client := startHTTP2DeadlineTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = NewWriteIdleDeadlineResponseWriter(w, timeout)
		body := NewReadIdleDeadlineBody(r.Body, w, timeout)
		data, err := io.ReadAll(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestTimeout)
			return
		}
		_, _ = w.Write(data)
	}), timeout, timeout)

	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	writeDone := make(chan error, 1)
	go func() {
		for _, chunk := range []byte("abcdef") {
			if _, err := writer.Write([]byte{chunk}); err != nil {
				writeDone <- err
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
		writeDone <- writer.Close()
	}()
	request, err := http.NewRequest(http.MethodPost, server.URL, reader)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response) error = %v", err)
	}
	if response.ProtoMajor != 2 {
		t.Fatalf("response protocol = %s, want HTTP/2", response.Proto)
	}
	if response.StatusCode != http.StatusOK || string(data) != "abcdef" {
		t.Fatalf("response = %d %q, want 200 abcdef", response.StatusCode, data)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("stream request body error = %v", err)
	}
}

func TestWriteIdleDeadlineAllowsContinuousResponseBeyondServerTimeout(t *testing.T) {
	const timeout = 600 * time.Millisecond
	server := &http.Server{
		ReadTimeout:  2 * time.Second,
		WriteTimeout: timeout,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w = NewWriteIdleDeadlineResponseWriter(w, timeout)
			for _, chunk := range []byte("abcde") {
				if _, err := w.Write([]byte{chunk}); err != nil {
					return
				}
				if err := http.NewResponseController(w).Flush(); err != nil {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
		}),
	}
	baseURL := startDeadlineTestServer(t, server)
	client := &http.Client{Timeout: 3 * time.Second}

	response, err := client.Get(baseURL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response) error = %v", err)
	}
	if got := string(data); got != "abcde" {
		t.Fatalf("response body = %q, want abcde", got)
	}
}

func TestReadIdleDeadlineAllowsContinuousUploadBeyondServerTimeout(t *testing.T) {
	const timeout = 600 * time.Millisecond
	server := &http.Server{
		ReadTimeout:  timeout,
		WriteTimeout: 2 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := NewReadIdleDeadlineBody(r.Body, w, timeout)
			data, err := io.ReadAll(body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusRequestTimeout)
				return
			}
			_, _ = fmt.Fprintf(w, "%s", data)
		}),
	}
	baseURL := startDeadlineTestServer(t, server)
	reader, writer := io.Pipe()
	writeDone := make(chan error, 1)
	go func() {
		for _, chunk := range []byte("abcde") {
			if _, err := writer.Write([]byte{chunk}); err != nil {
				writeDone <- err
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		writeDone <- writer.Close()
	}()
	request, err := http.NewRequest(http.MethodPost, baseURL, reader)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	client := &http.Client{Timeout: 3 * time.Second}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response) error = %v", err)
	}
	if response.StatusCode != http.StatusOK || string(data) != "abcde" {
		t.Fatalf("response = %d %q, want 200 abcde", response.StatusCode, data)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("stream request body error = %v", err)
	}
}

func TestReadIdleDeadlineRejectsStalledUpload(t *testing.T) {
	const timeout = 200 * time.Millisecond
	server := &http.Server{
		ReadTimeout:  timeout,
		WriteTimeout: 2 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := NewReadIdleDeadlineBody(r.Body, w, timeout)
			if _, err := io.ReadAll(body); err != nil {
				http.Error(w, "upload stalled", http.StatusRequestTimeout)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	baseURL := startDeadlineTestServer(t, server)
	reader, writer := io.Pipe()
	go func() {
		_, _ = writer.Write([]byte("a"))
		time.Sleep(500 * time.Millisecond)
		_, _ = writer.Write([]byte("b"))
		_ = writer.Close()
	}()
	request, err := http.NewRequest(http.MethodPost, baseURL, reader)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	client := &http.Client{Timeout: 2 * time.Second}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestTimeout {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("response = %d %q, want 408", response.StatusCode, data)
	}
}
