// Package httpstream provides connection deadline helpers for streamed HTTP transfers.
package httpstream

import (
	"errors"
	"io"
	"net/http"
	"time"
)

type writeIdleDeadlineResponseWriter struct {
	http.ResponseWriter
	timeout time.Duration
	now     func() time.Time
}

// NewWriteIdleDeadlineResponseWriter applies timeout as an idle deadline for
// response writes. A non-positive timeout leaves the writer unchanged.
func NewWriteIdleDeadlineResponseWriter(w http.ResponseWriter, timeout time.Duration) http.ResponseWriter {
	if w == nil || timeout <= 0 {
		return w
	}
	wrapper := &writeIdleDeadlineResponseWriter{
		ResponseWriter: w,
		timeout:        timeout,
		now:            time.Now,
	}
	// The server-level WriteTimeout is a fixed stream lifetime in HTTP/2 and
	// can expire before a long-running handler emits its first byte. Clear it
	// when switching this response to sliding idle deadlines.
	_ = wrapper.clearWriteDeadline()
	return wrapper
}

// Unwrap lets http.ResponseController reach the original ResponseWriter.
func (w *writeIdleDeadlineResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *writeIdleDeadlineResponseWriter) WriteHeader(statusCode int) {
	// WriteHeader cannot report a deadline update failure. Proceeding preserves
	// the ResponseWriter contract and lets the underlying write report failures.
	_ = w.refreshWriteDeadline()
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *writeIdleDeadlineResponseWriter) Write(p []byte) (int, error) {
	if err := w.refreshWriteDeadline(); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}

func (w *writeIdleDeadlineResponseWriter) Flush() {
	_ = w.FlushError()
}

func (w *writeIdleDeadlineResponseWriter) FlushError() error {
	if err := w.refreshWriteDeadline(); err != nil {
		return err
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (w *writeIdleDeadlineResponseWriter) refreshWriteDeadline() error {
	now := w.now
	if now == nil {
		now = time.Now
	}
	err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(now().Add(w.timeout))
	if errors.Is(err, http.ErrNotSupported) {
		// Recorders and some middleware do not expose connection deadlines.
		return nil
	}
	return err
}

func (w *writeIdleDeadlineResponseWriter) clearWriteDeadline() error {
	err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Time{})
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

type readIdleDeadlineBody struct {
	io.ReadCloser
	w       http.ResponseWriter
	timeout time.Duration
	now     func() time.Time
}

// NewReadIdleDeadlineBody applies timeout as an idle deadline before each
// request-body read. A non-positive timeout leaves the body unchanged.
func NewReadIdleDeadlineBody(body io.ReadCloser, w http.ResponseWriter, timeout time.Duration) io.ReadCloser {
	if body == nil || w == nil || timeout <= 0 {
		return body
	}
	return &readIdleDeadlineBody{
		ReadCloser: body,
		w:          w,
		timeout:    timeout,
		now:        time.Now,
	}
}

func (b *readIdleDeadlineBody) Read(p []byte) (int, error) {
	if err := b.refreshReadDeadline(); err != nil {
		return 0, err
	}
	return b.ReadCloser.Read(p)
}

func (b *readIdleDeadlineBody) refreshReadDeadline() error {
	now := b.now
	if now == nil {
		now = time.Now
	}
	err := http.NewResponseController(b.w).SetReadDeadline(now().Add(b.timeout))
	if errors.Is(err, http.ErrNotSupported) {
		// Recorders and some middleware do not expose connection deadlines.
		return nil
	}
	return err
}
