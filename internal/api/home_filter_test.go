package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/storage"
)

type failingAPIResponseWriter struct {
	header   http.Header
	writeErr error
}

func (w *failingAPIResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingAPIResponseWriter) WriteHeader(int) {}

func (w *failingAPIResponseWriter) Write([]byte) (int, error) {
	return 0, w.writeErr
}

func contextWithAPIUser(user *auth.User) context.Context {
	return context.WithValue(context.Background(), auth.ContextKeyUser, user)
}

func TestAPIStreamResponseErrorWrapsAndTracksStartedState(t *testing.T) {
	baseErr := errors.New("client write failed")
	streamErr := &apiStreamResponseError{err: baseErr, responseStarted: true}

	if streamErr.Error() != baseErr.Error() {
		t.Fatalf("Error() = %q, want %q", streamErr.Error(), baseErr.Error())
	}
	if !errors.Is(streamErr, baseErr) {
		t.Fatalf("expected stream response error to unwrap %v", baseErr)
	}
	if !apiStreamResponseStarted(streamErr) {
		t.Fatal("expected started API stream response to be detected")
	}
	if apiStreamResponseStarted(&apiStreamResponseError{err: baseErr}) {
		t.Fatal("unexpected started response before write")
	}
	if apiStreamResponseStarted(baseErr) {
		t.Fatal("unexpected started response for unrelated error")
	}

	recorder := httptest.NewRecorder()
	if err := streamAPIResponse(recorder, strings.NewReader("ok")); err != nil {
		t.Fatalf("streamAPIResponse(success) error: %v", err)
	}
	if recorder.Body.String() != "ok" {
		t.Fatalf("streamAPIResponse body = %q, want ok", recorder.Body.String())
	}

	failingWriter := &failingAPIResponseWriter{writeErr: baseErr}
	err := streamAPIResponse(failingWriter, strings.NewReader("boom"))
	if !errors.Is(err, baseErr) {
		t.Fatalf("streamAPIResponse(failure) error = %v, want %v", err, baseErr)
	}
	if apiStreamResponseStarted(err) {
		t.Fatal("failed API stream write with no bytes written should not be treated as response-started")
	}
}

func TestFilterSearchResultsByHomeDir_ScopesNonAdminResults(t *testing.T) {
	server := &Server{authEnabled: true}
	ctx := contextWithAPIUser(&auth.User{
		ID:       "u1",
		Username: "tester",
		Role:     auth.RoleUser,
		HomeDir:  "/users/tester",
	})
	results := []*storage.SearchResult{
		{Path: "/users/tester/report.txt"},
		{Path: "/users/tester/nested/photo.jpg"},
		{Path: "/users/tester-archive/leak.txt"},
		{Path: "/shared/outside.txt"},
		nil,
	}

	filtered, err := server.filterSearchResultsByHomeDir(ctx, results)
	if err != nil {
		t.Fatalf("filterSearchResultsByHomeDir() error: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("filtered result count = %d, want 2: %+v", len(filtered), filtered)
	}
	for _, result := range filtered {
		if result == nil || !pathWithinBase("/users/tester", result.Path) {
			t.Fatalf("unexpected filtered result: %+v", result)
		}
	}
}

func TestFilterSearchResultsByHomeDir_ReturnsOriginalWhenUnscoped(t *testing.T) {
	results := []*storage.SearchResult{{Path: "/outside.txt"}}

	for name, tt := range map[string]struct {
		server *Server
		ctx    context.Context
	}{
		"auth disabled": {
			server: &Server{authEnabled: false},
			ctx:    context.Background(),
		},
		"admin": {
			server: &Server{authEnabled: true},
			ctx: contextWithAPIUser(&auth.User{
				ID:       "admin",
				Username: "admin",
				Role:     auth.RoleAdmin,
				HomeDir:  "/admins/admin",
			}),
		},
		"anonymous": {
			server: &Server{authEnabled: true},
			ctx:    context.Background(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			filtered, err := tt.server.filterSearchResultsByHomeDir(tt.ctx, results)
			if err != nil {
				t.Fatalf("filterSearchResultsByHomeDir() error: %v", err)
			}
			if len(filtered) != 1 || filtered[0] != results[0] {
				t.Fatalf("expected original results to be returned, got %+v", filtered)
			}
		})
	}
}

func TestFilterSearchResultsByHomeDir_RejectsInvalidHomeDir(t *testing.T) {
	server := &Server{authEnabled: true}
	ctx := contextWithAPIUser(&auth.User{
		ID:       "u1",
		Username: "tester",
		Role:     auth.RoleUser,
		HomeDir:  "../escape",
	})

	filtered, err := server.filterSearchResultsByHomeDir(ctx, []*storage.SearchResult{{Path: "/anything"}})
	if !errors.Is(err, errPathOutsideHomeDir) {
		t.Fatalf("filterSearchResultsByHomeDir() error = %v, want %v", err, errPathOutsideHomeDir)
	}
	if filtered != nil {
		t.Fatalf("expected nil results on invalid home dir, got %+v", filtered)
	}
}

func TestRespondHomeDirFilterError(t *testing.T) {
	server := &Server{}

	for name, tt := range map[string]struct {
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		"outside home": {
			err:         errPathOutsideHomeDir,
			wantStatus:  http.StatusForbidden,
			wantCode:    ErrCodeForbidden,
			wantMessage: "path is outside the assigned home directory",
		},
		"internal": {
			err:         errors.New("filter failed"),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    ErrCodeInternal,
			wantMessage: "internal server error",
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			server.respondHomeDirFilterError(recorder, "filter search", tt.err)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			var apiErr APIError
			if err := json.Unmarshal(recorder.Body.Bytes(), &apiErr); err != nil {
				t.Fatalf("failed to decode API error: %v", err)
			}
			if apiErr.Code != tt.wantCode || apiErr.Message != tt.wantMessage {
				t.Fatalf("unexpected API error: %+v", apiErr)
			}
		})
	}
}
