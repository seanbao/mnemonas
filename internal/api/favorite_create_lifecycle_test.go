package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/favorites"
	"github.com/seanbao/mnemonas/internal/storage"
)

func TestServer_AddFavoriteSerializesPathCheckAndStoreCommitWithDelete(t *testing.T) {
	server := setupFavoritesPathSyncServer(t)
	deleteURL := deleteFileRequestURL(t, server.fs, "/api/v1/files/docs/a.txt")

	persistEntered := make(chan struct{})
	releasePersist := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releasePersist) }) }
	t.Cleanup(release)

	var syncCalls atomic.Int32
	restoreSync := favorites.SetSyncFavoritesStoreRootDirForTest(func(*os.Root) error {
		if syncCalls.Add(1) == 1 {
			close(persistEntered)
			<-releasePersist
		}
		return nil
	})
	t.Cleanup(restoreSync)

	addDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/a.txt","note":"important"}`))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.Router().ServeHTTP(recorder, req)
		addDone <- recorder
	}()

	select {
	case <-persistEntered:
	case recorder := <-addDone:
		t.Fatalf("favorite creation completed before persistence checkpoint: status=%d body=%s", recorder.Code, recorder.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("favorite creation did not reach the persistence checkpoint")
	}

	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodDelete, deleteURL, nil)
		recorder := httptest.NewRecorder()
		server.Router().ServeHTTP(recorder, req)
		deleteDone <- recorder
	}()

	select {
	case recorder := <-deleteDone:
		t.Fatalf("delete completed before favorite creation released its mutation lease: status=%d body=%s", recorder.Code, recorder.Body.String())
	case <-time.After(150 * time.Millisecond):
	}

	release()
	addRecorder := <-addDone
	if addRecorder.Code != http.StatusCreated {
		t.Fatalf("add favorite status = %d, want %d; body=%s", addRecorder.Code, http.StatusCreated, addRecorder.Body.String())
	}
	deleteRecorder := <-deleteDone
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("delete file status = %d, want %d; body=%s", deleteRecorder.Code, http.StatusOK, deleteRecorder.Body.String())
	}

	if server.favoritesStore.IsFavorite("anonymous", "/docs/a.txt") {
		t.Fatal("favorite committed before delete remained after the file was deleted")
	}
	if _, err := server.fs.Stat(t.Context(), "/docs/a.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat(deleted file) error = %v, want ErrNotFound", err)
	}
}

func TestServer_AddFavoriteRejectsMissingPathBeforePersisting(t *testing.T) {
	server := setupFavoritesPathSyncServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/missing.txt","note":"missing"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.Router().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("add missing favorite status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	if server.favoritesStore.IsFavorite("anonymous", "/docs/missing.txt") {
		t.Fatal("missing path was persisted as a favorite")
	}
}
