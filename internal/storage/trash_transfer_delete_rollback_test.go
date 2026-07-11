package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const trashTransferRollbackTestPayload = "payload"

func newPreparedDeleteTrashTransferRecord(t *testing.T, fixture *trashPurgeRecoveryTestFixture) *trashTransferJournalRecord {
	t.Helper()

	originalPath := "/docs/report.txt"
	originalRel := storageWorkspaceRelativeName(originalPath)
	originalAbs := filepath.Join(fixture.cfg.FilesRoot, originalRel)
	if err := os.MkdirAll(filepath.Dir(originalAbs), 0o750); err != nil {
		t.Fatalf("MkdirAll(source parent) error: %v", err)
	}
	if err := os.WriteFile(originalAbs, []byte(trashTransferRollbackTestPayload), 0o640); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	workspaceRoot := &storagePathRoot{absRoot: fixture.fs.workspace.Root(), handle: fixture.fs.filesRootHandle}
	sourceManifest, _, err := fixture.fs.scanTrashTransferTree(context.Background(), workspaceRoot, originalRel, nil, false)
	if err != nil {
		t.Fatalf("scanTrashTransferTree(source) error: %v", err)
	}

	operationID := strings.Repeat("a", 32)
	filesRootIdentity, trashRootIdentity, err := fixture.fs.captureTrashTransferRootIdentities()
	if err != nil {
		t.Fatalf("captureTrashTransferRootIdentities() error: %v", err)
	}
	record := &trashTransferJournalRecord{
		Version:           trashTransferJournalVersion,
		Decision:          trashTransferPrepared,
		Kind:              trashTransferDeleteToTrash,
		OperationID:       operationID,
		FilesRootIdentity: filesRootIdentity,
		TrashRootIdentity: trashRootIdentity,
		Item: trashPurgeJournalItem{
			ID:            "rollback-delete",
			OriginalPath:  originalPath,
			Size:          int64(len(trashTransferRollbackTestPayload)),
			DeletedAtUnix: 1,
			ExpiresAtUnix: 2,
			RestoreData:   []byte{},
		},
		WorkspaceStagePath: trashTransferWorkspaceStagePath("/docs", operationID),
		TrashStagePath:     trashTransferItemStageRel(operationID),
		SourceManifest:     sourceManifest,
		ParticipantPayload: []byte{},
	}
	if err := validateTrashTransferJournalRecord(record, trashTransferPrepared); err != nil {
		t.Fatalf("validateTrashTransferJournalRecord(prepared) error: %v", err)
	}
	return record
}

func publishDeleteTrashTransferRecordForTest(t *testing.T, fs *FileSystem, record *trashTransferJournalRecord) {
	t.Helper()
	published, err := fs.publishTrashTransferJournalRecord(record)
	if err != nil || !published {
		t.Fatalf("publishTrashTransferJournalRecord(%s) = (%t, %v)", record.Decision, published, err)
	}
}

func requireDeleteTrashTransferJournal(t *testing.T, fixture *trashPurgeRecoveryTestFixture, record *trashTransferJournalRecord, decision string, exists bool) {
	t.Helper()
	journalPath := filepath.Join(fixture.fs.trashRoot, filepath.FromSlash(trashTransferJournalRel(record.OperationID, decision)))
	_, err := os.Stat(journalPath)
	if exists && err != nil {
		t.Fatalf("Stat(%s journal) error: %v", decision, err)
	}
	if !exists && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(%s journal) error = %v, want os.ErrNotExist", decision, err)
	}
}

func stageDeleteTrashTransferSourceForTest(t *testing.T, fixture *trashPurgeRecoveryTestFixture, record *trashTransferJournalRecord) os.FileInfo {
	t.Helper()
	originalAbs := filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.Item.OriginalPath))
	stageAbs := filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.WorkspaceStagePath))
	if err := os.Rename(originalAbs, stageAbs); err != nil {
		t.Fatalf("Rename(source, stage) error: %v", err)
	}
	info, err := os.Lstat(stageAbs)
	if err != nil {
		t.Fatalf("Lstat(source stage) error: %v", err)
	}
	return info
}

func prepareReadyDeleteTrashTransferReplicaForTest(t *testing.T, fixture *trashPurgeRecoveryTestFixture, record *trashTransferJournalRecord) {
	t.Helper()
	stageRel := filepath.FromSlash(record.TrashStagePath)
	stageAbs := filepath.Join(fixture.fs.trashRoot, stageRel)
	trashRoot := &storagePathRoot{absRoot: fixture.fs.trashRoot, handle: fixture.fs.trashRootHandle}
	stageIdentity, _, err := fixture.fs.createTrashTransferOwnedContainer(trashRoot, stageRel)
	if err != nil {
		t.Fatalf("createTrashTransferOwnedContainer(Trash stage) error: %v", err)
	}
	record.Decision = trashTransferCopying
	record.TrashStageIdentity = stageIdentity
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	if err := os.WriteFile(filepath.Join(stageAbs, "content"), []byte(trashTransferRollbackTestPayload), 0o640); err != nil {
		t.Fatalf("WriteFile(Trash stage content) error: %v", err)
	}

	replicaManifest, _, err := fixture.fs.scanTrashTransferTree(context.Background(), trashRoot, filepath.Join(stageRel, "content"), nil, false)
	if err != nil {
		t.Fatalf("scanTrashTransferTree(Trash replica) error: %v", err)
	}
	record.Decision = trashTransferReady
	record.ReplicaManifest = replicaManifest
	if err := validateTrashTransferJournalRecord(record, trashTransferReady); err != nil {
		t.Fatalf("validateTrashTransferJournalRecord(ready) error: %v", err)
	}
}

func requireTrashTransferPathPayload(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != trashTransferRollbackTestPayload {
		t.Fatalf("ReadFile(%q) = %q, %v", path, data, err)
	}
}

func TestFileSystem_RollbackPreparedDeleteTrashTransferClearsOriginalOnlyJournal(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	record := newPreparedDeleteTrashTransferRecord(t, fixture)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)

	if err := fixture.fs.rollbackPreparedDeleteTrashTransfer(context.Background(), record); err != nil {
		t.Fatalf("rollbackPreparedDeleteTrashTransfer() error: %v", err)
	}

	requireTrashTransferPathPayload(t, filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.Item.OriginalPath)))
	requireDeleteTrashTransferJournal(t, fixture, record, trashTransferPrepared, false)
}

func TestFileSystem_RollbackPreparedDeleteTrashTransferRestoresStagedSource(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	record := newPreparedDeleteTrashTransferRecord(t, fixture)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	stageDeleteTrashTransferSourceForTest(t, fixture, record)

	if err := fixture.fs.rollbackPreparedDeleteTrashTransfer(context.Background(), record); err != nil {
		t.Fatalf("rollbackPreparedDeleteTrashTransfer() error: %v", err)
	}

	originalAbs := filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.Item.OriginalPath))
	stageAbs := filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.WorkspaceStagePath))
	requireTrashTransferPathPayload(t, originalAbs)
	if _, err := os.Lstat(stageAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(source stage) error = %v, want os.ErrNotExist", err)
	}
	requireDeleteTrashTransferJournal(t, fixture, record, trashTransferPrepared, false)
}

func TestFileSystem_RollbackPreparedDeleteTrashTransferRemovesVerifiedReplica(t *testing.T) {
	for _, test := range []struct {
		name      string
		canonical bool
	}{
		{name: "stage"},
		{name: "canonical", canonical: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			record := newPreparedDeleteTrashTransferRecord(t, fixture)
			publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
			prepareReadyDeleteTrashTransferReplicaForTest(t, fixture, record)
			publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
			if test.canonical {
				if err := fixture.fs.publishDeleteTrashTransferItem(context.Background(), record); err != nil {
					t.Fatalf("publishDeleteTrashTransferItem() error: %v", err)
				}
			}

			if err := fixture.fs.rollbackPreparedDeleteTrashTransfer(context.Background(), record); err != nil {
				t.Fatalf("rollbackPreparedDeleteTrashTransfer() error: %v", err)
			}

			for _, rel := range []string{filepath.FromSlash(record.TrashStagePath), record.Item.ID} {
				if _, err := fixture.fs.trashRootHandle.Lstat(rel); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Lstat(Trash replica %q) error = %v, want os.ErrNotExist", rel, err)
				}
			}
			requireDeleteTrashTransferJournal(t, fixture, record, trashTransferPrepared, false)
			requireDeleteTrashTransferJournal(t, fixture, record, trashTransferReady, false)
		})
	}
}

func TestFileSystem_RollbackPreparedDeleteTrashTransferPreservesUnverifiedPartialReplica(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	record := newPreparedDeleteTrashTransferRecord(t, fixture)
	record.ParticipantPayload = []byte(`{"participant":"snapshot"}`)
	record.Item.RestoreData = append([]byte(nil), record.ParticipantPayload...)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	sourceStageInfo := stageDeleteTrashTransferSourceForTest(t, fixture, record)

	replicaContent := filepath.Join(fixture.fs.trashRoot, filepath.FromSlash(record.TrashStagePath), "content")
	if err := os.MkdirAll(filepath.Dir(replicaContent), 0o700); err != nil {
		t.Fatalf("MkdirAll(partial replica) error: %v", err)
	}
	if err := os.WriteFile(replicaContent, []byte("partial evidence"), 0o600); err != nil {
		t.Fatalf("WriteFile(partial replica) error: %v", err)
	}
	participantCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RollbackDelete: func(context.Context, string, string, []byte) error {
			participantCalls++
			return nil
		},
	})

	err := fixture.fs.rollbackPreparedDeleteTrashTransfer(context.Background(), record)
	if err == nil || !strings.Contains(err.Error(), "unverified partial") {
		t.Fatalf("rollbackPreparedDeleteTrashTransfer() error = %v, want unverified partial replica", err)
	}
	if participantCalls != 0 {
		t.Fatalf("participant rollback calls = %d, want 0", participantCalls)
	}
	originalAbs := filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.Item.OriginalPath))
	if _, err := os.Lstat(originalAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(original source) error = %v, want os.ErrNotExist", err)
	}
	stageAbs := filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.WorkspaceStagePath))
	currentStageInfo, err := os.Lstat(stageAbs)
	if err != nil || !os.SameFile(sourceStageInfo, currentStageInfo) {
		t.Fatalf("source stage identity changed: %v, %v", currentStageInfo, err)
	}
	requireTrashTransferPathPayload(t, stageAbs)
	data, err := os.ReadFile(replicaContent)
	if err != nil || string(data) != "partial evidence" {
		t.Fatalf("partial replica evidence = %q, %v", data, err)
	}
	requireDeleteTrashTransferJournal(t, fixture, record, trashTransferPrepared, true)
}

func TestFileSystem_RollbackPreparedDeleteTrashTransferRejectsMatchingCommitMarker(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	record := newPreparedDeleteTrashTransferRecord(t, fixture)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	operation, err := trashTransferOperationForRecord(record)
	if err != nil {
		t.Fatalf("trashTransferOperationForRecord() error: %v", err)
	}
	if err := fixture.fs.versions.CommitTrashDelete(context.Background(), record.Item.storeItem(), operation); err != nil {
		t.Fatalf("CommitTrashDelete() error: %v", err)
	}

	err = fixture.fs.rollbackPreparedDeleteTrashTransfer(context.Background(), record)
	if err == nil || !strings.Contains(err.Error(), "committed delete-to-Trash operation") {
		t.Fatalf("rollbackPreparedDeleteTrashTransfer() error = %v, want committed operation rejection", err)
	}
	requireTrashTransferPathPayload(t, filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.Item.OriginalPath)))
	requireDeleteTrashTransferJournal(t, fixture, record, trashTransferPrepared, true)
}

func TestFileSystem_RollbackPreparedDeleteTrashTransferRetriesParticipantBeforeJournalCleanup(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	record := newPreparedDeleteTrashTransferRecord(t, fixture)
	record.ParticipantPayload = []byte(`{"participant":"snapshot"}`)
	record.Item.RestoreData = append([]byte(nil), record.ParticipantPayload...)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)

	participantErr := errors.New("participant rollback failed")
	participantCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RollbackDelete: func(_ context.Context, operationID, sourcePath string, payload []byte) error {
			participantCalls++
			if operationID != record.OperationID || sourcePath != record.Item.OriginalPath || string(payload) != string(record.ParticipantPayload) {
				t.Errorf("RollbackDelete(%q, %q, %q) did not receive journaled state", operationID, sourcePath, payload)
			}
			if participantCalls == 1 {
				return participantErr
			}
			return nil
		},
	})

	if err := fixture.fs.rollbackPreparedDeleteTrashTransfer(context.Background(), record); !errors.Is(err, participantErr) {
		t.Fatalf("first rollbackPreparedDeleteTrashTransfer() error = %v, want %v", err, participantErr)
	}
	if participantCalls != 1 {
		t.Fatalf("participant rollback calls after failure = %d, want 1", participantCalls)
	}
	requireDeleteTrashTransferJournal(t, fixture, record, trashTransferPrepared, true)

	if err := fixture.fs.rollbackPreparedDeleteTrashTransfer(context.Background(), record); err != nil {
		t.Fatalf("second rollbackPreparedDeleteTrashTransfer() error: %v", err)
	}
	if participantCalls != 2 {
		t.Fatalf("participant rollback calls after retry = %d, want 2", participantCalls)
	}
	requireDeleteTrashTransferJournal(t, fixture, record, trashTransferPrepared, false)
}
