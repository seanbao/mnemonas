package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireTrashTransferOwnershipMarkerName(t *testing.T, operationID string) string {
	t.Helper()
	name, err := trashTransferOwnershipMarkerName(operationID)
	if err != nil {
		t.Fatalf("trashTransferOwnershipMarkerName(%q) error: %v", operationID, err)
	}
	return name
}

func TestFileSystem_RecoverTrashTransfersRollsBackMarkedPreparedOrphans(t *testing.T) {
	t.Run("delete container", func(t *testing.T) {
		fixture := newTrashPurgeRecoveryTestFixture(t)
		record := newPreparedDeleteTrashTransferRecord(t, fixture)
		publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
		trashRoot := &storagePathRoot{absRoot: fixture.fs.trashRoot, handle: fixture.fs.trashRootHandle}
		stageRel := filepath.FromSlash(record.TrashStagePath)
		if _, _, err := fixture.fs.createPreparedTrashTransferOwnedContainer(trashRoot, stageRel, record, trashTransferOwnershipRoleTrashContainer); err != nil {
			t.Fatalf("createPreparedTrashTransferOwnedContainer() error: %v", err)
		}
		fixture.reopen(t)

		report, err := fixture.fs.RecoverTrashTransfers(context.Background())
		if err != nil || report.RolledBack != 1 {
			t.Fatalf("RecoverTrashTransfers() = %+v, %v, want one rollback", report, err)
		}
		if _, err := os.Lstat(filepath.Join(fixture.fs.trashRoot, stageRel)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Lstat(orphan container) error = %v, want os.ErrNotExist", err)
		}
		requireDeleteTrashTransferSourceForManifest(t, fixture, record)
		requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
	})

	t.Run("restore parents and container", func(t *testing.T) {
		fixture := newTrashPurgeRecoveryTestFixture(t)
		item := seedTrashPurgeRecoveryItem(t, fixture.fs, "restore-transfer-item", false, map[string]string{".": trashTransferRollbackTestPayload})
		_, record := prepareRestoreTrashTransferItemAtDestinationForTest(
			t,
			fixture,
			strings.Repeat("6", 32),
			nil,
			item,
			"/new/.mnemonas-trash-transfer-owner/file",
		)
		created, err := fixture.fs.createPreparedTrashTransferWorkspaceParentDirs(record)
		if err != nil || len(created) != len(record.WorkspaceParentDirs) {
			t.Fatalf("createPreparedTrashTransferWorkspaceParentDirs() = %+v, %v", created, err)
		}
		workspaceRoot := &storagePathRoot{absRoot: fixture.fs.workspace.Root(), handle: fixture.fs.filesRootHandle}
		stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
		if _, _, err := fixture.fs.createPreparedTrashTransferOwnedContainer(workspaceRoot, stageRel, record, trashTransferOwnershipRoleWorkspaceContainer); err != nil {
			t.Fatalf("createPreparedTrashTransferOwnedContainer() error: %v", err)
		}
		fixture.reopen(t)

		report, err := fixture.fs.RecoverTrashTransfers(context.Background())
		if err != nil || report.RolledBack != 1 {
			t.Fatalf("RecoverTrashTransfers() = %+v, %v, want one rollback", report, err)
		}
		if _, err := os.Lstat(fixture.fs.workspace.FullPath("/new")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Lstat(marked restore parent) error = %v, want os.ErrNotExist", err)
		}
		requireTrashTransferPathPayloadForManifest(t, fixture, record)
		requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
	})
}

func TestFileSystem_RestoreFromTrashAllowsOwnershipMarkerPrefixPathComponent(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	const sourcePath = "/owner-marker-source.bin"
	const destinationPath = "/new/.mnemonas-trash-transfer-owner/file"
	if err := fs.WriteFile(ctx, sourcePath, strings.NewReader("payload")); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.Delete(ctx, sourcePath); err != nil {
		t.Fatalf("Delete(source) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v, want one item", items, err)
	}
	if err := fs.RestoreFromTrashTo(ctx, items[0].ID, destinationPath); err != nil {
		t.Fatalf("RestoreFromTrashTo() error: %v", err)
	}
	data, err := os.ReadFile(fs.workspace.FullPath(destinationPath))
	if err != nil || string(data) != "payload" {
		t.Fatalf("ReadFile(destination) = %q, %v, want payload", data, err)
	}
	if fs.trashMutationBlocked != nil {
		t.Fatalf("trash mutation gate after restore = %v, want nil", fs.trashMutationBlocked)
	}
	operations, err := fs.versions.ListTrashOperations(ctx)
	if err != nil || len(operations) != 0 {
		t.Fatalf("ListTrashOperations() = %+v, %v, want empty", operations, err)
	}
	for _, root := range []string{fs.workspace.Root(), fs.trashRoot} {
		var leakedMarker string
		walkErr := filepath.Walk(root, func(current string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info != nil && strings.HasPrefix(info.Name(), trashTransferOwnershipMarkerPrefix) {
				leakedMarker = current
				return errors.New("leaked Trash transfer ownership marker")
			}
			return nil
		})
		if walkErr != nil || leakedMarker != "" {
			t.Fatalf("ownership marker scan under %q = %q, %v", root, leakedMarker, walkErr)
		}
	}
}

func TestFileSystem_RecoverTrashTransfersHandlesPreMarkerMkdirWindow(t *testing.T) {
	t.Run("empty private delete stage is reclaimed", func(t *testing.T) {
		fixture := newTrashPurgeRecoveryTestFixture(t)
		record := newPreparedDeleteTrashTransferRecord(t, fixture)
		publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
		trashRoot := &storagePathRoot{absRoot: fixture.fs.trashRoot, handle: fixture.fs.trashRootHandle}
		stageRel := filepath.FromSlash(record.TrashStagePath)
		if _, _, err := fixture.fs.createTrashTransferOwnedContainer(trashRoot, stageRel); err != nil {
			t.Fatalf("createTrashTransferOwnedContainer() error: %v", err)
		}
		fixture.reopen(t)

		report, err := fixture.fs.RecoverTrashTransfers(context.Background())
		if err != nil || report.RolledBack != 1 {
			t.Fatalf("RecoverTrashTransfers() = %+v, %v, want one rollback", report, err)
		}
		if _, err := os.Lstat(filepath.Join(fixture.fs.trashRoot, stageRel)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Lstat(unmarked empty stage) error = %v, want os.ErrNotExist", err)
		}
	})

	t.Run("unmarked restore parent is retained", func(t *testing.T) {
		fixture := newTrashPurgeRecoveryTestFixture(t)
		_, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("7", 32), nil)
		created, err := fixture.fs.createTrashTransferWorkspaceParentDirs(record.WorkspaceParentDirs)
		if err != nil || len(created) != len(record.WorkspaceParentDirs) {
			t.Fatalf("createTrashTransferWorkspaceParentDirs() = %+v, %v", created, err)
		}
		fixture.reopen(t)

		report, err := fixture.fs.RecoverTrashTransfers(context.Background())
		if err != nil || report.RolledBack != 1 {
			t.Fatalf("RecoverTrashTransfers() = %+v, %v, want one rollback", report, err)
		}
		info, err := os.Lstat(fixture.fs.workspace.FullPath("/restored"))
		if err != nil || !info.IsDir() {
			t.Fatalf("Lstat(retained unmarked parent) = %+v, %v, want directory", info, err)
		}
		requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
	})
}

func TestFileSystem_RecoverTrashTransfersRejectsTamperedPreparedOwnership(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{
			name: "tampered marker",
			mutate: func(t *testing.T, stageAbs, markerName string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(stageAbs, markerName), []byte("{}\n"), 0o600); err != nil {
					t.Fatalf("WriteFile(tampered marker) error: %v", err)
				}
			},
		},
		{
			name: "unknown entry",
			mutate: func(t *testing.T, stageAbs, _ string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(stageAbs, "unknown"), []byte("external"), 0o600); err != nil {
					t.Fatalf("WriteFile(unknown entry) error: %v", err)
				}
			},
		},
		{
			name: "replaced container",
			mutate: func(t *testing.T, stageAbs, markerName string) {
				t.Helper()
				marker, err := os.ReadFile(filepath.Join(stageAbs, markerName))
				if err != nil {
					t.Fatalf("ReadFile(ownership marker) error: %v", err)
				}
				backupAbs := filepath.Join(filepath.Dir(filepath.Dir(stageAbs)), "ownership-replaced-backup")
				if err := os.Rename(stageAbs, backupAbs); err != nil {
					t.Fatalf("Rename(original container) error: %v", err)
				}
				if err := os.Mkdir(stageAbs, 0o700); err != nil {
					t.Fatalf("Mkdir(replacement container) error: %v", err)
				}
				if err := os.WriteFile(filepath.Join(stageAbs, markerName), marker, 0o600); err != nil {
					t.Fatalf("WriteFile(copied ownership marker) error: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			record := newPreparedDeleteTrashTransferRecord(t, fixture)
			publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
			trashRoot := &storagePathRoot{absRoot: fixture.fs.trashRoot, handle: fixture.fs.trashRootHandle}
			stageRel := filepath.FromSlash(record.TrashStagePath)
			if _, _, err := fixture.fs.createPreparedTrashTransferOwnedContainer(trashRoot, stageRel, record, trashTransferOwnershipRoleTrashContainer); err != nil {
				t.Fatalf("createPreparedTrashTransferOwnedContainer() error: %v", err)
			}
			stageAbs := filepath.Join(fixture.fs.trashRoot, stageRel)
			test.mutate(t, stageAbs, requireTrashTransferOwnershipMarkerName(t, record.OperationID))
			fixture.reopen(t)

			report, err := fixture.fs.RecoverTrashTransfers(context.Background())
			if err == nil || !errors.Is(err, ErrTrashRecoveryRequired) || len(report.Blocked) == 0 {
				t.Fatalf("RecoverTrashTransfers() = %+v, %v, want fail-closed recovery", report, err)
			}
			if _, err := os.Lstat(stageAbs); err != nil {
				t.Fatalf("Lstat(retained prepared container) error: %v", err)
			}
		})
	}
}

func TestFileSystem_OwnershipMarkerCleanupSyncFailureGatesBeforeCopy(t *testing.T) {
	for _, test := range []struct {
		name       string
		restore    bool
		targetRole string
	}{
		{name: "delete container", targetRole: trashTransferOwnershipRoleTrashContainer},
		{name: "restore container", restore: true, targetRole: trashTransferOwnershipRoleWorkspaceContainer},
		{name: "restore parent", restore: true, targetRole: trashTransferOwnershipRoleWorkspaceParent},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			ctx := context.Background()
			sourcePath := "/ownership-cleanup-" + strings.ReplaceAll(test.name, " ", "-") + ".bin"
			if err := fs.WriteFile(ctx, sourcePath, strings.NewReader("payload")); err != nil {
				t.Fatalf("WriteFile() error: %v", err)
			}

			operation := func() error { return fs.Delete(ctx, sourcePath) }
			if test.restore {
				if err := fs.Delete(ctx, sourcePath); err != nil {
					t.Fatalf("Delete(setup source) error: %v", err)
				}
				items, err := fs.ListTrash(ctx)
				if err != nil || len(items) != 1 {
					t.Fatalf("ListTrash() = %+v, %v, want one item", items, err)
				}
				destinationPath := "/ownership-cleanup-" + strings.ReplaceAll(test.name, " ", "-") + "/nested/restored.bin"
				operation = func() error { return fs.RestoreFromTrashTo(ctx, items[0].ID, destinationPath) }
			}

			originalSync := syncTrashTransferOwnershipMarkerCleanupDir
			injectedErr := errors.New("injected ownership marker cleanup sync failure")
			injected := false
			syncTrashTransferOwnershipMarkerCleanupDir = func(dir *os.File, role string) error {
				if role == test.targetRole && !injected {
					info, err := dir.Stat()
					if err != nil {
						return err
					}
					if !info.IsDir() {
						return errors.New("ownership marker cleanup handle is not a directory")
					}
					injected = true
					return injectedErr
				}
				return originalSync(dir, role)
			}
			t.Cleanup(func() { syncTrashTransferOwnershipMarkerCleanupDir = originalSync })
			originalCopyHook := afterStorageCopySourceStat
			copyCalls := 0
			afterStorageCopySourceStat = func(string) error {
				copyCalls++
				return nil
			}
			t.Cleanup(func() { afterStorageCopySourceStat = originalCopyHook })

			err := operation()
			if !injected || !errors.Is(err, injectedErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
				t.Fatalf("Trash transfer error = %v, injected=%t, want recovery gate", err, injected)
			}
			if fs.trashMutationBlocked == nil {
				t.Fatal("trash mutation gate after sync failure = nil, want active gate")
			}
			if copyCalls != 0 {
				t.Fatalf("payload copy hooks = %d, want zero before durable marker cleanup", copyCalls)
			}
			recovery := requireJournaledTrashRecovery(t, fs, err)
			record, readErr := fs.readTrashTransferJournalRecord(
				trashTransferJournalRel(recovery.OperationID, trashTransferCopying),
				trashTransferCopying,
			)
			if readErr != nil {
				t.Fatalf("readTrashTransferJournalRecord(copying) error: %v", readErr)
			}
			root := fs.trashRootHandle
			stageRel := filepath.FromSlash(record.TrashStagePath)
			if test.restore {
				root = fs.filesRootHandle
				stageRel = storageWorkspaceRelativeName(record.WorkspaceStagePath)
				if len(record.WorkspaceParentDirs) == 0 {
					t.Fatal("copying restore journal has no created parent directories")
				}
			}
			if _, statErr := root.Lstat(stageRel); statErr != nil {
				t.Fatalf("Lstat(retained owned container) error: %v", statErr)
			}

			syncTrashTransferOwnershipMarkerCleanupDir = originalSync
			afterStorageCopySourceStat = originalCopyHook
			report, recoveryErr := fs.RecoverTrashTransfers(ctx)
			if recoveryErr != nil || report.RolledBack != 1 {
				t.Fatalf("RecoverTrashTransfers() = %+v, %v, want one rollback", report, recoveryErr)
			}
			if fs.trashMutationBlocked != nil {
				t.Fatalf("trash mutation gate after recovery = %v, want nil", fs.trashMutationBlocked)
			}
			if copyCalls != 0 {
				t.Fatalf("payload copy hooks after recovery = %d, want zero", copyCalls)
			}
		})
	}
}
