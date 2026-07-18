package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/activity"
	quotareservation "github.com/seanbao/mnemonas/internal/quota"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/uploadsession"
)

const (
	uploadOffsetHeader             = "Upload-Offset"
	uploadLengthHeader             = "Upload-Length"
	uploadChunkIDHeader            = "Upload-Chunk-ID"
	uploadChunkSHA256Header        = "X-MnemoNAS-Chunk-SHA256"
	uploadSessionMinNonFinalBytes  = int64(1024 * 1024)
	uploadSessionCleanupInterval   = time.Hour
	uploadSessionTransitionTimeout = 30 * time.Second
)

var (
	errUploadSessionInsufficientStorage = errors.New("upload session physical capacity is insufficient")
	errUploadSessionRecoveryTargetDrift = errors.New("upload target changed during recovery validation")
)

type uploadSessionCommitLock struct {
	token chan struct{}
	refs  int
}

type uploadSessionLimitedBody struct {
	io.ReadCloser
	maxBytesErr *http.MaxBytesError
}

func (b *uploadSessionLimitedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		b.maxBytesErr = maxBytesErr
	}
	return n, err
}

func newUploadSessionStagingCapacityCheck(
	logger zerolog.Logger,
	fs *storage.FileSystem,
	minFreeSpace uint64,
) func(context.Context, int64) error {
	return func(ctx context.Context, additionalBytes int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if fs == nil || additionalBytes <= 0 {
			return errors.Join(
				errUploadSessionInsufficientStorage,
				errors.New("upload session filesystem capacity is unavailable"),
			)
		}
		stats, err := getDiskStats(fs)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to inspect upload session staging capacity")
			return errors.Join(errUploadSessionInsufficientStorage, err)
		}
		if stats == nil {
			err := errors.New("disk statistics are unavailable")
			logger.Error().Err(err).Msg("Failed to inspect upload session staging capacity")
			return errors.Join(errUploadSessionInsufficientStorage, err)
		}
		additional := uint64(additionalBytes)
		if stats.AvailableBytes < additional ||
			stats.AvailableBytes-additional < minFreeSpace {
			return errUploadSessionInsufficientStorage
		}
		return nil
	}
}

type createUploadSessionRequest struct {
	Path            string `json:"path"`
	TotalBytes      int64  `json:"total_bytes"`
	ClientRequestID string `json:"client_request_id"`
}

type uploadSessionResponse struct {
	ID                 string              `json:"id"`
	Path               string              `json:"path"`
	State              uploadsession.State `json:"state"`
	DurableOffset      int64               `json:"durable_offset"`
	TotalBytes         int64               `json:"total_bytes"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
	ExpiresAt          time.Time           `json:"expires_at"`
	ContentBLAKE3      *string             `json:"content_blake3"`
	PersistenceWarning bool                `json:"persistence_warning"`
}

func (s *Server) acquireUploadSessionCommitLock(ctx context.Context, id string) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.uploadSessionCommitMu.Lock()
	if s.uploadSessionCommitLocks == nil {
		s.uploadSessionCommitLocks = make(map[string]*uploadSessionCommitLock)
	}
	lock := s.uploadSessionCommitLocks[id]
	if lock == nil {
		lock = &uploadSessionCommitLock{token: make(chan struct{}, 1)}
		lock.token <- struct{}{}
		s.uploadSessionCommitLocks[id] = lock
	}
	lock.refs++
	s.uploadSessionCommitMu.Unlock()

	select {
	case <-ctx.Done():
		s.releaseUploadSessionCommitLockReference(id, lock)
		return nil, ctx.Err()
	case <-lock.token:
	}
	var releaseOnce sync.Once
	return func() {
		releaseOnce.Do(func() {
			lock.token <- struct{}{}
			s.releaseUploadSessionCommitLockReference(id, lock)
		})
	}, nil
}

func (s *Server) releaseUploadSessionCommitLockReference(id string, lock *uploadSessionCommitLock) {
	s.uploadSessionCommitMu.Lock()
	defer s.uploadSessionCommitMu.Unlock()
	lock.refs--
	if lock.refs == 0 && s.uploadSessionCommitLocks[id] == lock {
		delete(s.uploadSessionCommitLocks, id)
	}
}

func (s *Server) startUploadSessionCleanupScheduler(parent context.Context) bool {
	if s == nil || s.uploadSessions == nil || s.uploadSessionCleanupCancel != nil {
		return false
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.uploadSessionCleanupCancel = cancel
	s.uploadSessionCleanupDone = done
	go func() {
		defer close(done)
		s.cleanupExpiredUploadSessions(ctx)
		ticker := time.NewTicker(uploadSessionCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runUploadSessionReconciliation(ctx)
				s.cleanupExpiredUploadSessions(ctx)
			}
		}
	}()
	return true
}

func (s *Server) runUploadSessionReconciliation(ctx context.Context) {
	reconciled, err := s.reconcileInterruptedUploadSessions(ctx)
	if reconciled > 0 {
		s.logger.Info().Int("count", reconciled).Msg("Interrupted upload sessions reconciled")
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn().Err(err).Msg("Failed to reconcile interrupted upload sessions")
	}
}

func (s *Server) reconcileInterruptedUploadSessions(ctx context.Context) (int, error) {
	sessions, err := s.uploadSessions.ListCommitting(ctx)
	if err != nil {
		return 0, err
	}
	reconciled := 0
	var reconcileErr error
	for _, candidate := range sessions {
		if err := ctx.Err(); err != nil {
			return reconciled, errors.Join(reconcileErr, err)
		}
		release, err := s.acquireUploadSessionCommitLock(ctx, candidate.ID)
		if err != nil {
			return reconciled, errors.Join(reconcileErr, err)
		}
		current, getErr := s.uploadSessions.Get(ctx, candidate.Owner, candidate.ID)
		if getErr == nil && current.State == uploadsession.StateCommitting {
			_, getErr = s.reconcileCommittingUploadSession(ctx, candidate.Owner, current)
			if getErr == nil {
				reconciled++
			}
		}
		release()
		if getErr != nil &&
			!errors.Is(getErr, uploadsession.ErrNotFound) &&
			!errors.Is(getErr, uploadsession.ErrExpired) {
			reconcileErr = errors.Join(
				reconcileErr,
				fmt.Errorf("reconcile upload session %s: %w", candidate.ID, getErr),
			)
		}
	}
	return reconciled, reconcileErr
}

func (s *Server) cleanupExpiredUploadSessions(ctx context.Context) {
	cleaned, err := s.uploadSessions.CleanupExpired(ctx, time.Now())
	if err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn().Err(err).Msg("Failed to clean expired upload sessions")
		return
	}
	if cleaned > 0 {
		s.logger.Info().Int("count", cleaned).Msg("Expired upload sessions cleaned")
	}
}

func newUploadSessionResponse(session uploadsession.Session) uploadSessionResponse {
	var contentBLAKE3 *string
	if session.ContentBLAKE3 != "" {
		value := session.ContentBLAKE3
		contentBLAKE3 = &value
	}
	return uploadSessionResponse{
		ID:                 session.ID,
		Path:               session.Path,
		State:              session.State,
		DurableOffset:      session.DurableOffset,
		TotalBytes:         session.TotalBytes,
		CreatedAt:          session.CreatedAt.UTC(),
		UpdatedAt:          session.UpdatedAt.UTC(),
		ExpiresAt:          session.ExpiresAt.UTC(),
		ContentBLAKE3:      contentBLAKE3,
		PersistenceWarning: session.PersistenceWarning,
	}
}

func writeUploadSessionResponse(w http.ResponseWriter, status int, session uploadsession.Session, message string) {
	w.Header().Set(uploadOffsetHeader, strconv.FormatInt(session.DurableOffset, 10))
	w.Header().Set(uploadLengthHeader, strconv.FormatInt(session.TotalBytes, 10))
	NewAPIResponse(newUploadSessionResponse(session)).WithMessage(message).Write(w, status)
}

func (s *Server) uploadSessionStore(w http.ResponseWriter) *uploadsession.Store {
	if s.uploadSessions != nil {
		return s.uploadSessions
	}
	if s.uploadSessionsConfigured {
		ServiceUnavailable(w, "upload session store unavailable")
		return nil
	}
	ServiceUnavailable(w, "upload sessions are not configured")
	return nil
}

func (s *Server) handleCreateUploadSession(w http.ResponseWriter, r *http.Request) {
	store := s.uploadSessionStore(w)
	if store == nil {
		return
	}
	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	var request createUploadSessionRequest
	if err := decodeJSONBody(r, &request); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}
	cleanPath, err := validatePath(request.Path)
	if err != nil || cleanPath != request.Path || isMutationRootPath(cleanPath) {
		badRequestInvalidPath(w)
		return
	}
	if request.TotalBytes < 0 {
		BadRequest(w, "total_bytes must be non-negative")
		return
	}
	if request.TotalBytes > DefaultMaxUploadSize {
		respondPayloadTooLarge(w, fmt.Sprintf("file too large (max %d bytes)", DefaultMaxUploadSize))
		return
	}
	if !isSafeUploadIdentifier(request.ClientRequestID, 128) {
		BadRequest(w, "client_request_id is invalid")
		return
	}
	owner, _ := getFavoriteUserIdentifiersFromRequest(r.Context())
	existing, existingErr := store.GetByClientRequestID(r.Context(), owner, request.ClientRequestID)
	if existingErr == nil {
		if existing.Path != cleanPath || existing.TotalBytes != request.TotalBytes {
			Conflict(w, "client_request_id already belongs to another upload")
			return
		}
		if err := s.authorizeUserWritePath(r.Context(), existing.Path); err != nil {
			respondPathAccessError(w, err)
			return
		}
		writeUploadSessionResponse(w, http.StatusOK, existing, "upload session already exists")
		return
	}
	if !errors.Is(existingErr, uploadsession.ErrNotFound) {
		s.respondUploadSessionStoreError(w, existingErr)
		return
	}
	if err := s.authorizeUserWritePath(r.Context(), cleanPath); err != nil {
		respondPathAccessError(w, err)
		return
	}

	_, condition, releaseQuota, err := s.quotaCheckedUploadReader(
		r.Context(),
		cleanPath,
		bytes.NewReader(nil),
		request.TotalBytes,
	)
	if releaseQuota != nil {
		releaseQuota()
	}
	if err != nil {
		s.respondUploadSessionAdmissionError(w, r, err)
		return
	}

	result, err := store.Create(r.Context(), uploadsession.CreateRequest{
		Owner:           owner,
		ClientRequestID: request.ClientRequestID,
		Path:            cleanPath,
		TotalBytes:      request.TotalBytes,
		OriginalCondition: uploadsession.OriginalCondition{
			ExpectedExists:      condition.ExpectedExists,
			DeleteIdentityToken: condition.DeleteIdentityToken,
		},
	})
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	status := http.StatusCreated
	message := "upload session created"
	if !result.Created {
		status = http.StatusOK
		message = "upload session already exists"
	}
	writeUploadSessionResponse(w, status, result.Session, message)
}

func (s *Server) respondUploadSessionAdmissionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, storage.ErrIsDir):
		BadRequest(w, "cannot upload to directory")
	case errors.Is(err, storage.ErrNotDir):
		Conflict(w, "parent path is not a directory")
	case errors.Is(err, storage.ErrNotRegular):
		Conflict(w, "upload target is not a regular file")
	case errors.As(err, new(*quotaExceededError)):
		s.sendQuotaExceededAlertEvent(r.Context(), "upload_session_create", err)
		respondQuotaExceeded(w, err)
	default:
		s.respondInternalError(w, "check upload session quota", err)
	}
}

func (s *Server) handleGetUploadSession(w http.ResponseWriter, r *http.Request) {
	store := s.uploadSessionStore(w)
	if store == nil {
		return
	}
	owner, _ := getFavoriteUserIdentifiersFromRequest(r.Context())
	session, err := store.Get(r.Context(), owner, chi.URLParam(r, "id"))
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	if err := s.authorizeUserWritePath(r.Context(), session.Path); err != nil {
		respondPathAccessError(w, err)
		return
	}
	release, err := s.acquireUploadSessionCommitLock(r.Context(), session.ID)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	defer release()
	session, err = store.Get(r.Context(), owner, session.ID)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	session, err = s.reconcileCommittingUploadSession(r.Context(), owner, session)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	writeUploadSessionResponse(w, http.StatusOK, session, "upload session status")
}

func (s *Server) handleGetUploadSessionByClientRequest(
	w http.ResponseWriter,
	r *http.Request,
) {
	store := s.uploadSessionStore(w)
	if store == nil {
		return
	}
	clientRequestID := chi.URLParam(r, "client_request_id")
	if !isSafeUploadIdentifier(clientRequestID, 128) {
		BadRequest(w, "client_request_id is invalid")
		return
	}
	owner, _ := getFavoriteUserIdentifiersFromRequest(r.Context())
	session, err := store.GetByClientRequestID(
		r.Context(),
		owner,
		clientRequestID,
	)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	if err := s.authorizeUserWritePath(r.Context(), session.Path); err != nil {
		respondPathAccessError(w, err)
		return
	}
	writeUploadSessionResponse(w, http.StatusOK, session, "upload session status")
}

func (s *Server) handleAppendUploadSession(w http.ResponseWriter, r *http.Request) {
	store := s.uploadSessionStore(w)
	if store == nil {
		return
	}
	owner, _ := getFavoriteUserIdentifiersFromRequest(r.Context())
	id := chi.URLParam(r, "id")
	session, err := store.Get(r.Context(), owner, id)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	if err := s.authorizeUserWritePath(r.Context(), session.Path); err != nil {
		respondPathAccessError(w, err)
		return
	}

	offsetValue, ok := singleUploadHeader(r, uploadOffsetHeader)
	if !ok {
		BadRequest(w, "Upload-Offset must appear exactly once")
		return
	}
	offset, err := parseCanonicalUploadInteger(offsetValue)
	if err != nil {
		BadRequest(w, "Upload-Offset is invalid")
		return
	}
	chunkID, ok := singleUploadHeader(r, uploadChunkIDHeader)
	if !ok || !isSafeUploadIdentifier(chunkID, 128) {
		BadRequest(w, "Upload-Chunk-ID is invalid")
		return
	}
	chunkSHA256, ok := singleUploadHeader(r, uploadChunkSHA256Header)
	if !ok || !isLowercaseHex(chunkSHA256, 64) {
		BadRequest(w, "X-MnemoNAS-Chunk-SHA256 is invalid")
		return
	}
	if r.ContentLength < 0 {
		BadRequest(w, "Content-Length is required")
		return
	}
	isFinalChunk := offset <= session.TotalBytes &&
		r.ContentLength == session.TotalBytes-offset
	if offset == session.DurableOffset &&
		r.ContentLength > 0 &&
		r.ContentLength < uploadSessionMinNonFinalBytes &&
		!isFinalChunk {
		BadRequest(
			w,
			fmt.Sprintf(
				"non-final upload chunks must be at least %d bytes",
				uploadSessionMinNonFinalBytes,
			),
		)
		return
	}

	limitedBody := &uploadSessionLimitedBody{
		ReadCloser: http.MaxBytesReader(w, r.Body, uploadsession.MaxChunkBytes()),
	}
	r.Body = limitedBody
	w = s.withUploadIdleDeadlines(w, r)
	result, err := store.AppendChunk(r.Context(), uploadsession.AppendChunkRequest{
		Owner:       owner,
		ID:          id,
		Offset:      offset,
		Length:      r.ContentLength,
		ChunkID:     chunkID,
		ChunkSHA256: chunkSHA256,
		Body:        r.Body,
	})
	if err != nil {
		if limitedBody.maxBytesErr != nil {
			err = errors.Join(err, limitedBody.maxBytesErr)
		}
		s.respondUploadSessionStoreError(w, err)
		return
	}
	message := "upload chunk stored"
	if result.Replayed {
		message = "upload chunk already stored"
	}
	writeUploadSessionResponse(w, http.StatusOK, result.Session, message)
}

func (s *Server) handleCommitUploadSession(w http.ResponseWriter, r *http.Request) {
	store := s.uploadSessionStore(w)
	if store == nil {
		return
	}
	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}
	owner, _ := getFavoriteUserIdentifiersFromRequest(r.Context())
	id := chi.URLParam(r, "id")
	release, err := s.acquireUploadSessionCommitLock(r.Context(), id)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	defer release()
	session, err := store.Get(r.Context(), owner, id)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	if err := s.authorizeUserWritePath(r.Context(), session.Path); err != nil {
		respondPathAccessError(w, err)
		return
	}
	session, err = s.reconcileCommittingUploadSession(r.Context(), owner, session)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	if session.State == uploadsession.StateCommitted {
		writeUploadSessionResponse(w, http.StatusOK, session, "upload already committed")
		return
	}
	if session.State == uploadsession.StateConflict {
		Conflict(w, "upload target changed")
		return
	}

	session, err = store.BeginCommit(r.Context(), owner, id)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	payload, payloadSession, err := store.OpenPayload(r.Context(), owner, id)
	if err != nil {
		if rollbackErr := s.rollbackUploadSessionCommit(r.Context(), owner, id); rollbackErr != nil {
			s.respondInternalError(
				w,
				"restore upload session after payload open failure",
				errors.Join(err, rollbackErr),
			)
			return
		}
		s.respondUploadSessionStoreError(w, err)
		return
	}
	defer payload.Close()
	session = payloadSession

	reader, currentCondition, releaseQuota, err := s.quotaCheckedUploadReader(
		r.Context(),
		session.Path,
		payload,
		session.TotalBytes,
	)
	if err != nil {
		if rollbackErr := s.rollbackUploadSessionCommit(r.Context(), owner, id); rollbackErr != nil {
			s.respondInternalError(
				w,
				"restore upload session after admission failure",
				errors.Join(err, rollbackErr),
			)
			return
		}
		s.respondUploadSessionAdmissionError(w, r, err)
		return
	}
	defer releaseQuota()
	if !uploadConditionsEqual(currentCondition, session.OriginalCondition) {
		resolved, resolveErr := s.resolvePrePublicationUploadSessionChange(
			r.Context(),
			owner,
			session,
		)
		if resolveErr != nil {
			s.respondInternalError(w, "resolve upload target before commit", resolveErr)
			return
		}
		if resolved.State == uploadsession.StateReady {
			Conflict(w, "upload target changed while preparing commit; retry")
			return
		}
		Conflict(w, "upload target changed")
		return
	}

	commitReader := newQuotaMutationCommitReader(
		r.Context(),
		reader,
		func(ctx context.Context) (*quotareservation.MutationLease, error) {
			mutation, acquireErr := s.acquireQuotaMutationForCommit(ctx, "upload_session_commit")
			if acquireErr != nil {
				return nil, acquireErr
			}
			started, startErr := store.MarkPublicationStarted(ctx, owner, id)
			if startErr != nil {
				mutation.Release()
				return nil, startErr
			}
			session = started
			return mutation, nil
		},
	)
	defer commitReader.Release()
	writeErr := s.fs.WriteFileIfUnchanged(
		r.Context(),
		session.Path,
		commitReader,
		storage.WriteFileCondition{
			ExpectedExists:      session.OriginalCondition.ExpectedExists,
			DeleteIdentityToken: session.OriginalCondition.DeleteIdentityToken,
		},
	)

	decisionCtx, cancelDecision := newUploadSessionTransitionContext(r.Context())
	defer cancelDecision()
	releaseDecisionLease, leaseErr := s.ensureUploadSessionDecisionLease(
		decisionCtx,
		commitReader,
	)
	if leaseErr != nil {
		s.respondInternalError(w, "acquire upload session decision lease", leaseErr)
		return
	}
	defer releaseDecisionLease()

	if writeErr == nil {
		s.finishUploadSessionCommit(
			w,
			r,
			decisionCtx,
			owner,
			session,
			false,
			"upload committed",
		)
		return
	}
	if isOnlyVisibleMutationWarning(writeErr) {
		markWorkspaceMutationWarningHeaders(w)
		s.finishUploadSessionCommit(
			w,
			r,
			decisionCtx,
			owner,
			session,
			true,
			"upload committed",
		)
		return
	}
	if errors.Is(writeErr, storage.ErrWriteRecoveryRequired) {
		s.respondUploadSessionCommitError(w, r, writeErr)
		return
	}
	if errors.Is(writeErr, storage.ErrWriteConflict) {
		if _, markErr := s.markUploadSessionConflict(
			decisionCtx,
			owner,
			id,
			"target changed during commit",
		); markErr != nil {
			s.respondInternalError(
				w,
				"persist upload session conflict",
				errors.Join(writeErr, markErr),
			)
			return
		}
		s.respondUploadSessionCommitError(w, r, writeErr)
		return
	}
	evidence, evidenceErr := s.captureUploadSessionTargetEvidence(decisionCtx, session)
	if evidenceErr != nil {
		s.respondInternalError(
			w,
			"inspect upload target after rolled-back commit",
			errors.Join(writeErr, evidenceErr),
		)
		return
	}
	if evidence.matchesOriginal(session.OriginalCondition) {
		if _, rollbackErr := s.markUploadSessionReady(decisionCtx, owner, id); rollbackErr != nil {
			s.respondInternalError(
				w,
				"restore upload session after commit failure",
				errors.Join(writeErr, rollbackErr),
			)
			return
		}
	} else {
		if _, markErr := s.markUploadSessionConflict(
			decisionCtx,
			owner,
			id,
			"target changed while failed commit was rolled back",
		); markErr != nil {
			s.respondInternalError(
				w,
				"persist upload session conflict",
				errors.Join(writeErr, markErr),
			)
			return
		}
	}
	s.respondUploadSessionCommitError(w, r, writeErr)
}

func (s *Server) finishUploadSessionCommit(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	owner string,
	session uploadsession.Session,
	persistenceWarning bool,
	message string,
) {
	committed, err := s.persistCommittedUploadSession(
		ctx,
		owner,
		session,
		persistenceWarning,
	)
	if err != nil {
		markAuditFailureHeaders(w)
		s.respondInternalError(w, "persist committed upload session", err)
		return
	}
	writeUploadSessionResponse(w, http.StatusOK, committed, message)
}

func newUploadSessionTransitionContext(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if parent != nil {
		base = context.WithoutCancel(parent)
	}
	return context.WithTimeout(base, uploadSessionTransitionTimeout)
}

func (s *Server) ensureUploadSessionDecisionLease(
	ctx context.Context,
	commitReader *quotaMutationCommitReader,
) (func(), error) {
	if commitReader != nil && commitReader.mutation != nil {
		return func() {}, nil
	}
	mutation, err := s.acquireQuotaMutationForCommit(ctx, "upload_session_commit_decision")
	if err != nil {
		return nil, err
	}
	return mutation.Release, nil
}

func (s *Server) rollbackUploadSessionCommit(parent context.Context, owner, id string) error {
	ctx, cancel := newUploadSessionTransitionContext(parent)
	defer cancel()
	mutation, err := s.acquireQuotaMutationForCommit(ctx, "upload_session_commit_rollback")
	if err != nil {
		return err
	}
	defer mutation.Release()
	_, err = s.markUploadSessionReady(ctx, owner, id)
	return err
}

func (s *Server) resolvePrePublicationUploadSessionChange(
	parent context.Context,
	owner string,
	session uploadsession.Session,
) (uploadsession.Session, error) {
	ctx, cancel := newUploadSessionTransitionContext(parent)
	defer cancel()
	mutation, err := s.acquireQuotaMutationForCommit(ctx, "upload_session_prepublication_decision")
	if err != nil {
		return uploadsession.Session{}, err
	}
	defer mutation.Release()

	evidence, err := s.captureUploadSessionTargetEvidence(ctx, session)
	if err != nil {
		return uploadsession.Session{}, err
	}
	if evidence.matchesOriginal(session.OriginalCondition) {
		return s.markUploadSessionReady(ctx, owner, session.ID)
	}
	return s.markUploadSessionConflict(
		ctx,
		owner,
		session.ID,
		"target changed before publication",
	)
}

func (s *Server) persistCommittedUploadSession(
	ctx context.Context,
	owner string,
	session uploadsession.Session,
	persistenceWarning bool,
) (uploadsession.Session, error) {
	if err := s.ensureUploadSessionActivity(session); err != nil {
		return uploadsession.Session{}, fmt.Errorf("persist upload activity: %w", err)
	}
	if s.beforeUploadSessionTransition != nil {
		s.beforeUploadSessionTransition(uploadsession.StateCommitted)
	}
	return s.uploadSessions.MarkCommitted(
		ctx,
		owner,
		session.ID,
		uploadsession.CommitResult{
			ContentBLAKE3:      session.ContentBLAKE3,
			PersistenceWarning: persistenceWarning,
		},
	)
}

func (s *Server) ensureUploadSessionActivity(session uploadsession.Session) error {
	if s.activity == nil {
		if s.activityConfigured {
			return errors.New("activity log is configured but unavailable")
		}
		return nil
	}
	return s.activity.LogOnce(
		"upload-session-"+session.ID,
		activity.ActionUpload,
		session.Path,
		session.Owner,
		"",
		map[string]string{"upload_session_id": session.ID},
	)
}

func (s *Server) markUploadSessionReady(
	ctx context.Context,
	owner, id string,
) (uploadsession.Session, error) {
	if s.beforeUploadSessionTransition != nil {
		s.beforeUploadSessionTransition(uploadsession.StateReady)
	}
	return s.uploadSessions.MarkReady(ctx, owner, id)
}

func (s *Server) markUploadSessionConflict(
	ctx context.Context,
	owner, id, reason string,
) (uploadsession.Session, error) {
	if s.beforeUploadSessionTransition != nil {
		s.beforeUploadSessionTransition(uploadsession.StateConflict)
	}
	return s.uploadSessions.MarkConflict(ctx, owner, id, reason)
}

func (s *Server) respondUploadSessionCommitError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case respondStreamedWriteStateError(w, err):
		return
	case errors.As(err, new(*quotaExceededError)):
		s.sendQuotaExceededAlertEvent(r.Context(), "upload_session_commit", err)
		respondQuotaExceeded(w, err)
	case errors.Is(err, storage.ErrFileTooLarge):
		respondPayloadTooLarge(w, fmt.Sprintf("file too large (max %d bytes)", DefaultMaxUploadSize))
	case errors.Is(err, storage.ErrIsDir):
		BadRequest(w, "cannot upload to directory")
	case errors.Is(err, storage.ErrNotDir):
		Conflict(w, "parent path is not a directory")
	case errors.Is(err, storage.ErrNotRegular), errors.Is(err, storage.ErrWriteConflict):
		Conflict(w, "upload target changed")
	default:
		s.respondInternalError(w, "commit upload session", err)
	}
}

func (s *Server) handleCancelUploadSession(w http.ResponseWriter, r *http.Request) {
	store := s.uploadSessionStore(w)
	if store == nil {
		return
	}
	owner, _ := getFavoriteUserIdentifiersFromRequest(r.Context())
	id := chi.URLParam(r, "id")
	session, err := store.Get(r.Context(), owner, id)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	if err := s.authorizeUserWritePath(r.Context(), session.Path); err != nil {
		respondPathAccessError(w, err)
		return
	}
	release, err := s.acquireUploadSessionCommitLock(r.Context(), id)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	defer release()
	session, err = store.Get(r.Context(), owner, id)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	session, err = store.Cancel(r.Context(), owner, id)
	if err != nil {
		s.respondUploadSessionStoreError(w, err)
		return
	}
	writeUploadSessionResponse(w, http.StatusOK, session, "upload session cancelled")
}

func (s *Server) reconcileCommittingUploadSession(
	parent context.Context,
	owner string,
	session uploadsession.Session,
) (uploadsession.Session, error) {
	if session.State != uploadsession.StateCommitting {
		return session, nil
	}
	if s.fs == nil {
		return uploadsession.Session{}, errors.New("filesystem not initialized")
	}
	ctx, cancel := newUploadSessionTransitionContext(parent)
	defer cancel()
	mutation, err := s.acquireQuotaMutationForCommit(ctx, "upload_session_recovery")
	if err != nil {
		return uploadsession.Session{}, err
	}
	defer mutation.Release()

	storageMutation, err := s.fs.AcquireMutationLease(ctx)
	if err != nil {
		return uploadsession.Session{}, err
	}
	storageMutation.Release()

	evidence, err := s.captureUploadSessionTargetEvidence(ctx, session)
	if err != nil {
		return uploadsession.Session{}, err
	}
	if s.afterUploadSessionRecoveryEvidence != nil {
		s.afterUploadSessionRecoveryEvidence()
	}
	finalStorageMutation, err := s.fs.AcquireMutationLease(ctx)
	if err != nil {
		return uploadsession.Session{}, err
	}
	defer finalStorageMutation.Release()
	consistent, err := validateUploadSessionTargetEvidence(
		ctx,
		finalStorageMutation,
		session.Path,
		evidence,
	)
	if err != nil {
		return uploadsession.Session{}, err
	}
	if !consistent {
		return uploadsession.Session{}, errUploadSessionRecoveryTargetDrift
	}
	if evidence.matchesOriginal(session.OriginalCondition) {
		return s.markUploadSessionReady(ctx, owner, session.ID)
	}
	if session.PublicationStarted && evidence.contentMatches {
		return s.persistCommittedUploadSession(ctx, owner, session, false)
	}
	reason := "target changed before publication"
	if session.PublicationStarted {
		reason = "target changed while publication outcome was unconfirmed"
	}
	return s.markUploadSessionConflict(ctx, owner, session.ID, reason)
}

type uploadSessionTargetEvidence struct {
	condition      storage.WriteFileCondition
	conditionKnown bool
	size           int64
	contentMatches bool
}

func (e uploadSessionTargetEvidence) matchesOriginal(
	original uploadsession.OriginalCondition,
) bool {
	return e.conditionKnown && uploadConditionsEqual(e.condition, original)
}

func validateUploadSessionTargetEvidence(
	ctx context.Context,
	lease *storage.MutationLease,
	targetPath string,
	evidence uploadSessionTargetEvidence,
) (bool, error) {
	info, err := lease.Stat(ctx, targetPath)
	if err != nil {
		switch {
		case isStorageNotFound(err):
			return evidence.conditionKnown &&
				!evidence.condition.ExpectedExists, nil
		case errors.Is(err, storage.ErrIsDir),
			errors.Is(err, storage.ErrNotDir),
			errors.Is(err, storage.ErrNotRegular):
			return !evidence.conditionKnown, nil
		default:
			return false, err
		}
	}
	if info.IsDir || !info.Mode.IsRegular() {
		return !evidence.conditionKnown, nil
	}
	if !evidence.conditionKnown || !evidence.condition.ExpectedExists {
		return false, nil
	}
	return info.DeleteIdentityToken == evidence.condition.DeleteIdentityToken &&
		info.Size == evidence.size, nil
}

func (s *Server) captureUploadSessionTargetEvidence(
	ctx context.Context,
	session uploadsession.Session,
) (uploadSessionTargetEvidence, error) {
	if s.fs == nil {
		return uploadSessionTargetEvidence{}, errors.New("filesystem not initialized")
	}
	file, info, err := s.fs.OpenFileSnapshot(ctx, session.Path)
	if err != nil {
		if isStorageNotFound(err) {
			return uploadSessionTargetEvidence{
				condition:      storage.WriteFileCondition{ExpectedExists: false},
				conditionKnown: true,
			}, nil
		}
		if errors.Is(err, storage.ErrIsDir) ||
			errors.Is(err, storage.ErrNotDir) ||
			errors.Is(err, storage.ErrNotRegular) {
			return uploadSessionTargetEvidence{}, nil
		}
		return uploadSessionTargetEvidence{}, err
	}
	closeErr := file.Close()
	if closeErr != nil {
		return uploadSessionTargetEvidence{}, closeErr
	}
	return uploadSessionTargetEvidence{
		condition: storage.WriteFileCondition{
			ExpectedExists:      true,
			DeleteIdentityToken: info.DeleteIdentityToken,
		},
		conditionKnown: true,
		size:           info.Size,
		contentMatches: session.ContentBLAKE3 != "" &&
			info.Size == session.TotalBytes &&
			info.ContentHash == session.ContentBLAKE3,
	}, nil
}

func uploadConditionsEqual(actual storage.WriteFileCondition, expected uploadsession.OriginalCondition) bool {
	return actual.ExpectedExists == expected.ExpectedExists &&
		actual.DeleteIdentityToken == expected.DeleteIdentityToken
}

func (s *Server) respondUploadSessionStoreError(w http.ResponseWriter, err error) {
	var offsetErr *uploadsession.OffsetMismatchError
	var maxBytesErr *http.MaxBytesError
	switch {
	case errors.Is(err, uploadsession.ErrNotFound):
		NewAPIError("UPLOAD_SESSION_NOT_FOUND", "upload session not found").Write(w, http.StatusNotFound)
	case errors.Is(err, uploadsession.ErrExpired):
		NewAPIError("UPLOAD_SESSION_EXPIRED", "upload session expired").Write(w, http.StatusGone)
	case errors.As(err, &offsetErr):
		NewAPIError("UPLOAD_OFFSET_CONFLICT", "upload offset does not match server state").
			WithDetails(map[string]int64{"durable_offset": offsetErr.Expected}).
			Write(w, http.StatusConflict)
	case errors.As(err, &maxBytesErr):
		respondPayloadTooLarge(
			w,
			fmt.Sprintf("upload chunk too large (max %d bytes)", uploadsession.MaxChunkBytes()),
		)
	case errors.Is(err, uploadsession.ErrChunkTooLarge):
		respondPayloadTooLarge(w, fmt.Sprintf("upload chunk too large (max %d bytes)", uploadsession.MaxChunkBytes()))
	case errors.Is(err, uploadsession.ErrChunkLength),
		errors.Is(err, uploadsession.ErrChunkDigest):
		NewAPIError("UPLOAD_CHUNK_INVALID", "upload chunk validation failed").Write(w, http.StatusBadRequest)
	case errors.Is(err, uploadsession.ErrLimitExceeded):
		w.Header().Set("Retry-After", "60")
		NewAPIError("UPLOAD_SESSION_LIMIT", "too many retained upload sessions").Write(w, http.StatusTooManyRequests)
	case errors.Is(err, errUploadSessionInsufficientStorage):
		NewAPIError(
			"UPLOAD_STAGING_CAPACITY_EXCEEDED",
			"upload staging capacity is insufficient",
		).Write(w, http.StatusInsufficientStorage)
	case errors.Is(err, uploadsession.ErrStagingLimit):
		w.Header().Set("Retry-After", "60")
		NewAPIError(
			"UPLOAD_STAGING_LIMIT",
			"upload staging limit reached",
		).Write(w, http.StatusTooManyRequests)
	case errors.Is(err, uploadsession.ErrConflict),
		errors.Is(err, uploadsession.ErrInvalidState):
		NewAPIError("UPLOAD_SESSION_CONFLICT", "upload session state conflicts with the request").Write(w, http.StatusConflict)
	case errors.Is(err, uploadsession.ErrRecoveryRequired),
		errors.Is(err, uploadsession.ErrClosed):
		NewAPIError("UPLOAD_SESSION_UNAVAILABLE", "upload session state is unavailable").Write(w, http.StatusServiceUnavailable)
	case errors.Is(err, storage.ErrWriteRecoveryRequired),
		errors.Is(err, storage.ErrTrashRecoveryRequired):
		NewAPIError("UPLOAD_SESSION_UNAVAILABLE", "storage recovery is required").Write(w, http.StatusServiceUnavailable)
	case errors.Is(err, errUploadSessionRecoveryTargetDrift):
		NewAPIError("UPLOAD_SESSION_UNAVAILABLE", "upload target recovery evidence changed").Write(w, http.StatusServiceUnavailable)
	default:
		s.respondInternalError(w, "operate on upload session", err)
	}
}

func singleUploadHeader(r *http.Request, name string) (string, bool) {
	if r == nil {
		return "", false
	}
	values := r.Header.Values(name)
	if len(values) != 1 || values[0] == "" || strings.TrimSpace(values[0]) != values[0] {
		return "", false
	}
	return values[0], true
}

func parseCanonicalUploadInteger(value string) (int64, error) {
	if value == "" || value == "-0" || (len(value) > 1 && value[0] == '0') {
		return 0, errors.New("non-canonical integer")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("invalid integer")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("invalid integer")
	}
	return parsed, nil
}

func isSafeUploadIdentifier(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength {
		return false
	}
	for index, character := range value {
		isAlphaNumeric := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9'
		if isAlphaNumeric {
			continue
		}
		if index == 0 || !strings.ContainsRune("._:-", character) {
			return false
		}
	}
	return true
}

func isLowercaseHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}
