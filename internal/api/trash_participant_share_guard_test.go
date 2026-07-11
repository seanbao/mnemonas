package api

import (
	"context"
	"errors"
	"testing"

	"github.com/seanbao/mnemonas/internal/share"
)

func TestDurableTrashParticipantFirstRestoreApplyPreservesShareLifecycleChanges(t *testing.T) {
	ctx := context.Background()
	server, shareStore, _ := newDurableTrashParticipantTestServer(t)
	createShare := func(path string) *share.Share {
		t.Helper()
		created, err := shareStore.Create(share.CreateShareOptions{
			Path:      path,
			Type:      share.ShareTypeFile,
			CreatedBy: "user-a",
		})
		if err != nil {
			t.Fatalf("Create(%s) error: %v", path, err)
		}
		return created
	}

	deletedByUser := createShare("/docs/deleted.txt")
	changedByUser := createShare("/docs/changed.txt")
	enabledByUser := createShare("/docs/enabled.txt")
	ownedByDelete := createShare("/docs/owned.txt")

	hooks := newDurableTrashParticipantHooks(server)
	payload, err := hooks.PrepareDelete(ctx, durableTrashOperationA, "/docs")
	if err != nil {
		t.Fatalf("PrepareDelete() error: %v", err)
	}
	if err := hooks.ApplyDelete(ctx, durableTrashOperationA, "/docs", payload, false); err != nil {
		t.Fatalf("ApplyDelete(precommit) error: %v", err)
	}
	if err := hooks.ApplyDelete(ctx, durableTrashOperationA, "/docs", payload, true); err != nil {
		t.Fatalf("ApplyDelete(committed) error: %v", err)
	}
	if err := hooks.CompleteDelete(ctx, durableTrashOperationA, "/docs", payload); err != nil {
		t.Fatalf("CompleteDelete() error: %v", err)
	}

	if err := shareStore.Delete(deletedByUser.ID); err != nil {
		t.Fatalf("Delete(user-deleted share) error: %v", err)
	}
	if err := shareStore.Update(changedByUser.ID, func(current *share.Share) error {
		current.Path = "/archive/user-moved.txt"
		current.Description = "user changed while item was in Trash"
		return nil
	}); err != nil {
		t.Fatalf("Update(user-changed share) error: %v", err)
	}
	if err := shareStore.Update(enabledByUser.ID, func(current *share.Share) error {
		current.Enabled = true
		return nil
	}); err != nil {
		t.Fatalf("Update(user-enabled share) error: %v", err)
	}

	if err := hooks.ApplyRestore(ctx, durableTrashOperationB, "/docs", "/restored/docs", payload); err != nil {
		t.Fatalf("ApplyRestore() error: %v", err)
	}
	if _, err := shareStore.Get(deletedByUser.ID); !errors.Is(err, share.ErrShareNotFound) {
		t.Fatalf("Get(user-deleted share) error = %v, want ErrShareNotFound", err)
	}
	changed, err := shareStore.Get(changedByUser.ID)
	if err != nil {
		t.Fatalf("Get(user-changed share) error: %v", err)
	}
	if changed.Path != "/archive/user-moved.txt" || changed.Enabled || changed.Description != "user changed while item was in Trash" {
		t.Fatalf("user-changed share was overwritten: %+v", changed)
	}
	enabled, err := shareStore.Get(enabledByUser.ID)
	if err != nil {
		t.Fatalf("Get(user-enabled share) error: %v", err)
	}
	if enabled.Path != "/docs/enabled.txt" || !enabled.Enabled {
		t.Fatalf("user-enabled share was overwritten: %+v", enabled)
	}
	restored, err := shareStore.Get(ownedByDelete.ID)
	if err != nil {
		t.Fatalf("Get(restored share) error: %v", err)
	}
	if restored.Path != "/restored/docs/owned.txt" || !restored.Enabled {
		t.Fatalf("delete-owned share was not restored: %+v", restored)
	}
}
