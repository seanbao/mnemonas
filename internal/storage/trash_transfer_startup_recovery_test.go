package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/versionstore"
)

func readyDeleteTrashTransferForStartupRecoveryTest(
	t *testing.T,
	fixture *trashPurgeRecoveryTestFixture,
	participantPayload []byte,
) *trashTransferJournalRecord {
	t.Helper()

	record := newPreparedDeleteTrashTransferRecord(t, fixture)
	record.ParticipantPayload = append([]byte(nil), participantPayload...)
	record.Item.RestoreData = append([]byte(nil), participantPayload...)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	stageDeleteTrashTransferSourceForTest(t, fixture, record)
	prepareReadyDeleteTrashTransferReplicaForTest(t, fixture, record)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	return record
}

func commitDeleteTrashTransferForStartupRecoveryTest(
	t *testing.T,
	fixture *trashPurgeRecoveryTestFixture,
	record *trashTransferJournalRecord,
) *versionstore.TrashOperation {
	t.Helper()

	operation, err := trashTransferOperationForRecord(record)
	if err != nil {
		t.Fatalf("trashTransferOperationForRecord() error: %v", err)
	}
	if err := fixture.fs.versions.CommitTrashDelete(context.Background(), record.Item.storeItem(), operation); err != nil {
		t.Fatalf("CommitTrashDelete() error: %v", err)
	}
	return operation
}

func cloneDeleteTrashTransferCheckpointForTest(record *trashTransferJournalRecord, decision string) *trashTransferJournalRecord {
	checkpoint := *record
	checkpoint.Decision = decision
	return &checkpoint
}

func requireNoDeleteTrashTransferSidecars(t *testing.T, fixture *trashPurgeRecoveryTestFixture, operationID string) {
	t.Helper()

	for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
		rel := filepath.FromSlash(trashTransferJournalRel(operationID, decision))
		if _, err := fixture.fs.trashRootHandle.Lstat(rel); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Lstat(%s sidecar) error = %v, want os.ErrNotExist", decision, err)
		}
	}
}

func requireNoTrashTransferOperations(t *testing.T, fixture *trashPurgeRecoveryTestFixture) {
	t.Helper()

	operations, err := fixture.fs.versions.ListTrashOperations(context.Background())
	if err != nil {
		t.Fatalf("ListTrashOperations() error: %v", err)
	}
	if len(operations) != 0 {
		t.Fatalf("ListTrashOperations() = %+v, want no pending operation", operations)
	}
}

func TestFileSystem_RecoverTrashTransfersRollsBackPreparedTransferAfterReopen(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	record := newPreparedDeleteTrashTransferRecord(t, fixture)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, record)
	stageDeleteTrashTransferSourceForTest(t, fixture, record)
	fixture.reopen(t)

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || report.Completed != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}

	originalAbs := filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.Item.OriginalPath))
	stageAbs := filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.WorkspaceStagePath))
	requireTrashTransferPathPayload(t, originalAbs)
	if _, err := os.Lstat(stageAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(source stage) error = %v, want os.ErrNotExist", err)
	}
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
	requireNoTrashTransferOperations(t, fixture)
}

func TestFileSystem_RecoverTrashTransfersRollsForwardReadyTransferWithMatchingMarker(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	record := readyDeleteTrashTransferForStartupRecoveryTest(t, fixture, nil)
	if err := fixture.fs.publishDeleteTrashTransferItem(context.Background(), record); err != nil {
		t.Fatalf("publishDeleteTrashTransferItem() error: %v", err)
	}
	commitDeleteTrashTransferForStartupRecoveryTest(t, fixture, record)
	fixture.reopen(t)

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.RolledBack != 0 || report.RolledForward != 1 || report.Completed != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one roll-forward", report)
	}

	originalAbs := filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.Item.OriginalPath))
	stageAbs := filepath.Join(fixture.cfg.FilesRoot, storageWorkspaceRelativeName(record.WorkspaceStagePath))
	if _, err := os.Lstat(originalAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(original source) error = %v, want os.ErrNotExist", err)
	}
	if _, err := os.Lstat(stageAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(source stage) error = %v, want os.ErrNotExist", err)
	}
	requireTrashTransferPathPayload(t, filepath.Join(fixture.fs.trashRoot, record.Item.ID, "content"))
	stored, err := fixture.fs.versions.GetTrashItem(context.Background(), record.Item.ID)
	if err != nil || stored.OriginalPath != record.Item.OriginalPath {
		t.Fatalf("GetTrashItem() = %+v, %v, want committed item", stored, err)
	}
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
	requireNoTrashTransferOperations(t, fixture)
}

func TestFileSystem_RecoverTrashTransfersCleansCommittedAndCompletedCheckpoints(t *testing.T) {
	for _, test := range []struct {
		name          string
		completed     bool
		wantForwarded int
		wantCompleted int
	}{
		{name: "committed", wantForwarded: 1},
		{name: "completed", completed: true, wantCompleted: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			record := readyDeleteTrashTransferForStartupRecoveryTest(t, fixture, nil)
			if err := fixture.fs.publishDeleteTrashTransferItem(context.Background(), record); err != nil {
				t.Fatalf("publishDeleteTrashTransferItem() error: %v", err)
			}
			operation := commitDeleteTrashTransferForStartupRecoveryTest(t, fixture, record)
			committed := cloneDeleteTrashTransferCheckpointForTest(record, trashTransferCommitted)
			publishDeleteTrashTransferRecordForTest(t, fixture.fs, committed)

			if test.completed {
				if err := fixture.fs.removeCommittedDeleteTransferSource(context.Background(), committed); err != nil {
					t.Fatalf("removeCommittedDeleteTransferSource() error: %v", err)
				}
				completed := cloneDeleteTrashTransferCheckpointForTest(record, trashTransferCompleted)
				publishDeleteTrashTransferRecordForTest(t, fixture.fs, completed)
				if err := fixture.fs.versions.CompleteTrashOperation(context.Background(), record.OperationID, operation.JournalHash); err != nil {
					t.Fatalf("CompleteTrashOperation() error: %v", err)
				}
			}
			fixture.reopen(t)

			report, err := fixture.fs.RecoverTrashTransfers(context.Background())
			if err != nil {
				t.Fatalf("RecoverTrashTransfers() error: %v", err)
			}
			if report.RolledBack != 0 || report.RolledForward != test.wantForwarded || report.Completed != test.wantCompleted || len(report.Blocked) != 0 {
				t.Fatalf("RecoverTrashTransfers() report = %+v", report)
			}
			requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
			requireNoTrashTransferOperations(t, fixture)
			requireTrashTransferPathPayload(t, filepath.Join(fixture.fs.trashRoot, record.Item.ID, "content"))
		})
	}
}

func TestFileSystem_RecoverTrashTransfersDoesNotRedeliverCompletedDeleteParticipant(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	payload := []byte(`{"participant":"already-delivered"}`)
	record := readyDeleteTrashTransferForStartupRecoveryTest(t, fixture, payload)
	if err := fixture.fs.publishDeleteTrashTransferItem(context.Background(), record); err != nil {
		t.Fatalf("publishDeleteTrashTransferItem() error: %v", err)
	}
	commitDeleteTrashTransferForStartupRecoveryTest(t, fixture, record)
	committed := cloneDeleteTrashTransferCheckpointForTest(record, trashTransferCommitted)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, committed)
	if err := fixture.fs.removeCommittedDeleteTransferSource(context.Background(), committed); err != nil {
		t.Fatalf("removeCommittedDeleteTransferSource() error: %v", err)
	}
	completed := cloneDeleteTrashTransferCheckpointForTest(record, trashTransferCompleted)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, completed)

	applyCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RecoveryStateReliable: func() error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		ApplyDelete: func(_ context.Context, _ string, _ string, _ []byte, _ bool) error {
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

func TestFileSystem_RecoverTrashTransfersRevalidatesTrashReplicaAfterParticipantWhenSourceStageIsMissing(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	payload := []byte(`{"participant":"replace-trash-replica-after-source-removal"}`)
	record := readyDeleteTrashTransferForStartupRecoveryTest(t, fixture, payload)
	if err := fixture.fs.publishDeleteTrashTransferItem(context.Background(), record); err != nil {
		t.Fatalf("publishDeleteTrashTransferItem() error: %v", err)
	}
	commitDeleteTrashTransferForStartupRecoveryTest(t, fixture, record)
	committed := cloneDeleteTrashTransferCheckpointForTest(record, trashTransferCommitted)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, committed)
	if err := fixture.fs.removeCommittedDeleteTransferSource(context.Background(), committed); err != nil {
		t.Fatalf("removeCommittedDeleteTransferSource() error: %v", err)
	}

	canonicalAbs := filepath.Join(fixture.fs.trashRoot, record.Item.ID)
	ownedReplicaAbs := canonicalAbs + ".owned-replica"
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RecoveryStateReliable: func() error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		ApplyDelete: func(_ context.Context, _, _ string, _ []byte, committed bool) error {
			if !committed {
				t.Fatal("ApplyDelete() committed = false, want true")
			}
			if err := os.Rename(canonicalAbs, ownedReplicaAbs); err != nil {
				t.Fatalf("Rename(canonical Trash replica) error: %v", err)
			}
			if err := os.MkdirAll(canonicalAbs, 0o700); err != nil {
				t.Fatalf("MkdirAll(replacement Trash item) error: %v", err)
			}
			if err := os.WriteFile(filepath.Join(canonicalAbs, "content"), []byte("unknown replacement"), 0o600); err != nil {
				t.Fatalf("WriteFile(replacement Trash content) error: %v", err)
			}
			return nil
		},
	})

	report, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if !errors.Is(err, ErrTrashRecoveryRequired) || len(report.Blocked) == 0 {
		t.Fatalf("RecoverTrashTransfers() = %+v, %v, want blocked recovery", report, err)
	}
	requireTrashTransferPathPayload(t, filepath.Join(ownedReplicaAbs, "content"))
	replacement, readErr := os.ReadFile(filepath.Join(canonicalAbs, "content"))
	if readErr != nil || string(replacement) != "unknown replacement" {
		t.Fatalf("replacement Trash content = %q, %v", replacement, readErr)
	}
	requireDeleteTrashTransferJournal(t, fixture, committed, trashTransferCommitted, true)
	operations, listErr := fixture.fs.versions.ListTrashOperations(context.Background())
	if listErr != nil || len(operations) != 1 || operations[0].ID != record.OperationID {
		t.Fatalf("ListTrashOperations() = %+v, %v, want pending operation %q", operations, listErr, record.OperationID)
	}
}

func TestFileSystem_RecoverTrashTransfersFailsClosedForCommittedPrivateStageAndGatesMutations(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	record := readyDeleteTrashTransferForStartupRecoveryTest(t, fixture, nil)
	commitDeleteTrashTransferForStartupRecoveryTest(t, fixture, record)
	committed := cloneDeleteTrashTransferCheckpointForTest(record, trashTransferCommitted)
	publishDeleteTrashTransferRecordForTest(t, fixture.fs, committed)

	report, recoveryErr := fixture.fs.RecoverTrashTransfers(context.Background())
	if recoveryErr == nil || !errors.Is(recoveryErr, ErrTrashRecoveryRequired) || !strings.Contains(recoveryErr.Error(), "retains a private stage") {
		t.Fatalf("RecoverTrashTransfers() error = %v, want committed private-stage rejection", recoveryErr)
	}
	if len(report.Blocked) == 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want blocked recovery", report)
	}
	if err := fixture.fs.Mkdir(context.Background(), "/must-stay-blocked"); !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Mkdir() error = %v, want global Trash recovery gate", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.cfg.FilesRoot, "must-stay-blocked")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(blocked mutation target) error = %v, want os.ErrNotExist", err)
	}

	stageRel := filepath.FromSlash(record.TrashStagePath)
	if err := fixture.fs.trashRootHandle.Rename(stageRel, record.Item.ID); err != nil {
		t.Fatalf("Rename(private stage, canonical item) error: %v", err)
	}
	report, recoveryErr = fixture.fs.RecoverTrashTransfers(context.Background())
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after repair error: %v", recoveryErr)
	}
	if report.RolledForward != 1 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() after repair report = %+v, want one roll-forward", report)
	}
	if err := fixture.fs.Mkdir(context.Background(), "/after-recovery"); err != nil {
		t.Fatalf("Mkdir() after recovery error: %v", err)
	}
}

func TestFileSystem_RecoverTrashTransfersRetriesParticipantRecovery(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	payload := []byte(`{"participant":"snapshot"}`)
	record := readyDeleteTrashTransferForStartupRecoveryTest(t, fixture, payload)
	if err := fixture.fs.publishDeleteTrashTransferItem(context.Background(), record); err != nil {
		t.Fatalf("publishDeleteTrashTransferItem() error: %v", err)
	}
	commitDeleteTrashTransferForStartupRecoveryTest(t, fixture, record)

	reliabilityErr := errors.New("participant recovery evidence is unreliable")
	applyErr := errors.New("participant replay failed")
	reliabilityCalls := 0
	applyCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		CompleteDelete: completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error {
			reliabilityCalls++
			if reliabilityCalls == 1 {
				return reliabilityErr
			}
			return nil
		},
		ApplyDelete: func(_ context.Context, operationID, path string, gotPayload []byte, committed bool) error {
			applyCalls++
			if operationID != record.OperationID || path != record.Item.OriginalPath || string(gotPayload) != string(payload) || !committed {
				t.Errorf("ApplyDelete(%q, %q, %q, %t) did not receive committed journal state", operationID, path, gotPayload, committed)
			}
			if applyCalls == 1 {
				return applyErr
			}
			return nil
		},
	})

	first, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if !errors.Is(err, reliabilityErr) || len(first.Blocked) == 0 {
		t.Fatalf("first RecoverTrashTransfers() = %+v, %v, want reliability failure", first, err)
	}
	if applyCalls != 0 {
		t.Fatalf("ApplyDelete calls after reliability failure = %d, want 0", applyCalls)
	}
	requireDeleteTrashTransferJournal(t, fixture, record, trashTransferReady, true)

	second, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if !errors.Is(err, applyErr) || len(second.Blocked) == 0 {
		t.Fatalf("second RecoverTrashTransfers() = %+v, %v, want replay failure", second, err)
	}
	if applyCalls != 1 {
		t.Fatalf("ApplyDelete calls after replay failure = %d, want 1", applyCalls)
	}
	requireDeleteTrashTransferJournal(t, fixture, record, trashTransferCommitted, true)

	third, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("third RecoverTrashTransfers() error: %v", err)
	}
	if third.RolledForward != 1 || len(third.Blocked) != 0 {
		t.Fatalf("third RecoverTrashTransfers() report = %+v, want one roll-forward", third)
	}
	if reliabilityCalls != 3 || applyCalls != 2 {
		t.Fatalf("participant recovery calls = reliability %d, apply %d; want 3, 2", reliabilityCalls, applyCalls)
	}
	requireNoDeleteTrashTransferSidecars(t, fixture, record.OperationID)
	requireNoTrashTransferOperations(t, fixture)
}
