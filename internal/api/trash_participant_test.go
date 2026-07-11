package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/favorites"
	"github.com/seanbao/mnemonas/internal/share"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/workspace"
)

const (
	durableTrashOperationA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	durableTrashOperationB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	durableTrashOperationC = "cccccccccccccccccccccccccccccccc"
	durableTrashOperationD = "dddddddddddddddddddddddddddddddd"
	durableTrashOperationE = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func newDurableTrashParticipantTestServer(t *testing.T) (*Server, *share.ShareStore, *favorites.Store) {
	t.Helper()
	root := t.TempDir()
	shareStore, err := share.NewShareStore(filepath.Join(root, "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	favoritesStore, err := favorites.NewStore(filepath.Join(root, "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	return &Server{shareStore: shareStore, favoritesStore: favoritesStore}, shareStore, favoritesStore
}

func TestDurableTrashParticipantPrepareDeleteIsReadOnlyAndDeterministic(t *testing.T) {
	server, shareStore, favoritesStore := newDurableTrashParticipantTestServer(t)

	cShare, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/c.txt", Type: share.ShareTypeFile, CreatedBy: "user-b"})
	if err != nil {
		t.Fatalf("Create(/docs/c.txt) error: %v", err)
	}
	aShare, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/a.txt", Type: share.ShareTypeFile, CreatedBy: "user-a"})
	if err != nil {
		t.Fatalf("Create(/docs/a.txt) error: %v", err)
	}
	disabledShare, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/b.txt", Type: share.ShareTypeFile, CreatedBy: "user-a"})
	if err != nil {
		t.Fatalf("Create(/docs/b.txt) error: %v", err)
	}
	if err := shareStore.Update(disabledShare.ID, func(updated *share.Share) error {
		updated.Enabled = false
		return nil
	}); err != nil {
		t.Fatalf("Update(disabled share) error: %v", err)
	}
	if _, err := shareStore.Create(share.CreateShareOptions{Path: "/other.txt", Type: share.ShareTypeFile, CreatedBy: "user-a"}); err != nil {
		t.Fatalf("Create(/other.txt) error: %v", err)
	}

	for _, favorite := range []struct {
		userID string
		path   string
	}{
		{userID: "user-b", path: "/docs/c.txt"},
		{userID: "user-a", path: "/docs/a.txt"},
		{userID: "user-a", path: "/other.txt"},
	} {
		if _, err := favoritesStore.Add(favorite.userID, favorite.path, favorite.path); err != nil {
			t.Fatalf("Add(%s:%s) error: %v", favorite.userID, favorite.path, err)
		}
	}

	notificationCalls := 0
	server.afterPathDeleted = func(string) *storage.PathDeleteHookResult {
		notificationCalls++
		return nil
	}
	hooks := newDurableTrashParticipantHooks(server)
	first, err := hooks.PrepareDelete(context.Background(), durableTrashOperationA, `docs`)
	if err != nil {
		t.Fatalf("PrepareDelete(first) error: %v", err)
	}
	second, err := hooks.PrepareDelete(context.Background(), durableTrashOperationA, "/docs")
	if err != nil {
		t.Fatalf("PrepareDelete(second) error: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("deterministic payload mismatch:\nfirst:  %s\nsecond: %s", first, second)
	}
	otherOperation, err := hooks.PrepareDelete(context.Background(), durableTrashOperationB, "/docs")
	if err != nil {
		t.Fatalf("PrepareDelete(other operation) error: %v", err)
	}
	if bytes.Equal(first, otherOperation) {
		t.Fatal("different delete operation IDs produced the same payload")
	}
	if notificationCalls != 0 {
		t.Fatalf("PrepareDelete notification calls = %d, want 0", notificationCalls)
	}

	payload, err := decodeDurableTrashParticipantPayload(first, "/docs")
	if err != nil {
		t.Fatalf("decodeDurableTrashParticipantPayload() error: %v", err)
	}
	if payload.Version != durableTrashParticipantPayloadVersion || payload.DeleteOperationID != durableTrashOperationA || payload.SourcePath != "/docs" {
		t.Fatalf("payload header = {Version:%d DeleteOperationID:%q SourcePath:%q}", payload.Version, payload.DeleteOperationID, payload.SourcePath)
	}
	otherPayload, err := decodeDurableTrashParticipantPayload(otherOperation, "/docs")
	if err != nil {
		t.Fatalf("decodeDurableTrashParticipantPayload(other operation) error: %v", err)
	}
	if otherPayload.DeleteOperationID != durableTrashOperationB {
		t.Fatalf("other payload delete operation ID = %q, want %q", otherPayload.DeleteOperationID, durableTrashOperationB)
	}
	if len(payload.Shares) != 2 || payload.Shares[0].ID != aShare.ID || payload.Shares[1].ID != cShare.ID {
		t.Fatalf("payload shares = %+v", payload.Shares)
	}
	if len(payload.Favorites) != 2 || payload.Favorites[0].UserID != "user-a" || payload.Favorites[1].UserID != "user-b" {
		t.Fatalf("payload favorites = %+v", payload.Favorites)
	}
	for _, item := range []*share.Share{aShare, cShare} {
		current, err := shareStore.Get(item.ID)
		if err != nil {
			t.Fatalf("Get(%s) error: %v", item.ID, err)
		}
		if !current.Enabled {
			t.Fatalf("PrepareDelete disabled share %s", item.ID)
		}
	}
	if !favoritesStore.IsFavorite("user-a", "/docs/a.txt") || !favoritesStore.IsFavorite("user-b", "/docs/c.txt") {
		t.Fatal("PrepareDelete removed a favorite")
	}
}

func TestDurableTrashParticipantRejectsNonCanonicalOrMismatchedPayload(t *testing.T) {
	server, shareStore, _ := newDurableTrashParticipantTestServer(t)
	created, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/a.txt", Type: share.ShareTypeFile, CreatedBy: "user-a"})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	hooks := newDurableTrashParticipantHooks(server)
	valid, err := hooks.PrepareDelete(context.Background(), durableTrashOperationA, "/docs")
	if err != nil {
		t.Fatalf("PrepareDelete() error: %v", err)
	}

	var wrongVersion durableTrashParticipantPayload
	if err := json.Unmarshal(valid, &wrongVersion); err != nil {
		t.Fatalf("Unmarshal(valid) error: %v", err)
	}
	wrongVersion.Version++
	wrongVersionPayload, err := json.Marshal(wrongVersion)
	if err != nil {
		t.Fatalf("Marshal(wrongVersion) error: %v", err)
	}
	unknownField := append(append([]byte(nil), valid[:len(valid)-1]...), []byte(`,"unknown":true}`)...)
	duplicateVersion := []byte(strings.Replace(string(valid), `{"version":1,`, `{"version":1,"version":1,`, 1))

	tests := []struct {
		name      string
		path      string
		payload   []byte
		operation string
		committed bool
	}{
		{name: "wrong version", path: "/docs", payload: wrongVersionPayload, operation: durableTrashOperationA},
		{name: "unknown field", path: "/docs", payload: unknownField, operation: durableTrashOperationB},
		{name: "duplicate field", path: "/docs", payload: duplicateVersion, operation: durableTrashOperationC},
		{name: "trailing whitespace", path: "/docs", payload: append(append([]byte(nil), valid...), '\n'), operation: durableTrashOperationD},
		{name: "source mismatch", path: "/archive", payload: valid, operation: durableTrashOperationE},
		{name: "operation mismatch", path: "/docs", payload: valid, operation: durableTrashOperationB},
		{name: "empty operation ID", path: "/docs", payload: valid, operation: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := hooks.ApplyDelete(context.Background(), tt.operation, tt.path, tt.payload, tt.committed); err == nil {
				t.Fatal("ApplyDelete() error = nil")
			}
		})
	}

	current, err := shareStore.Get(created.ID)
	if err != nil {
		t.Fatalf("Get(created) error: %v", err)
	}
	if !current.Enabled {
		t.Fatal("rejected payload changed participant state")
	}
	for _, invalidPath := range []string{"", "../docs", "/docs/./report.txt", "/docs/\nreport.txt"} {
		if _, err := hooks.PrepareDelete(context.Background(), durableTrashOperationA, invalidPath); err == nil {
			t.Fatalf("PrepareDelete(%q) error = nil", invalidPath)
		}
	}
}

func TestDurableTrashParticipantApplyDeleteUsesExactSnapshotAndCommittedNotification(t *testing.T) {
	server, shareStore, favoritesStore := newDurableTrashParticipantTestServer(t)
	matching, err := shareStore.Create(share.CreateShareOptions{
		Path:        "/docs/matching.txt",
		Type:        share.ShareTypeFile,
		CreatedBy:   "user-a",
		Description: "original",
	})
	if err != nil {
		t.Fatalf("Create(matching) error: %v", err)
	}
	moved, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/moved.txt", Type: share.ShareTypeFile, CreatedBy: "user-a"})
	if err != nil {
		t.Fatalf("Create(moved) error: %v", err)
	}
	matchingFavorite, err := favoritesStore.Add("user-a", "/docs/matching.txt", "original")
	if err != nil {
		t.Fatalf("Add(matching favorite) error: %v", err)
	}
	recreatedOriginal, err := favoritesStore.Add("user-a", "/docs/recreated.txt", "original")
	if err != nil {
		t.Fatalf("Add(recreated original) error: %v", err)
	}

	notificationCalls := 0
	notificationRollbacks := 0
	server.afterPathDeleted = func(targetPath string) *storage.PathDeleteHookResult {
		if targetPath != "/docs" {
			t.Fatalf("notification path = %q, want /docs", targetPath)
		}
		notificationCalls++
		return &storage.PathDeleteHookResult{Rollback: func() error {
			notificationRollbacks++
			return nil
		}}
	}
	hooks := newDurableTrashParticipantHooks(server)
	payload, err := hooks.PrepareDelete(context.Background(), durableTrashOperationA, "/docs")
	if err != nil {
		t.Fatalf("PrepareDelete() error: %v", err)
	}

	if err := shareStore.Update(matching.ID, func(updated *share.Share) error {
		updated.Description = "newer"
		updated.AccessCount = 9
		return nil
	}); err != nil {
		t.Fatalf("Update(matching metadata) error: %v", err)
	}
	if err := shareStore.Update(moved.ID, func(updated *share.Share) error {
		updated.Path = "/archive/moved.txt"
		return nil
	}); err != nil {
		t.Fatalf("Update(moved path) error: %v", err)
	}
	createdAfterSnapshot, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/new.txt", Type: share.ShareTypeFile, CreatedBy: "user-a"})
	if err != nil {
		t.Fatalf("Create(after snapshot) error: %v", err)
	}
	if err := favoritesStore.Remove("user-a", recreatedOriginal.Path); err != nil {
		t.Fatalf("Remove(recreated original) error: %v", err)
	}
	time.Sleep(time.Millisecond)
	recreated, err := favoritesStore.Add("user-a", recreatedOriginal.Path, "recreated")
	if err != nil {
		t.Fatalf("Add(recreated) error: %v", err)
	}
	if recreated.CreatedAt.Equal(recreatedOriginal.CreatedAt) {
		t.Fatal("recreated favorite must have a new identity")
	}
	newFavorite, err := favoritesStore.Add("user-a", "/docs/new.txt", "new")
	if err != nil {
		t.Fatalf("Add(new favorite) error: %v", err)
	}

	if err := hooks.ApplyDelete(context.Background(), durableTrashOperationA, "/docs", payload, false); err != nil {
		t.Fatalf("ApplyDelete(pre-commit) error: %v", err)
	}
	if notificationCalls != 0 {
		t.Fatalf("pre-commit notification calls = %d, want 0", notificationCalls)
	}
	deletedShare, err := shareStore.Get(matching.ID)
	if err != nil {
		t.Fatalf("Get(matching) error: %v", err)
	}
	if deletedShare.Enabled || deletedShare.Description != "newer" || deletedShare.AccessCount != 9 {
		t.Fatalf("matching share = %+v", deletedShare)
	}
	movedShare, err := shareStore.Get(moved.ID)
	if err != nil {
		t.Fatalf("Get(moved) error: %v", err)
	}
	if movedShare.Path != "/archive/moved.txt" || !movedShare.Enabled {
		t.Fatalf("moved share = %+v", movedShare)
	}
	newShare, err := shareStore.Get(createdAfterSnapshot.ID)
	if err != nil {
		t.Fatalf("Get(createdAfterSnapshot) error: %v", err)
	}
	if !newShare.Enabled {
		t.Fatal("share created after the snapshot was disabled")
	}
	if favoritesStore.IsFavorite("user-a", matchingFavorite.Path) {
		t.Fatal("matching favorite was not removed")
	}
	if !favoritesStore.IsFavorite("user-a", recreated.Path) || !favoritesStore.IsFavorite("user-a", newFavorite.Path) {
		t.Fatal("newer favorite state was removed")
	}

	for i := 0; i < 2; i++ {
		if err := hooks.ApplyDelete(context.Background(), durableTrashOperationA, "/docs", payload, true); err != nil {
			t.Fatalf("ApplyDelete(committed replay %d) error: %v", i, err)
		}
	}
	if notificationCalls != 2 {
		t.Fatalf("committed notification calls = %d, want 2", notificationCalls)
	}
	if notificationRollbacks != 0 {
		t.Fatalf("committed notification rollback calls = %d, want 0", notificationRollbacks)
	}
}

func TestDurableTrashParticipantCommittedNotificationRejectsRestoreMetadata(t *testing.T) {
	server := &Server{}
	notificationCalls := 0
	notificationRollbacks := 0
	server.afterPathDeleted = func(string) *storage.PathDeleteHookResult {
		notificationCalls++
		return &storage.PathDeleteHookResult{
			RestoreData: []byte(`{"unsupported":true}`),
			Rollback: func() error {
				notificationRollbacks++
				return nil
			},
		}
	}
	hooks := newDurableTrashParticipantHooks(server)
	payload, err := hooks.PrepareDelete(context.Background(), durableTrashOperationA, "/docs")
	if err != nil {
		t.Fatalf("PrepareDelete() error: %v", err)
	}
	if len(payload) == 0 {
		t.Fatal("empty participant payload would skip the committed notification")
	}
	if err := hooks.ApplyDelete(context.Background(), durableTrashOperationA, "/docs", payload, false); err != nil {
		t.Fatalf("ApplyDelete(pre-commit) error: %v", err)
	}
	if notificationCalls != 0 {
		t.Fatalf("pre-commit notification calls = %d, want 0", notificationCalls)
	}

	err = hooks.ApplyDelete(context.Background(), durableTrashOperationA, "/docs", payload, true)
	if err == nil || !strings.Contains(err.Error(), "unsupported restore metadata") {
		t.Fatalf("ApplyDelete(committed) error = %v", err)
	}
	if notificationCalls != 1 {
		t.Fatalf("committed notification calls = %d, want 1", notificationCalls)
	}
	if notificationRollbacks != 0 {
		t.Fatalf("committed notification rollback calls = %d, want 0", notificationRollbacks)
	}
}

func TestDurableTrashParticipantRollbackDeleteDoesNotOverwriteNewerState(t *testing.T) {
	server, shareStore, favoritesStore := newDurableTrashParticipantTestServer(t)
	createdShare, err := shareStore.Create(share.CreateShareOptions{
		Path:        "/docs/report.txt",
		Type:        share.ShareTypeFile,
		CreatedBy:   "user-a",
		Description: "original",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	createdFavorite, err := favoritesStore.Add("user-a", "/docs/report.txt", "original")
	if err != nil {
		t.Fatalf("Add() error: %v", err)
	}

	notificationCalls := 0
	server.afterPathDeleted = func(string) *storage.PathDeleteHookResult {
		notificationCalls++
		return nil
	}
	hooks := newDurableTrashParticipantHooks(server)
	payload, err := hooks.PrepareDelete(context.Background(), durableTrashOperationA, "/docs")
	if err != nil {
		t.Fatalf("PrepareDelete() error: %v", err)
	}
	if err := hooks.ApplyDelete(context.Background(), durableTrashOperationA, "/docs", payload, false); err != nil {
		t.Fatalf("ApplyDelete() error: %v", err)
	}
	if err := shareStore.Update(createdShare.ID, func(updated *share.Share) error {
		updated.Description = "newer"
		return nil
	}); err != nil {
		t.Fatalf("Update(newer share metadata) error: %v", err)
	}
	time.Sleep(time.Millisecond)
	if _, err := favoritesStore.Add("user-a", createdFavorite.Path, "newer"); err != nil {
		t.Fatalf("Add(recreated favorite) error: %v", err)
	}

	if err := hooks.RollbackDelete(context.Background(), durableTrashOperationA, "/docs", payload); err != nil {
		t.Fatalf("RollbackDelete() error: %v", err)
	}
	if err := hooks.RollbackDelete(context.Background(), durableTrashOperationA, "/docs", payload); err != nil {
		t.Fatalf("RollbackDelete(replay) error: %v", err)
	}
	if notificationCalls != 0 {
		t.Fatalf("rollback notification calls = %d, want 0", notificationCalls)
	}
	currentShare, err := shareStore.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(current share) error: %v", err)
	}
	if currentShare.Enabled || currentShare.Description != "newer" {
		t.Fatalf("rollback overwrote newer share intent: %+v", currentShare)
	}
	restoredFavorites := favoritesStore.List("user-a")
	if len(restoredFavorites) != 1 || restoredFavorites[0].Note != "newer" {
		t.Fatalf("restored favorites = %+v", restoredFavorites)
	}
}

func TestDurableTrashParticipantApplyRestoreRelocatesAndIsIdempotent(t *testing.T) {
	server, shareStore, favoritesStore := newDurableTrashParticipantTestServer(t)
	createdShare, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/report.txt", Type: share.ShareTypeFile, CreatedBy: "user-a"})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := favoritesStore.Add("user-a", "/docs/report.txt", "report"); err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	hooks := newDurableTrashParticipantHooks(server)
	payload, err := hooks.PrepareDelete(context.Background(), durableTrashOperationA, "/docs")
	if err != nil {
		t.Fatalf("PrepareDelete() error: %v", err)
	}
	if err := hooks.ApplyDelete(context.Background(), durableTrashOperationA, "/docs", payload, false); err != nil {
		t.Fatalf("ApplyDelete(precommit) error: %v", err)
	}
	if err := hooks.ApplyDelete(context.Background(), durableTrashOperationA, "/docs", payload, true); err != nil {
		t.Fatalf("ApplyDelete(committed) error: %v", err)
	}
	if err := hooks.CompleteDelete(context.Background(), durableTrashOperationA, "/docs", payload); err != nil {
		t.Fatalf("CompleteDelete() error: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := hooks.ApplyRestore(context.Background(), durableTrashOperationB, "/docs", "/restored/docs", payload); err != nil {
			t.Fatalf("ApplyRestore(replay %d) error: %v", i, err)
		}
	}
	restoredShare, err := shareStore.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(restored share) error: %v", err)
	}
	if restoredShare.Path != "/restored/docs/report.txt" || !restoredShare.Enabled {
		t.Fatalf("restored share = %+v", restoredShare)
	}
	if !favoritesStore.IsFavorite("user-a", "/restored/docs/report.txt") {
		t.Fatal("relocated favorite was not restored")
	}
	if favoritesStore.IsFavorite("user-a", "/docs/report.txt") {
		t.Fatal("favorite was restored at its original path")
	}
}

func TestDurableTrashParticipantRestoreReceiptProtectsUserStateAcrossReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	shareStorePath := filepath.Join(root, "shares.json")
	favoritesStorePath := filepath.Join(root, "favorites.json")
	shareStore, err := share.NewShareStore(shareStorePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	favoritesStore, err := favorites.NewStore(favoritesStorePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	createdShare, err := shareStore.Create(share.CreateShareOptions{
		Path:        "/docs/report.txt",
		Type:        share.ShareTypeFile,
		CreatedBy:   "user-a",
		Description: "original",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := favoritesStore.Add("user-a", "/docs/report.txt", "original"); err != nil {
		t.Fatalf("Add() error: %v", err)
	}

	hooks := newDurableTrashParticipantHooks(&Server{shareStore: shareStore, favoritesStore: favoritesStore})
	payload, err := hooks.PrepareDelete(ctx, durableTrashOperationA, "/docs")
	if err != nil {
		t.Fatalf("PrepareDelete() error: %v", err)
	}
	if err := hooks.ApplyDelete(ctx, durableTrashOperationA, "/docs", payload, false); err != nil {
		t.Fatalf("ApplyDelete(pre-commit) error: %v", err)
	}
	if err := hooks.ApplyDelete(ctx, durableTrashOperationA, "/docs", payload, true); err != nil {
		t.Fatalf("ApplyDelete(committed) error: %v", err)
	}
	if err := hooks.CompleteDelete(ctx, durableTrashOperationA, "/docs", payload); err != nil {
		t.Fatalf("CompleteDelete() error: %v", err)
	}

	const restoredPath = "/restored/docs"
	if err := hooks.ApplyRestore(ctx, durableTrashOperationB, "/docs", restoredPath, payload); err != nil {
		t.Fatalf("ApplyRestore(first) error: %v", err)
	}
	assertDurableTrashRestoreReceipt(t, shareStorePath, durableTrashOperationB, true)
	assertDurableTrashRestoreReceipt(t, favoritesStorePath, durableTrashOperationB, true)

	if err := shareStore.Update(createdShare.ID, func(updated *share.Share) error {
		updated.Path = "/archive/user-moved.txt"
		updated.Enabled = false
		updated.Description = "user updated"
		return nil
	}); err != nil {
		t.Fatalf("Update(user state) error: %v", err)
	}
	if err := favoritesStore.Remove("user-a", "/restored/docs/report.txt"); err != nil {
		t.Fatalf("Remove(restored favorite) error: %v", err)
	}

	reopenedShares, err := share.NewShareStore(shareStorePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen before replay) error: %v", err)
	}
	reopenedFavorites, err := favorites.NewStore(favoritesStorePath)
	if err != nil {
		t.Fatalf("NewStore(reopen before replay) error: %v", err)
	}
	reopenedHooks := newDurableTrashParticipantHooks(&Server{
		shareStore:     reopenedShares,
		favoritesStore: reopenedFavorites,
	})
	if err := reopenedHooks.ApplyRestore(ctx, durableTrashOperationB, "/docs", restoredPath, payload); err != nil {
		t.Fatalf("ApplyRestore(replay after reopen) error: %v", err)
	}
	assertDurableTrashRestoreUserState(t, reopenedShares, reopenedFavorites, createdShare.ID)

	if err := reopenedHooks.CompleteRestore(ctx, durableTrashOperationB, "/docs", restoredPath, payload); err != nil {
		t.Fatalf("CompleteRestore() error: %v", err)
	}
	if err := reopenedHooks.CompleteRestore(ctx, durableTrashOperationB, "/docs", restoredPath, payload); err != nil {
		t.Fatalf("CompleteRestore(idempotent) error: %v", err)
	}
	assertDurableTrashRestoreReceipt(t, shareStorePath, durableTrashOperationB, false)
	assertDurableTrashRestoreReceipt(t, favoritesStorePath, durableTrashOperationB, false)

	completedShares, err := share.NewShareStore(shareStorePath)
	if err != nil {
		t.Fatalf("NewShareStore(reopen after completion) error: %v", err)
	}
	completedFavorites, err := favorites.NewStore(favoritesStorePath)
	if err != nil {
		t.Fatalf("NewStore(reopen after completion) error: %v", err)
	}
	assertDurableTrashRestoreUserState(t, completedShares, completedFavorites, createdShare.ID)
}

func assertDurableTrashRestoreUserState(
	t *testing.T,
	shareStore *share.ShareStore,
	favoritesStore *favorites.Store,
	shareID string,
) {
	t.Helper()
	current, err := shareStore.Get(shareID)
	if err != nil {
		t.Fatalf("Get(user-updated share) error: %v", err)
	}
	if current.Path != "/archive/user-moved.txt" || current.Enabled || current.Description != "user updated" {
		t.Fatalf("restore replay changed user-updated share: %+v", current)
	}
	if favoritesStore.IsFavorite("user-a", "/restored/docs/report.txt") {
		t.Fatal("restore replay recreated a user-deleted favorite")
	}
}

func assertDurableTrashRestoreReceipt(t *testing.T, storePath, operationID string, want bool) {
	t.Helper()
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", storePath, err)
	}
	var state struct {
		TrashRestoreOperations map[string]json.RawMessage `json:"trash_restore_operations"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal(%s) error: %v", storePath, err)
	}
	if state.TrashRestoreOperations == nil {
		t.Fatalf("%s has a null trash_restore_operations map", storePath)
	}
	_, exists := state.TrashRestoreOperations[operationID]
	if exists != want {
		t.Fatalf("restore receipt %q in %s: exists=%t, want %t", operationID, storePath, exists, want)
	}
}

func TestDurableTrashParticipantCompletePurgeRemovesDeleteOwnership(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	shareStorePath := filepath.Join(root, "shares.json")
	favoritesStorePath := filepath.Join(root, "favorites.json")
	shareStore, err := share.NewShareStore(shareStorePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	favoritesStore, err := favorites.NewStore(favoritesStorePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := shareStore.Create(share.CreateShareOptions{
		Path:      "/docs/report.txt",
		Type:      share.ShareTypeFile,
		CreatedBy: "user-a",
	}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := favoritesStore.Add("user-a", "/docs/report.txt", "report"); err != nil {
		t.Fatalf("Add() error: %v", err)
	}

	participant := &durableTrashParticipant{server: &Server{
		shareStore:     shareStore,
		favoritesStore: favoritesStore,
	}}
	payload, err := participant.prepareDelete(ctx, durableTrashOperationA, "/docs")
	if err != nil {
		t.Fatalf("prepareDelete() error: %v", err)
	}
	if err := participant.applyDelete(ctx, durableTrashOperationA, "/docs", payload, false); err != nil {
		t.Fatalf("applyDelete(precommit) error: %v", err)
	}
	if err := participant.applyDelete(ctx, durableTrashOperationA, "/docs", payload, true); err != nil {
		t.Fatalf("applyDelete(committed) error: %v", err)
	}
	if err := participant.completeDelete(ctx, durableTrashOperationA, "/docs", payload); err != nil {
		t.Fatalf("completeDelete() error: %v", err)
	}
	assertDurableTrashDeleteOwnership(t, shareStorePath, durableTrashOperationA, true)
	assertDurableTrashDeleteOwnership(t, favoritesStorePath, durableTrashOperationA, true)

	if err := participant.validatePurge(ctx, durableTrashOperationC, "/docs", payload); err != nil {
		t.Fatalf("validatePurge() error: %v", err)
	}
	if err := participant.completePurge(ctx, durableTrashOperationC, "/docs", payload); err != nil {
		t.Fatalf("completePurge() error: %v", err)
	}
	if err := participant.completePurge(ctx, durableTrashOperationC, "/docs", payload); err != nil {
		t.Fatalf("completePurge(replay) error: %v", err)
	}
	assertDurableTrashDeleteOwnership(t, shareStorePath, durableTrashOperationA, false)
	assertDurableTrashDeleteOwnership(t, favoritesStorePath, durableTrashOperationA, false)

	if err := participant.applyRestore(ctx, durableTrashOperationB, "/docs", "/restored/docs", payload); err == nil {
		t.Fatal("applyRestore() after purge error = nil")
	}
}

func assertDurableTrashDeleteOwnership(t *testing.T, storePath, operationID string, want bool) {
	t.Helper()
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", storePath, err)
	}
	var state struct {
		TrashDeleteOperations map[string]json.RawMessage `json:"trash_delete_operations"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal(%s) error: %v", storePath, err)
	}
	if state.TrashDeleteOperations == nil {
		t.Fatalf("%s has a null trash_delete_operations map", storePath)
	}
	_, exists := state.TrashDeleteOperations[operationID]
	if exists != want {
		t.Fatalf("delete ownership %q in %s: exists=%t, want %t", operationID, storePath, exists, want)
	}
}

func TestDurableTrashParticipantPersistenceWarningsRemainVisible(t *testing.T) {
	server, shareStore, favoritesStore := newDurableTrashParticipantTestServer(t)
	createdShare, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/report.txt", Type: share.ShareTypeFile, CreatedBy: "user-a"})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := favoritesStore.Add("user-a", "/docs/report.txt", "report"); err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	hooks := newDurableTrashParticipantHooks(server)
	payload, err := hooks.PrepareDelete(context.Background(), durableTrashOperationA, "/docs")
	if err != nil {
		t.Fatalf("PrepareDelete() error: %v", err)
	}

	restoreShareSync := share.SetSyncShareStoreRootDirForTest(func(*os.Root) error {
		return errors.New("share directory fsync failed")
	})
	restoreFavoriteSync := favorites.SetSyncFavoritesStoreRootDirForTest(func(*os.Root) error {
		return errors.New("favorites directory fsync failed")
	})
	defer restoreShareSync()
	defer restoreFavoriteSync()

	applyErr := hooks.ApplyDelete(context.Background(), durableTrashOperationA, "/docs", payload, false)
	if !workspace.IsVisibleMutationWarning(applyErr) {
		t.Fatalf("ApplyDelete() error = %v, want visible mutation warning", applyErr)
	}
	currentShare, err := shareStore.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(current share) error: %v", err)
	}
	if currentShare.Enabled || favoritesStore.IsFavorite("user-a", "/docs/report.txt") {
		t.Fatal("persistence warnings did not commit participant state")
	}

	rollbackErr := hooks.RollbackDelete(context.Background(), durableTrashOperationA, "/docs", payload)
	if rollbackErr == nil {
		t.Fatal("RollbackDelete() error = nil, want persistence warning")
	}
	restoredShare, err := shareStore.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(restored share) error: %v", err)
	}
	if !restoredShare.Enabled || !favoritesStore.IsFavorite("user-a", "/docs/report.txt") {
		t.Fatal("rollback warning did not restore participant state")
	}
}
