//go:build linux || darwin

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
)

type writeTransactionRecoveryStoreStub struct {
	state                  versionstore.WriteMetadataState
	objects                map[string][]byte
	references             map[string]bool
	calls                  []string
	rollbackAfterEffectErr error
	commitAfterEffectErr   error
	putAfterEffectErr      error
	referenceErr           error
	deleteAfterEffectErr   error
}

func newWriteTransactionRecoveryStoreStub(
	state versionstore.WriteMetadataState,
) *writeTransactionRecoveryStoreStub {
	return &writeTransactionRecoveryStoreStub{
		state:      state,
		objects:    make(map[string][]byte),
		references: make(map[string]bool),
	}
}

func (store *writeTransactionRecoveryStoreStub) InspectWriteMetadata(
	context.Context,
	versionstore.WriteMetadataPlan,
) (versionstore.WriteMetadataState, error) {
	store.calls = append(store.calls, "inspect")
	return store.state, nil
}

func (store *writeTransactionRecoveryStoreStub) RollbackWriteMetadata(
	context.Context,
	versionstore.WriteMetadataPlan,
) error {
	store.calls = append(store.calls, "rollback-metadata")
	if store.state == versionstore.WriteMetadataStateConflict {
		return versionstore.ErrWriteMetadataConflict
	}
	store.state = versionstore.WriteMetadataStateBefore
	if store.rollbackAfterEffectErr != nil {
		err := store.rollbackAfterEffectErr
		store.rollbackAfterEffectErr = nil
		return err
	}
	return nil
}

func (store *writeTransactionRecoveryStoreStub) EnsureWriteMetadataCommitted(
	context.Context,
	versionstore.WriteMetadataPlan,
) error {
	store.calls = append(store.calls, "commit-metadata")
	if store.state == versionstore.WriteMetadataStateConflict {
		return versionstore.ErrWriteMetadataConflict
	}
	store.state = versionstore.WriteMetadataStateAfter
	if store.commitAfterEffectErr != nil {
		err := store.commitAfterEffectErr
		store.commitAfterEffectErr = nil
		return err
	}
	return nil
}

func (store *writeTransactionRecoveryStoreStub) GetObject(
	_ context.Context,
	hash string,
) ([]byte, error) {
	store.calls = append(store.calls, "get-object")
	data, ok := store.objects[hash]
	if !ok {
		return nil, versionstore.ErrNotFound
	}
	return append([]byte(nil), data...), nil
}

func (store *writeTransactionRecoveryStoreStub) PutObjectExpected(
	_ context.Context,
	data []byte,
	expectedHash string,
) (versionstore.ObjectPutResult, error) {
	store.calls = append(store.calls, "put-object")
	if computeHash(data) != expectedHash {
		return versionstore.ObjectPutResult{}, errors.New("unexpected CAS content")
	}
	_, existed := store.objects[expectedHash]
	store.objects[expectedHash] = append([]byte(nil), data...)
	if store.putAfterEffectErr != nil {
		err := store.putAfterEffectErr
		store.putAfterEffectErr = nil
		return versionstore.ObjectPutResult{}, err
	}
	return versionstore.ObjectPutResult{
		Hash:         expectedHash,
		Size:         int64(len(data)),
		Deduplicated: existed,
	}, nil
}

func (store *writeTransactionRecoveryStoreStub) HasVersionReference(
	_ context.Context,
	hash string,
) (bool, error) {
	store.calls = append(store.calls, "has-reference")
	if store.referenceErr != nil {
		err := store.referenceErr
		store.referenceErr = nil
		return false, err
	}
	return store.references[hash], nil
}

func (store *writeTransactionRecoveryStoreStub) DeleteObject(
	_ context.Context,
	hash string,
) error {
	store.calls = append(store.calls, "delete-object")
	if _, ok := store.objects[hash]; !ok {
		return versionstore.ErrNotFound
	}
	delete(store.objects, hash)
	if store.deleteAfterEffectErr != nil {
		err := store.deleteAfterEffectErr
		store.deleteAfterEffectErr = nil
		return err
	}
	return nil
}

type writeTransactionRecoveryHarness struct {
	filesPath    string
	internalPath string
	trashPath    string
	filesRoot    *os.Root
	internalRoot *os.Root
	journal      *WriteTransactionJournal
	fs           *FileSystem
	plan         WriteTransactionPlan
	outcome      WriteTransactionPublishedOutcome
	operationID  string
	newData      []byte
	oldData      []byte
	closed       bool
}

func newWriteTransactionRecoveryHarness(
	t *testing.T,
	kind WriteTransactionKind,
) *writeTransactionRecoveryHarness {
	t.Helper()
	return newWriteTransactionRecoveryHarnessConfigured(t, kind, nil)
}

func newWriteTransactionRecoveryHarnessConfigured(
	t *testing.T,
	kind WriteTransactionKind,
	configure func(*writeTransactionRecoveryHarness),
) *writeTransactionRecoveryHarness {
	t.Helper()
	harness := &writeTransactionRecoveryHarness{
		filesPath:    t.TempDir(),
		internalPath: t.TempDir(),
		trashPath:    t.TempDir(),
		operationID:  strings.Repeat("7", 32),
		newData:      []byte("new durable content"),
		oldData:      []byte("old durable content"),
	}
	if err := os.Mkdir(filepath.Join(harness.internalPath, writeStagingDir), 0o700); err != nil {
		t.Fatal(err)
	}
	var err error
	harness.filesRoot, err = os.OpenRoot(harness.filesPath)
	if err != nil {
		t.Fatal(err)
	}
	harness.internalRoot, err = os.OpenRoot(harness.internalPath)
	if err != nil {
		t.Fatal(err)
	}
	harness.journal, err = OpenWriteTransactionJournal(harness.internalRoot)
	if err != nil {
		t.Fatal(err)
	}
	harness.fs = &FileSystem{
		filesRootHandle:    harness.filesRoot,
		internalRootHandle: harness.internalRoot,
		config: &Config{
			FilesRoot:    harness.filesPath,
			InternalRoot: harness.internalPath,
			TrashRoot:    harness.trashPath,
		},
	}
	t.Cleanup(func() { harness.close() })

	filesBinding := captureWriteTransactionTestDirectoryBinding(t, harness.filesRoot, ".")
	stagingBinding := captureWriteTransactionTestDirectoryBinding(
		t,
		harness.internalRoot,
		writeStagingDir,
	)
	newStage, newEvidence := createWriteTransactionRecoveryStage(
		t,
		harness,
		writeSourceStagePrefix,
		".tmp",
		"0000000000000001",
		harness.newData,
		0o600,
	)
	targetRel := "target.txt"
	after := writeTransactionExpectationFromEvidence(newEvidence, targetRel)
	harness.plan = WriteTransactionPlan{
		Kind: kind,
		Roots: WriteTransactionRootBindings{
			Files:    filesBinding,
			Internal: harness.journal.InternalRootBinding(),
			Staging:  stagingBinding,
			Journal:  harness.journal.JournalRootBinding(),
		},
		Target: WriteTransactionTargetEvidence{
			Path:                     "/target.txt",
			RelativePath:             targetRel,
			ParentRelativePath:       ".",
			ParentPersistentIdentity: filesBinding.PersistentIdentity,
			ParentMode:               filesBinding.Mode,
			After:                    after,
		},
		Source: newEvidence,
		Metadata: versionstore.WriteMetadataPlan{
			IndexAfter: versionstore.FileIndexRecord{
				Path:        "/target.txt",
				Size:        after.Size,
				ModTimeUnix: time.Unix(0, after.ModTimeUnixNano).Unix(),
				ContentHash: after.BLAKE3,
			},
		},
	}
	if kind == WriteTransactionKindCreate {
		harness.plan.Stages.Source = newStage
	} else {
		harness.plan.Stages.Source = newStage
		if err := os.WriteFile(
			filepath.Join(harness.filesPath, targetRel),
			harness.oldData,
			0o640,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(harness.filesPath, targetRel), 0o640); err != nil {
			t.Fatal(err)
		}
		oldEvidence := captureWriteTransactionRecoveryEvidence(
			t,
			harness.filesRoot,
			targetRel,
		)
		harness.plan.Target.Before = &oldEvidence
		oldExpectation := writeTransactionExpectationFromEvidence(oldEvidence, newStage)
		harness.plan.OldTarget = &oldExpectation
		harness.plan.Metadata.IndexBefore = &versionstore.FileIndexRecord{
			Path:        "/target.txt",
			Size:        oldEvidence.Size,
			ModTimeUnix: time.Unix(0, oldEvidence.ModTimeUnixNano).Unix(),
			ContentHash: oldEvidence.BLAKE3,
		}
	}
	if configure != nil {
		configure(harness)
	}
	if result, err := harness.journal.PublishPrepared(harness.operationID, harness.plan); err != nil ||
		!result.FinalObserved {
		t.Fatalf("PublishPrepared() result=%+v error=%v", result, err)
	}
	return harness
}

func (harness *writeTransactionRecoveryHarness) close() {
	if harness == nil || harness.closed {
		return
	}
	harness.closed = true
	if harness.journal != nil {
		_ = harness.journal.Close()
	}
	if harness.filesRoot != nil {
		_ = harness.filesRoot.Close()
	}
	if harness.internalRoot != nil {
		_ = harness.internalRoot.Close()
	}
}

func (harness *writeTransactionRecoveryHarness) publish(
	t *testing.T,
	committed bool,
	casOutcome WriteTransactionCASOutcome,
) {
	t.Helper()
	switch harness.plan.Kind {
	case WriteTransactionKindCreate:
		if err := rootio.RenameLeafBetweenRootsNoReplace(
			harness.internalRoot,
			harness.plan.Stages.Source,
			harness.filesRoot,
			harness.plan.Target.RelativePath,
		); err != nil {
			t.Fatal(err)
		}
	case WriteTransactionKindOverwrite:
		if err := rootio.ExchangeLeavesBetweenRoots(
			harness.internalRoot,
			harness.plan.Stages.Source,
			harness.filesRoot,
			harness.plan.Target.RelativePath,
		); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported kind %q", harness.plan.Kind)
	}
	target := captureWriteTransactionRecoveryEvidence(
		t,
		harness.filesRoot,
		harness.plan.Target.RelativePath,
	)
	harness.outcome = WriteTransactionPublishedOutcome{Target: target, CAS: casOutcome}
	if result, err := harness.journal.PublishPublished(
		harness.operationID,
		harness.outcome,
	); err != nil || !result.FinalObserved {
		t.Fatalf("PublishPublished() result=%+v error=%v", result, err)
	}
	if committed {
		if result, err := harness.journal.PublishCommitted(harness.operationID); err != nil ||
			!result.FinalObserved {
			t.Fatalf("PublishCommitted() result=%+v error=%v", result, err)
		}
	}
}

func createWriteTransactionRecoveryStage(
	t *testing.T,
	harness *writeTransactionRecoveryHarness,
	prefix string,
	extension string,
	suffix string,
	data []byte,
	mode os.FileMode,
) (string, WriteTransactionObjectEvidence) {
	t.Helper()
	rawRel := filepath.Join(writeStagingDir, "bootstrap-"+suffix)
	rawPath := filepath.Join(harness.internalPath, rawRel)
	if err := os.WriteFile(rawPath, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(rawPath, mode); err != nil {
		t.Fatal(err)
	}
	rawEvidence := captureWriteTransactionRecoveryEvidence(t, harness.internalRoot, rawRel)
	stageRel := testWriteTransactionStagePath(
		prefix,
		rawEvidence.PersistentIdentity,
		extension,
		suffix,
	)
	if err := os.Rename(rawPath, filepath.Join(harness.internalPath, stageRel)); err != nil {
		t.Fatal(err)
	}
	return stageRel, captureWriteTransactionRecoveryEvidence(t, harness.internalRoot, stageRel)
}

func captureWriteTransactionRecoveryEvidence(
	t *testing.T,
	root *os.Root,
	relativePath string,
) WriteTransactionObjectEvidence {
	t.Helper()
	observed, err := inspectWriteTransactionObject(context.Background(), root, relativePath)
	if err != nil {
		t.Fatal(err)
	}
	return WriteTransactionObjectEvidence{
		RelativePath:       relativePath,
		PersistentIdentity: observed.persistentIdentity,
		DeleteIdentity:     observed.deleteIdentity,
		Mode:               uint32(observed.mode),
		Size:               observed.size,
		ModTimeUnixNano:    observed.modTimeUnixNano,
		BLAKE3:             observed.hash,
	}
}

func writeTransactionExpectationFromEvidence(
	evidence WriteTransactionObjectEvidence,
	relativePath string,
) WriteTransactionObjectExpectation {
	return WriteTransactionObjectExpectation{
		RelativePath:       relativePath,
		PersistentIdentity: evidence.PersistentIdentity,
		Mode:               evidence.Mode,
		Size:               evidence.Size,
		ModTimeUnixNano:    evidence.ModTimeUnixNano,
		BLAKE3:             evidence.BLAKE3,
	}
}

func captureWriteTransactionTestDirectoryBinding(
	t *testing.T,
	root *os.Root,
	relativePath string,
) WriteTransactionRootBinding {
	t.Helper()
	dir, err := rootio.OpenDirNoFollow(root, relativePath)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	binding, err := CaptureWriteTransactionRootBinding(dir)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestWriteTransactionRecoveryCreateAndOverwriteDecisions(t *testing.T) {
	tests := []struct {
		name          string
		kind          WriteTransactionKind
		published     bool
		committed     bool
		metadataState versionstore.WriteMetadataState
		wantRollback  int
		wantForward   int
		wantContent   []byte
		wantAbsent    bool
	}{
		{
			name:          "create prepared rollback",
			kind:          WriteTransactionKindCreate,
			metadataState: versionstore.WriteMetadataStateBefore,
			wantRollback:  1,
			wantAbsent:    true,
		},
		{
			name:          "create published rollback",
			kind:          WriteTransactionKindCreate,
			published:     true,
			metadataState: versionstore.WriteMetadataStateAfter,
			wantRollback:  1,
			wantAbsent:    true,
		},
		{
			name:          "create committed rollforward",
			kind:          WriteTransactionKindCreate,
			published:     true,
			committed:     true,
			metadataState: versionstore.WriteMetadataStateBefore,
			wantForward:   1,
			wantContent:   []byte("new durable content"),
		},
		{
			name:          "overwrite prepared rollback",
			kind:          WriteTransactionKindOverwrite,
			metadataState: versionstore.WriteMetadataStateBefore,
			wantRollback:  1,
			wantContent:   []byte("old durable content"),
		},
		{
			name:          "overwrite published rollback",
			kind:          WriteTransactionKindOverwrite,
			published:     true,
			metadataState: versionstore.WriteMetadataStateAfter,
			wantRollback:  1,
			wantContent:   []byte("old durable content"),
		},
		{
			name:          "overwrite committed rollforward",
			kind:          WriteTransactionKindOverwrite,
			published:     true,
			committed:     true,
			metadataState: versionstore.WriteMetadataStateBefore,
			wantForward:   1,
			wantContent:   []byte("new durable content"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newWriteTransactionRecoveryHarness(t, test.kind)
			if test.published {
				harness.publish(t, test.committed, WriteTransactionCASOutcome{})
			}
			store := newWriteTransactionRecoveryStoreStub(test.metadataState)
			report, err := harness.fs.recoverWriteTransactionsWithStore(
				context.Background(),
				harness.journal,
				store,
			)
			if err != nil {
				t.Fatalf("recoverWriteTransactionsWithStore() error: %v", err)
			}
			if report.RolledBack != test.wantRollback ||
				report.RolledForward != test.wantForward ||
				len(report.Blocked) != 0 {
				t.Fatalf("recovery report = %+v", report)
			}
			targetPath := filepath.Join(harness.filesPath, harness.plan.Target.RelativePath)
			if test.wantAbsent {
				if _, err := os.Lstat(targetPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("target Lstat error = %v, want not exist", err)
				}
			} else {
				data, err := os.ReadFile(targetPath)
				if err != nil || string(data) != string(test.wantContent) {
					t.Fatalf("target content = %q, %v; want %q", data, err, test.wantContent)
				}
			}
			operations, err := harness.journal.Scan()
			if err != nil || len(operations) != 0 {
				t.Fatalf("journal Scan() = %+v, %v; want empty", operations, err)
			}
			for _, stagePath := range writeTransactionStagePaths(harness.plan.Stages) {
				if _, err := harness.internalRoot.Lstat(stagePath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("stage %q Lstat error = %v, want not exist", stagePath, err)
				}
			}
			second, err := harness.fs.recoverWriteTransactionsWithStore(
				context.Background(),
				harness.journal,
				store,
			)
			if err != nil || second.RolledBack != 0 || second.RolledForward != 0 ||
				len(second.Blocked) != 0 {
				t.Fatalf("second recovery = %+v, %v", second, err)
			}
		})
	}
}

func TestWriteTransactionRecoveryFailsClosedForMetadataConflictAndCancellation(t *testing.T) {
	t.Run("unknown metadata state", func(t *testing.T) {
		harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindCreate)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataState("unknown"))
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(err, ErrWriteRecoveryRequired) ||
			!errors.Is(err, versionstore.ErrWriteMetadataConflict) {
			t.Fatalf("recovery error = %v, want recovery-required metadata conflict", err)
		}
		if len(report.Blocked) != 1 || report.Blocked[0] != harness.operationID ||
			len(report.InspectionPaths) == 0 {
			t.Fatalf("blocked report = %+v", report)
		}
		if _, err := harness.internalRoot.Lstat(harness.plan.Stages.Source); err != nil {
			t.Fatalf("prepared stage changed after blocked recovery: %v", err)
		}
		operations, err := harness.journal.Scan()
		if err != nil || len(operations) != 1 {
			t.Fatalf("journal Scan() = %+v, %v; want retained operation", operations, err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindCreate)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		report, err := harness.fs.recoverWriteTransactionsWithStore(ctx, harness.journal, store)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("recovery error = %v, want context canceled", err)
		}
		if report.RolledBack != 0 || report.RolledForward != 0 || len(store.calls) != 0 {
			t.Fatalf("cancelled recovery report=%+v calls=%v", report, store.calls)
		}
		if _, err := harness.internalRoot.Lstat(harness.plan.Stages.Source); err != nil {
			t.Fatalf("prepared stage changed after cancellation: %v", err)
		}
	})
}

func newWriteTransactionRecoveryCASHarness(
	t *testing.T,
	existedBefore bool,
) *writeTransactionRecoveryHarness {
	t.Helper()
	return newWriteTransactionRecoveryHarnessConfigured(
		t,
		WriteTransactionKindOverwrite,
		func(harness *writeTransactionRecoveryHarness) {
			old := harness.plan.OldTarget
			if old == nil {
				t.Fatal("overwrite harness lacks old target")
			}
			harness.plan.CAS = WriteTransactionCASPlan{
				Enabled:       true,
				Hash:          old.BLAKE3,
				Size:          old.Size,
				ExistedBefore: existedBefore,
				PutRequired:   !existedBefore,
			}
			harness.plan.Metadata.VersionAfter = &versionstore.VersionRecord{
				Path:          harness.plan.Target.Path,
				Hash:          old.BLAKE3,
				Size:          old.Size,
				CreatedAtUnix: 10,
				Comment:       "write recovery test",
			}
		},
	)
}

func writeTransactionRecoveryCASOutcome(
	plan WriteTransactionCASPlan,
	kind string,
) WriteTransactionCASOutcome {
	outcome := WriteTransactionCASOutcome{
		Enabled:      true,
		VerifiedHash: plan.Hash,
		VerifiedSize: plan.Size,
	}
	switch kind {
	case "existing":
		outcome.VerifiedBefore = true
	case "created":
		outcome.PutAttempted = true
		outcome.PutObserved = true
		outcome.CreatedByOperation = true
	case "deduplicated":
		outcome.PutAttempted = true
		outcome.PutObserved = true
		outcome.Deduplicated = true
	default:
		panic("unsupported CAS outcome")
	}
	return outcome
}

func TestWriteTransactionRecoveryCASOwnershipAndRestoration(t *testing.T) {
	t.Run("committed existing CAS is verified without put", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCASHarness(t, true)
		harness.publish(
			t,
			true,
			writeTransactionRecoveryCASOutcome(harness.plan.CAS, "existing"),
		)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		store.objects[harness.plan.CAS.Hash] = append([]byte(nil), harness.oldData...)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if err != nil || report.RolledForward != 1 {
			t.Fatalf("recovery = %+v, %v", report, err)
		}
		for _, call := range store.calls {
			if call == "put-object" || call == "delete-object" {
				t.Fatalf("existing CAS unexpectedly mutated: calls=%v", store.calls)
			}
		}
	})

	t.Run("committed missing CAS is restored from old stage", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCASHarness(t, false)
		harness.publish(
			t,
			true,
			writeTransactionRecoveryCASOutcome(harness.plan.CAS, "created"),
		)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if err != nil || report.RolledForward != 1 {
			t.Fatalf("recovery = %+v, %v", report, err)
		}
		if data := store.objects[harness.plan.CAS.Hash]; string(data) != string(harness.oldData) {
			t.Fatalf("restored CAS content = %q, want %q", data, harness.oldData)
		}
		assertWriteTransactionRecoveryCallOrder(t, store.calls, "put-object", "commit-metadata")
	})

	t.Run("published operation-created CAS is deleted after metadata rollback", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCASHarness(t, false)
		harness.publish(
			t,
			false,
			writeTransactionRecoveryCASOutcome(harness.plan.CAS, "created"),
		)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
		store.objects[harness.plan.CAS.Hash] = append([]byte(nil), harness.oldData...)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if err != nil || report.RolledBack != 1 {
			t.Fatalf("recovery = %+v, %v", report, err)
		}
		if _, ok := store.objects[harness.plan.CAS.Hash]; ok {
			t.Fatal("operation-created unreferenced CAS object was retained")
		}
		assertWriteTransactionRecoveryCallOrder(t, store.calls, "rollback-metadata", "delete-object")
	})

	t.Run("prepared uncheckpointed CAS put is retained as orphan", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCASHarness(t, false)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		store.objects[harness.plan.CAS.Hash] = append([]byte(nil), harness.oldData...)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if err != nil || report.RolledBack != 1 {
			t.Fatalf("recovery = %+v, %v", report, err)
		}
		if data := store.objects[harness.plan.CAS.Hash]; string(data) != string(harness.oldData) {
			t.Fatalf("uncheckpointed CAS orphan = %q, want retained", data)
		}
		for _, call := range store.calls {
			if call == "delete-object" {
				t.Fatalf("prepared-only CAS was deleted: calls=%v", store.calls)
			}
		}
	})

	t.Run("referenced operation-created CAS is retained", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCASHarness(t, false)
		harness.publish(
			t,
			false,
			writeTransactionRecoveryCASOutcome(harness.plan.CAS, "created"),
		)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
		store.objects[harness.plan.CAS.Hash] = append([]byte(nil), harness.oldData...)
		store.references[harness.plan.CAS.Hash] = true
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if err != nil || report.RolledBack != 1 {
			t.Fatalf("recovery = %+v, %v", report, err)
		}
		if _, ok := store.objects[harness.plan.CAS.Hash]; !ok {
			t.Fatal("referenced CAS object was deleted")
		}
	})

	t.Run("deduplicated CAS is never deleted", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCASHarness(t, false)
		harness.publish(
			t,
			false,
			writeTransactionRecoveryCASOutcome(harness.plan.CAS, "deduplicated"),
		)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
		store.objects[harness.plan.CAS.Hash] = append([]byte(nil), harness.oldData...)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if err != nil || report.RolledBack != 1 {
			t.Fatalf("recovery = %+v, %v", report, err)
		}
		if _, ok := store.objects[harness.plan.CAS.Hash]; !ok {
			t.Fatal("deduplicated CAS object was deleted")
		}
	})
}

func TestWriteTransactionRecoveryRetriesMetadataAndCASAfterEffectErrors(t *testing.T) {
	t.Run("CAS put created object then returned error", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCASHarness(t, false)
		harness.publish(
			t,
			true,
			writeTransactionRecoveryCASOutcome(harness.plan.CAS, "created"),
		)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		store.putAfterEffectErr = errors.New("ambiguous CAS put")
		_, firstErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(firstErr, ErrWriteRecoveryRequired) {
			t.Fatalf("first recovery error = %v, want recovery required", firstErr)
		}
		if data := store.objects[harness.plan.CAS.Hash]; string(data) != string(harness.oldData) {
			t.Fatalf("after-effect CAS content = %q, want %q", data, harness.oldData)
		}
		if _, err := harness.internalRoot.Lstat(harness.plan.Stages.Source); err != nil {
			t.Fatalf("old stage removed after ambiguous CAS put: %v", err)
		}
		if operations, err := harness.journal.Scan(); err != nil || len(operations) != 1 {
			t.Fatalf("journal after ambiguous CAS put = %+v, %v", operations, err)
		}
		report, retryErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if retryErr != nil || report.RolledForward != 1 {
			t.Fatalf("recovery retry = %+v, %v", report, retryErr)
		}
	})

	t.Run("CAS delete removed object then returned error", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCASHarness(t, false)
		harness.publish(
			t,
			false,
			writeTransactionRecoveryCASOutcome(harness.plan.CAS, "created"),
		)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
		store.objects[harness.plan.CAS.Hash] = append([]byte(nil), harness.oldData...)
		store.deleteAfterEffectErr = errors.New("ambiguous CAS delete")
		_, firstErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(firstErr, ErrWriteRecoveryRequired) {
			t.Fatalf("first recovery error = %v, want recovery required", firstErr)
		}
		if _, ok := store.objects[harness.plan.CAS.Hash]; ok {
			t.Fatal("after-effect CAS delete did not remove object")
		}
		if _, err := harness.internalRoot.Lstat(harness.plan.Stages.Source); err != nil {
			t.Fatalf("new stage removed after ambiguous CAS delete: %v", err)
		}
		report, retryErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if retryErr != nil || report.RolledBack != 1 {
			t.Fatalf("recovery retry = %+v, %v", report, retryErr)
		}
	})

	t.Run("CAS reference inspection error retains object", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCASHarness(t, false)
		harness.publish(
			t,
			false,
			writeTransactionRecoveryCASOutcome(harness.plan.CAS, "created"),
		)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
		store.objects[harness.plan.CAS.Hash] = append([]byte(nil), harness.oldData...)
		store.referenceErr = errors.New("reference query failed")
		_, firstErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(firstErr, ErrWriteRecoveryRequired) {
			t.Fatalf("first recovery error = %v, want recovery required", firstErr)
		}
		if _, ok := store.objects[harness.plan.CAS.Hash]; !ok {
			t.Fatal("CAS object deleted after reference query error")
		}
		if operations, err := harness.journal.Scan(); err != nil || len(operations) != 1 {
			t.Fatalf("journal after reference query error = %+v, %v", operations, err)
		}
		report, retryErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if retryErr != nil || report.RolledBack != 1 {
			t.Fatalf("recovery retry = %+v, %v", report, retryErr)
		}
	})

	t.Run("metadata rollback changed state then returned unknown", func(t *testing.T) {
		harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindOverwrite)
		harness.publish(t, false, WriteTransactionCASOutcome{})
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
		store.rollbackAfterEffectErr = versionstore.ErrWriteMetadataOutcomeUnknown
		_, firstErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(firstErr, ErrWriteRecoveryRequired) ||
			!errors.Is(firstErr, versionstore.ErrWriteMetadataOutcomeUnknown) {
			t.Fatalf("first recovery error = %v, want metadata outcome unknown", firstErr)
		}
		if store.state != versionstore.WriteMetadataStateBefore {
			t.Fatalf("metadata state = %q, want after-effect before", store.state)
		}
		if _, err := harness.internalRoot.Lstat(harness.plan.Stages.Source); err != nil {
			t.Fatalf("stage removed after ambiguous metadata rollback: %v", err)
		}
		report, retryErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if retryErr != nil || report.RolledBack != 1 {
			t.Fatalf("recovery retry = %+v, %v", report, retryErr)
		}
	})

	t.Run("metadata commit changed state then returned unknown", func(t *testing.T) {
		harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindOverwrite)
		harness.publish(t, true, WriteTransactionCASOutcome{})
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		store.commitAfterEffectErr = versionstore.ErrWriteMetadataOutcomeUnknown
		_, firstErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(firstErr, ErrWriteRecoveryRequired) ||
			!errors.Is(firstErr, versionstore.ErrWriteMetadataOutcomeUnknown) {
			t.Fatalf("first recovery error = %v, want metadata outcome unknown", firstErr)
		}
		if store.state != versionstore.WriteMetadataStateAfter {
			t.Fatalf("metadata state = %q, want after-effect after", store.state)
		}
		if _, err := harness.internalRoot.Lstat(harness.plan.Stages.Source); err != nil {
			t.Fatalf("old stage removed after ambiguous metadata commit: %v", err)
		}
		report, retryErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if retryErr != nil || report.RolledForward != 1 {
			t.Fatalf("recovery retry = %+v, %v", report, retryErr)
		}
	})
}

func addPreparedWriteTransactionRecoveryCreate(
	t *testing.T,
	harness *writeTransactionRecoveryHarness,
	operationID string,
	targetRel string,
	data []byte,
	suffix string,
) WriteTransactionPlan {
	t.Helper()
	stage, evidence := createWriteTransactionRecoveryStage(
		t,
		harness,
		writeSourceStagePrefix,
		".tmp",
		suffix,
		data,
		0o600,
	)
	plan := harness.plan
	plan.Target.Path = "/" + targetRel
	plan.Target.RelativePath = targetRel
	plan.Target.After = writeTransactionExpectationFromEvidence(evidence, targetRel)
	plan.Source = evidence
	plan.Stages = WriteTransactionStagePlan{Source: stage}
	plan.Metadata = versionstore.WriteMetadataPlan{
		IndexAfter: versionstore.FileIndexRecord{
			Path:        "/" + targetRel,
			Size:        evidence.Size,
			ModTimeUnix: time.Unix(0, evidence.ModTimeUnixNano).Unix(),
			ContentHash: evidence.BLAKE3,
		},
	}
	if result, err := harness.journal.PublishPrepared(operationID, plan); err != nil ||
		!result.FinalObserved {
		t.Fatalf("PublishPrepared(%s) result=%+v error=%v", operationID, result, err)
	}
	return plan
}

func TestWriteTransactionRecoveryContinuesAfterIndependentOperationBlocks(t *testing.T) {
	harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindCreate)
	blockedID := strings.Repeat("1", 32)
	blockedPlan := addPreparedWriteTransactionRecoveryCreate(
		t,
		harness,
		blockedID,
		"blocked.txt",
		[]byte("blocked content"),
		"0000000000000003",
	)
	if err := os.WriteFile(
		filepath.Join(harness.internalPath, blockedPlan.Stages.Source),
		[]byte("attacker replacement"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
	report, err := harness.fs.recoverWriteTransactionsWithStore(
		context.Background(),
		harness.journal,
		store,
	)
	if !errors.Is(err, ErrWriteRecoveryRequired) {
		t.Fatalf("recovery error = %v, want recovery required", err)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 ||
		len(report.Blocked) != 1 || report.Blocked[0] != blockedID {
		t.Fatalf("recovery report = %+v", report)
	}
	operations, scanErr := harness.journal.Scan()
	if scanErr != nil || len(operations) != 1 || operations[0].OperationID != blockedID {
		t.Fatalf("remaining operations = %+v, %v; want only %s", operations, scanErr, blockedID)
	}
	if _, statErr := harness.internalRoot.Lstat(harness.plan.Stages.Source); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("independent valid stage Lstat error = %v, want cleaned", statErr)
	}
	if _, statErr := harness.internalRoot.Lstat(blockedPlan.Stages.Source); statErr != nil {
		t.Fatalf("blocked stage was removed: %v", statErr)
	}
}

func TestWriteTransactionRecoveryRetriesCrashBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		kind       WriteTransactionKind
		publish    bool
		committed  bool
		state      versionstore.WriteMetadataState
		faultPoint string
	}{
		{
			name:       "rename completed before parent sync",
			kind:       WriteTransactionKindCreate,
			publish:    true,
			state:      versionstore.WriteMetadataStateAfter,
			faultPoint: "namespace:after-rename",
		},
		{
			name:       "exchange completed before parent sync",
			kind:       WriteTransactionKindOverwrite,
			publish:    true,
			state:      versionstore.WriteMetadataStateAfter,
			faultPoint: "namespace:after-exchange",
		},
		{
			name:       "rename source parent synced before target parent",
			kind:       WriteTransactionKindCreate,
			publish:    true,
			state:      versionstore.WriteMetadataStateAfter,
			faultPoint: "namespace:after-source-parent-sync",
		},
		{
			name:       "exchange target parent synced before staging parent",
			kind:       WriteTransactionKindOverwrite,
			publish:    true,
			state:      versionstore.WriteMetadataStateAfter,
			faultPoint: "namespace:after-target-parent-sync",
		},
		{
			name:       "stage unlink completed before parent sync",
			kind:       WriteTransactionKindOverwrite,
			state:      versionstore.WriteMetadataStateBefore,
			faultPoint: "remove-stage:after-unlink:",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newWriteTransactionRecoveryHarness(t, test.kind)
			if test.publish {
				harness.publish(t, test.committed, WriteTransactionCASOutcome{})
			}
			store := newWriteTransactionRecoveryStoreStub(test.state)
			originalHook := writeTransactionRecoveryFaultHook
			triggered := false
			writeTransactionRecoveryFaultHook = func(point string) error {
				if !triggered &&
					(point == test.faultPoint ||
						strings.HasPrefix(point, test.faultPoint)) {
					triggered = true
					return errors.New("simulated process crash")
				}
				return nil
			}
			_, firstErr := harness.fs.recoverWriteTransactionsWithStore(
				context.Background(),
				harness.journal,
				store,
			)
			writeTransactionRecoveryFaultHook = originalHook
			if !errors.Is(firstErr, ErrWriteRecoveryRequired) || !triggered {
				t.Fatalf("first recovery error = %v, triggered=%v", firstErr, triggered)
			}
			operations, scanErr := harness.journal.Scan()
			if scanErr != nil || len(operations) != 1 {
				t.Fatalf("journal after crash = %+v, %v; want retained operation", operations, scanErr)
			}
			report, retryErr := harness.fs.recoverWriteTransactionsWithStore(
				context.Background(),
				harness.journal,
				store,
			)
			if retryErr != nil || report.RolledBack != 1 {
				t.Fatalf("recovery retry = %+v, %v", report, retryErr)
			}
			if operations, scanErr = harness.journal.Scan(); scanErr != nil || len(operations) != 0 {
				t.Fatalf("journal after retry = %+v, %v; want empty", operations, scanErr)
			}
		})
	}
}

func TestWriteTransactionRecoveryNamespaceSyncPriorityIsFailFast(t *testing.T) {
	tests := []struct {
		name             string
		kind             WriteTransactionKind
		firstParentLabel string
		secondHook       string
	}{
		{
			name:             "create rollback protects files before staging",
			kind:             WriteTransactionKindCreate,
			firstParentLabel: "source",
			secondHook:       "namespace:before-target-parent-sync",
		},
		{
			name:             "overwrite rollback protects canonical files first",
			kind:             WriteTransactionKindOverwrite,
			firstParentLabel: "target",
			secondHook:       "namespace:before-source-parent-sync",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newWriteTransactionRecoveryHarness(t, test.kind)
			harness.publish(t, false, WriteTransactionCASOutcome{})
			store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
			firstHook := "namespace:before-" + test.firstParentLabel + "-parent-sync"
			originalHook := writeTransactionRecoveryFaultHook
			var observed []string
			writeTransactionRecoveryFaultHook = func(point string) error {
				if strings.HasPrefix(point, "namespace:before-") &&
					strings.HasSuffix(point, "-parent-sync") {
					observed = append(observed, point)
				}
				if point == firstHook {
					return errors.New("first parent sync failed")
				}
				return nil
			}
			_, firstErr := harness.fs.recoverWriteTransactionsWithStore(
				context.Background(),
				harness.journal,
				store,
			)
			writeTransactionRecoveryFaultHook = originalHook
			if !errors.Is(firstErr, ErrWriteRecoveryRequired) {
				t.Fatalf("first recovery error = %v, want recovery required", firstErr)
			}
			if len(observed) != 1 || observed[0] != firstHook {
				t.Fatalf("sync hook order = %v, want only %q", observed, firstHook)
			}
			for _, point := range observed {
				if point == test.secondHook {
					t.Fatalf("second parent sync ran after first failure: %v", observed)
				}
			}
			report, retryErr := harness.fs.recoverWriteTransactionsWithStore(
				context.Background(),
				harness.journal,
				store,
			)
			if retryErr != nil || report.RolledBack != 1 {
				t.Fatalf("recovery retry = %+v, %v", report, retryErr)
			}
		})
	}
}

func TestWriteTransactionRecoveryRetriesJournalCleanupFault(t *testing.T) {
	harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindOverwrite)
	harness.publish(t, false, WriteTransactionCASOutcome{})
	store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
	originalHook := writeTransactionJournalFaultHook
	triggered := false
	writeTransactionJournalFaultHook = func(point string) error {
		if !triggered &&
			point == "checkpoint:cleanup:rollback:published_directory_sync" {
			triggered = true
			return errors.New("simulated journal directory sync crash")
		}
		return nil
	}
	_, firstErr := harness.fs.recoverWriteTransactionsWithStore(
		context.Background(),
		harness.journal,
		store,
	)
	writeTransactionJournalFaultHook = originalHook
	if !errors.Is(firstErr, ErrWriteRecoveryRequired) || !triggered {
		t.Fatalf("first recovery error = %v, triggered=%v", firstErr, triggered)
	}
	operations, scanErr := harness.journal.Scan()
	if scanErr != nil || len(operations) != 1 ||
		operations[0].State != WriteTransactionStatePrepared {
		t.Fatalf("journal suffix after fault = %+v, %v", operations, scanErr)
	}
	report, retryErr := harness.fs.recoverWriteTransactionsWithStore(
		context.Background(),
		harness.journal,
		store,
	)
	if retryErr != nil || report.RolledBack != 1 {
		t.Fatalf("recovery retry = %+v, %v", report, retryErr)
	}
}

func newWriteTransactionRecoveryCreatedDirectoryHarness(
	t *testing.T,
) *writeTransactionRecoveryHarness {
	t.Helper()
	return newWriteTransactionRecoveryHarnessConfigured(
		t,
		WriteTransactionKindCreate,
		func(harness *writeTransactionRecoveryHarness) {
			for _, relativePath := range []string{"base", "base/owned", "base/owned/deep"} {
				if err := os.Mkdir(
					filepath.Join(harness.filesPath, filepath.FromSlash(relativePath)),
					0o755,
				); err != nil {
					t.Fatal(err)
				}
			}
			base := captureWriteTransactionTestDirectoryBinding(t, harness.filesRoot, "base")
			owned := captureWriteTransactionTestDirectoryBinding(t, harness.filesRoot, "base/owned")
			deep := captureWriteTransactionTestDirectoryBinding(t, harness.filesRoot, "base/owned/deep")
			targetRel := "base/owned/deep/target.txt"
			harness.plan.Target.Path = "/" + targetRel
			harness.plan.Target.RelativePath = targetRel
			harness.plan.Target.ParentRelativePath = "base/owned/deep"
			harness.plan.Target.ParentPersistentIdentity = deep.PersistentIdentity
			harness.plan.Target.ParentMode = deep.Mode
			harness.plan.Target.After.RelativePath = targetRel
			harness.plan.Metadata.IndexAfter.Path = "/" + targetRel
			harness.plan.CreatedDirectories = []WriteTransactionCreatedDirectory{
				{
					Path:               "/base/owned/deep",
					RelativePath:       "base/owned/deep",
					PreAbsent:          true,
					PersistentIdentity: deep.PersistentIdentity,
					Mode:               deep.Mode,
				},
				{
					Path:               "/base/owned",
					RelativePath:       "base/owned",
					PreAbsent:          true,
					PersistentIdentity: owned.PersistentIdentity,
					Mode:               owned.Mode,
				},
			}
			harness.plan.CreatedDirectoryBase = &WriteTransactionCreatedDirectoryBase{
				RelativePath:       "base",
				PersistentIdentity: base.PersistentIdentity,
				Mode:               base.Mode,
			}
		},
	)
}

func TestWriteTransactionRecoveryCreatedDirectoryOwnership(t *testing.T) {
	t.Run("removes exact owned chain and retains base", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCreatedDirectoryHarness(t)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if err != nil || report.RolledBack != 1 {
			t.Fatalf("recovery = %+v, %v", report, err)
		}
		for _, relativePath := range []string{"base/owned/deep", "base/owned"} {
			if _, err := harness.filesRoot.Lstat(relativePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("owned directory %q Lstat error = %v, want not exist", relativePath, err)
			}
		}
		if info, err := harness.filesRoot.Lstat("base"); err != nil || !info.IsDir() {
			t.Fatalf("base directory = %+v, %v; want retained", info, err)
		}
	})

	t.Run("foreign child blocks cleanup and is retained", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCreatedDirectoryHarness(t)
		foreignPath := filepath.Join(harness.filesPath, "base/owned/deep/foreign.txt")
		if err := os.WriteFile(foreignPath, []byte("foreign"), 0o600); err != nil {
			t.Fatal(err)
		}
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(err, ErrWriteRecoveryRequired) ||
			len(report.Blocked) != 1 || report.Blocked[0] != harness.operationID {
			t.Fatalf("blocked recovery = %+v, %v", report, err)
		}
		if data, err := os.ReadFile(foreignPath); err != nil || string(data) != "foreign" {
			t.Fatalf("foreign file = %q, %v; want retained", data, err)
		}
		if operations, scanErr := harness.journal.Scan(); scanErr != nil || len(operations) != 1 {
			t.Fatalf("journal after blocked cleanup = %+v, %v", operations, scanErr)
		}
	})

	t.Run("missing unowned base blocks journal cleanup", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCreatedDirectoryHarness(t)
		if err := os.Remove(filepath.Join(harness.internalPath, harness.plan.Stages.Source)); err != nil {
			t.Fatal(err)
		}
		for _, relativePath := range []string{
			"base/owned/deep",
			"base/owned",
			"base",
		} {
			if err := os.Remove(filepath.Join(harness.filesPath, relativePath)); err != nil {
				t.Fatal(err)
			}
		}
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(err, ErrWriteRecoveryRequired) ||
			len(report.Blocked) != 1 || report.Blocked[0] != harness.operationID {
			t.Fatalf("blocked recovery = %+v, %v", report, err)
		}
		if operations, scanErr := harness.journal.Scan(); scanErr != nil || len(operations) != 1 {
			t.Fatalf("journal after missing base = %+v, %v", operations, scanErr)
		}
	})

	t.Run("same-mode replacement base blocks journal cleanup", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCreatedDirectoryHarness(t)
		if err := os.Remove(filepath.Join(harness.internalPath, harness.plan.Stages.Source)); err != nil {
			t.Fatal(err)
		}
		for _, relativePath := range []string{"base/owned/deep", "base/owned"} {
			if err := os.Remove(filepath.Join(harness.filesPath, relativePath)); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Rename(
			filepath.Join(harness.filesPath, "base"),
			filepath.Join(harness.filesPath, "original-base"),
		); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(harness.filesPath, "base"), 0o755); err != nil {
			t.Fatal(err)
		}
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(err, ErrWriteRecoveryRequired) ||
			len(report.Blocked) != 1 || report.Blocked[0] != harness.operationID {
			t.Fatalf("blocked recovery = %+v, %v", report, err)
		}
		if info, statErr := harness.filesRoot.Lstat("base"); statErr != nil || !info.IsDir() {
			t.Fatalf("replacement base = %+v, %v; want retained", info, statErr)
		}
		if operations, scanErr := harness.journal.Scan(); scanErr != nil || len(operations) != 1 {
			t.Fatalf("journal after replacement base = %+v, %v", operations, scanErr)
		}
	})

	t.Run("replacement base with exact owned chain blocks before mutation", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCreatedDirectoryHarness(t)
		if err := os.Rename(
			filepath.Join(harness.filesPath, "base"),
			filepath.Join(harness.filesPath, "original-base"),
		); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(harness.filesPath, "base"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(
			filepath.Join(harness.filesPath, "original-base/owned"),
			filepath.Join(harness.filesPath, "base/owned"),
		); err != nil {
			t.Fatal(err)
		}
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(err, ErrWriteRecoveryRequired) ||
			len(report.Blocked) != 1 || report.Blocked[0] != harness.operationID {
			t.Fatalf("blocked recovery = %+v, %v", report, err)
		}
		if _, err := harness.internalRoot.Lstat(harness.plan.Stages.Source); err != nil {
			t.Fatalf("stage changed before base mismatch block: %v", err)
		}
		for _, relativePath := range []string{"base/owned", "base/owned/deep"} {
			if info, err := harness.filesRoot.Lstat(relativePath); err != nil || !info.IsDir() {
				t.Fatalf("owned directory %q = %+v, %v; want unchanged", relativePath, info, err)
			}
		}
		if operations, scanErr := harness.journal.Scan(); scanErr != nil || len(operations) != 1 {
			t.Fatalf("journal after base mismatch = %+v, %v", operations, scanErr)
		}
	})

	t.Run("directory unlink before parent sync is retryable", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCreatedDirectoryHarness(t)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		originalHook := writeTransactionRecoveryFaultHook
		triggered := false
		writeTransactionRecoveryFaultHook = func(point string) error {
			if !triggered &&
				point == "remove-created-directory:after-unlink:base/owned/deep" {
				triggered = true
				return errors.New("simulated process crash")
			}
			return nil
		}
		_, firstErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		writeTransactionRecoveryFaultHook = originalHook
		if !errors.Is(firstErr, ErrWriteRecoveryRequired) || !triggered {
			t.Fatalf("first recovery error = %v, triggered=%v", firstErr, triggered)
		}
		if _, err := harness.filesRoot.Lstat("base/owned/deep"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deepest directory Lstat error = %v, want removed", err)
		}
		report, retryErr := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if retryErr != nil || report.RolledBack != 1 {
			t.Fatalf("recovery retry = %+v, %v", report, retryErr)
		}
	})
}

func TestWriteTransactionRecoveryRejectsFilesystemAttacks(t *testing.T) {
	t.Run("stage symlink", func(t *testing.T) {
		harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindCreate)
		outsidePath := filepath.Join(t.TempDir(), "outside.txt")
		if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		stagePath := filepath.Join(harness.internalPath, harness.plan.Stages.Source)
		if err := os.Remove(stagePath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsidePath, stagePath); err != nil {
			t.Fatal(err)
		}
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(err, ErrWriteRecoveryRequired) || len(report.Blocked) != 1 {
			t.Fatalf("blocked recovery = %+v, %v", report, err)
		}
		if data, err := os.ReadFile(outsidePath); err != nil || string(data) != "outside" {
			t.Fatalf("outside content = %q, %v; want unchanged", data, err)
		}
		if info, err := os.Lstat(stagePath); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("stage symlink = %+v, %v; want retained", info, err)
		}
	})

	t.Run("canonical same-content inode replacement", func(t *testing.T) {
		harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindCreate)
		harness.publish(t, true, WriteTransactionCASOutcome{})
		targetPath := filepath.Join(harness.filesPath, harness.plan.Target.RelativePath)
		originalPath := filepath.Join(harness.filesPath, "original-target.txt")
		if err := os.Rename(targetPath, originalPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(targetPath, harness.newData, 0o600); err != nil {
			t.Fatal(err)
		}
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(err, ErrWriteRecoveryRequired) || len(report.Blocked) != 1 {
			t.Fatalf("blocked recovery = %+v, %v", report, err)
		}
		if data, err := os.ReadFile(targetPath); err != nil || string(data) != string(harness.newData) {
			t.Fatalf("replacement target = %q, %v; want retained", data, err)
		}
		if data, err := os.ReadFile(originalPath); err != nil || string(data) != string(harness.newData) {
			t.Fatalf("original target = %q, %v; want retained", data, err)
		}
	})

	t.Run("private staging mode drift", func(t *testing.T) {
		harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindCreate)
		stagingPath := filepath.Join(harness.internalPath, writeStagingDir)
		if err := os.Chmod(stagingPath, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(stagingPath, 0o700) })
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(err, ErrWriteRecoveryRequired) ||
			len(report.Blocked) != 1 || report.Blocked[0] != "journal" {
			t.Fatalf("blocked recovery = %+v, %v", report, err)
		}
		if _, err := harness.internalRoot.Lstat(harness.plan.Stages.Source); err != nil {
			t.Fatalf("stage changed after private-root drift: %v", err)
		}
	})

	t.Run("create no-replace retains injected destination", func(t *testing.T) {
		harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindCreate)
		harness.publish(t, false, WriteTransactionCASOutcome{})
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
		originalHook := writeTransactionRecoveryFaultHook
		injected := false
		writeTransactionRecoveryFaultHook = func(point string) error {
			if point == "namespace:before-rename" && !injected {
				injected = true
				return os.WriteFile(
					filepath.Join(harness.internalPath, harness.plan.Stages.Source),
					[]byte("attacker"),
					0o600,
				)
			}
			return nil
		}
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		writeTransactionRecoveryFaultHook = originalHook
		if !errors.Is(err, ErrWriteRecoveryRequired) || !injected || len(report.Blocked) != 1 {
			t.Fatalf("blocked recovery = %+v, %v; injected=%v", report, err, injected)
		}
		if data, err := os.ReadFile(
			filepath.Join(harness.internalPath, harness.plan.Stages.Source),
		); err != nil || string(data) != "attacker" {
			t.Fatalf("injected destination = %q, %v; want retained", data, err)
		}
	})

	t.Run("exchange postcheck retains unknown swapped object", func(t *testing.T) {
		harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindOverwrite)
		harness.publish(t, false, WriteTransactionCASOutcome{})
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateAfter)
		originalHook := writeTransactionRecoveryFaultHook
		injected := false
		writeTransactionRecoveryFaultHook = func(point string) error {
			if point != "namespace:before-exchange" || injected {
				return nil
			}
			injected = true
			targetPath := filepath.Join(harness.filesPath, harness.plan.Target.RelativePath)
			if err := os.Rename(
				targetPath,
				filepath.Join(harness.filesPath, "attacked-original-new.txt"),
			); err != nil {
				return err
			}
			return os.WriteFile(targetPath, []byte("attacker"), 0o600)
		}
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		writeTransactionRecoveryFaultHook = originalHook
		if !errors.Is(err, ErrWriteRecoveryRequired) || !injected || len(report.Blocked) != 1 {
			t.Fatalf("blocked recovery = %+v, %v; injected=%v", report, err, injected)
		}
		if data, err := os.ReadFile(
			filepath.Join(harness.internalPath, harness.plan.Stages.Source),
		); err != nil || string(data) != "attacker" {
			t.Fatalf("unknown swapped stage = %q, %v; want retained", data, err)
		}
		if operations, scanErr := harness.journal.Scan(); scanErr != nil || len(operations) != 1 {
			t.Fatalf("journal after exchange attack = %+v, %v", operations, scanErr)
		}
	})

	t.Run("wrong CAS content", func(t *testing.T) {
		harness := newWriteTransactionRecoveryCASHarness(t, true)
		harness.publish(
			t,
			true,
			writeTransactionRecoveryCASOutcome(harness.plan.CAS, "existing"),
		)
		store := newWriteTransactionRecoveryStoreStub(versionstore.WriteMetadataStateBefore)
		store.objects[harness.plan.CAS.Hash] = []byte("wrong")
		report, err := harness.fs.recoverWriteTransactionsWithStore(
			context.Background(),
			harness.journal,
			store,
		)
		if !errors.Is(err, ErrWriteRecoveryRequired) || len(report.Blocked) != 1 {
			t.Fatalf("blocked recovery = %+v, %v", report, err)
		}
		if operations, scanErr := harness.journal.Scan(); scanErr != nil || len(operations) != 1 {
			t.Fatalf("journal after CAS attack = %+v, %v", operations, scanErr)
		}
	})
}

func TestNewRecoversPreparedCreateWriteTransaction(t *testing.T) {
	harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindCreate)
	plan := harness.plan
	config := writeTransactionRecoveryTestConfig(harness)
	harness.close()

	fs, err := New(config)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer fs.Close()
	if _, err := os.Lstat(filepath.Join(harness.filesPath, plan.Target.RelativePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target Lstat error = %v, want rolled back", err)
	}
	if _, err := os.Lstat(filepath.Join(harness.internalPath, plan.Stages.Source)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage Lstat error = %v, want cleaned", err)
	}
	operations, err := fs.writeTransactionJournal.Scan()
	if err != nil || len(operations) != 0 {
		t.Fatalf("startup journal Scan() = %+v, %v; want empty", operations, err)
	}
	state, err := fs.versions.InspectWriteMetadata(context.Background(), plan.Metadata)
	if err != nil || (state != versionstore.WriteMetadataStateBefore &&
		state != versionstore.WriteMetadataStateBoth) {
		t.Fatalf("startup metadata state = %q, %v; want before", state, err)
	}
}

func TestNewRecoversCommittedOverwriteWriteTransaction(t *testing.T) {
	harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindOverwrite)
	harness.publish(t, true, WriteTransactionCASOutcome{})
	plan := harness.plan
	config := writeTransactionRecoveryTestConfig(harness)
	before := plan.Metadata.IndexBefore
	if before == nil {
		t.Fatal("overwrite plan lacks before metadata")
	}
	store, err := versionstore.New(versionstore.Config{
		DBRoot:    harness.internalRoot,
		DBName:    "index.db",
		Dataplane: dataplane.NewClient("unused"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateFileIndex(
		context.Background(),
		before.Path,
		before.Size,
		time.Unix(before.ModTimeUnix, 0),
		before.ContentHash,
	); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	harness.close()

	fs, err := New(config)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer fs.Close()
	data, err := os.ReadFile(filepath.Join(harness.filesPath, plan.Target.RelativePath))
	if err != nil || string(data) != string(harness.newData) {
		t.Fatalf("target after startup recovery = %q, %v; want %q", data, err, harness.newData)
	}
	if _, err := os.Lstat(filepath.Join(harness.internalPath, plan.Stages.Source)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old stage Lstat error = %v, want cleaned", err)
	}
	operations, err := fs.writeTransactionJournal.Scan()
	if err != nil || len(operations) != 0 {
		t.Fatalf("startup journal Scan() = %+v, %v; want empty", operations, err)
	}
	state, err := fs.versions.InspectWriteMetadata(context.Background(), plan.Metadata)
	if err != nil || (state != versionstore.WriteMetadataStateAfter &&
		state != versionstore.WriteMetadataStateBoth) {
		t.Fatalf("startup metadata state = %q, %v; want after", state, err)
	}
}

func TestNewFailsClosedForCorruptSQLiteWithPendingWriteTransaction(t *testing.T) {
	harness := newWriteTransactionRecoveryHarness(t, WriteTransactionKindCreate)
	plan := harness.plan
	config := writeTransactionRecoveryTestConfig(harness)
	harness.close()
	if err := os.WriteFile(
		filepath.Join(harness.internalPath, "index.db"),
		[]byte("not a sqlite database"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	fs, err := New(config)
	if fs != nil {
		_ = fs.Close()
		t.Fatal("New() returned a filesystem after metadata corruption")
	}
	if !errors.Is(err, ErrWriteRecoveryRequired) {
		t.Fatalf("New() error = %v, want ErrWriteRecoveryRequired", err)
	}
	if _, err := os.Lstat(filepath.Join(harness.internalPath, plan.Stages.Source)); err != nil {
		t.Fatalf("pending stage changed after fail-closed startup: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(harness.filesPath, plan.Target.RelativePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target Lstat error = %v, want startup recovery not run", err)
	}
	internalRoot, err := os.OpenRoot(harness.internalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer internalRoot.Close()
	journal, err := OpenWriteTransactionJournal(internalRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	operations, err := journal.Scan()
	if err != nil || len(operations) != 1 || operations[0].OperationID != harness.operationID {
		t.Fatalf("pending journal after fail-closed startup = %+v, %v", operations, err)
	}
}

func assertWriteTransactionRecoveryCallOrder(
	t *testing.T,
	calls []string,
	first string,
	second string,
) {
	t.Helper()
	firstIndex := -1
	secondIndex := -1
	for index, call := range calls {
		if call == first && firstIndex == -1 {
			firstIndex = index
		}
		if call == second && secondIndex == -1 {
			secondIndex = index
		}
	}
	if firstIndex == -1 || secondIndex == -1 || firstIndex >= secondIndex {
		t.Fatalf("calls = %v; want %q before %q", calls, first, second)
	}
}

func writeTransactionRecoveryTestConfig(harness *writeTransactionRecoveryHarness) *Config {
	return &Config{
		FilesRoot:    harness.filesPath,
		InternalRoot: harness.internalPath,
		TrashRoot:    harness.trashPath,
		Dataplane:    dataplane.NewClient("unused"),
	}
}

func (harness *writeTransactionRecoveryHarness) String() string {
	return fmt.Sprintf("%s:%s", harness.plan.Kind, harness.operationID)
}
