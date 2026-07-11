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

func TestFileSystem_TrashPurgeParticipantPreflightFailsBeforeMutation(t *testing.T) {
	preflightFailure := errors.New("Trash purge participant preflight failed")
	tests := []struct {
		name  string
		hooks func(completeCalls *int) TrashParticipantHooks
		want  error
	}{
		{
			name: "validation unavailable",
			hooks: func(completeCalls *int) TrashParticipantHooks {
				return TrashParticipantHooks{
					CompletePurge: func(context.Context, string, string, []byte) error {
						*completeCalls++
						return nil
					},
					RecoveryStateReliable: func() error { return nil },
				}
			},
		},
		{
			name: "completion unavailable",
			hooks: func(*int) TrashParticipantHooks {
				return TrashParticipantHooks{
					ValidatePurge:         func(context.Context, string, string, []byte) error { return nil },
					RecoveryStateReliable: func() error { return nil },
				}
			},
		},
		{
			name: "recovery evidence unavailable",
			hooks: func(completeCalls *int) TrashParticipantHooks {
				return TrashParticipantHooks{
					ValidatePurge: func(context.Context, string, string, []byte) error { return nil },
					CompletePurge: func(context.Context, string, string, []byte) error {
						*completeCalls++
						return nil
					},
				}
			},
		},
		{
			name: "recovery evidence unreliable",
			hooks: func(completeCalls *int) TrashParticipantHooks {
				return TrashParticipantHooks{
					ValidatePurge: func(context.Context, string, string, []byte) error { return nil },
					CompletePurge: func(context.Context, string, string, []byte) error {
						*completeCalls++
						return nil
					},
					RecoveryStateReliable: func() error { return preflightFailure },
				}
			},
			want: preflightFailure,
		},
		{
			name: "payload rejected",
			hooks: func(completeCalls *int) TrashParticipantHooks {
				return TrashParticipantHooks{
					ValidatePurge: func(context.Context, string, string, []byte) error {
						return preflightFailure
					},
					CompletePurge: func(context.Context, string, string, []byte) error {
						*completeCalls++
						return nil
					},
					RecoveryStateReliable: func() error { return nil },
				}
			},
			want: preflightFailure,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			item := seedTrashPurgeRecoveryItem(t, fixture.fs, "preflight-"+sanitizeTrashPurgeTestName(test.name), false, map[string]string{".": "retained"})
			completeCalls := 0
			fixture.fs.SetTrashParticipantHooks(test.hooks(&completeCalls))

			err := fixture.fs.DeleteFromTrash(context.Background(), item.ID)
			if err == nil {
				t.Fatal("DeleteFromTrash() error = nil, want preflight failure")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("DeleteFromTrash() error = %v, want %v", err, test.want)
			}
			if completeCalls != 0 {
				t.Fatalf("CompletePurge() calls = %d, want 0", completeCalls)
			}
			requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
			requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
			requireNoRecognizedTrashPurgeArtifacts(t, fixture.fs)
			if fixture.fs.trashMutationBlocked != nil {
				t.Fatalf("preflight failure blocked later Trash mutations: %v", fixture.fs.trashMutationBlocked)
			}
		})
	}
}

func TestFileSystem_TrashPurgeParticipantRunsAfterPhysicalCleanup(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "participant-order", false, map[string]string{".": "purged"})
	payload := append([]byte(nil), item.RestoreData...)
	validateCalls := 0
	completeCalls := 0
	operationID := ""
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		ValidatePurge: func(_ context.Context, gotOperationID, originalPath string, gotPayload []byte) error {
			validateCalls++
			operationID = gotOperationID
			if !validTrashPurgeOperationID(gotOperationID) || originalPath != item.OriginalPath || !bytes.Equal(gotPayload, payload) {
				t.Fatalf("ValidatePurge() = (%q, %q, %q)", gotOperationID, originalPath, gotPayload)
			}
			requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
			requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
			return nil
		},
		CompletePurge: func(ctx context.Context, gotOperationID, originalPath string, gotPayload []byte) error {
			completeCalls++
			if ctx.Err() != nil {
				t.Fatalf("CompletePurge() context error: %v", ctx.Err())
			}
			if gotOperationID != operationID || originalPath != item.OriginalPath || !bytes.Equal(gotPayload, payload) {
				t.Fatalf("CompletePurge() = (%q, %q, %q)", gotOperationID, originalPath, gotPayload)
			}
			requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, item.ID))
			requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, trashPurgeStageRel(operationID)))
			requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})

	if err := fixture.fs.DeleteFromTrash(context.Background(), item.ID); err != nil {
		t.Fatalf("DeleteFromTrash() error: %v", err)
	}
	if validateCalls != 1 || completeCalls != 1 {
		t.Fatalf("participant calls = validate %d complete %d, want 1, 1", validateCalls, completeCalls)
	}
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operationID)))
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operationID)))
}

func TestFileSystem_TrashPurgeParticipantCompletionDurabilityBarrier(t *testing.T) {
	for _, test := range []struct {
		name      string
		complete  func(error, *int) func(context.Context, string, string, []byte) error
		wantCalls int
	}{
		{
			name: "persistent warning",
			complete: func(completionFailure error, calls *int) func(context.Context, string, string, []byte) error {
				return func(context.Context, string, string, []byte) error {
					*calls++
					return wrapVisibleMutationWarning(completionFailure)
				}
			},
			wantCalls: 2,
		},
		{
			name: "hard failure",
			complete: func(completionFailure error, calls *int) func(context.Context, string, string, []byte) error {
				return func(context.Context, string, string, []byte) error {
					*calls++
					return completionFailure
				}
			},
			wantCalls: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			item := seedTrashPurgeRecoveryItem(t, fixture.fs, "barrier-"+sanitizeTrashPurgeTestName(test.name), false, map[string]string{".": "purged"})
			completionFailure := errors.New("Trash purge participant completion failed")
			operationID := ""
			completeCalls := 0
			fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
				ValidatePurge: func(_ context.Context, gotOperationID, _ string, _ []byte) error {
					operationID = gotOperationID
					return nil
				},
				CompletePurge:         test.complete(completionFailure, &completeCalls),
				RecoveryStateReliable: func() error { return nil },
			})

			err := fixture.fs.DeleteFromTrash(context.Background(), item.ID)
			var warning *TrashDeleteWarningError
			if !errors.As(err, &warning) || !errors.Is(err, completionFailure) || !errors.Is(err, ErrTrashRecoveryRequired) {
				t.Fatalf("DeleteFromTrash() error = %v, want cleanup warning and recovery gate", err)
			}
			if completeCalls != test.wantCalls {
				t.Fatalf("CompletePurge() calls = %d, want %d", completeCalls, test.wantCalls)
			}
			requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, item.ID))
			requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)
			requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operationID)))
			if blockedErr := fixture.fs.DeleteFromTrash(context.Background(), item.ID); !errors.Is(blockedErr, ErrTrashRecoveryRequired) {
				t.Fatalf("DeleteFromTrash() after participant failure = %v, want ErrTrashRecoveryRequired", blockedErr)
			}

			recoveryValidateCalls := 0
			recoveryCompleteCalls := 0
			fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
				ValidatePurge: func(context.Context, string, string, []byte) error {
					recoveryValidateCalls++
					return nil
				},
				CompletePurge: func(context.Context, string, string, []byte) error {
					recoveryCompleteCalls++
					return nil
				},
				RecoveryStateReliable: func() error { return nil },
			})
			report, recoveryErr := fixture.fs.RecoverTrashDeletions(context.Background())
			if recoveryErr != nil {
				t.Fatalf("RecoverTrashDeletions() error: %v", recoveryErr)
			}
			if report.RolledForward != 1 || recoveryValidateCalls != 1 || recoveryCompleteCalls != 1 {
				t.Fatalf("recovery = %+v, validate %d complete %d", report, recoveryValidateCalls, recoveryCompleteCalls)
			}
			if fixture.fs.trashMutationBlocked != nil {
				t.Fatalf("successful recovery retained mutation gate: %v", fixture.fs.trashMutationBlocked)
			}
			requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operationID)))
		})
	}
}

func TestFileSystem_TrashPurgeParticipantTransientWarningCompletesJournal(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "transient-warning", false, map[string]string{".": "purged"})
	completionWarning := errors.New("transient Trash purge participant warning")
	operationID := ""
	completeCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		ValidatePurge: func(_ context.Context, gotOperationID, _ string, _ []byte) error {
			operationID = gotOperationID
			return nil
		},
		CompletePurge: func(context.Context, string, string, []byte) error {
			completeCalls++
			if completeCalls == 1 {
				return wrapVisibleMutationWarning(completionWarning)
			}
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})

	err := fixture.fs.DeleteFromTrash(context.Background(), item.ID)
	var warning *TrashDeleteWarningError
	if !errors.As(err, &warning) || !errors.Is(err, completionWarning) || errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("DeleteFromTrash() error = %v, want resolved cleanup warning", err)
	}
	if completeCalls != 2 {
		t.Fatalf("CompletePurge() calls = %d, want 2", completeCalls)
	}
	if fixture.fs.trashMutationBlocked != nil {
		t.Fatalf("resolved participant warning retained mutation gate: %v", fixture.fs.trashMutationBlocked)
	}
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operationID)))
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operationID)))
}

func TestFileSystem_RecoverTrashDeletionsPreflightsPurgeParticipantsGlobally(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	preparedItem := seedTrashPurgeRecoveryItem(t, fixture.fs, "participant-global-prepared", false, map[string]string{".": "prepared"})
	preparedOperation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, preparedItem)
	preparedStage := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, preparedOperation)
	committedItem := seedTrashPurgeRecoveryItem(t, fixture.fs, "participant-global-committed", false, map[string]string{".": "committed"})
	committedOperation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, committedItem)
	committedStage := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, committedOperation)
	commitTrashPurgeRecoveryCheckpoint(t, fixture.fs, committedOperation)
	secondCommittedItem := seedTrashPurgeRecoveryItem(t, fixture.fs, "participant-global-second-committed", false, map[string]string{".": "second committed"})
	secondCommittedOperation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, secondCommittedItem)
	secondCommittedStage := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, secondCommittedOperation)
	commitTrashPurgeRecoveryCheckpoint(t, fixture.fs, secondCommittedOperation)
	fixture.reopen(t)

	validationFailure := errors.New("reject committed purge participant")
	reliabilityCalls := 0
	validateCalls := 0
	completeCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		ValidatePurge: func(context.Context, string, string, []byte) error {
			validateCalls++
			return validationFailure
		},
		CompletePurge: func(context.Context, string, string, []byte) error {
			completeCalls++
			return nil
		},
		RecoveryStateReliable: func() error {
			reliabilityCalls++
			return nil
		},
	})

	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	if !errors.Is(err, ErrTrashRecoveryRequired) || !errors.Is(err, validationFailure) {
		t.Fatalf("RecoverTrashDeletions() error = %v, want global participant preflight failure", err)
	}
	if report.RolledBack != 0 || report.RolledForward != 0 || len(report.Blocked) != 3 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want all operations blocked", report)
	}
	if reliabilityCalls != 1 || validateCalls != 2 || completeCalls != 0 {
		t.Fatalf("participant calls = reliability %d validate %d complete %d", reliabilityCalls, validateCalls, completeCalls)
	}
	requireTrashPurgePathExists(t, preparedStage)
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, preparedItem.ID))
	requireTrashPurgeMetadata(t, fixture.fs, preparedItem.ID, true)
	requireTrashPurgePathExists(t, committedStage)
	requireTrashPurgeMetadata(t, fixture.fs, committedItem.ID, false)
	requireTrashPurgePathExists(t, secondCommittedStage)
	requireTrashPurgeMetadata(t, fixture.fs, secondCommittedItem.ID, false)
}

func TestFileSystem_RecoverPreparedTrashPurgeDoesNotRequireParticipant(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "prepared-no-participant", false, map[string]string{".": "prepared"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	fixture.reopen(t)
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{})

	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashDeletions() error: %v", err)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want one rollback", report)
	}
	requireTrashPurgePathAbsent(t, stagePath)
	requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
}

func TestFileSystem_TrashPurgeCompletionIgnoresCanceledRequestContext(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "completion-context", false, map[string]string{".": "purged"})
	completeCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		ValidatePurge: func(context.Context, string, string, []byte) error { return nil },
		CompletePurge: func(ctx context.Context, _ string, _ string, _ []byte) error {
			completeCalls++
			if ctx.Err() != nil {
				t.Fatalf("CompletePurge() context error = %v, want non-canceled completion context", ctx.Err())
			}
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})

	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	committed, err := fixture.fs.executeTrashPurge(context.Background(), operation)
	if err != nil || !committed {
		t.Fatalf("executeTrashPurge() = (%t, %v), want committed", committed, err)
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fixture.fs.finishTrashPurge(canceledCtx, operation); err != nil {
		t.Fatalf("finishTrashPurge(canceled context) error: %v", err)
	}
	if completeCalls != 1 {
		t.Fatalf("CompletePurge() calls = %d, want 1", completeCalls)
	}
}

func TestFileSystem_TrashPurgeVersionCleanupFailureSetsRecoveryGate(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "version-cleanup-gate", false, map[string]string{".": "purged"})
	if err := fixture.fs.versions.RemoveFromTrash(context.Background(), item.ID); err != nil {
		t.Fatalf("RemoveFromTrash() error: %v", err)
	}
	item.HadVersions = true
	if err := fixture.fs.versions.AddToTrash(context.Background(), item); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}

	originalRemoveTrashPath := fixture.fs.removeTrashPath
	workspaceClosed := false
	fixture.fs.removeTrashPath = func(target string) error {
		if err := originalRemoveTrashPath(target); err != nil {
			return err
		}
		workspaceClosed = true
		return fixture.fs.workspace.Close()
	}

	err := fixture.fs.DeleteFromTrash(context.Background(), item.ID)
	var warning *TrashDeleteWarningError
	var recoveryRequired *TrashRecoveryRequiredError
	if !errors.As(err, &warning) || !errors.As(err, &recoveryRequired) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("DeleteFromTrash() error = %v, want cleanup warning and recovery gate", err)
	}
	if !workspaceClosed {
		t.Fatal("version cleanup failure was not injected after physical purge")
	}
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, item.ID))
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)
	requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(recoveryRequired.OperationID)))
	requireTrashPurgeResidueCounts(t, fixture.fs, 1, 1, 0)
	if blockedErr := fixture.fs.DeleteFromTrash(context.Background(), item.ID); !errors.Is(blockedErr, ErrTrashRecoveryRequired) {
		t.Fatalf("DeleteFromTrash() after version cleanup failure = %v, want ErrTrashRecoveryRequired", blockedErr)
	}

	fixture.reopen(t)
	report, recoveryErr := fixture.fs.RecoverTrashDeletions(context.Background())
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashDeletions() error: %v", recoveryErr)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want one roll-forward", report)
	}
	if fixture.fs.trashMutationBlocked != nil {
		t.Fatalf("successful recovery retained mutation gate: %v", fixture.fs.trashMutationBlocked)
	}
	requireTrashPurgeResidueCounts(t, fixture.fs, 0, 0, 0)
}

func TestFileSystem_TrashPurgeJournalCompletionFailureSetsRecoveryGate(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "journal-completion-gate", false, map[string]string{".": "purged"})
	journalFailure := errors.New("sync completed Trash purge journal")
	failJournalSync := false
	journalCompletionSyncs := 0
	completeCalls := 0
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		ValidatePurge: func(context.Context, string, string, []byte) error { return nil },
		CompletePurge: func(context.Context, string, string, []byte) error {
			completeCalls++
			if completeCalls == 1 {
				failJournalSync = true
			}
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})
	originalSyncManagedStorageDir := syncManagedStorageDir
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if failJournalSync && relName == trashPurgeJournalDir {
			journalCompletionSyncs++
			if journalCompletionSyncs == 2 {
				failJournalSync = false
				return journalFailure
			}
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	err := fixture.fs.DeleteFromTrash(context.Background(), item.ID)
	var warning *TrashDeleteWarningError
	var recoveryRequired *TrashRecoveryRequiredError
	if !errors.As(err, &warning) || !errors.As(err, &recoveryRequired) || !errors.Is(err, ErrTrashRecoveryRequired) || !errors.Is(err, journalFailure) {
		t.Fatalf("DeleteFromTrash() error = %v, want journal cleanup warning and recovery gate", err)
	}
	if completeCalls != 1 {
		t.Fatalf("CompletePurge() calls = %d, want 1", completeCalls)
	}
	if journalCompletionSyncs != 2 {
		t.Fatalf("journal completion syncs = %d, want failure after committed journal removal", journalCompletionSyncs)
	}
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, item.ID))
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)
	requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(recoveryRequired.OperationID)))
	requireTrashPurgeResidueCounts(t, fixture.fs, 0, 1, 0)
	if blockedErr := fixture.fs.DeleteFromTrash(context.Background(), item.ID); !errors.Is(blockedErr, ErrTrashRecoveryRequired) {
		t.Fatalf("DeleteFromTrash() after journal completion failure = %v, want ErrTrashRecoveryRequired", blockedErr)
	}

	report, recoveryErr := fixture.fs.RecoverTrashDeletions(context.Background())
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashDeletions() error: %v", recoveryErr)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want one roll-forward", report)
	}
	if completeCalls != 2 {
		t.Fatalf("CompletePurge() calls after recovery = %d, want 2", completeCalls)
	}
	if fixture.fs.trashMutationBlocked != nil {
		t.Fatalf("successful recovery retained mutation gate: %v", fixture.fs.trashMutationBlocked)
	}
	requireTrashPurgeResidueCounts(t, fixture.fs, 0, 0, 0)
}

func requireNoRecognizedTrashPurgeArtifacts(t *testing.T, fs *FileSystem) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(fs.trashRoot, trashPurgeJournalDir))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("ReadDir(Trash purge journal) error: %v", err)
	}
	for _, entry := range entries {
		if operationID, _, ok := parseTrashPurgeJournalName(entry.Name()); ok {
			t.Fatalf("unexpected Trash purge journal for operation %s", operationID)
		}
		if operationID, ok := parseTrashPurgeStageName(entry.Name()); ok {
			t.Fatalf("unexpected Trash purge stage for operation %s", operationID)
		}
	}
}

func sanitizeTrashPurgeTestName(name string) string {
	result := make([]byte, 0, len(name))
	for index := 0; index < len(name); index++ {
		character := name[index]
		if character == ' ' {
			character = '-'
		}
		result = append(result, character)
	}
	return string(result)
}

func TestFileSystem_TrashPurgeWithoutParticipantPayloadNeedsNoHooks(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "empty-participant", false, map[string]string{".": "purged"})
	item.RestoreData = nil
	if err := fixture.fs.versions.UpdateTrashRestoreData(context.Background(), item.ID, nil); err != nil {
		t.Fatalf("UpdateTrashRestoreData() error: %v", err)
	}
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{})

	if err := fixture.fs.DeleteFromTrash(context.Background(), item.ID); err != nil {
		t.Fatalf("DeleteFromTrash() error: %v", err)
	}
	if _, err := fixture.fs.versions.GetTrashItem(context.Background(), item.ID); !errors.Is(err, versionstore.ErrNotFound) {
		t.Fatalf("GetTrashItem() error = %v, want ErrNotFound", err)
	}
}
