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

	"github.com/seanbao/mnemonas/internal/share"
	"github.com/seanbao/mnemonas/internal/storage"
)

func TestServer_CreateShareSerializesStatAndStoreCommitWithDelete(t *testing.T) {
	server, _ := setupShareServer(t)
	deleteURL := deleteFileRequestURL(t, server.fs, "/api/v1/files/docs/a.txt")

	persistEntered := make(chan struct{})
	releasePersist := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releasePersist) }) }
	t.Cleanup(release)

	var syncCalls atomic.Int32
	restoreSync := share.SetSyncShareStoreRootDirForTest(func(*os.Root) error {
		if syncCalls.Add(1) == 1 {
			close(persistEntered)
			<-releasePersist
		}
		return nil
	})
	t.Cleanup(restoreSync)

	createDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", strings.NewReader(`{"path":"/docs/a.txt","type":"file"}`))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.Router().ServeHTTP(recorder, req)
		createDone <- recorder
	}()

	select {
	case <-persistEntered:
	case recorder := <-createDone:
		t.Fatalf("share creation completed before persistence checkpoint: status=%d body=%s", recorder.Code, recorder.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("share creation did not reach the persistence checkpoint")
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
		t.Fatalf("delete completed before share creation released its mutation lease: status=%d body=%s", recorder.Code, recorder.Body.String())
	case <-time.After(150 * time.Millisecond):
	}

	release()
	createRecorder := <-createDone
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create share status = %d, want %d; body=%s", createRecorder.Code, http.StatusCreated, createRecorder.Body.String())
	}
	deleteRecorder := <-deleteDone
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("delete file status = %d, want %d; body=%s", deleteRecorder.Code, http.StatusOK, deleteRecorder.Body.String())
	}

	var createdID string
	for _, current := range server.shareStore.ListAll() {
		if current.Path == "/docs/a.txt" {
			createdID = current.ID
			if current.Enabled {
				t.Fatalf("share created before delete remained enabled: %+v", current)
			}
		}
	}
	if createdID == "" {
		t.Fatal("created share was not persisted")
	}
	if _, err := server.fs.Stat(t.Context(), "/docs/a.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat(deleted file) error = %v, want ErrNotFound", err)
	}
}
