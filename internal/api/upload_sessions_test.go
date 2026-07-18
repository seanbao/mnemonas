package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/uploadsession"
)

func TestUploadSessions_NoAuthRealStorageEndToEnd(t *testing.T) {
	root := t.TempDir()
	internalRoot := filepath.Join(root, ".mnemonas")
	uploadSessionRoot := filepath.Join(internalRoot, "upload-sessions")
	dataplaneClient := dataplane.NewClient("127.0.0.1:1")
	t.Cleanup(func() {
		if err := dataplaneClient.Close(); err != nil {
			t.Errorf("dataplane client Close() error: %v", err)
		}
	})

	fs, err := storage.New(&storage.Config{
		FilesRoot:          filepath.Join(root, "files"),
		InternalRoot:       internalRoot,
		TrashRoot:          filepath.Join(internalRoot, "trash"),
		TrashRetentionDays: 30,
		Dataplane:          dataplaneClient,
	})
	if err != nil {
		t.Fatalf("storage.New() error: %v", err)
	}
	t.Cleanup(func() {
		if err := fs.Close(); err != nil {
			t.Errorf("filesystem Close() error: %v", err)
		}
	})

	var server *Server
	t.Cleanup(func() {
		if server != nil {
			if err := server.Close(); err != nil {
				t.Errorf("server Close() error: %v", err)
			}
		}
	})
	server = newUploadSessionE2EServer(t, fs, uploadSessionRoot)

	directoryRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/directories/uploads",
		nil,
	)
	directoryResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(directoryResponse, directoryRequest)
	if directoryResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create directory status = %d, want %d; body=%s",
			directoryResponse.Code,
			http.StatusCreated,
			directoryResponse.Body.String(),
		)
	}

	firstChunk := bytes.Repeat([]byte("a"), int(uploadSessionMinNonFinalBytes))
	secondChunk := []byte(" and second chunk")
	payload := append(append([]byte(nil), firstChunk...), secondChunk...)
	const targetPath = "/uploads/restart.txt"
	createResponse := uploadSessionE2ECreate(
		t,
		server,
		targetPath,
		int64(len(payload)),
		"restart-request-1",
		http.StatusCreated,
	)
	if createResponse.Message != "upload session created" {
		t.Fatalf("create message = %q, want upload session created", createResponse.Message)
	}
	if createResponse.Data.ID == "" ||
		createResponse.Data.Path != targetPath ||
		createResponse.Data.State != uploadsession.StateUploading ||
		createResponse.Data.DurableOffset != 0 ||
		createResponse.Data.TotalBytes != int64(len(payload)) ||
		createResponse.Data.ContentBLAKE3 != nil {
		t.Fatalf("unexpected created upload session: %+v", createResponse.Data)
	}

	replayCreateResponse := uploadSessionE2ECreate(
		t,
		server,
		targetPath,
		int64(len(payload)),
		"restart-request-1",
		http.StatusOK,
	)
	if replayCreateResponse.Message != "upload session already exists" {
		t.Fatalf(
			"idempotent create message = %q, want upload session already exists",
			replayCreateResponse.Message,
		)
	}
	if replayCreateResponse.Data.ID != createResponse.Data.ID ||
		replayCreateResponse.Data.CreatedAt != createResponse.Data.CreatedAt ||
		replayCreateResponse.Data.UpdatedAt != createResponse.Data.UpdatedAt ||
		replayCreateResponse.Data.ExpiresAt != createResponse.Data.ExpiresAt {
		t.Fatalf(
			"idempotent create changed immutable session: first=%+v replay=%+v",
			createResponse.Data,
			replayCreateResponse.Data,
		)
	}

	firstPatchResponse := uploadSessionE2EPatch(
		t,
		server,
		createResponse.Data.ID,
		0,
		"chunk-1",
		firstChunk,
		http.StatusOK,
	)
	if firstPatchResponse.Message != "upload chunk stored" ||
		firstPatchResponse.Data.State != uploadsession.StateUploading ||
		firstPatchResponse.Data.DurableOffset != int64(len(firstChunk)) {
		t.Fatalf("unexpected first PATCH response: %+v", firstPatchResponse)
	}

	firstReplayResponse := uploadSessionE2EPatch(
		t,
		server,
		createResponse.Data.ID,
		0,
		"chunk-1",
		firstChunk,
		http.StatusOK,
	)
	if firstReplayResponse.Message != "upload chunk already stored" ||
		firstReplayResponse.Data.DurableOffset != int64(len(firstChunk)) {
		t.Fatalf("unexpected replayed PATCH response: %+v", firstReplayResponse)
	}

	wrongOffsetRequest := uploadSessionE2EPatchRequest(
		createResponse.Data.ID,
		1,
		"chunk-2",
		secondChunk,
	)
	wrongOffsetResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(wrongOffsetResponse, wrongOffsetRequest)
	if wrongOffsetResponse.Code != http.StatusConflict {
		t.Fatalf(
			"wrong-offset PATCH status = %d, want %d; body=%s",
			wrongOffsetResponse.Code,
			http.StatusConflict,
			wrongOffsetResponse.Body.String(),
		)
	}
	offsetError := decodeUploadSessionE2EError(t, wrongOffsetResponse)
	if offsetError.Code != "UPLOAD_OFFSET_CONFLICT" ||
		offsetError.Message != "upload offset does not match server state" ||
		offsetError.Details.DurableOffset != int64(len(firstChunk)) {
		t.Fatalf("unexpected wrong-offset error: %+v", offsetError)
	}

	secondPatchResponse := uploadSessionE2EPatch(
		t,
		server,
		createResponse.Data.ID,
		int64(len(firstChunk)),
		"chunk-2",
		secondChunk,
		http.StatusOK,
	)
	if secondPatchResponse.Message != "upload chunk stored" ||
		secondPatchResponse.Data.State != uploadsession.StateReady ||
		secondPatchResponse.Data.DurableOffset != int64(len(payload)) ||
		secondPatchResponse.Data.ContentBLAKE3 == nil {
		t.Fatalf("unexpected second PATCH response: %+v", secondPatchResponse)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("first server Close() error: %v", err)
	}
	server = nil
	server = newUploadSessionE2EServer(t, fs, uploadSessionRoot)

	recoveredResponse := uploadSessionE2EGet(
		t,
		server,
		createResponse.Data.ID,
		http.StatusOK,
	)
	if recoveredResponse.Message != "upload session status" ||
		recoveredResponse.Data.ID != createResponse.Data.ID ||
		recoveredResponse.Data.State != uploadsession.StateReady ||
		recoveredResponse.Data.DurableOffset != int64(len(payload)) ||
		recoveredResponse.Data.ContentBLAKE3 == nil ||
		*recoveredResponse.Data.ContentBLAKE3 != *secondPatchResponse.Data.ContentBLAKE3 {
		t.Fatalf("unexpected recovered upload session: %+v", recoveredResponse)
	}

	commitResponse := uploadSessionE2ECommit(
		t,
		server,
		createResponse.Data.ID,
		http.StatusOK,
	)
	if commitResponse.Message != "upload committed" ||
		commitResponse.Data.State != uploadsession.StateCommitted ||
		commitResponse.Data.DurableOffset != int64(len(payload)) ||
		commitResponse.Data.ContentBLAKE3 == nil {
		t.Fatalf("unexpected commit response: %+v", commitResponse)
	}
	if got := uploadSessionE2EReadTarget(t, fs, targetPath); !bytes.Equal(got, payload) {
		t.Fatalf("committed target = %q, want %q", got, payload)
	}

	replayedCommitResponse := uploadSessionE2ECommit(
		t,
		server,
		createResponse.Data.ID,
		http.StatusOK,
	)
	if replayedCommitResponse.Message != "upload already committed" ||
		replayedCommitResponse.Data.State != uploadsession.StateCommitted ||
		replayedCommitResponse.Data.ContentBLAKE3 == nil ||
		*replayedCommitResponse.Data.ContentBLAKE3 != *commitResponse.Data.ContentBLAKE3 {
		t.Fatalf("unexpected replayed commit response: %+v", replayedCommitResponse)
	}
	if got := uploadSessionE2EReadTarget(t, fs, targetPath); !bytes.Equal(got, payload) {
		t.Fatalf("target after replayed commit = %q, want %q", got, payload)
	}

	t.Run("target change conflicts without overwrite", func(t *testing.T) {
		sessionPayload := []byte("session-owned-content")
		externalPayload := []byte("newer-target-content")
		const conflictPath = "/uploads/conflict.txt"
		created := uploadSessionE2ECreate(
			t,
			server,
			conflictPath,
			int64(len(sessionPayload)),
			"conflict-request-1",
			http.StatusCreated,
		)
		if err := fs.WriteFile(
			context.Background(),
			conflictPath,
			bytes.NewReader(externalPayload),
		); err != nil {
			t.Fatalf("external target WriteFile() error: %v", err)
		}
		ready := uploadSessionE2EPatch(
			t,
			server,
			created.Data.ID,
			0,
			"conflict-chunk-1",
			sessionPayload,
			http.StatusOK,
		)
		if ready.Data.State != uploadsession.StateReady {
			t.Fatalf("conflict fixture state = %q, want ready", ready.Data.State)
		}

		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/upload-sessions/"+created.Data.ID+"/commit",
			nil,
		)
		response := httptest.NewRecorder()
		server.Router().ServeHTTP(response, request)
		if response.Code != http.StatusConflict {
			t.Fatalf(
				"changed-target commit status = %d, want %d; body=%s",
				response.Code,
				http.StatusConflict,
				response.Body.String(),
			)
		}
		apiError := decodeUploadSessionE2EError(t, response)
		if apiError.Code != ErrCodeConflict ||
			apiError.Message != "upload target changed" {
			t.Fatalf("unexpected changed-target commit error: %+v", apiError)
		}
		if got := uploadSessionE2EReadTarget(t, fs, conflictPath); !bytes.Equal(got, externalPayload) {
			t.Fatalf("changed target was overwritten: got %q, want %q", got, externalPayload)
		}
		status := uploadSessionE2EGet(t, server, created.Data.ID, http.StatusOK)
		if status.Data.State != uploadsession.StateConflict {
			t.Fatalf("conflicted session state = %q, want conflict", status.Data.State)
		}
	})

	t.Run("DELETE is idempotently cancelled", func(t *testing.T) {
		created := uploadSessionE2ECreate(
			t,
			server,
			"/uploads/cancelled.txt",
			64,
			"cancel-request-1",
			http.StatusCreated,
		)
		for attempt := 1; attempt <= 2; attempt++ {
			request := httptest.NewRequest(
				http.MethodDelete,
				"/api/v1/upload-sessions/"+created.Data.ID,
				nil,
			)
			response := httptest.NewRecorder()
			server.Router().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf(
					"DELETE attempt %d status = %d, want %d; body=%s",
					attempt,
					response.Code,
					http.StatusOK,
					response.Body.String(),
				)
			}
			cancelled := decodeUploadSessionE2ESuccess(t, response)
			if cancelled.Message != "upload session cancelled" ||
				string(cancelled.Data.State) != "cancelled" ||
				cancelled.Data.ID != created.Data.ID ||
				cancelled.Data.DurableOffset != 0 {
				t.Fatalf(
					"unexpected DELETE attempt %d response: %+v",
					attempt,
					cancelled,
				)
			}
		}
	})
}

func TestGetUploadSessionByClientRequestIsOwnerScopedAndReadOnly(t *testing.T) {
	now := time.Date(2026, time.July, 19, 10, 0, 0, 0, time.UTC)
	store, err := uploadsession.Open(t.TempDir(), uploadsession.Options{
		TTL: time.Hour,
		Now: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("uploadsession.Open() error: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("upload session store Close() error: %v", err)
		}
	})
	created, err := store.Create(t.Context(), uploadsession.CreateRequest{
		Owner:           "alice-id",
		ClientRequestID: "lookup-request-1",
		Path:            "/home/alice/recovered.txt",
		TotalBytes:      42,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	server := &Server{
		uploadSessions:           store,
		uploadSessionsConfigured: true,
		authEnabled:              true,
	}
	router := chi.NewRouter()
	router.Get(
		"/api/v1/upload-sessions/by-client-request/{client_request_id}",
		server.handleGetUploadSessionByClientRequest,
	)
	lookup := func(
		user *auth.User,
		clientRequestID string,
	) *httptest.ResponseRecorder {
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/upload-sessions/by-client-request/"+clientRequestID,
			nil,
		)
		if user != nil {
			request = request.WithContext(
				context.WithValue(request.Context(), auth.ContextKeyUser, user),
			)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	alice := &auth.User{
		ID:       "alice-id",
		Username: "alice",
		Role:     auth.RoleUser,
		HomeDir:  "/home/alice",
	}

	found := lookup(alice, "lookup-request-1")
	if found.Code != http.StatusOK {
		t.Fatalf("owner lookup status=%d, want 200; body=%s", found.Code, found.Body.String())
	}
	foundSession := decodeUploadSessionE2ESuccess(t, found)
	if foundSession.Data.ID != created.Session.ID ||
		foundSession.Data.Path != created.Session.Path {
		t.Fatalf("owner lookup returned unexpected session: %+v", foundSession.Data)
	}

	otherOwner := lookup(&auth.User{
		ID:       "bob-id",
		Username: "bob",
		Role:     auth.RoleUser,
		HomeDir:  "/home/bob",
	}, "lookup-request-1")
	if otherOwner.Code != http.StatusNotFound {
		t.Fatalf(
			"cross-owner lookup status=%d, want 404; body=%s",
			otherOwner.Code,
			otherOwner.Body.String(),
		)
	}
	deniedPath := lookup(&auth.User{
		ID:       "alice-id",
		Username: "alice",
		Role:     auth.RoleUser,
		HomeDir:  "/home/other",
	}, "lookup-request-1")
	if deniedPath.Code != http.StatusForbidden {
		t.Fatalf(
			"path-denied lookup status=%d, want 403; body=%s",
			deniedPath.Code,
			deniedPath.Body.String(),
		)
	}
	invalid := lookup(alice, "invalid!")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf(
			"invalid client_request_id status=%d, want 400; body=%s",
			invalid.Code,
			invalid.Body.String(),
		)
	}
	missing := lookup(alice, "missing-request")
	if missing.Code != http.StatusNotFound {
		t.Fatalf(
			"missing lookup status=%d, want 404; body=%s",
			missing.Code,
			missing.Body.String(),
		)
	}

	now = now.Add(2 * time.Hour)
	expired := lookup(alice, "lookup-request-1")
	if expired.Code != http.StatusGone {
		t.Fatalf(
			"expired lookup status=%d, want 410; body=%s",
			expired.Code,
			expired.Body.String(),
		)
	}
	if cleaned, err := store.CleanupExpired(t.Context(), now); err != nil {
		t.Fatalf("CleanupExpired() error: %v", err)
	} else if cleaned != 1 {
		t.Fatalf("CleanupExpired() cleaned=%d, want 1", cleaned)
	}
	cleaned := lookup(alice, "lookup-request-1")
	if cleaned.Code != http.StatusNotFound {
		t.Fatalf(
			"cleaned lookup status=%d, want 404; body=%s",
			cleaned.Code,
			cleaned.Body.String(),
		)
	}
}

type uploadSessionE2ESuccess struct {
	Success   bool                  `json:"success"`
	Data      uploadSessionResponse `json:"data"`
	Message   string                `json:"message"`
	Timestamp string                `json:"timestamp"`
}

type uploadSessionE2EError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details struct {
		DurableOffset int64 `json:"durable_offset"`
	} `json:"details"`
	Timestamp string `json:"timestamp"`
}

func newUploadSessionE2EServer(
	t *testing.T,
	fs *storage.FileSystem,
	uploadSessionRoot string,
) *Server {
	t.Helper()
	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		FileSystem:           fs,
		UploadSessionRoot:    uploadSessionRoot,
		DeferBackgroundTasks: true,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	return server
}

func uploadSessionE2ECreate(
	t *testing.T,
	server *Server,
	targetPath string,
	totalBytes int64,
	clientRequestID string,
	wantStatus int,
) uploadSessionE2ESuccess {
	t.Helper()
	body, err := json.Marshal(createUploadSessionRequest{
		Path:            targetPath,
		TotalBytes:      totalBytes,
		ClientRequestID: clientRequestID,
	})
	if err != nil {
		t.Fatalf("marshal create upload session request: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/upload-sessions",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf(
			"create upload session status = %d, want %d; body=%s",
			response.Code,
			wantStatus,
			response.Body.String(),
		)
	}
	result := decodeUploadSessionE2ESuccess(t, response)
	requireUploadSessionE2EHeaders(t, response, result.Data)
	return result
}

func uploadSessionE2EPatch(
	t *testing.T,
	server *Server,
	id string,
	offset int64,
	chunkID string,
	payload []byte,
	wantStatus int,
) uploadSessionE2ESuccess {
	t.Helper()
	request := uploadSessionE2EPatchRequest(id, offset, chunkID, payload)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf(
			"PATCH upload session status = %d, want %d; body=%s",
			response.Code,
			wantStatus,
			response.Body.String(),
		)
	}
	result := decodeUploadSessionE2ESuccess(t, response)
	requireUploadSessionE2EHeaders(t, response, result.Data)
	return result
}

func uploadSessionE2EPatchRequest(
	id string,
	offset int64,
	chunkID string,
	payload []byte,
) *http.Request {
	digest := sha256.Sum256(payload)
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/upload-sessions/"+id,
		bytes.NewReader(payload),
	)
	request.Header.Set(uploadOffsetHeader, strconv.FormatInt(offset, 10))
	request.Header.Set(uploadChunkIDHeader, chunkID)
	request.Header.Set(uploadChunkSHA256Header, hex.EncodeToString(digest[:]))
	return request
}

func uploadSessionE2EGet(
	t *testing.T,
	server *Server,
	id string,
	wantStatus int,
) uploadSessionE2ESuccess {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/upload-sessions/"+id,
		nil,
	)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf(
			"GET upload session status = %d, want %d; body=%s",
			response.Code,
			wantStatus,
			response.Body.String(),
		)
	}
	result := decodeUploadSessionE2ESuccess(t, response)
	requireUploadSessionE2EHeaders(t, response, result.Data)
	return result
}

func uploadSessionE2ECommit(
	t *testing.T,
	server *Server,
	id string,
	wantStatus int,
) uploadSessionE2ESuccess {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/upload-sessions/"+id+"/commit",
		nil,
	)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf(
			"commit upload session status = %d, want %d; body=%s",
			response.Code,
			wantStatus,
			response.Body.String(),
		)
	}
	result := decodeUploadSessionE2ESuccess(t, response)
	requireUploadSessionE2EHeaders(t, response, result.Data)
	return result
}

func decodeUploadSessionE2ESuccess(
	t *testing.T,
	response *httptest.ResponseRecorder,
) uploadSessionE2ESuccess {
	t.Helper()
	var result uploadSessionE2ESuccess
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode upload session success response: %v; body=%s", err, response.Body.String())
	}
	if !result.Success || result.Timestamp == "" {
		t.Fatalf("invalid upload session success envelope: %+v", result)
	}
	return result
}

func decodeUploadSessionE2EError(
	t *testing.T,
	response *httptest.ResponseRecorder,
) uploadSessionE2EError {
	t.Helper()
	var result uploadSessionE2EError
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode upload session error response: %v; body=%s", err, response.Body.String())
	}
	if result.Timestamp == "" {
		t.Fatalf("upload session error timestamp is empty: %+v", result)
	}
	return result
}

func requireUploadSessionE2EHeaders(
	t *testing.T,
	response *httptest.ResponseRecorder,
	session uploadSessionResponse,
) {
	t.Helper()
	if got, want := response.Header().Get(uploadOffsetHeader), strconv.FormatInt(session.DurableOffset, 10); got != want {
		t.Fatalf("%s = %q, want %q", uploadOffsetHeader, got, want)
	}
	if got, want := response.Header().Get(uploadLengthHeader), strconv.FormatInt(session.TotalBytes, 10); got != want {
		t.Fatalf("%s = %q, want %q", uploadLengthHeader, got, want)
	}
}

func uploadSessionE2EReadTarget(
	t *testing.T,
	fs *storage.FileSystem,
	targetPath string,
) []byte {
	t.Helper()
	file, err := fs.OpenFile(context.Background(), targetPath)
	if err != nil {
		t.Fatalf("OpenFile(%s) error: %v", targetPath, err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf(
			"read target %s errors: read=%v close=%v",
			targetPath,
			readErr,
			closeErr,
		)
	}
	return data
}
