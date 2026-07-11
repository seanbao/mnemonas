package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/seanbao/mnemonas/internal/versionstore"
)

func TestFileSystem_RestoreParticipantPersistenceWarningBlocksUntilRetryIsDurable(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	payload := []byte(`{"participant":"restore-durability-barrier"}`)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "restore-durability-barrier", false, map[string]string{".": trashTransferRollbackTestPayload})
	item.RestoreData = append([]byte(nil), payload...)
	if err := fixture.fs.versions.UpdateTrashRestoreData(context.Background(), item.ID, item.RestoreData); err != nil {
		t.Fatalf("UpdateTrashRestoreData() error: %v", err)
	}

	persistenceErr := errors.New("restore participant directory sync failed")
	applyCalls := 0
	completeCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RecoveryStateReliable: func() error { return nil },
		ApplyRestore: func(context.Context, string, string, string, []byte) error {
			applyCalls++
			return wrapVisibleMutationWarning(persistenceErr)
		},
		CompleteRestore: func(context.Context, string, string, string, []byte) error {
			completeCalls++
			return nil
		},
	})

	err := fixture.fs.RestoreFromTrash(context.Background(), item.ID)
	if !errors.Is(err, persistenceErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, want persistence failure and ErrTrashRecoveryRequired", err)
	}
	if applyCalls != 2 || completeCalls != 0 {
		t.Fatalf("participant calls after restore = apply %d, complete %d; want 2, 0", applyCalls, completeCalls)
	}
	if _, err := fixture.fs.versions.GetTrashItem(context.Background(), item.ID); !errors.Is(err, versionstore.ErrNotFound) {
		t.Fatalf("GetTrashItem() error = %v, want committed restore metadata", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.fs.trashRoot, item.ID)); err != nil {
		t.Fatalf("Lstat(retained Trash source) error: %v", err)
	}
	operations, err := fixture.fs.versions.ListTrashOperations(context.Background())
	if err != nil || len(operations) != 1 {
		t.Fatalf("ListTrashOperations() = %+v, %v, want one pending operation", operations, err)
	}
	operationID := operations[0].ID
	requireDeleteTrashTransferJournal(t, fixture, &trashTransferJournalRecord{OperationID: operationID}, trashTransferCommitted, true)
	requireDeleteTrashTransferJournal(t, fixture, &trashTransferJournalRecord{OperationID: operationID}, trashTransferCompleted, false)

	firstRecoveryCalls := applyCalls
	first, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if !errors.Is(err, persistenceErr) || !errors.Is(err, ErrTrashRecoveryRequired) || len(first.Blocked) == 0 {
		t.Fatalf("first RecoverTrashTransfers() = %+v, %v, want persistent participant warning", first, err)
	}
	if applyCalls-firstRecoveryCalls != 2 || completeCalls != 0 {
		t.Fatalf("participant calls after blocked recovery = apply %d, complete %d; want two more Apply calls and no Complete", applyCalls, completeCalls)
	}
	if _, err := os.Lstat(filepath.Join(fixture.fs.trashRoot, item.ID)); err != nil {
		t.Fatalf("Lstat(Trash source after blocked recovery) error: %v", err)
	}

	transientCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		RecoveryStateReliable: func() error { return nil },
		ApplyRestore: func(context.Context, string, string, string, []byte) error {
			applyCalls++
			transientCalls++
			if transientCalls == 1 {
				return wrapVisibleMutationWarning(persistenceErr)
			}
			return nil
		},
		CompleteRestore: func(context.Context, string, string, string, []byte) error {
			completeCalls++
			return nil
		},
	})
	second, err := fixture.fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("second RecoverTrashTransfers() error: %v", err)
	}
	if second.RolledForward != 1 || transientCalls != 2 || completeCalls != 1 {
		t.Fatalf("second RecoverTrashTransfers() = %+v, transient apply %d, complete %d", second, transientCalls, completeCalls)
	}
	if _, err := os.Lstat(filepath.Join(fixture.fs.trashRoot, item.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(Trash source after durable retry) error = %v, want os.ErrNotExist", err)
	}
	requireNoTrashTransferOperations(t, fixture)
	requireNoDeleteTrashTransferSidecars(t, fixture, operationID)
}

func TestFileSystem_DeleteParticipantCompletionWarningRetainsRecoveryEvidence(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	const sourcePath = "/delete-completion-durability.txt"
	payload := []byte(`{"participant":"delete-completion-durability"}`)
	if err := fs.WriteFile(ctx, sourcePath, bytes.NewReader([]byte("delete completion durability"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	persistenceErr := errors.New("delete participant completion directory sync failed")
	operationID := ""
	applyCalls := 0
	completeCalls := 0
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(_ context.Context, gotOperationID, _ string) ([]byte, error) {
			operationID = gotOperationID
			return append([]byte(nil), payload...), nil
		},
		ApplyDelete: func(context.Context, string, string, []byte, bool) error {
			applyCalls++
			return nil
		},
		RollbackDelete: func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete: func(context.Context, string, string, []byte) error {
			completeCalls++
			return wrapVisibleMutationWarning(persistenceErr)
		},
		RecoveryStateReliable: func() error { return nil },
	})

	err := fs.Delete(ctx, sourcePath)
	if !errors.Is(err, persistenceErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Delete() error = %v, want completion warning and ErrTrashRecoveryRequired", err)
	}
	if applyCalls != 2 || completeCalls != 2 {
		t.Fatalf("participant calls after Delete() = apply %d, complete %d; want 2, 2", applyCalls, completeCalls)
	}
	if _, err := fs.trashRootHandle.Lstat(filepath.FromSlash(trashTransferJournalRel(operationID, trashTransferCompleted))); err != nil {
		t.Fatalf("Lstat(completed journal) error: %v", err)
	}
	operations, err := fs.versions.ListTrashOperations(ctx)
	if err != nil || len(operations) != 1 || operations[0].ID != operationID {
		t.Fatalf("ListTrashOperations() = %+v, %v, want pending operation %q", operations, err, operationID)
	}

	beforeRecovery := completeCalls
	first, err := fs.RecoverTrashTransfers(ctx)
	if !errors.Is(err, persistenceErr) || !errors.Is(err, ErrTrashRecoveryRequired) || len(first.Blocked) == 0 {
		t.Fatalf("first RecoverTrashTransfers() = %+v, %v, want completion warning", first, err)
	}
	if completeCalls-beforeRecovery != 2 || applyCalls != 2 {
		t.Fatalf("participant calls after blocked recovery = apply %d, complete %d", applyCalls, completeCalls)
	}

	transientCalls := 0
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		CompleteDelete: func(context.Context, string, string, []byte) error {
			completeCalls++
			transientCalls++
			if transientCalls == 1 {
				return wrapVisibleMutationWarning(persistenceErr)
			}
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})
	second, err := fs.RecoverTrashTransfers(ctx)
	if err != nil {
		t.Fatalf("second RecoverTrashTransfers() error: %v", err)
	}
	if second.Completed != 1 || transientCalls != 2 || applyCalls != 2 {
		t.Fatalf("second RecoverTrashTransfers() = %+v, transient complete %d, apply %d", second, transientCalls, applyCalls)
	}
	requireNoTrashTransferCompletionArtifacts(t, fs, operationID)
}

func TestFileSystem_DeleteTerminalCompletionSyncWarningRetainsRecoveryGate(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	const sourcePath = "/terminal-completion-sync.txt"
	if err := fs.WriteFile(ctx, sourcePath, bytes.NewReader([]byte("terminal completion sync"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	operationID := ""
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(_ context.Context, gotOperationID, _ string) ([]byte, error) {
			operationID = gotOperationID
			return []byte(`{"participant":"terminal-completion"}`), nil
		},
		ApplyDelete:           func(context.Context, string, string, []byte, bool) error { return nil },
		RollbackDelete:        func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		ValidatePurge:         func(context.Context, string, string, []byte) error { return nil },
		CompletePurge:         func(context.Context, string, string, []byte) error { return nil },
		RecoveryStateReliable: func() error { return nil },
	})

	originalSync := syncManagedStorageDir
	injectedErr := errors.New("terminal completion journal sync failed")
	injected := false
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if !injected && operationID != "" && root == fs.trashRootHandle && filepath.Clean(relName) == trashTransferJournalDir && trashTransferCheckpointsMissingForTest(fs, operationID) {
			injected = true
			return injectedErr
		}
		return originalSync(root, relName, absPath)
	}
	t.Cleanup(func() { syncManagedStorageDir = originalSync })

	err := fs.Delete(ctx, sourcePath)
	if !injected || !errors.Is(err, injectedErr) || !errors.Is(err, ErrTrashRecoveryRequired) || !isVisibleMutationWarning(err) {
		t.Fatalf("Delete() error = %v, injected=%t; want terminal visible warning with recovery gate", err, injected)
	}
	if fs.trashMutationBlocked == nil {
		t.Fatal("terminal cleanup warning did not leave the Trash mutation gate blocked")
	}
	completedRel := filepath.FromSlash(trashTransferJournalRel(operationID, trashTransferCompleted))
	if _, err := fs.trashRootHandle.Lstat(completedRel); err != nil {
		t.Fatalf("Lstat(retained completed journal) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v, want one committed item", items, err)
	}
	if err := fs.DeleteFromTrash(ctx, items[0].ID); !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("DeleteFromTrash() while terminal cleanup is unresolved = %v, want ErrTrashRecoveryRequired", err)
	}
	report, err := fs.RecoverTrashTransfers(ctx)
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() after terminal cleanup warning error: %v", err)
	}
	if report.Completed != 1 || report.RolledForward != 0 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one completed recovery", report)
	}
	if fs.trashMutationBlocked != nil {
		t.Fatalf("successful terminal cleanup recovery retained mutation gate: %v", fs.trashMutationBlocked)
	}
	requireNoTrashTransferCompletionArtifacts(t, fs, operationID)
	if err := fs.DeleteFromTrash(ctx, items[0].ID); err != nil {
		t.Fatalf("DeleteFromTrash() after recovery error: %v", err)
	}
}

func TestFileSystem_RestoreTerminalCompletionSyncWarningRetainsRecoveryGate(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	const sourcePath = "/terminal-restore-completion-sync.txt"
	payload := []byte(`{"participant":"terminal-restore-completion"}`)
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(context.Context, string, string) ([]byte, error) {
			return append([]byte(nil), payload...), nil
		},
		ApplyDelete:           func(context.Context, string, string, []byte, bool) error { return nil },
		RollbackDelete:        func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error { return nil },
	})
	if err := fs.WriteFile(ctx, sourcePath, bytes.NewReader([]byte("terminal restore completion sync"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, sourcePath); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v, want one item", items, err)
	}

	operationID := ""
	completeCalls := 0
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		ApplyRestore: func(_ context.Context, gotOperationID, _, _ string, _ []byte) error {
			operationID = gotOperationID
			return nil
		},
		CompleteRestore: func(_ context.Context, gotOperationID, _, _ string, _ []byte) error {
			if gotOperationID != operationID {
				t.Errorf("CompleteRestore() operation ID = %q, want %q", gotOperationID, operationID)
			}
			completeCalls++
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})

	originalSync := syncManagedStorageDir
	injectedErr := errors.New("terminal restore completion journal sync failed")
	injected := false
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if !injected && operationID != "" && root == fs.trashRootHandle && filepath.Clean(relName) == trashTransferJournalDir && trashTransferCheckpointsMissingForTest(fs, operationID) {
			injected = true
			return injectedErr
		}
		return originalSync(root, relName, absPath)
	}
	t.Cleanup(func() { syncManagedStorageDir = originalSync })

	err = fs.RestoreFromTrash(ctx, items[0].ID)
	if !injected || !errors.Is(err, injectedErr) || !errors.Is(err, ErrTrashRecoveryRequired) || !isVisibleMutationWarning(err) {
		t.Fatalf("RestoreFromTrash() error = %v, injected=%t; want terminal visible warning with recovery gate", err, injected)
	}
	if completeCalls != 1 {
		t.Fatalf("CompleteRestore() calls = %d, want 1", completeCalls)
	}
	if fs.trashMutationBlocked == nil {
		t.Fatal("terminal restore cleanup warning did not leave the Trash mutation gate blocked")
	}
	completedRel := filepath.FromSlash(trashTransferJournalRel(operationID, trashTransferCompleted))
	if _, err := fs.trashRootHandle.Lstat(completedRel); err != nil {
		t.Fatalf("Lstat(retained completed journal) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/blocked-after-terminal-restore.txt", bytes.NewReader([]byte("blocked"))); !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("WriteFile() while terminal restore cleanup is unresolved = %v, want ErrTrashRecoveryRequired", err)
	}

	report, err := fs.RecoverTrashTransfers(ctx)
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() after terminal restore cleanup warning error: %v", err)
	}
	if report.Completed != 1 || report.RolledForward != 0 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one completed recovery", report)
	}
	if completeCalls != 2 {
		t.Fatalf("CompleteRestore() calls after recovery = %d, want 2", completeCalls)
	}
	if fs.trashMutationBlocked != nil {
		t.Fatalf("successful terminal restore cleanup recovery retained mutation gate: %v", fs.trashMutationBlocked)
	}
	requireNoTrashTransferCompletionArtifacts(t, fs, operationID)
	if err := fs.WriteFile(ctx, "/allowed-after-terminal-restore.txt", bytes.NewReader([]byte("allowed"))); err != nil {
		t.Fatalf("WriteFile() after recovery error: %v", err)
	}
}

func TestFileSystem_DeleteTerminalRollbackSyncWarningRetainsRecoveryGate(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	const sourcePath = "/terminal-rollback-sync.txt"
	payload := []byte(`{"participant":"terminal-rollback"}`)
	content := []byte("terminal rollback sync")
	if err := fs.WriteFile(ctx, sourcePath, bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	operationID := ""
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(_ context.Context, gotOperationID, _ string) ([]byte, error) {
			operationID = gotOperationID
			return append([]byte(nil), payload...), nil
		},
		ApplyDelete:           func(context.Context, string, string, []byte, bool) error { return nil },
		RollbackDelete:        func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error { return nil },
	})
	commitErr := errors.New("injected delete metadata commit failure")
	fs.commitTrashDelete = func(context.Context, *versionstore.TrashItem, *versionstore.TrashOperation) error {
		return commitErr
	}

	originalSync := syncManagedStorageDir
	injectedErr := errors.New("terminal rollback journal sync failed")
	injected := false
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if !injected && operationID != "" && root == fs.trashRootHandle && filepath.Clean(relName) == trashTransferJournalDir && trashTransferCheckpointsMissingForTest(fs, operationID) {
			injected = true
			return injectedErr
		}
		return originalSync(root, relName, absPath)
	}
	t.Cleanup(func() { syncManagedStorageDir = originalSync })

	err := fs.Delete(ctx, sourcePath)
	if !injected || !errors.Is(err, commitErr) || !errors.Is(err, injectedErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Delete() error = %v, injected=%t; want commit failure, terminal cleanup warning, and recovery gate", err, injected)
	}
	if fs.trashMutationBlocked == nil {
		t.Fatal("terminal rollback warning did not leave the Trash mutation gate blocked")
	}
	restored, err := os.ReadFile(fs.workspace.FullPath(sourcePath))
	if err != nil || !bytes.Equal(restored, content) {
		t.Fatalf("restored source = %q, %v; want %q", restored, err, content)
	}
	preparedRel := filepath.FromSlash(trashTransferJournalRel(operationID, trashTransferPrepared))
	if _, err := fs.trashRootHandle.Lstat(preparedRel); err != nil {
		t.Fatalf("Lstat(retained prepared journal) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/blocked-after-delete-rollback.txt", bytes.NewReader([]byte("blocked"))); !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("WriteFile() while terminal rollback cleanup is unresolved = %v, want ErrTrashRecoveryRequired", err)
	}
	if err := fs.Delete(ctx, sourcePath); !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Delete() while terminal rollback cleanup is unresolved = %v, want ErrTrashRecoveryRequired", err)
	}

	report, err := fs.RecoverTrashTransfers(ctx)
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() after terminal rollback warning error: %v", err)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || report.Completed != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}
	if fs.trashMutationBlocked != nil {
		t.Fatalf("successful terminal rollback recovery retained mutation gate: %v", fs.trashMutationBlocked)
	}
	operations, err := fs.versions.ListTrashOperations(ctx)
	if err != nil || len(operations) != 0 {
		t.Fatalf("ListTrashOperations() = %+v, %v, want none", operations, err)
	}
	requireNoTrashTransferCompletionArtifacts(t, fs, operationID)
	fs.commitTrashDelete = nil
	if err := fs.WriteFile(ctx, "/allowed-after-delete-rollback.txt", bytes.NewReader([]byte("allowed"))); err != nil {
		t.Fatalf("WriteFile() after terminal rollback recovery error: %v", err)
	}
	if err := fs.Delete(ctx, sourcePath); err != nil {
		t.Fatalf("Delete() after terminal rollback recovery error: %v", err)
	}
}

func TestFileSystem_RestoreTerminalRollbackSyncWarningRetainsRecoveryGate(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	const sourcePath = "/terminal-restore-rollback-sync.txt"
	content := []byte("terminal restore rollback sync")
	if err := fs.WriteFile(ctx, sourcePath, bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, sourcePath); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v, want one item", items, err)
	}

	originalCommit := fs.commitTrashRestore
	commitErr := errors.New("injected restore metadata commit failure")
	operationID := ""
	fs.commitTrashRestore = func(_ context.Context, _ *versionstore.TrashItem, _ string, _ []versionstore.FileIndexEntry, _ bool, operation *versionstore.TrashOperation) error {
		if operation != nil {
			operationID = operation.ID
		}
		return commitErr
	}
	originalSync := syncManagedStorageDir
	injectedErr := errors.New("terminal restore rollback journal sync failed")
	injected := false
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if !injected && operationID != "" && root == fs.trashRootHandle && filepath.Clean(relName) == trashTransferJournalDir && trashTransferCheckpointsMissingForTest(fs, operationID) {
			injected = true
			return injectedErr
		}
		return originalSync(root, relName, absPath)
	}
	t.Cleanup(func() { syncManagedStorageDir = originalSync })

	err = fs.RestoreFromTrash(ctx, items[0].ID)
	if !injected || !errors.Is(err, commitErr) || !errors.Is(err, injectedErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, injected=%t; want commit failure, terminal cleanup warning, and recovery gate", err, injected)
	}
	if fs.trashMutationBlocked == nil {
		t.Fatal("terminal restore rollback warning did not leave the Trash mutation gate blocked")
	}
	preparedRel := filepath.FromSlash(trashTransferJournalRel(operationID, trashTransferPrepared))
	if _, err := fs.trashRootHandle.Lstat(preparedRel); err != nil {
		t.Fatalf("Lstat(retained restore prepared journal) error: %v", err)
	}
	if _, err := os.Lstat(fs.workspace.FullPath(sourcePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(rolled-back restore destination) error = %v, want os.ErrNotExist", err)
	}
	if err := fs.WriteFile(ctx, sourcePath, bytes.NewReader([]byte("blocked"))); !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("WriteFile() while terminal restore rollback cleanup is unresolved = %v, want ErrTrashRecoveryRequired", err)
	}
	if err := fs.DeleteFromTrash(ctx, items[0].ID); !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("DeleteFromTrash() while terminal restore rollback cleanup is unresolved = %v, want ErrTrashRecoveryRequired", err)
	}

	report, err := fs.RecoverTrashTransfers(ctx)
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() after terminal restore rollback warning error: %v", err)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || report.Completed != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one restore rollback", report)
	}
	if fs.trashMutationBlocked != nil {
		t.Fatalf("successful terminal restore rollback recovery retained mutation gate: %v", fs.trashMutationBlocked)
	}
	requireNoTrashTransferCompletionArtifacts(t, fs, operationID)
	fs.commitTrashRestore = originalCommit
	if err := fs.RestoreFromTrash(ctx, items[0].ID); err != nil {
		t.Fatalf("RestoreFromTrash() after terminal rollback recovery error: %v", err)
	}
	restored, err := os.ReadFile(fs.workspace.FullPath(sourcePath))
	if err != nil || !bytes.Equal(restored, content) {
		t.Fatalf("restored source after recovery = %q, %v; want %q", restored, err, content)
	}
}

func trashTransferCheckpointsMissingForTest(fs *FileSystem, operationID string) bool {
	for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
		if _, err := fs.trashRootHandle.Lstat(filepath.FromSlash(trashTransferJournalRel(operationID, decision))); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}
