package api

import (
	"errors"
	"net/http"
	"time"
)

type downloadWriteDeadlineResponseWriter struct {
	http.ResponseWriter
	timeout time.Duration
	now     func() time.Time
}

// withDownloadWriteDeadline applies the configured server write timeout as an
// idle deadline for a download response. A non-positive timeout keeps the
// ResponseWriter unchanged because net/http uses zero to disable WriteTimeout.
func (s *Server) withDownloadWriteDeadline(w http.ResponseWriter) http.ResponseWriter {
	cfg := s.currentConfig()
	if cfg == nil {
		return w
	}
	return newDownloadWriteDeadlineResponseWriter(w, cfg.Server.WriteTimeout)
}

func newDownloadWriteDeadlineResponseWriter(w http.ResponseWriter, timeout time.Duration) http.ResponseWriter {
	if w == nil || timeout <= 0 {
		return w
	}
	return &downloadWriteDeadlineResponseWriter{
		ResponseWriter: w,
		timeout:        timeout,
		now:            time.Now,
	}
}

// Unwrap lets http.ResponseController reach the original ResponseWriter.
func (w *downloadWriteDeadlineResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *downloadWriteDeadlineResponseWriter) WriteHeader(statusCode int) {
	// WriteHeader cannot report a deadline update failure. Proceeding preserves
	// the ResponseWriter contract and lets the underlying write report failures.
	_ = w.refreshWriteDeadline()
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *downloadWriteDeadlineResponseWriter) Write(p []byte) (int, error) {
	if err := w.refreshWriteDeadline(); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}

func (w *downloadWriteDeadlineResponseWriter) Flush() {
	_ = w.FlushError()
}

func (w *downloadWriteDeadlineResponseWriter) FlushError() error {
	if err := w.refreshWriteDeadline(); err != nil {
		return err
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (w *downloadWriteDeadlineResponseWriter) refreshWriteDeadline() error {
	now := w.now
	if now == nil {
		now = time.Now
	}
	err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(now().Add(w.timeout))
	if errors.Is(err, http.ErrNotSupported) {
		// ResponseRecorders and some middleware do not expose connection
		// deadlines. Downloads must retain their normal behavior in that case.
		return nil
	}
	return err
}
