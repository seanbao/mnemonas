//go:build linux || darwin

package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/versionstore"
)

type writeTransactionJournalHarness struct {
	internalPath string
	internalRoot *os.Root
	journal      *WriteTransactionJournal
	plan         WriteTransactionPlan
	outcome      WriteTransactionPublishedOutcome
}

func newWriteTransactionJournalHarness(t *testing.T) *writeTransactionJournalHarness {
	t.Helper()
	internalPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(internalPath, writeStagingDir), 0o700); err != nil {
		t.Fatal(err)
	}
	internalRoot, err := os.OpenRoot(internalPath)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := OpenWriteTransactionJournal(internalRoot)
	if err != nil {
		_ = internalRoot.Close()
		t.Fatal(err)
	}
	harness := &writeTransactionJournalHarness{
		internalPath: internalPath,
		internalRoot: internalRoot,
		journal:      journal,
	}
	t.Cleanup(func() {
		_ = harness.journal.Close()
		_ = harness.internalRoot.Close()
	})

	filesDir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer filesDir.Close()
	stagingDir, err := os.Open(filepath.Join(internalPath, writeStagingDir))
	if err != nil {
		t.Fatal(err)
	}
	defer stagingDir.Close()
	filesBinding, err := CaptureWriteTransactionRootBinding(filesDir)
	if err != nil {
		t.Fatal(err)
	}
	stagingBinding, err := CaptureWriteTransactionRootBinding(stagingDir)
	if err != nil {
		t.Fatal(err)
	}

	sourceIdentity := strings.Repeat("a", 64)
	sourceDeleteIdentity := strings.Repeat("b", 64)
	contentHash := strings.Repeat("c", 64)
	sourceStage := testWriteTransactionStagePath(
		writeSourceStagePrefix,
		sourceIdentity,
		".tmp",
		"0000000000000001",
	)
	source := WriteTransactionObjectEvidence{
		RelativePath:       sourceStage,
		PersistentIdentity: sourceIdentity,
		DeleteIdentity:     sourceDeleteIdentity,
		Mode:               uint32(0o600),
		Size:               7,
		ModTimeUnixNano:    2_000_000_123,
		BLAKE3:             contentHash,
	}
	after := WriteTransactionObjectExpectation{
		RelativePath:       "target.txt",
		PersistentIdentity: sourceIdentity,
		Mode:               source.Mode,
		Size:               source.Size,
		ModTimeUnixNano:    source.ModTimeUnixNano,
		BLAKE3:             source.BLAKE3,
	}
	harness.plan = WriteTransactionPlan{
		Kind: WriteTransactionKindCreate,
		Roots: WriteTransactionRootBindings{
			Files:    filesBinding,
			Internal: journal.InternalRootBinding(),
			Staging:  stagingBinding,
			Journal:  journal.JournalRootBinding(),
		},
		Target: WriteTransactionTargetEvidence{
			Path:                     "/target.txt",
			RelativePath:             "target.txt",
			ParentRelativePath:       ".",
			ParentPersistentIdentity: filesBinding.PersistentIdentity,
			ParentMode:               filesBinding.Mode,
			After:                    after,
		},
		Source: source,
		Stages: WriteTransactionStagePlan{
			Source: sourceStage,
		},
		Metadata: versionstore.WriteMetadataPlan{
			IndexAfter: versionstore.FileIndexRecord{
				Path:        "/target.txt",
				Size:        7,
				ModTimeUnix: 2,
				ContentHash: contentHash,
			},
		},
	}
	harness.outcome = WriteTransactionPublishedOutcome{
		Target: WriteTransactionObjectEvidence{
			RelativePath:       after.RelativePath,
			PersistentIdentity: after.PersistentIdentity,
			DeleteIdentity:     strings.Repeat("d", 64),
			Mode:               after.Mode,
			Size:               after.Size,
			ModTimeUnixNano:    after.ModTimeUnixNano,
			BLAKE3:             after.BLAKE3,
		},
	}
	return harness
}

func testWriteTransactionStagePath(
	prefix string,
	identity string,
	extension string,
	suffix string,
) string {
	return filepath.Join(writeStagingDir, prefix+identity+"-"+suffix+extension)
}

func (harness *writeTransactionJournalHarness) reopen(t *testing.T) {
	t.Helper()
	if err := harness.journal.Close(); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenWriteTransactionJournal(harness.internalRoot)
	if err != nil {
		t.Fatal(err)
	}
	harness.journal = journal
}

func publishWriteTransactionCheckpoint(
	t *testing.T,
	harness *writeTransactionJournalHarness,
	operationID string,
	checkpoint WriteTransactionCheckpoint,
) (WriteTransactionPublishResult, error) {
	t.Helper()
	switch checkpoint {
	case WriteTransactionCheckpointPrepared:
		return harness.journal.PublishPrepared(operationID, harness.plan)
	case WriteTransactionCheckpointPublished:
		return harness.journal.PublishPublished(operationID, harness.outcome)
	case WriteTransactionCheckpointCommitted:
		return harness.journal.PublishCommitted(operationID)
	default:
		t.Fatalf("unsupported checkpoint %q", checkpoint)
		return WriteTransactionPublishResult{}, nil
	}
}

func prepareWriteTransactionPredecessors(
	t *testing.T,
	harness *writeTransactionJournalHarness,
	operationID string,
	checkpoint WriteTransactionCheckpoint,
) {
	t.Helper()
	if checkpoint == WriteTransactionCheckpointPrepared {
		return
	}
	if result, err := harness.journal.PublishPrepared(operationID, harness.plan); err != nil ||
		!result.FinalObserved {
		t.Fatalf("publish prepared predecessor: result=%+v err=%v", result, err)
	}
	if checkpoint == WriteTransactionCheckpointCommitted {
		if result, err := harness.journal.PublishPublished(operationID, harness.outcome); err != nil ||
			!result.FinalObserved {
			t.Fatalf("publish published predecessor: result=%+v err=%v", result, err)
		}
	}
}

func TestWriteTransactionJournalLifecycleAndIdempotency(t *testing.T) {
	harness := newWriteTransactionJournalHarness(t)
	operationID := strings.Repeat("1", 32)

	prepared, err := harness.journal.PublishPrepared(operationID, harness.plan)
	if err != nil || !prepared.FinalObserved ||
		prepared.FinalObservation != WriteTransactionFinalObservedValid {
		t.Fatalf("publish prepared: result=%+v err=%v", prepared, err)
	}
	repeatedPrepared, err := harness.journal.PublishPrepared(operationID, harness.plan)
	if err != nil || repeatedPrepared.Record.RecordDigest != prepared.Record.RecordDigest ||
		!repeatedPrepared.FinalObserved {
		t.Fatalf("repeat prepared: result=%+v err=%v", repeatedPrepared, err)
	}

	published, err := harness.journal.PublishPublished(operationID, harness.outcome)
	if err != nil || !published.FinalObserved || published.Record.Outcome == nil ||
		published.Record.PlanDigest != prepared.Record.PlanDigest ||
		published.Record.OutcomeDigest == "" {
		t.Fatalf("publish outcome: result=%+v err=%v", published, err)
	}
	if published.Record.Outcome.Target.DeleteIdentity ==
		prepared.Record.Plan.Source.DeleteIdentity {
		t.Fatal("published evidence unexpectedly reused the pre-rename delete token")
	}

	committed, err := harness.journal.PublishCommitted(operationID)
	if err != nil || !committed.FinalObserved || committed.Record.Outcome == nil ||
		committed.Record.OutcomeDigest != published.Record.OutcomeDigest {
		t.Fatalf("publish committed: result=%+v err=%v", committed, err)
	}
	operations, err := harness.journal.Scan()
	if err != nil || len(operations) != 1 ||
		operations[0].State != WriteTransactionStateCommitted ||
		operations[0].Decision != WriteTransactionDecisionRollforward {
		t.Fatalf("scan committed: operations=%+v err=%v", operations, err)
	}
	if err := harness.journal.CleanupRollback(operationID); !errors.Is(err, ErrWriteTransactionJournalConflict) {
		t.Fatalf("rollback committed error = %v", err)
	}
	if err := harness.journal.CleanupRollforward(operationID); err != nil {
		t.Fatal(err)
	}
	if operations, err = harness.journal.Scan(); err != nil || len(operations) != 0 {
		t.Fatalf("scan after cleanup: operations=%+v err=%v", operations, err)
	}
}

func TestOpenWriteTransactionJournalSyncsJournalBeforeInternalParent(t *testing.T) {
	internalPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(internalPath, writeStagingDir), 0o700); err != nil {
		t.Fatal(err)
	}
	internalRoot, err := os.OpenRoot(internalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer internalRoot.Close()
	var points []string
	originalHook := writeTransactionJournalFaultHook
	writeTransactionJournalFaultHook = func(point string) error {
		if strings.HasPrefix(point, "open:") {
			points = append(points, point)
		}
		return nil
	}
	journal, err := OpenWriteTransactionJournal(internalRoot)
	writeTransactionJournalFaultHook = originalHook
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if got, want := strings.Join(points, ","), "open:journal_directory_synced,open:internal_directory_synced"; got != want {
		t.Fatalf("open sync order=%q, want %q", got, want)
	}
}

func TestWriteTransactionJournalScanOrderIsDeterministic(t *testing.T) {
	harness := newWriteTransactionJournalHarness(t)
	for _, operationID := range []string{strings.Repeat("2", 32), strings.Repeat("1", 32)} {
		if result, err := harness.journal.PublishPrepared(operationID, harness.plan); err != nil ||
			!result.FinalObserved {
			t.Fatalf("publish %s: result=%+v err=%v", operationID, result, err)
		}
	}
	operations, err := harness.journal.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 2 ||
		operations[0].OperationID != strings.Repeat("1", 32) ||
		operations[1].OperationID != strings.Repeat("2", 32) {
		t.Fatalf("unexpected scan order: %+v", operations)
	}
}

func TestWriteTransactionJournalCheckpointFaultMatrix(t *testing.T) {
	checkpoints := []WriteTransactionCheckpoint{
		WriteTransactionCheckpointPrepared,
		WriteTransactionCheckpointPublished,
		WriteTransactionCheckpointCommitted,
	}
	points := []struct {
		name          string
		finalObserved bool
	}{
		{"pending_created", false},
		{"pending_partial", false},
		{"pending_file_sync", false},
		{"pending_file_synced", false},
		{"final_renamed", true},
		{"directory_sync", true},
		{"directory_synced", true},
	}
	for _, checkpoint := range checkpoints {
		for _, point := range points {
			t.Run(string(checkpoint)+"/"+point.name, func(t *testing.T) {
				harness := newWriteTransactionJournalHarness(t)
				operationID := strings.Repeat("3", 32)
				prepareWriteTransactionPredecessors(t, harness, operationID, checkpoint)

				faultPoint := "checkpoint:" + string(checkpoint) + ":" + point.name
				originalHook := writeTransactionJournalFaultHook
				writeTransactionJournalFaultHook = func(current string) error {
					if current == faultPoint {
						return fmt.Errorf("injected %s", current)
					}
					return nil
				}
				result, err := publishWriteTransactionCheckpoint(
					t,
					harness,
					operationID,
					checkpoint,
				)
				writeTransactionJournalFaultHook = originalHook
				if !errors.Is(err, errWriteTransactionJournalCrashInjected) {
					t.Fatalf("publish error = %v", err)
				}
				if result.FinalObserved != point.finalObserved {
					t.Fatalf("FinalObserved=%v, want %v; result=%+v err=%v",
						result.FinalObserved, point.finalObserved, result, err)
				}
				expectedObservation := WriteTransactionFinalAbsent
				if point.finalObserved {
					expectedObservation = WriteTransactionFinalObservedValid
				}
				if result.FinalObservation != expectedObservation {
					t.Fatalf("FinalObservation=%q, want %q", result.FinalObservation, expectedObservation)
				}

				harness.reopen(t)
				operations, scanErr := harness.journal.Scan()
				if scanErr != nil {
					t.Fatal(scanErr)
				}
				if point.finalObserved {
					if len(operations) != 1 ||
						operations[0].State != writeTransactionExpectedState(checkpoint) {
						t.Fatalf("observed final scan=%+v", operations)
					}
					if checkpoint == WriteTransactionCheckpointCommitted &&
						operations[0].Decision != WriteTransactionDecisionRollforward {
						t.Fatalf("committed decision=%q", operations[0].Decision)
					}
					return
				}
				switch checkpoint {
				case WriteTransactionCheckpointPrepared:
					if len(operations) != 0 {
						t.Fatalf("prepared pending was promoted: %+v", operations)
					}
				case WriteTransactionCheckpointPublished:
					if len(operations) != 1 ||
						operations[0].State != WriteTransactionStatePrepared {
						t.Fatalf("published pending changed decision: %+v", operations)
					}
				case WriteTransactionCheckpointCommitted:
					if len(operations) != 1 ||
						operations[0].State != WriteTransactionStatePublished ||
						operations[0].Decision != WriteTransactionDecisionRollback {
						t.Fatalf("committed pending changed decision: %+v", operations)
					}
				}
				again, scanErr := harness.journal.Scan()
				if scanErr != nil || len(again) != len(operations) {
					t.Fatalf("idempotent rescan=%+v err=%v", again, scanErr)
				}
			})
		}
	}
}

func writeTransactionExpectedState(checkpoint WriteTransactionCheckpoint) WriteTransactionState {
	switch checkpoint {
	case WriteTransactionCheckpointPrepared:
		return WriteTransactionStatePrepared
	case WriteTransactionCheckpointPublished:
		return WriteTransactionStatePublished
	case WriteTransactionCheckpointCommitted:
		return WriteTransactionStateCommitted
	default:
		return ""
	}
}

func TestWriteTransactionJournalCleanupFaultsAreRepeatable(t *testing.T) {
	t.Run("rollback", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("4", 32)
		prepareWriteTransactionPredecessors(
			t,
			harness,
			operationID,
			WriteTransactionCheckpointCommitted,
		)
		originalHook := writeTransactionJournalFaultHook
		writeTransactionJournalFaultHook = func(point string) error {
			if point == "checkpoint:cleanup:rollback:published_directory_sync" {
				return errors.New("rollback sync failed")
			}
			return nil
		}
		err := harness.journal.CleanupRollback(operationID)
		writeTransactionJournalFaultHook = originalHook
		if !errors.Is(err, errWriteTransactionJournalCrashInjected) {
			t.Fatalf("rollback cleanup error=%v", err)
		}
		harness.reopen(t)
		operations, err := harness.journal.Scan()
		if err != nil || len(operations) != 1 ||
			operations[0].State != WriteTransactionStatePrepared {
			t.Fatalf("rollback suffix state=%+v err=%v", operations, err)
		}
		if err := harness.journal.CleanupRollback(operationID); err != nil {
			t.Fatal(err)
		}
		if operations, err = harness.journal.Scan(); err != nil || len(operations) != 0 {
			t.Fatalf("rollback retry=%+v err=%v", operations, err)
		}
	})

	t.Run("rollforward", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("5", 32)
		prepareWriteTransactionPredecessors(
			t,
			harness,
			operationID,
			WriteTransactionCheckpointCommitted,
		)
		if result, err := harness.journal.PublishCommitted(operationID); err != nil ||
			!result.FinalObserved {
			t.Fatalf("commit: result=%+v err=%v", result, err)
		}

		for _, fault := range []struct {
			point string
			state WriteTransactionState
		}{
			{
				"checkpoint:cleanup:rollforward:prepared_directory_sync",
				WriteTransactionStateRollforwardWithoutPrepared,
			},
			{
				"checkpoint:cleanup:rollforward:published_directory_sync",
				WriteTransactionStateRollforwardCommittedOnly,
			},
		} {
			originalHook := writeTransactionJournalFaultHook
			writeTransactionJournalFaultHook = func(point string) error {
				if point == fault.point {
					return errors.New("rollforward sync failed")
				}
				return nil
			}
			err := harness.journal.CleanupRollforward(operationID)
			writeTransactionJournalFaultHook = originalHook
			if !errors.Is(err, errWriteTransactionJournalCrashInjected) {
				t.Fatalf("%s error=%v", fault.point, err)
			}
			harness.reopen(t)
			operations, scanErr := harness.journal.Scan()
			if scanErr != nil || len(operations) != 1 ||
				operations[0].State != fault.state ||
				operations[0].Decision != WriteTransactionDecisionRollforward {
				t.Fatalf("%s state=%+v err=%v", fault.point, operations, scanErr)
			}
		}
		if err := harness.journal.CleanupRollforward(operationID); err != nil {
			t.Fatal(err)
		}
		operations, err := harness.journal.Scan()
		if err != nil || len(operations) != 0 {
			t.Fatalf("rollforward retry=%+v err=%v", operations, err)
		}
	})
}

func TestWriteTransactionJournalCleanupFaultMatrix(t *testing.T) {
	for _, direction := range []string{"rollback", "rollforward"} {
		checkpoints := []WriteTransactionCheckpoint{
			WriteTransactionCheckpointPublished,
			WriteTransactionCheckpointPrepared,
		}
		if direction == "rollforward" {
			checkpoints = []WriteTransactionCheckpoint{
				WriteTransactionCheckpointPrepared,
				WriteTransactionCheckpointPublished,
				WriteTransactionCheckpointCommitted,
			}
		}
		for _, checkpoint := range checkpoints {
			for _, boundary := range []string{"directory_sync", "removed"} {
				t.Run(direction+"/"+string(checkpoint)+"/"+boundary, func(t *testing.T) {
					harness := newWriteTransactionJournalHarness(t)
					operationID := strings.Repeat("0", 32)
					prepareWriteTransactionPredecessors(
						t,
						harness,
						operationID,
						WriteTransactionCheckpointCommitted,
					)
					if direction == "rollforward" {
						if result, err := harness.journal.PublishCommitted(operationID); err != nil ||
							!result.FinalObserved {
							t.Fatalf("commit: result=%+v err=%v", result, err)
						}
					}
					faultPoint := "checkpoint:cleanup:" + direction + ":" +
						string(checkpoint) + "_" + boundary
					originalHook := writeTransactionJournalFaultHook
					writeTransactionJournalFaultHook = func(point string) error {
						if point == faultPoint {
							return errors.New("cleanup fault")
						}
						return nil
					}
					var cleanupErr error
					if direction == "rollback" {
						cleanupErr = harness.journal.CleanupRollback(operationID)
					} else {
						cleanupErr = harness.journal.CleanupRollforward(operationID)
					}
					writeTransactionJournalFaultHook = originalHook
					if !errors.Is(cleanupErr, errWriteTransactionJournalCrashInjected) {
						t.Fatalf("cleanup error=%v", cleanupErr)
					}
					harness.reopen(t)
					operations, err := harness.journal.Scan()
					if err != nil {
						t.Fatal(err)
					}
					expectedState, expectOperation := expectedWriteTransactionCleanupFaultState(
						direction,
						checkpoint,
					)
					if !expectOperation {
						if len(operations) != 0 {
							t.Fatalf("cleanup left operations=%+v", operations)
						}
					} else if len(operations) != 1 || operations[0].State != expectedState {
						t.Fatalf("cleanup state=%+v, want %q", operations, expectedState)
					}
					if direction == "rollback" {
						err = harness.journal.CleanupRollback(operationID)
					} else {
						err = harness.journal.CleanupRollforward(operationID)
					}
					if err != nil {
						t.Fatalf("cleanup retry: %v", err)
					}
					if operations, err = harness.journal.Scan(); err != nil || len(operations) != 0 {
						t.Fatalf("cleanup rescan=%+v err=%v", operations, err)
					}
				})
			}
		}
	}
}

func expectedWriteTransactionCleanupFaultState(
	direction string,
	checkpoint WriteTransactionCheckpoint,
) (WriteTransactionState, bool) {
	if direction == "rollback" {
		if checkpoint == WriteTransactionCheckpointPublished {
			return WriteTransactionStatePrepared, true
		}
		return "", false
	}
	switch checkpoint {
	case WriteTransactionCheckpointPrepared:
		return WriteTransactionStateRollforwardWithoutPrepared, true
	case WriteTransactionCheckpointPublished:
		return WriteTransactionStateRollforwardCommittedOnly, true
	default:
		return "", false
	}
}

func TestWriteTransactionJournalPendingCleanupFaultsAreRepeatable(t *testing.T) {
	for _, boundary := range []string{"directory_sync", "removed"} {
		t.Run(boundary, func(t *testing.T) {
			harness := newWriteTransactionJournalHarness(t)
			operationID := strings.Repeat("0", 32)
			originalHook := writeTransactionJournalFaultHook
			writeTransactionJournalFaultHook = func(point string) error {
				if point == "checkpoint:prepared:pending_partial" {
					return errors.New("leave pending prefix")
				}
				return nil
			}
			result, err := harness.journal.PublishPrepared(operationID, harness.plan)
			writeTransactionJournalFaultHook = originalHook
			if !errors.Is(err, errWriteTransactionJournalCrashInjected) ||
				result.FinalObserved {
				t.Fatalf("prepare pending: result=%+v err=%v", result, err)
			}

			writeTransactionJournalFaultHook = func(point string) error {
				if boundary == "directory_sync" &&
					point == "checkpoint:pending_cleanup:directory_sync" {
					return errors.New("pending cleanup sync failed")
				}
				if boundary == "removed" &&
					strings.HasPrefix(point, "checkpoint:pending_removed:") {
					return errors.New("pending cleanup completion failed")
				}
				return nil
			}
			_, err = harness.journal.Scan()
			writeTransactionJournalFaultHook = originalHook
			if !errors.Is(err, errWriteTransactionJournalCrashInjected) {
				t.Fatalf("pending cleanup error=%v", err)
			}
			harness.reopen(t)
			operations, err := harness.journal.Scan()
			if err != nil || len(operations) != 0 {
				t.Fatalf("pending cleanup rescan=%+v err=%v", operations, err)
			}
		})
	}
}

func TestWriteTransactionJournalRejectsCorruptEvidence(t *testing.T) {
	t.Run("unknown entry", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		harness.writeRaw(t, "unknown", []byte("x"), os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
		if _, err := harness.internalRoot.Lstat(filepath.Join(writeTransactionJournalDir, "unknown")); err != nil {
			t.Fatalf("corrupt evidence was removed: %v", err)
		}
	})

	t.Run("trailing final bytes", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("6", 32)
		if result, err := harness.journal.PublishPrepared(operationID, harness.plan); err != nil ||
			!result.FinalObserved {
			t.Fatalf("prepare: result=%+v err=%v", result, err)
		}
		name := writeTransactionJournalName(operationID, WriteTransactionCheckpointPrepared, false)
		harness.writeRaw(t, name, []byte("x"), os.O_WRONLY|os.O_APPEND)
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})

	t.Run("record digest mismatch", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("7", 32)
		result, err := harness.journal.PublishPrepared(operationID, harness.plan)
		if err != nil || !result.FinalObserved {
			t.Fatalf("prepare: result=%+v err=%v", result, err)
		}
		record := result.Record
		record.RecordDigest = strings.Repeat("f", 64)
		data, err := marshalWriteTransactionRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		name := writeTransactionJournalName(operationID, WriteTransactionCheckpointPrepared, false)
		harness.writeRaw(t, name, data, os.O_WRONLY|os.O_TRUNC)
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})

	t.Run("truncated final record", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("7", 32)
		result, err := harness.journal.PublishPrepared(operationID, harness.plan)
		if err != nil || !result.FinalObserved {
			t.Fatalf("prepare: result=%+v err=%v", result, err)
		}
		name := writeTransactionJournalName(operationID, WriteTransactionCheckpointPrepared, false)
		data, err := harness.internalRoot.ReadFile(filepath.Join(writeTransactionJournalDir, name))
		if err != nil {
			t.Fatal(err)
		}
		harness.writeRaw(t, name, data[:len(data)/2], os.O_WRONLY|os.O_TRUNC)
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})

	t.Run("predecessor digest mismatch", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("7", 32)
		prepareWriteTransactionPredecessors(
			t,
			harness,
			operationID,
			WriteTransactionCheckpointCommitted,
		)
		name := writeTransactionJournalName(operationID, WriteTransactionCheckpointPublished, false)
		record, err := harness.journal.readRecordLocked(name)
		if err != nil {
			t.Fatal(err)
		}
		record.PreparedDigest = strings.Repeat("f", 64)
		record.PredecessorDigest = record.PreparedDigest
		record.RecordDigest, err = writeTransactionRecordDigest(record)
		if err != nil {
			t.Fatal(err)
		}
		data, err := marshalWriteTransactionRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		harness.writeRaw(t, name, data, os.O_WRONLY|os.O_TRUNC)
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})

	t.Run("published without prepared", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("8", 32)
		prepareWriteTransactionPredecessors(
			t,
			harness,
			operationID,
			WriteTransactionCheckpointCommitted,
		)
		preparedName := writeTransactionJournalName(
			operationID,
			WriteTransactionCheckpointPrepared,
			false,
		)
		if err := harness.internalRoot.Remove(filepath.Join(writeTransactionJournalDir, preparedName)); err != nil {
			t.Fatal(err)
		}
		if err := harness.journal.journalDir.Sync(); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})

	t.Run("garbage prepared pending", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("9", 32)
		name := writeTransactionJournalName(
			operationID,
			WriteTransactionCheckpointPrepared,
			true,
		)
		garbage := fmt.Sprintf(
			`{"schema":1,"checkpoint":"prepared","operation_id":%q,"evil":`,
			operationID,
		)
		harness.writeRaw(t, name, []byte(garbage), os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
		if _, err := harness.internalRoot.Lstat(filepath.Join(writeTransactionJournalDir, name)); err != nil {
			t.Fatalf("garbage pending was removed: %v", err)
		}
	})

	t.Run("out of order pending", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("a", 32)
		name := writeTransactionJournalName(
			operationID,
			WriteTransactionCheckpointPublished,
			true,
		)
		harness.writeRaw(t, name, nil, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})

	t.Run("concurrent pending", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("b", 32)
		for _, checkpoint := range []WriteTransactionCheckpoint{
			WriteTransactionCheckpointPrepared,
			WriteTransactionCheckpointPublished,
		} {
			name := writeTransactionJournalName(operationID, checkpoint, true)
			harness.writeRaw(t, name, nil, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		}
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})

	t.Run("pending and final checkpoint", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("b", 32)
		if result, err := harness.journal.PublishPrepared(operationID, harness.plan); err != nil ||
			!result.FinalObserved {
			t.Fatalf("prepare: result=%+v err=%v", result, err)
		}
		name := writeTransactionJournalName(
			operationID,
			WriteTransactionCheckpointPrepared,
			true,
		)
		harness.writeRaw(t, name, nil, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})
}

func TestWriteTransactionJournalValidatesBindingsAndPaths(t *testing.T) {
	t.Run("arbitrary internal path", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		plan := harness.plan
		plan.Stages.Source = "index.db"
		plan.Source.RelativePath = "index.db"
		if result, err := harness.journal.PublishPrepared(strings.Repeat("c", 32), plan); err == nil || result.FinalObserved {
			t.Fatalf("malicious stage accepted: result=%+v err=%v", result, err)
		}
	})

	t.Run("journal mode changed", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		if err := os.Chmod(
			filepath.Join(harness.internalPath, writeTransactionJournalDir),
			0o755,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})

	t.Run("internal root mode changed", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		if err := os.Chmod(harness.internalPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})

	t.Run("staging root binding changed", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		operationID := strings.Repeat("c", 32)
		if result, err := harness.journal.PublishPrepared(operationID, harness.plan); err != nil ||
			!result.FinalObserved {
			t.Fatalf("prepare: result=%+v err=%v", result, err)
		}
		if err := os.Chmod(filepath.Join(harness.internalPath, writeStagingDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.journal.Scan(); err == nil {
			t.Fatal("scan accepted a changed staging root")
		}
	})

	t.Run("journal directory replaced", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		journalPath := filepath.Join(harness.internalPath, writeTransactionJournalDir)
		if err := os.Rename(journalPath, journalPath+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(journalPath, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.journal.Scan(); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
			t.Fatalf("scan error=%v", err)
		}
	})

	t.Run("metadata content mismatch", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		plan := harness.plan
		plan.Metadata.IndexAfter.Size++
		if result, err := harness.journal.PublishPrepared(strings.Repeat("d", 32), plan); err == nil || result.FinalObserved {
			t.Fatalf("cross-field mismatch accepted: result=%+v err=%v", result, err)
		}
	})

	t.Run("CAS old-content mismatch", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		configureVersionedOverwritePlan(harness, false)
		harness.plan.CAS.Hash = strings.Repeat("2", 64)
		harness.plan.Metadata.VersionAfter.Hash = harness.plan.CAS.Hash
		if result, err := harness.journal.PublishPrepared(strings.Repeat("d", 32), harness.plan); err == nil || result.FinalObserved {
			t.Fatalf("CAS cross-field mismatch accepted: result=%+v err=%v", result, err)
		}
	})

	t.Run("overwrite cannot own directories", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		configureVersionedOverwritePlan(harness, true)
		harness.plan.CreatedDirectories = []WriteTransactionCreatedDirectory{{}}
		result, err := harness.journal.PublishPrepared(strings.Repeat("d", 32), harness.plan)
		if err == nil || result.FinalObserved ||
			!strings.Contains(err.Error(), "overwrite cannot own target directories") {
			t.Fatalf("overwrite directory ownership accepted: result=%+v err=%v", result, err)
		}
	})

	t.Run("created directory ancestry", func(t *testing.T) {
		harness := newWriteTransactionJournalHarness(t)
		plan := harness.plan
		plan.Target.Path = "/alpha/beta/target.txt"
		plan.Target.RelativePath = "alpha/beta/target.txt"
		plan.Target.ParentRelativePath = "alpha/beta"
		plan.Target.ParentPersistentIdentity = strings.Repeat("2", 64)
		plan.Target.ParentMode = uint32(os.ModeDir | 0o750)
		plan.Target.After.RelativePath = plan.Target.RelativePath
		plan.Metadata.IndexAfter.Path = plan.Target.Path
		plan.CreatedDirectories = []WriteTransactionCreatedDirectory{
			{
				Path:               "/alpha/beta",
				RelativePath:       "alpha/beta",
				PreAbsent:          true,
				PersistentIdentity: plan.Target.ParentPersistentIdentity,
				Mode:               plan.Target.ParentMode,
			},
			{
				Path:               "/alpha",
				RelativePath:       "alpha",
				PreAbsent:          true,
				PersistentIdentity: strings.Repeat("3", 64),
				Mode:               uint32(os.ModeDir | 0o750),
			},
		}
		if result, err := harness.journal.PublishPrepared(
			strings.Repeat("d", 32),
			plan,
		); err == nil || result.FinalObserved ||
			!strings.Contains(err.Error(), "lacks its base binding") {
			t.Fatalf("base-less created-directory chain accepted: result=%+v err=%v", result, err)
		}
		plan.CreatedDirectoryBase = &WriteTransactionCreatedDirectoryBase{
			RelativePath:       ".",
			PersistentIdentity: plan.Roots.Files.PersistentIdentity,
			Mode:               plan.Roots.Files.Mode,
		}
		if result, err := harness.journal.PublishPrepared(strings.Repeat("d", 32), plan); err != nil || !result.FinalObserved {
			t.Fatalf("valid created-directory chain rejected: result=%+v err=%v", result, err)
		}
	})
}

func TestWriteTransactionJournalReportsUnknownWhenFinalCannotBeVerified(t *testing.T) {
	harness := newWriteTransactionJournalHarness(t)
	operationID := strings.Repeat("c", 32)
	finalName := writeTransactionJournalName(
		operationID,
		WriteTransactionCheckpointPrepared,
		false,
	)
	originalHook := writeTransactionJournalFaultHook
	writeTransactionJournalFaultHook = func(point string) error {
		if point != "checkpoint:prepared:final_renamed" {
			return nil
		}
		if err := harness.internalRoot.Chmod(
			filepath.Join(writeTransactionJournalDir, finalName),
			0o644,
		); err != nil {
			return err
		}
		return errors.New("rename durability is unknown")
	}
	result, err := harness.journal.PublishPrepared(operationID, harness.plan)
	writeTransactionJournalFaultHook = originalHook
	if err == nil ||
		result.FinalObserved ||
		result.FinalObservation != WriteTransactionFinalUnknown {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestWriteTransactionJournalCommittedFinalRequiresDirectorySyncBarrier(t *testing.T) {
	harness := newWriteTransactionJournalHarness(t)
	operationID := strings.Repeat("c", 32)
	prepareWriteTransactionPredecessors(
		t,
		harness,
		operationID,
		WriteTransactionCheckpointCommitted,
	)

	persistentSyncErr := errors.New("persistent journal directory sync failure")
	failDirectorySync := false
	originalHook := writeTransactionJournalFaultHook
	originalSync := writeTransactionJournalDirectorySync
	t.Cleanup(func() {
		writeTransactionJournalFaultHook = originalHook
		writeTransactionJournalDirectorySync = originalSync
	})
	writeTransactionJournalDirectorySync = func(dir *os.File) error {
		if failDirectorySync {
			return persistentSyncErr
		}
		return originalSync(dir)
	}
	writeTransactionJournalFaultHook = func(point string) error {
		if point == "checkpoint:committed:directory_sync" {
			failDirectorySync = true
			return errors.New("injected pre-sync failure")
		}
		return nil
	}

	result, err := harness.journal.PublishCommitted(operationID)
	if err == nil || !errors.Is(err, persistentSyncErr) ||
		result.FinalObserved ||
		result.FinalObservation != WriteTransactionFinalUnknown {
		t.Fatalf("committed result=%+v err=%v", result, err)
	}
	if operations, scanErr := harness.journal.Scan(); scanErr == nil ||
		!errors.Is(scanErr, persistentSyncErr) || operations != nil {
		t.Fatalf("scan crossed failed sync barrier: operations=%+v err=%v", operations, scanErr)
	}

	failDirectorySync = false
	writeTransactionJournalFaultHook = originalHook
	operations, err := harness.journal.Scan()
	if err != nil || len(operations) != 1 ||
		operations[0].Decision != WriteTransactionDecisionRollforward ||
		operations[0].State != WriteTransactionStateCommitted {
		t.Fatalf("scan after durable retry: operations=%+v err=%v", operations, err)
	}
}

func TestWriteTransactionJournalCASExistingRequiresContentVerification(t *testing.T) {
	harness := newWriteTransactionJournalHarness(t)
	operationID := strings.Repeat("d", 32)
	configureVersionedOverwritePlan(harness, true)
	if result, err := harness.journal.PublishPrepared(operationID, harness.plan); err != nil ||
		!result.FinalObserved {
		t.Fatalf("prepare CAS: result=%+v err=%v", result, err)
	}
	outcome := harness.outcome
	outcome.CAS = WriteTransactionCASOutcome{
		Enabled:        true,
		VerifiedHash:   harness.plan.CAS.Hash,
		VerifiedSize:   harness.plan.CAS.Size,
		VerifiedBefore: true,
	}
	if result, err := harness.journal.PublishPublished(operationID, outcome); err != nil ||
		!result.FinalObserved {
		t.Fatalf("publish verified CAS: result=%+v err=%v", result, err)
	}

	other := newWriteTransactionJournalHarness(t)
	otherOperationID := strings.Repeat("e", 32)
	configureVersionedOverwritePlan(other, true)
	if result, err := other.journal.PublishPrepared(otherOperationID, other.plan); err != nil ||
		!result.FinalObserved {
		t.Fatalf("prepare second CAS: result=%+v err=%v", result, err)
	}
	badOutcome := other.outcome
	badOutcome.CAS = outcome.CAS
	badOutcome.CAS.Deduplicated = true
	if result, err := other.journal.PublishPublished(otherOperationID, badOutcome); err == nil || result.FinalObserved {
		t.Fatalf("unverified CAS semantics accepted: result=%+v err=%v", result, err)
	}
}

func configureVersionedOverwritePlan(
	harness *writeTransactionJournalHarness,
	existedBefore bool,
) {
	oldIdentity := strings.Repeat("e", 64)
	oldDeleteIdentity := strings.Repeat("f", 64)
	oldHash := strings.Repeat("1", 64)
	oldSize := int64(11)
	oldModTimeUnixNano := int64(3_000_000_456)
	harness.plan.Kind = WriteTransactionKindOverwrite
	harness.plan.Target.Before = &WriteTransactionObjectEvidence{
		RelativePath:       harness.plan.Target.RelativePath,
		PersistentIdentity: oldIdentity,
		DeleteIdentity:     oldDeleteIdentity,
		Mode:               uint32(0o640),
		Size:               oldSize,
		ModTimeUnixNano:    oldModTimeUnixNano,
		BLAKE3:             oldHash,
	}
	harness.plan.OldTarget = &WriteTransactionObjectExpectation{
		RelativePath:       harness.plan.Stages.Source,
		PersistentIdentity: oldIdentity,
		Mode:               uint32(0o640),
		Size:               oldSize,
		ModTimeUnixNano:    oldModTimeUnixNano,
		BLAKE3:             oldHash,
	}
	harness.plan.Metadata.IndexBefore = &versionstore.FileIndexRecord{
		Path:        harness.plan.Target.Path,
		Size:        oldSize,
		ModTimeUnix: 3,
		ContentHash: oldHash,
	}
	harness.plan.Metadata.VersionAfter = &versionstore.VersionRecord{
		Path:          harness.plan.Target.Path,
		Hash:          oldHash,
		Size:          oldSize,
		CreatedAtUnix: 4,
	}
	harness.plan.CAS = WriteTransactionCASPlan{
		Enabled:       true,
		Hash:          oldHash,
		Size:          oldSize,
		ExistedBefore: existedBefore,
		PutRequired:   !existedBefore,
	}
}

func (harness *writeTransactionJournalHarness) writeRaw(
	t *testing.T,
	name string,
	data []byte,
	flags int,
) {
	t.Helper()
	file, err := harness.internalRoot.OpenFile(
		filepath.Join(writeTransactionJournalDir, name),
		flags,
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 0 {
		written, writeErr := file.Write(data)
		if writeErr != nil || written != len(data) {
			_ = file.Close()
			t.Fatalf("write raw journal evidence: written=%d err=%v", written, writeErr)
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := harness.journal.journalDir.Sync(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTransactionJournalStrictJSONDecoderRejectsUnknownAndTrailing(t *testing.T) {
	harness := newWriteTransactionJournalHarness(t)
	operationID := strings.Repeat("f", 32)
	result, err := harness.journal.PublishPrepared(operationID, harness.plan)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result.Record)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WriteTransactionRecord
	if err := decodeStrictWriteTransactionRecord(append(data, []byte(` {}`)...), &decoded); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
		t.Fatalf("trailing decode error=%v", err)
	}
	unknown := append(data[:len(data)-1], []byte(`,"unknown":true}`)...)
	if err := decodeStrictWriteTransactionRecord(unknown, &decoded); !errors.Is(err, ErrWriteTransactionJournalCorrupt) {
		t.Fatalf("unknown field decode error=%v", err)
	}
}
