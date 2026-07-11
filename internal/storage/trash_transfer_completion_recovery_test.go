package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFileSystem_DeleteRetriesOnlyParticipantCompletionAfterCompletedCheckpoint(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	const sourcePath = "/participant-completion-delete.txt"
	payload := []byte(`{"participant":"delete-completion"}`)
	if err := fs.WriteFile(ctx, sourcePath, bytes.NewReader([]byte("delete completion payload"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	completeErr := errors.New("delete participant completion unavailable")
	operationID := ""
	applyCalls := make([]bool, 0, 2)
	completeCalls := 0
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(_ context.Context, gotOperationID, gotPath string) ([]byte, error) {
			operationID = gotOperationID
			if gotPath != sourcePath {
				t.Errorf("PrepareDelete() path = %q, want %q", gotPath, sourcePath)
			}
			return append([]byte(nil), payload...), nil
		},
		ApplyDelete: func(_ context.Context, gotOperationID, gotPath string, gotPayload []byte, committed bool) error {
			if gotOperationID != operationID || gotPath != sourcePath || !bytes.Equal(gotPayload, payload) {
				t.Errorf("ApplyDelete() = (%q, %q, %q), want (%q, %q, %q)", gotOperationID, gotPath, gotPayload, operationID, sourcePath, payload)
			}
			applyCalls = append(applyCalls, committed)
			return nil
		},
		RollbackDelete: func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete: func(_ context.Context, gotOperationID, gotPath string, gotPayload []byte) error {
			completeCalls++
			if gotOperationID != operationID || gotPath != sourcePath || !bytes.Equal(gotPayload, payload) {
				t.Errorf("CompleteDelete() = (%q, %q, %q), want (%q, %q, %q)", gotOperationID, gotPath, gotPayload, operationID, sourcePath, payload)
			}
			if completeCalls == 1 {
				return completeErr
			}
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})

	err := fs.Delete(ctx, sourcePath)
	if !errors.Is(err, completeErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Delete() error = %v, want completion failure and ErrTrashRecoveryRequired", err)
	}
	if !validTrashPurgeOperationID(operationID) {
		t.Fatalf("PrepareDelete() operation ID = %q, want 32 hexadecimal characters", operationID)
	}
	if !reflect.DeepEqual(applyCalls, []bool{false, true}) || completeCalls != 1 {
		t.Fatalf("participant calls after Delete() = apply %v, complete %d; want [false true], 1", applyCalls, completeCalls)
	}
	if _, err := fs.trashRootHandle.Lstat(filepath.FromSlash(trashTransferJournalRel(operationID, trashTransferCompleted))); err != nil {
		t.Fatalf("Lstat(completed journal) error: %v", err)
	}
	operations, err := fs.versions.ListTrashOperations(ctx)
	if err != nil || len(operations) != 1 || operations[0].ID != operationID {
		t.Fatalf("ListTrashOperations() = %+v, %v, want pending operation %q", operations, err, operationID)
	}

	report, err := fs.RecoverTrashTransfers(ctx)
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.Completed != 1 || report.RolledForward != 0 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one completed recovery", report)
	}
	if !reflect.DeepEqual(applyCalls, []bool{false, true}) || completeCalls != 2 {
		t.Fatalf("participant calls after recovery = apply %v, complete %d; want no Apply replay and two Complete attempts", applyCalls, completeCalls)
	}
	requireNoTrashTransferCompletionArtifacts(t, fs, operationID)
}

func TestFileSystem_RestoreRetriesOnlyParticipantCompletionAfterCompletedCheckpoint(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	const originalPath = "/participant-completion-restore.txt"
	payload := []byte(`{"participant":"restore-completion"}`)
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(context.Context, string, string) ([]byte, error) {
			return append([]byte(nil), payload...), nil
		},
		ApplyDelete:           func(context.Context, string, string, []byte, bool) error { return nil },
		RollbackDelete:        func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error { return nil },
	})
	if err := fs.WriteFile(ctx, originalPath, bytes.NewReader([]byte("restore completion payload"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, originalPath); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || !bytes.Equal(items[0].RestoreData, payload) {
		t.Fatalf("ListTrash() = %+v, %v, want one item with participant payload %q", items, err, payload)
	}

	completeErr := errors.New("restore participant completion unavailable")
	operationID := ""
	applyCalls := 0
	completeCalls := 0
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		ApplyRestore: func(_ context.Context, gotOperationID, gotOriginalPath, gotDestinationPath string, gotPayload []byte) error {
			operationID = gotOperationID
			applyCalls++
			if gotOriginalPath != originalPath || gotDestinationPath != originalPath || !bytes.Equal(gotPayload, payload) {
				t.Errorf("ApplyRestore() = (%q, %q, %q), want (%q, %q, %q)", gotOriginalPath, gotDestinationPath, gotPayload, originalPath, originalPath, payload)
			}
			return nil
		},
		CompleteRestore: func(_ context.Context, gotOperationID, gotOriginalPath, gotDestinationPath string, gotPayload []byte) error {
			completeCalls++
			if gotOperationID != operationID || gotOriginalPath != originalPath || gotDestinationPath != originalPath || !bytes.Equal(gotPayload, payload) {
				t.Errorf("CompleteRestore() = (%q, %q, %q, %q), want (%q, %q, %q, %q)", gotOperationID, gotOriginalPath, gotDestinationPath, gotPayload, operationID, originalPath, originalPath, payload)
			}
			if completeCalls == 1 {
				return completeErr
			}
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})

	err = fs.RestoreFromTrash(ctx, items[0].ID)
	if !errors.Is(err, completeErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, want completion failure and ErrTrashRecoveryRequired", err)
	}
	if !validTrashPurgeOperationID(operationID) {
		t.Fatalf("ApplyRestore() operation ID = %q, want 32 hexadecimal characters", operationID)
	}
	if applyCalls != 1 || completeCalls != 1 {
		t.Fatalf("participant calls after RestoreFromTrash() = apply %d, complete %d; want 1, 1", applyCalls, completeCalls)
	}
	if _, err := fs.trashRootHandle.Lstat(filepath.FromSlash(trashTransferJournalRel(operationID, trashTransferCompleted))); err != nil {
		t.Fatalf("Lstat(completed journal) error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(fs.trashRoot, items[0].ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(removed Trash source) error = %v, want os.ErrNotExist", err)
	}
	operations, err := fs.versions.ListTrashOperations(ctx)
	if err != nil || len(operations) != 1 || operations[0].ID != operationID {
		t.Fatalf("ListTrashOperations() = %+v, %v, want pending operation %q", operations, err, operationID)
	}

	report, err := fs.RecoverTrashTransfers(ctx)
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.Completed != 1 || report.RolledForward != 0 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one completed recovery", report)
	}
	if applyCalls != 1 || completeCalls != 2 {
		t.Fatalf("participant calls after recovery = apply %d, complete %d; want no Apply replay and two Complete attempts", applyCalls, completeCalls)
	}
	requireNoTrashTransferCompletionArtifacts(t, fs, operationID)
}

func requireNoTrashTransferCompletionArtifacts(t *testing.T, fs *FileSystem, operationID string) {
	t.Helper()

	operations, err := fs.versions.ListTrashOperations(context.Background())
	if err != nil {
		t.Fatalf("ListTrashOperations() error: %v", err)
	}
	if len(operations) != 0 {
		t.Fatalf("ListTrashOperations() = %+v, want no pending operation", operations)
	}
	for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
		rel := filepath.FromSlash(trashTransferJournalRel(operationID, decision))
		if _, err := fs.trashRootHandle.Lstat(rel); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Lstat(%s journal) error = %v, want os.ErrNotExist", decision, err)
		}
	}
}
