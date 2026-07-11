package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/favorites"
	"github.com/seanbao/mnemonas/internal/share"
	"github.com/seanbao/mnemonas/internal/storage"
)

func TestServerDeleteFromTrashPurgesDurableParticipantOwnership(t *testing.T) {
	server, fs, root := setupTestServer(t)
	ctx := context.Background()
	shareStorePath := filepath.Join(root, "purge-shares.json")
	favoritesStorePath := filepath.Join(root, "purge-favorites.json")
	shareStore, err := share.NewShareStore(shareStorePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	favoritesStore, err := favorites.NewStore(favoritesStorePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	server.shareStore = shareStore
	server.favoritesStore = favoritesStore
	fs.SetTrashParticipantHooks(newDurableTrashParticipantHooks(server))

	const filePath = "/purge-participant/report.txt"
	if err := fs.Mkdir(ctx, "/purge-participant"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, filePath, bytes.NewReader([]byte("purge participant"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if _, err := shareStore.Create(share.CreateShareOptions{
		Path:      filePath,
		Type:      share.ShareTypeFile,
		CreatedBy: "tester",
	}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := favoritesStore.Add("tester", filePath, "report"); err != nil {
		t.Fatalf("Add() error: %v", err)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, deleteFileRequestURL(t, fs, "/api/v1/files"+filePath), nil)
	deleteResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete-to-Trash status = %d, want %d; body=%s", deleteResponse.Code, http.StatusOK, deleteResponse.Body.String())
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ListTrash() items = %d, want 1", len(items))
	}
	payload, err := decodeDurableTrashParticipantPayload(items[0].RestoreData, filePath)
	if err != nil {
		t.Fatalf("decodeDurableTrashParticipantPayload() error: %v", err)
	}
	assertDurableTrashDeleteOwnership(t, shareStorePath, payload.DeleteOperationID, true)
	assertDurableTrashDeleteOwnership(t, favoritesStorePath, payload.DeleteOperationID, true)

	purgeRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/trash/"+items[0].ID, nil)
	purgeResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(purgeResponse, purgeRequest)
	if purgeResponse.Code != http.StatusOK {
		t.Fatalf("permanent Trash delete status = %d, want %d; body=%s", purgeResponse.Code, http.StatusOK, purgeResponse.Body.String())
	}
	assertDurableTrashDeleteOwnership(t, shareStorePath, payload.DeleteOperationID, false)
	assertDurableTrashDeleteOwnership(t, favoritesStorePath, payload.DeleteOperationID, false)
	items, err = fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after purge) error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("ListTrash(after purge) items = %d, want 0", len(items))
	}
}

func TestServerDeleteFromTrashParticipantWarningRetainsRecoveryGate(t *testing.T) {
	server, fs, root := setupTestServer(t)
	ctx := context.Background()
	shareStorePath := filepath.Join(root, "warning-purge-shares.json")
	favoritesStorePath := filepath.Join(root, "warning-purge-favorites.json")
	shareStore, err := share.NewShareStore(shareStorePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	favoritesStore, err := favorites.NewStore(favoritesStorePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	server.shareStore = shareStore
	server.favoritesStore = favoritesStore
	fs.SetTrashParticipantHooks(newDurableTrashParticipantHooks(server))

	const filePath = "/warning-purge-participant/report.txt"
	if err := fs.Mkdir(ctx, "/warning-purge-participant"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, filePath, bytes.NewReader([]byte("warning purge participant"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if _, err := shareStore.Create(share.CreateShareOptions{
		Path:      filePath,
		Type:      share.ShareTypeFile,
		CreatedBy: "tester",
	}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := favoritesStore.Add("tester", filePath, "report"); err != nil {
		t.Fatalf("Add() error: %v", err)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, deleteFileRequestURL(t, fs, "/api/v1/files"+filePath), nil)
	deleteResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete-to-Trash status = %d, want %d; body=%s", deleteResponse.Code, http.StatusOK, deleteResponse.Body.String())
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	payload, err := decodeDurableTrashParticipantPayload(items[0].RestoreData, filePath)
	if err != nil {
		t.Fatalf("decodeDurableTrashParticipantPayload() error: %v", err)
	}

	restoreSync := favorites.SetSyncFavoritesStoreRootDirForTest(func(*os.Root) error {
		return os.ErrInvalid
	})
	syncRestored := false
	t.Cleanup(func() {
		if !syncRestored {
			restoreSync()
		}
	})
	purgeRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/trash/"+items[0].ID, nil)
	purgeResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(purgeResponse, purgeRequest)
	if purgeResponse.Code != http.StatusOK {
		t.Fatalf("permanent Trash delete status = %d, want %d; body=%s", purgeResponse.Code, http.StatusOK, purgeResponse.Body.String())
	}
	if !strings.Contains(strings.Join(purgeResponse.Header().Values("Warning"), ","), trashDeleteCleanupWarningHeader) {
		t.Fatalf("permanent Trash delete warnings = %v, want %q", purgeResponse.Header().Values("Warning"), trashDeleteCleanupWarningHeader)
	}
	if !strings.Contains(purgeResponse.Body.String(), `"warning":true`) {
		t.Fatalf("permanent Trash delete body = %s, want warning=true", purgeResponse.Body.String())
	}
	assertDurableTrashDeleteOwnership(t, shareStorePath, payload.DeleteOperationID, false)
	assertDurableTrashDeleteOwnership(t, favoritesStorePath, payload.DeleteOperationID, false)

	if err := fs.Mkdir(ctx, "/blocked-before-purge-recovery"); !errors.Is(err, storage.ErrTrashRecoveryRequired) {
		t.Fatalf("Mkdir() before purge recovery error = %v, want ErrTrashRecoveryRequired", err)
	}
	restoreSync()
	syncRestored = true
	report, err := fs.RecoverTrashDeletions(ctx)
	if err != nil {
		t.Fatalf("RecoverTrashDeletions() error: %v", err)
	}
	if report.RolledForward != 1 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want one roll-forward", report)
	}
	if err := fs.Mkdir(ctx, "/allowed-after-purge-recovery"); err != nil {
		t.Fatalf("Mkdir() after purge recovery error: %v", err)
	}
}
