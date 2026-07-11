package storage

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/versionstore"
)

func prepareRestoreTrashTransferForTest(
	t *testing.T,
	fixture *trashPurgeRecoveryTestFixture,
	operationID string,
	payload []byte,
) (*versionstore.TrashItem, *trashTransferJournalRecord) {
	t.Helper()
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "restore-transfer-item", false, map[string]string{".": trashTransferRollbackTestPayload})
	return prepareRestoreTrashTransferItemForTest(t, fixture, operationID, payload, item)
}

func prepareRestoreTrashTransferItemForTest(
	t *testing.T,
	fixture *trashPurgeRecoveryTestFixture,
	operationID string,
	payload []byte,
	item *versionstore.TrashItem,
) (*versionstore.TrashItem, *trashTransferJournalRecord) {
	t.Helper()
	return prepareRestoreTrashTransferItemAtDestinationForTest(
		t,
		fixture,
		operationID,
		payload,
		item,
		"/restored/restore-transfer-item",
	)
}

func prepareRestoreTrashTransferItemAtDestinationForTest(
	t *testing.T,
	fixture *trashPurgeRecoveryTestFixture,
	operationID string,
	payload []byte,
	item *versionstore.TrashItem,
	destinationPath string,
) (*versionstore.TrashItem, *trashTransferJournalRecord) {
	t.Helper()
	item.RestoreData = append([]byte(nil), payload...)
	if err := fixture.fs.versions.UpdateTrashRestoreData(context.Background(), item.ID, item.RestoreData); err != nil {
		t.Fatalf("UpdateTrashRestoreData() error: %v", err)
	}
	identity, sourceManifest, err := fixture.fs.captureRestoreTrashTransferSource(context.Background(), item)
	if err != nil {
		t.Fatalf("captureRestoreTrashTransferSource() error: %v", err)
	}
	filesIdentity, trashIdentity, err := fixture.fs.captureTrashTransferRootIdentities()
	if err != nil {
		t.Fatalf("captureTrashTransferRootIdentities() error: %v", err)
	}
	workspaceParentDirs, err := fixture.fs.planTrashTransferWorkspaceParentDirs(destinationPath)
	if err != nil {
		t.Fatalf("planTrashTransferWorkspaceParentDirs() error: %v", err)
	}
	record := &trashTransferJournalRecord{
		Version:             trashTransferJournalVersion,
		Decision:            trashTransferPrepared,
		Kind:                trashTransferRestoreFromTrash,
		OperationID:         operationID,
		FilesRootIdentity:   filesIdentity,
		TrashRootIdentity:   trashIdentity,
		Item:                trashPurgeJournalItemFromStore(item),
		DestinationPath:     destinationPath,
		WorkspaceStagePath:  trashTransferWorkspaceStagePath(path.Dir(destinationPath), operationID),
		WorkspaceParentDirs: workspaceParentDirs,
		TrashItemIdentity:   identity,
		SourceManifest:      sourceManifest,
		ParticipantPayload:  append([]byte(nil), payload...),
	}
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	return item, record
}

func TestTrashTransferOwnershipMarkerName(t *testing.T) {
	operationID := strings.Repeat("a", 32)
	want := trashTransferOwnershipMarkerPrefix + operationID
	if got := requireTrashTransferOwnershipMarkerName(t, operationID); got != want {
		t.Fatalf("trashTransferOwnershipMarkerName() = %q, want %q", got, want)
	}
	for _, invalid := range []string{"", "../escape", strings.Repeat("g", 32), strings.Repeat("a", 31)} {
		if _, err := trashTransferOwnershipMarkerName(invalid); !errors.Is(err, ErrDeleteTargetChanged) {
			t.Fatalf("trashTransferOwnershipMarkerName(%q) error = %v, want ErrDeleteTargetChanged", invalid, err)
		}
	}
}

func TestFileSystem_AllocateTrashTransferRestoreOperationIDRetriesOwnershipMarkerParentCollision(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	first := strings.Repeat("b", 32)
	second := strings.Repeat("c", 32)
	destinationPath := "/new/" + requireTrashTransferOwnershipMarkerName(t, first) + "/file"
	candidates := []string{first, second}
	calls := 0
	got, err := fs.allocateTrashTransferRestoreOperationIDWithGenerator(
		context.Background(),
		"allocation-marker-collision",
		destinationPath,
		func() (string, error) {
			if calls >= len(candidates) {
				return "", errors.New("unexpected extra operation ID allocation")
			}
			candidate := candidates[calls]
			calls++
			return candidate, nil
		},
	)
	if err != nil || got != second || calls != 2 {
		t.Fatalf("allocateTrashTransferRestoreOperationIDWithGenerator() = %q, %v, calls=%d, want %q after two candidates", got, err, calls, second)
	}
}

func makeRestoreTrashTransferReadyForTest(t *testing.T, fixture *trashPurgeRecoveryTestFixture, record *trashTransferJournalRecord) {
	t.Helper()
	createdDirs, err := fixture.fs.createTrashTransferWorkspaceParentDirs(record.WorkspaceParentDirs)
	if err != nil {
		t.Fatalf("createTrashTransferWorkspaceParentDirs() error: %v", err)
	}
	record.Decision = trashTransferCopying
	record.WorkspaceParentDirs = createdDirs
	workspaceRoot := &storagePathRoot{absRoot: fixture.fs.workspace.Root(), handle: fixture.fs.filesRootHandle}
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	stageIdentity, _, err := fixture.fs.createTrashTransferOwnedContainer(workspaceRoot, stageRel)
	if err != nil {
		t.Fatalf("createTrashTransferOwnedContainer(workspace stage) error: %v", err)
	}
	record.WorkspaceStageIdentity = stageIdentity
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	if err := fixture.fs.copyRestoreTrashTransferReplica(context.Background(), record); err != nil {
		t.Fatalf("copyRestoreTrashTransferReplica() error: %v", err)
	}
	manifest, _, err := fixture.fs.scanTrashTransferTree(
		context.Background(),
		workspaceRoot,
		trashTransferOwnedContentRel(stageRel),
		nil,
		false,
	)
	if err != nil {
		t.Fatalf("scanTrashTransferTree(replica) error: %v", err)
	}
	record.Decision = trashTransferReady
	record.ReplicaManifest = manifest
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
}

func commitRestoreTrashTransferForTest(t *testing.T, fixture *trashPurgeRecoveryTestFixture, item *versionstore.TrashItem, record *trashTransferJournalRecord) *versionstore.TrashOperation {
	t.Helper()
	if err := fixture.fs.publishRestoreTrashTransferDestination(context.Background(), record); err != nil {
		t.Fatalf("publishRestoreTrashTransferDestination() error: %v", err)
	}
	index, err := trashRestoreFileIndexEntries(record)
	if err != nil {
		t.Fatalf("trashRestoreFileIndexEntries() error: %v", err)
	}
	operation, err := trashTransferOperationForRecord(record)
	if err != nil {
		t.Fatalf("trashTransferOperationForRecord() error: %v", err)
	}
	if err := fixture.fs.versions.CommitTrashRestore(context.Background(), item, record.DestinationPath, index, false, operation); err != nil {
		t.Fatalf("CommitTrashRestore() error: %v", err)
	}
	return operation
}

func TestFileSystem_RecoverTrashTransfersRollsBackPreparedRestore(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("1", 32), nil)
	fixture.reopen(t)

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || report.Completed != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}
	if _, err := fixture.fs.versions.GetTrashItem(context.Background(), item.ID); err != nil {
		t.Fatalf("GetTrashItem() after rollback error: %v", err)
	}
	requireTrashTransferPathPayload(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
	if _, err := os.Lstat(fixture.fs.workspace.FullPath(record.DestinationPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(destination) error = %v, want os.ErrNotExist", err)
	}
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
}

func TestFileSystem_RecoverTrashTransfersRollsBackReadyRestoreReplica(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("2", 32), nil)
	makeRestoreTrashTransferReadyForTest(t, fixture, record)
	stageAbs := fixture.fs.workspace.FullPath(record.WorkspaceStagePath)
	fixture.reopen(t)

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.RolledBack != 1 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}
	if _, err := os.Lstat(stageAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(workspace stage) error = %v, want os.ErrNotExist", err)
	}
	if _, err := fixture.fs.versions.GetTrashItem(context.Background(), item.ID); err != nil {
		t.Fatalf("GetTrashItem() after rollback error: %v", err)
	}
	requireTrashTransferPathPayload(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
}

func TestFileSystem_RecoverTrashTransfersCompletesReadyRestoreRollbackAfterReplicaRemoval(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("6", 32), nil)
	makeRestoreTrashTransferReadyForTest(t, fixture, record)
	stageAbs := fixture.fs.workspace.FullPath(record.WorkspaceStagePath)
	if err := os.RemoveAll(stageAbs); err != nil {
		t.Fatalf("RemoveAll(workspace stage) error: %v", err)
	}
	fixture.reopen(t)

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.RolledBack != 1 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}
	if _, err := fixture.fs.versions.GetTrashItem(context.Background(), item.ID); err != nil {
		t.Fatalf("GetTrashItem() after rollback error: %v", err)
	}
	requireTrashTransferPathPayload(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
}

func TestFileSystem_RecoverTrashTransfersCompletesReadyRestoreRollbackAfterPartialDirectoryReplicaRemoval(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "restore-transfer-directory", true, map[string]string{
		"a.txt":        "first",
		"nested/b.txt": "second",
	})
	item, record := prepareRestoreTrashTransferItemForTest(t, fixture, strings.Repeat("7", 32), nil, item)
	makeRestoreTrashTransferReadyForTest(t, fixture, record)
	stageAbs := fixture.fs.workspace.FullPath(record.WorkspaceStagePath)
	if err := os.Remove(filepath.Join(stageAbs, "content", "nested", "b.txt")); err != nil {
		t.Fatalf("Remove(partial workspace stage child) error: %v", err)
	}
	fixture.reopen(t)

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.RolledBack != 1 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}
	if _, err := fixture.fs.versions.GetTrashItem(context.Background(), item.ID); err != nil {
		t.Fatalf("GetTrashItem() after rollback error: %v", err)
	}
	if _, err := os.Lstat(stageAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(workspace stage) error = %v, want os.ErrNotExist", err)
	}
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
}

func TestFileSystem_RecoverTrashTransfersResumesReadyRestoreRollbackAfterJournalSyncFailure(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("a", 32), nil)
	makeRestoreTrashTransferReadyForTest(t, fixture, record)

	originalSyncManagedStorageDir := syncManagedStorageDir
	injectedErr := errors.New("injected rollback journal sync failure")
	injected := false
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if !injected && root == fixture.fs.trashRootHandle && filepath.Clean(relName) == trashTransferJournalDir {
			injected = true
			if err := originalSyncManagedStorageDir(root, relName, absPath); err != nil {
				return err
			}
			return injectedErr
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() { syncManagedStorageDir = originalSyncManagedStorageDir })

	first, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if !errors.Is(err, injectedErr) || len(first.Blocked) == 0 || !injected {
		t.Fatalf("first RecoverTrashTransfers() = %+v, %v, want injected cleanup failure", first, err)
	}
	syncManagedStorageDir = originalSyncManagedStorageDir
	fixture.reopen(t)

	second, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("second RecoverTrashTransfers() error: %v", err)
	}
	if second.RolledBack != 1 {
		t.Fatalf("second RecoverTrashTransfers() = %+v, want one rollback", second)
	}
	if _, err := fixture.fs.versions.GetTrashItem(context.Background(), item.ID); err != nil {
		t.Fatalf("GetTrashItem() after resumed rollback error: %v", err)
	}
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
}

func TestFileSystem_RecoverTrashTransfersRollsForwardCommittedRestore(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("3", 32), nil)
	makeRestoreTrashTransferReadyForTest(t, fixture, record)
	commitRestoreTrashTransferForTest(t, fixture, item, record)
	fixture.reopen(t)

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || report.Completed != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one roll-forward", report)
	}
	restored, err := os.ReadFile(fixture.fs.workspace.FullPath(record.DestinationPath))
	if err != nil || string(restored) != trashTransferRollbackTestPayload {
		t.Fatalf("restored destination = %q, %v", restored, err)
	}
	if _, err := fixture.fs.versions.GetTrashItem(context.Background(), item.ID); !errors.Is(err, versionstore.ErrNotFound) {
		t.Fatalf("GetTrashItem() after roll-forward error = %v, want ErrNotFound", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.fs.trashRoot, item.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(Trash source) error = %v, want os.ErrNotExist", err)
	}
	requireNoTrashTransferOperations(t, fixture)
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
}

func TestFileSystem_RecoverTrashTransfersRetriesRestoreParticipant(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	payload := []byte(`{"participant":"restore"}`)
	item, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("4", 32), payload)
	makeRestoreTrashTransferReadyForTest(t, fixture, record)
	commitRestoreTrashTransferForTest(t, fixture, item, record)

	participantErr := errors.New("restore participant unavailable")
	applyCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RecoveryStateReliable: func() error { return nil },
		CompleteRestore:       completeRestoreParticipantForTest,
		ApplyRestore: func(_ context.Context, operationID, originalPath, destinationPath string, gotPayload []byte) error {
			applyCalls++
			if operationID != record.OperationID || originalPath != record.Item.OriginalPath || destinationPath != record.DestinationPath || string(gotPayload) != string(payload) {
				t.Errorf("ApplyRestore(%q, %q, %q, %q) did not receive journal state", operationID, originalPath, destinationPath, gotPayload)
			}
			if applyCalls == 1 {
				return participantErr
			}
			return nil
		},
	})

	first, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if !errors.Is(err, participantErr) || len(first.Blocked) == 0 {
		t.Fatalf("first RecoverTrashTransfers() = %+v, %v, want participant failure", first, err)
	}
	requireDeleteTrashTransferJournal(t, fixture, record, trashTransferCommitted, true)
	if _, err := os.Lstat(filepath.Join(fixture.fs.trashRoot, item.ID)); err != nil {
		t.Fatalf("Lstat(Trash source after failed participant) error: %v", err)
	}

	second, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("second RecoverTrashTransfers() error: %v", err)
	}
	if second.RolledForward != 1 || applyCalls != 2 {
		t.Fatalf("second RecoverTrashTransfers() = %+v, apply calls %d", second, applyCalls)
	}
	if _, err := os.Lstat(filepath.Join(fixture.fs.trashRoot, item.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(Trash source after retry) error = %v, want os.ErrNotExist", err)
	}
	requireNoTrashTransferOperations(t, fixture)
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
}

func TestFileSystem_RestoreFromTrashRetainsSourceWhenParticipantReplacesDestination(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	payload := []byte(`{"participant":"replace-destination"}`)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "restore-replaced-destination", false, map[string]string{".": trashTransferRollbackTestPayload})
	item.RestoreData = append([]byte(nil), payload...)
	if err := fixture.fs.versions.UpdateTrashRestoreData(context.Background(), item.ID, item.RestoreData); err != nil {
		t.Fatalf("UpdateTrashRestoreData() error: %v", err)
	}

	destinationAbs := fixture.fs.workspace.FullPath(item.OriginalPath)
	ownedReplicaAbs := destinationAbs + ".owned-replica"
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RecoveryStateReliable: func() error { return nil },
		CompleteRestore:       completeRestoreParticipantForTest,
		ApplyRestore: func(_ context.Context, _, _, _ string, _ []byte) error {
			if err := os.Rename(destinationAbs, ownedReplicaAbs); err != nil {
				t.Fatalf("Rename(restored destination) error: %v", err)
			}
			if err := os.WriteFile(destinationAbs, []byte("unknown replacement"), 0o600); err != nil {
				t.Fatalf("WriteFile(replacement destination) error: %v", err)
			}
			return nil
		},
	})

	err := fixture.fs.RestoreFromTrash(context.Background(), item.ID)
	if !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, want ErrTrashRecoveryRequired", err)
	}
	restored, readErr := os.ReadFile(ownedReplicaAbs)
	if readErr != nil || string(restored) != trashTransferRollbackTestPayload {
		t.Fatalf("owned restored replica = %q, %v", restored, readErr)
	}
	replacement, readErr := os.ReadFile(destinationAbs)
	if readErr != nil || string(replacement) != "unknown replacement" {
		t.Fatalf("replacement destination = %q, %v", replacement, readErr)
	}
	requireTrashTransferPathPayload(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
}

func TestFileSystem_RecoverTrashTransfersRetainsSourceWhenParticipantReplacesDestination(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	payload := []byte(`{"participant":"replace-destination"}`)
	item, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("5", 32), payload)
	makeRestoreTrashTransferReadyForTest(t, fixture, record)
	commitRestoreTrashTransferForTest(t, fixture, item, record)

	destinationAbs := fixture.fs.workspace.FullPath(record.DestinationPath)
	ownedReplicaAbs := destinationAbs + ".owned-replica"
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RecoveryStateReliable: func() error { return nil },
		CompleteRestore:       completeRestoreParticipantForTest,
		ApplyRestore: func(_ context.Context, _, _, _ string, _ []byte) error {
			if err := os.Rename(destinationAbs, ownedReplicaAbs); err != nil {
				t.Fatalf("Rename(restored destination) error: %v", err)
			}
			if err := os.WriteFile(destinationAbs, []byte("unknown replacement"), 0o600); err != nil {
				t.Fatalf("WriteFile(replacement destination) error: %v", err)
			}
			return nil
		},
	})

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if !errors.Is(err, ErrTrashRecoveryRequired) || len(report.Blocked) == 0 {
		t.Fatalf("RecoverTrashTransfers() = %+v, %v, want blocked recovery", report, err)
	}
	restored, readErr := os.ReadFile(ownedReplicaAbs)
	if readErr != nil || string(restored) != trashTransferRollbackTestPayload {
		t.Fatalf("owned restored replica = %q, %v", restored, readErr)
	}
	replacement, readErr := os.ReadFile(destinationAbs)
	if readErr != nil || string(replacement) != "unknown replacement" {
		t.Fatalf("replacement destination = %q, %v", replacement, readErr)
	}
	requireTrashTransferPathPayload(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
}

func TestFileSystem_RecoverTrashTransfersRejectsParticipantDestinationReplacementAfterSourceRemoval(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	payload := []byte(`{"participant":"replace-destination-after-source-removal"}`)
	item, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("8", 32), payload)
	makeRestoreTrashTransferReadyForTest(t, fixture, record)
	commitRestoreTrashTransferForTest(t, fixture, item, record)
	committed := *record
	committed.Decision = trashTransferCommitted
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, &committed)
	if err := fixture.fs.removeCommittedRestoreTrashSource(context.Background(), &committed, false); err != nil {
		t.Fatalf("removeCommittedRestoreTrashSource() error: %v", err)
	}

	destinationAbs := fixture.fs.workspace.FullPath(record.DestinationPath)
	ownedReplicaAbs := destinationAbs + ".owned-replica"
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RecoveryStateReliable: func() error { return nil },
		CompleteRestore:       completeRestoreParticipantForTest,
		ApplyRestore: func(_ context.Context, _, _, _ string, _ []byte) error {
			if err := os.Rename(destinationAbs, ownedReplicaAbs); err != nil {
				t.Fatalf("Rename(restored destination) error: %v", err)
			}
			if err := os.WriteFile(destinationAbs, []byte("unknown replacement"), 0o600); err != nil {
				t.Fatalf("WriteFile(replacement destination) error: %v", err)
			}
			return nil
		},
	})

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if !errors.Is(err, ErrTrashRecoveryRequired) || len(report.Blocked) == 0 {
		t.Fatalf("RecoverTrashTransfers() = %+v, %v, want blocked recovery", report, err)
	}
	restored, readErr := os.ReadFile(ownedReplicaAbs)
	if readErr != nil || string(restored) != trashTransferRollbackTestPayload {
		t.Fatalf("owned restored replica = %q, %v", restored, readErr)
	}
	replacement, readErr := os.ReadFile(destinationAbs)
	if readErr != nil || string(replacement) != "unknown replacement" {
		t.Fatalf("replacement destination = %q, %v", replacement, readErr)
	}
	requireDeleteTrashTransferJournal(t, fixture, &committed, trashTransferCommitted, true)
}

func TestFileSystem_RecoverTrashTransfersDoesNotRedeliverCompletedRestoreParticipant(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	payload := []byte(`{"participant":"already-delivered"}`)
	item, record := prepareRestoreTrashTransferForTest(t, fixture, strings.Repeat("9", 32), payload)
	makeRestoreTrashTransferReadyForTest(t, fixture, record)
	commitRestoreTrashTransferForTest(t, fixture, item, record)
	committed := *record
	committed.Decision = trashTransferCommitted
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, &committed)
	if err := fixture.fs.removeCommittedRestoreTrashSource(context.Background(), &committed, false); err != nil {
		t.Fatalf("removeCommittedRestoreTrashSource() error: %v", err)
	}
	completed := committed
	completed.Decision = trashTransferCompleted
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, &completed)

	applyCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RecoveryStateReliable: func() error { return nil },
		CompleteRestore:       completeRestoreParticipantForTest,
		ApplyRestore: func(_ context.Context, _, _, _ string, _ []byte) error {
			applyCalls++
			return errors.New("completed participant must not be redelivered")
		},
	})

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.Completed != 1 || report.RolledForward != 0 || applyCalls != 0 {
		t.Fatalf("RecoverTrashTransfers() = %+v, apply calls %d", report, applyCalls)
	}
	requireNoTrashTransferOperations(t, fixture)
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
}
