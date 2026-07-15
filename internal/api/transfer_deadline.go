package api

import (
	"net/http"

	"github.com/seanbao/mnemonas/internal/httpstream"
)

// withDownloadWriteDeadline applies the configured server write timeout as an
// idle deadline for a download response.
func (s *Server) withDownloadWriteDeadline(w http.ResponseWriter) http.ResponseWriter {
	cfg := s.currentConfig()
	if cfg == nil {
		return w
	}
	return httpstream.NewWriteIdleDeadlineResponseWriter(w, cfg.Server.WriteTimeout)
}

// withUploadIdleDeadlines refreshes the connection read deadline before each
// request-body read and the write deadline before each response write. The API
// request timeout remains the total lifetime bound for the upload.
func (s *Server) withUploadIdleDeadlines(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	cfg := s.currentConfig()
	if cfg == nil {
		return w
	}
	w = httpstream.NewWriteIdleDeadlineResponseWriter(w, cfg.Server.WriteTimeout)
	if r != nil {
		r.Body = httpstream.NewReadIdleDeadlineBody(r.Body, w, cfg.Server.ReadTimeout)
	}
	return w
}
