package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/rootio"
)

const trashTransferCopyTempNameForTest = ".storage-copy-0123456789abcdef.tmp"

func prepareCopyingDeleteTrashTransferForTest(t *testing.T, fixture *trashPurgeRecoveryTestFixture, directory bool) *trashTransferJournalRecord {
	t.Helper()
	record := newPreparedDeleteTrashTransferRecord(t, fixture)
	if directory {
		originalRel := storageWorkspaceRelativeName(record.Item.OriginalPath)
		originalAbs := fixture.fs.workspace.FullPath(record.Item.OriginalPath)
		if err := os.Remove(originalAbs); err != nil {
			t.Fatalf("Remove(file source) error: %v", err)
		}
		if err := os.Mkdir(originalAbs, 0o750); err != nil {
			t.Fatalf("Mkdir(directory source) error: %v", err)
		}
		if err := os.Mkdir(filepath.Join(originalAbs, "nested"), 0o750); err != nil {
			t.Fatalf("Mkdir(nested source) error: %v", err)
		}
		if err := os.WriteFile(filepath.Join(originalAbs, "a.txt"), []byte("first"), 0o640); err != nil {
			t.Fatalf("WriteFile(first source) error: %v", err)
		}
		if err := os.WriteFile(filepath.Join(originalAbs, "nested", "b.txt"), []byte("second"), 0o640); err != nil {
			t.Fatalf("WriteFile(second source) error: %v", err)
		}
		workspaceRoot := &storagePathRoot{absRoot: fixture.fs.workspace.Root(), handle: fixture.fs.filesRootHandle}
		manifest, _, err := fixture.fs.scanTrashTransferTree(context.Background(), workspaceRoot, originalRel, nil, false)
		if err != nil {
			t.Fatalf("scanTrashTransferTree(directory source) error: %v", err)
		}
		record.Item.IsDir = true
		record.Item.Size = int64(len("first") + len("second"))
		record.SourceManifest = manifest
	}
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	trashRoot := &storagePathRoot{absRoot: fixture.fs.trashRoot, handle: fixture.fs.trashRootHandle}
	stageRel := filepath.FromSlash(record.TrashStagePath)
	identity, _, err := fixture.fs.createTrashTransferOwnedContainer(trashRoot, stageRel)
	if err != nil {
		t.Fatalf("createTrashTransferOwnedContainer(delete) error: %v", err)
	}
	record.Decision = trashTransferCopying
	record.TrashStageIdentity = identity
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	stageDeleteTrashTransferSourceForTest(t, fixture, record)
	return record
}

func prepareCopyingRestoreTrashTransferForTest(t *testing.T, fixture *trashPurgeRecoveryTestFixture, operationID string, directory bool) *trashTransferJournalRecord {
	t.Helper()
	var record *trashTransferJournalRecord
	if directory {
		item := seedTrashPurgeRecoveryItem(t, fixture.fs, "restore-copying-directory", true, map[string]string{
			"a.txt":        "first",
			"nested/b.txt": "second",
		})
		_, record = prepareRestoreTrashTransferItemForTest(t, fixture, operationID, nil, item)
	} else {
		_, record = prepareRestoreTrashTransferForTest(t, fixture, operationID, nil)
	}
	createdDirs, err := fixture.fs.createTrashTransferWorkspaceParentDirs(record.WorkspaceParentDirs)
	if err != nil {
		t.Fatalf("createTrashTransferWorkspaceParentDirs() error: %v", err)
	}
	record.Decision = trashTransferCopying
	record.WorkspaceParentDirs = createdDirs
	workspaceRoot := &storagePathRoot{absRoot: fixture.fs.workspace.Root(), handle: fixture.fs.filesRootHandle}
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	identity, _, err := fixture.fs.createTrashTransferOwnedContainer(workspaceRoot, stageRel)
	if err != nil {
		t.Fatalf("createTrashTransferOwnedContainer(restore) error: %v", err)
	}
	record.WorkspaceStageIdentity = identity
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	return record
}

func writePartialTrashTransferCopyForTest(t *testing.T, containerAbs string, directory bool) {
	t.Helper()
	if !directory {
		if err := os.WriteFile(filepath.Join(containerAbs, trashTransferCopyTempNameForTest), []byte("pay"), 0o600); err != nil {
			t.Fatalf("WriteFile(partial file temp) error: %v", err)
		}
		return
	}
	contentAbs := filepath.Join(containerAbs, "content")
	if err := os.Mkdir(contentAbs, 0o750); err != nil {
		t.Fatalf("Mkdir(partial directory content) error: %v", err)
	}
	if err := os.Mkdir(filepath.Join(contentAbs, "nested"), 0o750); err != nil {
		t.Fatalf("Mkdir(partial nested directory) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentAbs, "nested", trashTransferCopyTempNameForTest), []byte("sec"), 0o600); err != nil {
		t.Fatalf("WriteFile(partial directory temp) error: %v", err)
	}
}

func TestFileSystem_RecoverTrashTransfersRollsBackOwnedMidCopyContainers(t *testing.T) {
	for _, test := range []struct {
		name      string
		restore   bool
		directory bool
	}{
		{name: "delete file"},
		{name: "delete directory", directory: true},
		{name: "restore file", restore: true},
		{name: "restore directory", restore: true, directory: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			var record *trashTransferJournalRecord
			if test.restore {
				record = prepareCopyingRestoreTrashTransferForTest(t, fixture, strings.Repeat("c", 32), test.directory)
				writePartialTrashTransferCopyForTest(t, fixture.fs.workspace.FullPath(record.WorkspaceStagePath), test.directory)
			} else {
				record = prepareCopyingDeleteTrashTransferForTest(t, fixture, test.directory)
				writePartialTrashTransferCopyForTest(t, filepath.Join(fixture.fs.trashRoot, filepath.FromSlash(record.TrashStagePath)), test.directory)
			}
			fixture.reopen(t)

			report, err := fixture.fs.RecoverTrashTransfers(context.Background())
			if err != nil || report.RolledBack != 1 || len(report.Blocked) != 0 {
				t.Fatalf("RecoverTrashTransfers() = %+v, %v, want one rollback", report, err)
			}
			if test.restore {
				if _, err := os.Lstat(fixture.fs.workspace.FullPath(record.WorkspaceStagePath)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Lstat(restore container) error = %v, want os.ErrNotExist", err)
				}
				if _, err := os.Lstat(fixture.fs.workspace.FullPath("/restored")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Lstat(created restore parent) error = %v, want os.ErrNotExist", err)
				}
				requireTrashTransferPathPayloadForManifest(t, fixture, record)
			} else {
				requireDeleteTrashTransferSourceForManifest(t, fixture, record)
				if _, err := os.Lstat(filepath.Join(fixture.fs.trashRoot, filepath.FromSlash(record.TrashStagePath))); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Lstat(delete container) error = %v, want os.ErrNotExist", err)
				}
			}
			requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
		})
	}
}

func requireDeleteTrashTransferSourceForManifest(t *testing.T, fixture *trashPurgeRecoveryTestFixture, record *trashTransferJournalRecord) {
	t.Helper()
	workspaceRoot := &storagePathRoot{absRoot: fixture.fs.workspace.Root(), handle: fixture.fs.filesRootHandle}
	if _, _, err := fixture.fs.scanTrashTransferTree(context.Background(), workspaceRoot, storageWorkspaceRelativeName(record.Item.OriginalPath), record.SourceManifest, false); err != nil {
		t.Fatalf("scanTrashTransferTree(restored delete source) error: %v", err)
	}
}

func requireTrashTransferPathPayloadForManifest(t *testing.T, fixture *trashPurgeRecoveryTestFixture, record *trashTransferJournalRecord) {
	t.Helper()
	if _, _, err := fixture.fs.scanDeleteTrashTransferItem(context.Background(), filepath.FromSlash(record.Item.ID), record.TrashItemIdentity, record.SourceManifest, false); err != nil {
		t.Fatalf("scanDeleteTrashTransferItem(restore source) error: %v", err)
	}
}

func TestFileSystem_RecoverTrashTransfersRejectsUnownedCopyingContainerEntries(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *trashPurgeRecoveryTestFixture, *trashTransferJournalRecord)
	}{
		{
			name: "unknown child",
			mutate: func(t *testing.T, fixture *trashPurgeRecoveryTestFixture, record *trashTransferJournalRecord) {
				stageAbs := filepath.Join(fixture.fs.trashRoot, filepath.FromSlash(record.TrashStagePath))
				if err := os.WriteFile(filepath.Join(stageAbs, "unknown"), []byte("external"), 0o600); err != nil {
					t.Fatalf("WriteFile(unknown child) error: %v", err)
				}
			},
		},
		{
			name: "root replacement",
			mutate: func(t *testing.T, fixture *trashPurgeRecoveryTestFixture, record *trashTransferJournalRecord) {
				stageAbs := filepath.Join(fixture.fs.trashRoot, filepath.FromSlash(record.TrashStagePath))
				if err := os.Rename(stageAbs, stageAbs+".owned"); err != nil {
					t.Fatalf("Rename(owned container) error: %v", err)
				}
				if err := os.Mkdir(stageAbs, 0o700); err != nil {
					t.Fatalf("Mkdir(replacement container) error: %v", err)
				}
			},
		},
		{
			name: "child replacement",
			mutate: func(t *testing.T, fixture *trashPurgeRecoveryTestFixture, record *trashTransferJournalRecord) {
				stageAbs := filepath.Join(fixture.fs.trashRoot, filepath.FromSlash(record.TrashStagePath))
				tempAbs := filepath.Join(stageAbs, trashTransferCopyTempNameForTest)
				if err := os.WriteFile(tempAbs, []byte("pay"), 0o600); err != nil {
					t.Fatalf("WriteFile(owned child) error: %v", err)
				}
				originalHook := beforeTrashTransferOwnedContainerRemoval
				beforeTrashTransferOwnedContainerRemoval = func(path string) error {
					if filepath.Clean(path) != filepath.Clean(stageAbs) {
						return originalHook(path)
					}
					beforeTrashTransferOwnedContainerRemoval = originalHook
					if err := os.Rename(tempAbs, tempAbs+".owned"); err != nil {
						return err
					}
					return os.WriteFile(tempAbs, []byte("new"), 0o600)
				}
				t.Cleanup(func() { beforeTrashTransferOwnedContainerRemoval = originalHook })
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			record := prepareCopyingDeleteTrashTransferForTest(t, fixture, false)
			test.mutate(t, fixture, record)
			fixture.reopen(t)

			report, err := fixture.fs.RecoverTrashTransfers(context.Background())
			if !errors.Is(err, ErrTrashRecoveryRequired) || len(report.Blocked) == 0 {
				t.Fatalf("RecoverTrashTransfers() = %+v, %v, want fail-closed recovery", report, err)
			}
			stageAbs := filepath.Join(fixture.fs.trashRoot, filepath.FromSlash(record.TrashStagePath))
			if _, statErr := os.Lstat(stageAbs); statErr != nil {
				t.Fatalf("Lstat(retained inspection container) error: %v", statErr)
			}
			requireDeleteTrashTransferJournal(t, fixture, record, trashTransferCopying, true)
		})
	}
}

func TestFileSystem_CopyingJournalSyncPrecedesPayloadCopy(t *testing.T) {
	for _, restore := range []bool{false, true} {
		name := "delete"
		if restore {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			ctx := context.Background()
			path := "/copying-barrier.bin"
			if err := fs.WriteFile(ctx, path, strings.NewReader("payload")); err != nil {
				t.Fatalf("WriteFile(source) error: %v", err)
			}
			var trashID string
			if restore {
				if err := fs.Delete(ctx, path); err != nil {
					t.Fatalf("Delete(source) error: %v", err)
				}
				items, err := fs.ListTrash(ctx)
				if err != nil || len(items) != 1 {
					t.Fatalf("ListTrash() = %+v, %v", items, err)
				}
				trashID = items[0].ID
			}

			originalSync := syncManagedStorageDir
			injectedErr := errors.New("injected copying journal sync failure")
			injected := false
			syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
				if !injected && root == fs.trashRootHandle && filepath.Clean(relName) == trashTransferJournalDir {
					dir, openErr := rootio.OpenDirNoFollow(root, trashTransferJournalDir)
					if openErr != nil {
						return openErr
					}
					entries, readErr := dir.ReadDir(-1)
					closeErr := dir.Close()
					if readErr != nil || closeErr != nil {
						return errors.Join(readErr, closeErr)
					}
					for _, entry := range entries {
						_, decision, ok := parseTrashTransferJournalName(entry.Name())
						if ok && decision == trashTransferCopying {
							injected = true
							return injectedErr
						}
					}
				}
				return originalSync(root, relName, absPath)
			}
			t.Cleanup(func() { syncManagedStorageDir = originalSync })
			originalCopyHook := afterStorageCopySourceStat
			copyCalls := 0
			afterStorageCopySourceStat = func(string) error {
				copyCalls++
				return nil
			}
			t.Cleanup(func() { afterStorageCopySourceStat = originalCopyHook })

			var err error
			if restore {
				err = fs.RestoreFromTrash(ctx, trashID)
			} else {
				err = fs.Delete(ctx, path)
			}
			if !injected || !errors.Is(err, injectedErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
				t.Fatalf("operation error = %v, injected=%t, want copying journal sync recovery gate", err, injected)
			}
			if copyCalls != 0 {
				t.Fatalf("payload copy hooks = %d, want zero before durable copying checkpoint", copyCalls)
			}
			recovery := requireJournaledTrashRecovery(t, fs, err)
			record, readErr := fs.readTrashTransferJournalRecord(trashTransferJournalRel(recovery.OperationID, trashTransferCopying), trashTransferCopying)
			if readErr != nil {
				t.Fatalf("readTrashTransferJournalRecord(copying) error: %v", readErr)
			}
			var containerRel string
			var root *os.Root
			if restore {
				containerRel = storageWorkspaceRelativeName(record.WorkspaceStagePath)
				root = fs.filesRootHandle
			} else {
				containerRel = filepath.FromSlash(record.TrashStagePath)
				root = fs.trashRootHandle
			}
			dir, openErr := rootio.OpenDirNoFollow(root, containerRel)
			if openErr != nil {
				t.Fatalf("OpenDirNoFollow(owned container) error: %v", openErr)
			}
			entries, readDirErr := dir.ReadDir(-1)
			closeErr := dir.Close()
			markerName := requireTrashTransferOwnershipMarkerName(t, record.OperationID)
			if readDirErr != nil || closeErr != nil || len(entries) != 1 || entries[0].Name() != markerName {
				t.Fatalf("owned container entries = %+v, %v, %v, want only ownership marker", entries, readDirErr, closeErr)
			}
			syncManagedStorageDir = originalSync
			afterStorageCopySourceStat = originalCopyHook
			report, recoveryErr := fs.RecoverTrashTransfers(ctx)
			if recoveryErr != nil || report.RolledBack != 1 {
				t.Fatalf("RecoverTrashTransfers() = %+v, %v, want one rollback", report, recoveryErr)
			}
			if fs.trashMutationBlocked != nil {
				t.Fatalf("trash mutation gate after recovery = %v, want nil", fs.trashMutationBlocked)
			}
		})
	}
}

func TestFileSystem_CopyingJournalPublishFailurePrecedesPayloadCopy(t *testing.T) {
	for _, restore := range []bool{false, true} {
		name := "delete"
		if restore {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			ctx := context.Background()
			sourcePath := "/copying-publish-barrier.bin"
			if err := fs.WriteFile(ctx, sourcePath, strings.NewReader("payload")); err != nil {
				t.Fatalf("WriteFile(source) error: %v", err)
			}
			var trashID string
			if restore {
				if err := fs.Delete(ctx, sourcePath); err != nil {
					t.Fatalf("Delete(source) error: %v", err)
				}
				items, err := fs.ListTrash(ctx)
				if err != nil || len(items) != 1 {
					t.Fatalf("ListTrash() = %+v, %v", items, err)
				}
				trashID = items[0].ID
			}

			originalSync := syncManagedStorageDir
			injected := false
			var operationID string
			syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
				if !injected {
					dir, openErr := rootio.OpenDirNoFollow(fs.trashRootHandle, trashTransferJournalDir)
					if openErr == nil {
						entries, readErr := dir.ReadDir(-1)
						closeErr := dir.Close()
						if readErr != nil || closeErr != nil {
							return errors.Join(readErr, closeErr)
						}
						for _, entry := range entries {
							candidateID, decision, ok := parseTrashTransferJournalName(entry.Name())
							if !ok || decision != trashTransferPrepared {
								continue
							}
							prepared, readRecordErr := fs.readTrashTransferJournalRecord(trashTransferJournalRel(candidateID, trashTransferPrepared), trashTransferPrepared)
							if readRecordErr != nil {
								return readRecordErr
							}
							ownedRel := filepath.FromSlash(prepared.TrashStagePath)
							ownedRoot := fs.trashRootHandle
							if restore {
								ownedRel = storageWorkspaceRelativeName(prepared.WorkspaceStagePath)
								ownedRoot = fs.filesRootHandle
							}
							if _, stageErr := ownedRoot.Lstat(ownedRel); stageErr != nil {
								continue
							}
							operationID = candidateID
							collisionRel := filepath.FromSlash(trashTransferJournalRel(operationID, trashTransferCopying))
							if err := rootio.MkdirNoFollow(fs.trashRootHandle, collisionRel, 0o700); err != nil {
								return err
							}
							injected = true
							break
						}
					}
				}
				return originalSync(root, relName, absPath)
			}
			t.Cleanup(func() { syncManagedStorageDir = originalSync })
			originalCopyHook := afterStorageCopySourceStat
			copyCalls := 0
			afterStorageCopySourceStat = func(string) error {
				copyCalls++
				return nil
			}
			t.Cleanup(func() { afterStorageCopySourceStat = originalCopyHook })

			var err error
			if restore {
				err = fs.RestoreFromTrash(ctx, trashID)
			} else {
				err = fs.Delete(ctx, sourcePath)
			}
			if !injected || operationID == "" || !errors.Is(err, ErrTrashRecoveryRequired) {
				t.Fatalf("operation error = %v, injected=%t, operation=%q, want publish collision recovery gate", err, injected, operationID)
			}
			if copyCalls != 0 {
				t.Fatalf("payload copy hooks = %d, want zero before published copying checkpoint", copyCalls)
			}
			collisionInfo, statErr := fs.trashRootHandle.Lstat(filepath.FromSlash(trashTransferJournalRel(operationID, trashTransferCopying)))
			if statErr != nil || !collisionInfo.IsDir() {
				t.Fatalf("copying checkpoint collision = %v, %v, want retained directory", collisionInfo, statErr)
			}
			if !restore {
				data, readErr := os.ReadFile(fs.workspace.FullPath(sourcePath))
				if readErr != nil || string(data) != "payload" {
					t.Fatalf("source after publish failure = %q, %v", data, readErr)
				}
			}
		})
	}
}

func TestFileSystem_PublishRestoreTrashTransferDestinationRecoversAllReadyStates(t *testing.T) {
	for _, initialState := range []trashTransferRestorePublishState{
		trashTransferRestorePublishStaged,
		trashTransferRestorePublishRenamed,
		trashTransferRestorePublishComplete,
	} {
		t.Run(string(rune('0'+initialState)), func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			_, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("d", 32), nil)
			makeRestoreTrashTransferReadyForTest(t, fixture, record)
			stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
			contentRel := trashTransferOwnedContentRel(stageRel)
			destinationRel := storageWorkspaceRelativeName(record.DestinationPath)
			if initialState != trashTransferRestorePublishStaged {
				if err := rootio.RenameLeafNoReplace(fixture.fs.filesRootHandle, contentRel, destinationRel); err != nil {
					t.Fatalf("RenameLeafNoReplace(content, destination) error: %v", err)
				}
			}
			if initialState == trashTransferRestorePublishComplete {
				if err := os.Remove(fixture.fs.workspace.FullPath(record.WorkspaceStagePath)); err != nil {
					t.Fatalf("Remove(empty owned container) error: %v", err)
				}
			}

			if err := fixture.fs.publishRestoreTrashTransferDestination(context.Background(), record); err != nil {
				t.Fatalf("publishRestoreTrashTransferDestination() error: %v", err)
			}
			if err := fixture.fs.publishRestoreTrashTransferDestination(context.Background(), record); err != nil {
				t.Fatalf("publishRestoreTrashTransferDestination(idempotent) error: %v", err)
			}
			if _, err := os.Lstat(fixture.fs.workspace.FullPath(record.WorkspaceStagePath)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Lstat(owned container) error = %v, want os.ErrNotExist", err)
			}
			data, err := os.ReadFile(fixture.fs.workspace.FullPath(record.DestinationPath))
			if err != nil || string(data) != trashTransferRollbackTestPayload {
				t.Fatalf("ReadFile(destination) = %q, %v", data, err)
			}
		})
	}
}

func TestFileSystem_PublishRestoreTrashTransferDestinationResumesAfterRenameSyncFailure(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	_, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("e", 32), nil)
	makeRestoreTrashTransferReadyForTest(t, fixture, record)
	destinationRel := storageWorkspaceRelativeName(record.DestinationPath)
	originalSync := syncManagedStorageDir
	injectedErr := errors.New("injected restore rename sync failure")
	injected := false
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if !injected && root == fixture.fs.filesRootHandle && filepath.Clean(relName) == filepath.Dir(destinationRel) {
			injected = true
			return injectedErr
		}
		return originalSync(root, relName, absPath)
	}
	t.Cleanup(func() { syncManagedStorageDir = originalSync })

	err := fixture.fs.publishRestoreTrashTransferDestination(context.Background(), record)
	if !injected || !errors.Is(err, injectedErr) {
		t.Fatalf("publishRestoreTrashTransferDestination() = %v, injected=%t", err, injected)
	}
	syncManagedStorageDir = originalSync
	state, _, err := fixture.fs.inspectRestoreTrashTransferPublishState(context.Background(), record)
	if err != nil || state != trashTransferRestorePublishRenamed {
		t.Fatalf("inspectRestoreTrashTransferPublishState() = %v, %v, want renamed", state, err)
	}
	if err := fixture.fs.publishRestoreTrashTransferDestination(context.Background(), record); err != nil {
		t.Fatalf("publishRestoreTrashTransferDestination(resume) error: %v", err)
	}
	state, _, err = fixture.fs.inspectRestoreTrashTransferPublishState(context.Background(), record)
	if err != nil || state != trashTransferRestorePublishComplete {
		t.Fatalf("inspectRestoreTrashTransferPublishState(final) = %v, %v, want complete", state, err)
	}
}

func TestStorageCopyPublishTempNameIsExact(t *testing.T) {
	for _, name := range []string{
		trashTransferCopyTempNameForTest,
		".storage-copy-aaaaaaaaaaaaaaaa.tmp",
	} {
		if !isStorageCopyPublishTempName(name) {
			t.Fatalf("isStorageCopyPublishTempName(%q) = false", name)
		}
	}
	for _, name := range []string{
		".storage-copy-0123456789abcde.tmp",
		".storage-copy-0123456789abcdef0.tmp",
		".storage-copy-0123456789abcdeg.tmp",
		".storage-copy-ABCDEF0123456789.tmp",
		".storage-copy-0123456789abcdef.tmp.extra",
		"nested/.storage-copy-0123456789abcdef.tmp",
	} {
		if isStorageCopyPublishTempName(name) {
			t.Fatalf("isStorageCopyPublishTempName(%q) = true", name)
		}
	}
}
