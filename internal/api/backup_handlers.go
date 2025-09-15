package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/seanbao/mnemonas/internal/backup"
)

func (s *Server) backupService() (*backup.Manager, bool) {
	if s.backupManager == nil {
		return nil, false
	}
	return s.backupManager, true
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.backupService()
	if !ok {
		ServiceUnavailable(w, "backup manager not initialized")
		return
	}

	NewAPIResponse(manager.ListJobs()).Write(w, http.StatusOK)
}

func (s *Server) handleGetBackup(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.backupService()
	if !ok {
		ServiceUnavailable(w, "backup manager not initialized")
		return
	}

	job, err := manager.GetJob(chi.URLParam(r, "id"))
	if err != nil {
		s.writeBackupError(w, "get backup job", err, nil)
		return
	}

	NewAPIResponse(job).Write(w, http.StatusOK)
}

func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.backupService()
	if !ok {
		ServiceUnavailable(w, "backup manager not initialized")
		return
	}
	var req struct{}
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}

	result, err := manager.RunJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeBackupError(w, "run backup job", err, result)
		return
	}

	NewAPIResponse(result).WithMessage("backup completed").Write(w, http.StatusOK)
}

func (s *Server) handleRunBackupRestoreDrill(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.backupService()
	if !ok {
		ServiceUnavailable(w, "backup manager not initialized")
		return
	}

	var req backup.RestoreDrillOptions
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}

	result, err := manager.RunRestoreDrill(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		s.writeBackupError(w, "run backup restore drill", err, result)
		return
	}

	NewAPIResponse(result).WithMessage("restore drill completed").Write(w, http.StatusOK)
}

func (s *Server) handleRunBackupRestore(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.backupService()
	if !ok {
		ServiceUnavailable(w, "backup manager not initialized")
		return
	}

	var req backup.RestoreOptions
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}

	result, err := manager.RunRestore(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		s.writeBackupError(w, "run backup restore", err, result)
		return
	}

	NewAPIResponse(result).WithMessage("restore completed").Write(w, http.StatusOK)
}

func (s *Server) handlePreviewBackupRestore(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.backupService()
	if !ok {
		ServiceUnavailable(w, "backup manager not initialized")
		return
	}

	var req backup.RestorePreviewOptions
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}

	result, err := manager.RunRestorePreview(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		s.writeBackupError(w, "preview backup restore", err, result)
		return
	}

	NewAPIResponse(result).WithMessage("restore preview completed").Write(w, http.StatusOK)
}

func (s *Server) handleVerifyBackupRestore(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.backupService()
	if !ok {
		ServiceUnavailable(w, "backup manager not initialized")
		return
	}

	var req backup.RestoreVerifyOptions
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}

	result, err := manager.RunRestoreVerify(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		s.writeBackupError(w, "verify backup restore", err, result)
		return
	}

	NewAPIResponse(result).WithMessage("restore target verified").Write(w, http.StatusOK)
}

func (s *Server) writeBackupError(w http.ResponseWriter, operation string, err error, details any) {
	switch {
	case errors.Is(err, backup.ErrJobNotFound):
		NotFound(w, "backup job not found")
	case errors.Is(err, backup.ErrJobAlreadyRunning):
		Conflict(w, "backup job is already running")
	case errors.Is(err, backup.ErrJobDisabled):
		Conflict(w, "backup job is disabled")
	case errors.Is(err, backup.ErrNoSnapshots):
		Conflict(w, "backup job has no completed snapshots")
	case errors.Is(err, backup.ErrRestoreTargetExists):
		Conflict(w, "backup restore target already exists")
	case errors.Is(err, backup.ErrUnsupportedJobType), errors.Is(err, backup.ErrUnsafePath):
		BadRequest(w, err.Error())
	default:
		s.logger.Error().Err(err).Str("operation", operation).Msg("backup operation failed")
		apiErr := NewAPIError(ErrCodeInternal, "backup operation failed")
		if details != nil {
			apiErr.WithDetails(details)
		}
		apiErr.Write(w, http.StatusInternalServerError)
	}
}

func decodeOptionalJSONBody(r *http.Request, dst any) error {
	body, err := readBufferedRequestBody(r, DefaultJSONRequestBodyLimit)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}

	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing data")
		}
		return err
	}
	return nil
}
