//go:build linux || darwin

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

type writeTransactionRuntimeReaderFunc func([]byte) (int, error)

func (reader writeTransactionRuntimeReaderFunc) Read(buffer []byte) (int, error) {
	return reader(buffer)
}

func runWriteTransactionRuntimeTest(
	t *testing.T,
	ctx context.Context,
	fs *FileSystem,
	name string,
	content string,
	options writeFileTransactionOptions,
	store writeTransactionRuntimeStore,
	afterStage func(),
) (*stagedWriteFile, string, error) {
	t.Helper()
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil {
		store = newWriteTransactionRuntimeTestStore(fs)
	}
	normalizedName, err := normalizeStorageWorkspacePath(name)
	if err != nil {
		t.Fatal(err)
	}
	fs.closeMu.RLock()
	defer fs.closeMu.RUnlock()
	fs.writeStagingMu.RLock()
	defer fs.writeStagingMu.RUnlock()

	targetSnapshot, err := fs.captureWriteTarget(ctx, normalizedName)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWriteFileCondition(targetSnapshot, options.condition); err != nil {
		t.Fatal(err)
	}
	source, err := fs.stageWriteReader(
		ctx,
		strings.NewReader(content),
		defaultMaxWriteSize,
		writeSourceStagePrefix,
	)
	if err != nil {
		t.Fatal(err)
	}
	stagePath := source.rel
	if afterStage != nil {
		afterStage()
	}
	runtimeErr := fs.runWriteTransactionRuntimeLockedWithStore(
		ctx,
		normalizedName,
		source,
		options,
		targetSnapshot,
		store,
	)
	return source, stagePath, errors.Join(runtimeErr, source.discard())
}

func assertWriteTransactionRuntimeFileContent(
	t *testing.T,
	fs *FileSystem,
	name string,
	want string,
) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(
		fs.config.FilesRoot,
		filepath.FromSlash(strings.TrimPrefix(name, "/")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("content=%q, want %q", data, want)
	}
}

func assertWriteTransactionRuntimeTargetAbsent(
	t *testing.T,
	fs *FileSystem,
	name string,
) {
	t.Helper()
	_, err := os.Lstat(filepath.Join(
		fs.config.FilesRoot,
		filepath.FromSlash(strings.TrimPrefix(name, "/")),
	))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target absence error=%v", err)
	}
}

func assertWriteTransactionRuntimeJournalEmpty(t *testing.T, fs *FileSystem) {
	t.Helper()
	operations, err := fs.writeTransactionJournal.Scan()
	if err != nil || len(operations) != 0 {
		t.Fatalf("journal operations=%+v err=%v", operations, err)
	}
}

func assertWriteTransactionRuntimeStageAbsent(
	t *testing.T,
	fs *FileSystem,
	stagePath string,
) {
	t.Helper()
	if _, err := fs.internalRootHandle.Lstat(stagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage %q absence error=%v", stagePath, err)
	}
}

func TestWriteTransactionRuntimeCreateAndOverwrite(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fs := setupStandaloneFileSystem(t)
		source, stagePath, err := runWriteTransactionRuntimeTest(
			t,
			context.Background(),
			fs,
			"/created.txt",
			"created content",
			writeFileTransactionOptions{},
			nil,
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		if source.rel != "" || source.reservation != nil || source.retained {
			t.Fatalf("source was not released: %+v", source)
		}
		assertWriteTransactionRuntimeFileContent(t, fs, "/created.txt", "created content")
		assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
		assertWriteTransactionRuntimeJournalEmpty(t, fs)
		size, _, hash, err := fs.versions.GetFileIndex(context.Background(), "/created.txt")
		if err != nil || size != int64(len("created content")) ||
			hash != computeHash([]byte("created content")) {
			t.Fatalf("index size=%d hash=%q err=%v", size, hash, err)
		}
	})

	t.Run("overwrite with forced version", func(t *testing.T) {
		fs := setupStandaloneFileSystem(t)
		targetPath := filepath.Join(fs.config.FilesRoot, "overwritten.txt")
		if err := os.WriteFile(targetPath, []byte("old content"), 0o640); err != nil {
			t.Fatal(err)
		}
		store := newWriteTransactionRuntimeTestStore(fs)
		source, stagePath, err := runWriteTransactionRuntimeTest(
			t,
			context.Background(),
			fs,
			"/overwritten.txt",
			"new content",
			writeFileTransactionOptions{
				forceVersion:   true,
				versionComment: "runtime test",
			},
			store,
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		if source.rel != "" || source.reservation != nil || source.retained {
			t.Fatalf("source was not released: %+v", source)
		}
		assertWriteTransactionRuntimeFileContent(t, fs, "/overwritten.txt", "new content")
		assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
		assertWriteTransactionRuntimeJournalEmpty(t, fs)

		oldHash := computeHash([]byte("old content"))
		versions, err := fs.versions.GetVersions(context.Background(), "/overwritten.txt")
		if err != nil || len(versions) != 1 ||
			versions[0].Hash != oldHash ||
			versions[0].Comment != "runtime test" {
			t.Fatalf("versions=%+v err=%v", versions, err)
		}
		object, err := store.GetObject(context.Background(), oldHash)
		if err != nil || string(object) != "old content" {
			t.Fatalf("CAS object=%q err=%v", object, err)
		}
	})
}

func TestWriteTransactionRuntimeAppliesRetentionAfterCommittedRecovery(t *testing.T) {
	t.Run("enforces max versions", func(t *testing.T) {
		fs := setupStandaloneFileSystem(t)
		fs.config.MaxVersions = 1
		fs.config.MaxVersionAge = 0
		store := newWriteTransactionRuntimeTestStore(fs)
		fs.deleteVersionObject = store.DeleteObject

		for _, content := range []string{"v1", "v2", "v3"} {
			if _, _, err := runWriteTransactionRuntimeTest(
				t,
				context.Background(),
				fs,
				"/retained.txt",
				content,
				writeFileTransactionOptions{forceVersion: true},
				store,
				nil,
			); err != nil {
				t.Fatalf("write %q: %v", content, err)
			}
		}
		versions, err := fs.versions.GetVersions(context.Background(), "/retained.txt")
		if err != nil || len(versions) != 1 ||
			versions[0].Hash != computeHash([]byte("v2")) {
			t.Fatalf("versions=%+v err=%v", versions, err)
		}
		if _, err := store.GetObject(
			context.Background(),
			computeHash([]byte("v1")),
		); !errors.Is(err, versionstore.ErrNotFound) {
			t.Fatalf("expired v1 object error=%v", err)
		}
		assertWriteTransactionRuntimeFileContent(t, fs, "/retained.txt", "v3")
		assertWriteTransactionRuntimeJournalEmpty(t, fs)
	})

	t.Run("cleanup failure does not fail committed write", func(t *testing.T) {
		fs := setupStandaloneFileSystem(t)
		fs.config.MaxVersions = 1
		fs.config.MaxVersionAge = 0
		store := newWriteTransactionRuntimeTestStore(fs)
		fs.deleteVersionObject = store.DeleteObject

		for _, content := range []string{"v1", "v2"} {
			if _, _, err := runWriteTransactionRuntimeTest(
				t,
				context.Background(),
				fs,
				"/retention-warning.txt",
				content,
				writeFileTransactionOptions{forceVersion: true},
				store,
				nil,
			); err != nil {
				t.Fatalf("write %q: %v", content, err)
			}
		}
		store.deleteObjectErr = errors.New("retention object deletion failed")
		if _, _, err := runWriteTransactionRuntimeTest(
			t,
			context.Background(),
			fs,
			"/retention-warning.txt",
			"v3",
			writeFileTransactionOptions{forceVersion: true},
			store,
			nil,
		); err != nil {
			t.Fatalf("committed write failed on best-effort retention: %v", err)
		}
		versions, err := fs.versions.GetVersions(
			context.Background(),
			"/retention-warning.txt",
		)
		if err != nil || len(versions) != 2 {
			t.Fatalf("restored extra versions=%+v err=%v", versions, err)
		}
		assertWriteTransactionRuntimeFileContent(t, fs, "/retention-warning.txt", "v3")
		assertWriteTransactionRuntimeJournalEmpty(t, fs)
	})
}

func TestWriteTransactionRuntimeRevalidatesPreBodyTargetSnapshot(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	targetPath := filepath.Join(fs.config.FilesRoot, "conflict.txt")
	if err := os.WriteFile(targetPath, []byte("before body"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stagePath, err := runWriteTransactionRuntimeTest(
		t,
		context.Background(),
		fs,
		"/conflict.txt",
		"request body",
		writeFileTransactionOptions{},
		nil,
		func() {
			if writeErr := os.WriteFile(targetPath, []byte("external update"), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	)
	if !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("runtime error=%v, want ErrWriteConflict", err)
	}
	assertWriteTransactionRuntimeFileContent(t, fs, "/conflict.txt", "external update")
	assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
	assertWriteTransactionRuntimeJournalEmpty(t, fs)
}

func TestWriteTransactionRuntimeCancellationBoundary(t *testing.T) {
	t.Run("after prepared rolls back", func(t *testing.T) {
		fs := setupStandaloneFileSystem(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		originalHook := writeTransactionRuntimeFaultHook
		t.Cleanup(func() { writeTransactionRuntimeFaultHook = originalHook })
		writeTransactionRuntimeFaultHook = func(point string) error {
			if point == "after-prepared" {
				cancel()
			}
			return nil
		}
		_, stagePath, err := runWriteTransactionRuntimeTest(
			t,
			ctx,
			fs,
			"/cancel-before-visible.txt",
			"new content",
			writeFileTransactionOptions{},
			nil,
			nil,
		)
		if !errors.Is(err, context.Canceled) || isVisibleMutationWarning(err) {
			t.Fatalf("runtime error=%v", err)
		}
		assertWriteTransactionRuntimeTargetAbsent(t, fs, "/cancel-before-visible.txt")
		assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
		assertWriteTransactionRuntimeJournalEmpty(t, fs)
	})

	t.Run("after visible publish finishes detached commit", func(t *testing.T) {
		fs := setupStandaloneFileSystem(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		originalHook := writeTransactionRuntimeFaultHook
		t.Cleanup(func() { writeTransactionRuntimeFaultHook = originalHook })
		writeTransactionRuntimeFaultHook = func(point string) error {
			if point == "after-visible-publish" {
				cancel()
			}
			return nil
		}
		_, stagePath, err := runWriteTransactionRuntimeTest(
			t,
			ctx,
			fs,
			"/cancel-after-visible.txt",
			"committed content",
			writeFileTransactionOptions{},
			nil,
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		assertWriteTransactionRuntimeFileContent(
			t,
			fs,
			"/cancel-after-visible.txt",
			"committed content",
		)
		assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
		assertWriteTransactionRuntimeJournalEmpty(t, fs)
	})
}

func TestWriteTransactionRuntimeFaultDecisionMatrix(t *testing.T) {
	for _, testCase := range []struct {
		point     string
		committed bool
	}{
		{point: "after-prepared"},
		{point: "before-visible-publish"},
		{point: "after-visible-publish"},
		{point: "after-cas"},
		{point: "after-published"},
		{point: "after-metadata"},
		{point: "after-committed", committed: true},
	} {
		t.Run(testCase.point, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			originalHook := writeTransactionRuntimeFaultHook
			t.Cleanup(func() { writeTransactionRuntimeFaultHook = originalHook })
			writeTransactionRuntimeFaultHook = func(point string) error {
				if point == testCase.point {
					return fmt.Errorf("injected at %s", point)
				}
				return nil
			}
			_, stagePath, err := runWriteTransactionRuntimeTest(
				t,
				context.Background(),
				fs,
				"/fault.txt",
				"new content",
				writeFileTransactionOptions{},
				nil,
				nil,
			)
			if !errors.Is(err, errWriteTransactionRuntimeFaultInjected) {
				t.Fatalf("runtime error=%v", err)
			}
			if testCase.committed {
				if !isVisibleMutationWarning(err) {
					t.Fatalf("committed error is not a visible warning: %v", err)
				}
				assertWriteTransactionRuntimeFileContent(t, fs, "/fault.txt", "new content")
			} else {
				if isVisibleMutationWarning(err) {
					t.Fatalf("rollback error unexpectedly visible: %v", err)
				}
				assertWriteTransactionRuntimeTargetAbsent(t, fs, "/fault.txt")
			}
			assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
			assertWriteTransactionRuntimeJournalEmpty(t, fs)
		})
	}
}

func TestWriteTransactionRuntimePublishParentSyncPriorityIsFailFast(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		before        string
		targetName    string
		expectedFirst string
	}{
		{
			name:          "create target first",
			targetName:    "/parent-sync-create.txt",
			expectedFirst: "namespace:before-target-parent-sync",
		},
		{
			name:          "overwrite staging source first",
			before:        "old content",
			targetName:    "/parent-sync-overwrite.txt",
			expectedFirst: "namespace:before-source-parent-sync",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			if testCase.before != "" {
				if err := os.WriteFile(
					filepath.Join(
						fs.config.FilesRoot,
						strings.TrimPrefix(testCase.targetName, "/"),
					),
					[]byte(testCase.before),
					0o644,
				); err != nil {
					t.Fatal(err)
				}
			}
			injectedErr := errors.New("stop before the first parent sync")
			var forwardSyncs []string
			inRecovery := false
			originalHook := writeTransactionRecoveryFaultHook
			t.Cleanup(func() { writeTransactionRecoveryFaultHook = originalHook })
			writeTransactionRecoveryFaultHook = func(point string) error {
				if strings.HasSuffix(point, ":before") {
					inRecovery = true
					return nil
				}
				if inRecovery ||
					!strings.HasPrefix(point, "namespace:before-") ||
					!strings.HasSuffix(point, "-parent-sync") {
					return nil
				}
				forwardSyncs = append(forwardSyncs, point)
				if len(forwardSyncs) == 1 {
					return injectedErr
				}
				return nil
			}
			_, stagePath, err := runWriteTransactionRuntimeTest(
				t,
				context.Background(),
				fs,
				testCase.targetName,
				"new content",
				writeFileTransactionOptions{},
				newWriteTransactionRuntimeTestStore(fs),
				nil,
			)
			if !errors.Is(err, injectedErr) {
				t.Fatalf("runtime error=%v", err)
			}
			if len(forwardSyncs) != 1 ||
				forwardSyncs[0] != testCase.expectedFirst {
				t.Fatalf("forward syncs=%q, want only %q", forwardSyncs, testCase.expectedFirst)
			}
			if testCase.before == "" {
				assertWriteTransactionRuntimeTargetAbsent(t, fs, testCase.targetName)
			} else {
				assertWriteTransactionRuntimeFileContent(
					t,
					fs,
					testCase.targetName,
					testCase.before,
				)
			}
			assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
			assertWriteTransactionRuntimeJournalEmpty(t, fs)
		})
	}
}

func TestWriteTransactionRuntimeReconcilesUnknownMetadataOutcome(t *testing.T) {
	t.Run("observed after commits with warning", func(t *testing.T) {
		fs := setupStandaloneFileSystem(t)
		store := newWriteTransactionRuntimeTestStore(fs)
		store.ensureAfterThenUnknown = true
		_, stagePath, err := runWriteTransactionRuntimeTest(
			t,
			context.Background(),
			fs,
			"/metadata-after.txt",
			"committed content",
			writeFileTransactionOptions{},
			store,
			nil,
		)
		if !errors.Is(err, versionstore.ErrWriteMetadataOutcomeUnknown) ||
			!isVisibleMutationWarning(err) {
			t.Fatalf("runtime error=%v", err)
		}
		if store.ensureCalls < 2 {
			t.Fatalf("EnsureWriteMetadataCommitted calls=%d, want recovery retry", store.ensureCalls)
		}
		assertWriteTransactionRuntimeFileContent(
			t,
			fs,
			"/metadata-after.txt",
			"committed content",
		)
		assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
		assertWriteTransactionRuntimeJournalEmpty(t, fs)
	})

	t.Run("observed before rolls back", func(t *testing.T) {
		fs := setupStandaloneFileSystem(t)
		store := newWriteTransactionRuntimeTestStore(fs)
		store.ensureBeforeUnknown = true
		_, stagePath, err := runWriteTransactionRuntimeTest(
			t,
			context.Background(),
			fs,
			"/metadata-before.txt",
			"rolled-back content",
			writeFileTransactionOptions{},
			store,
			nil,
		)
		if !errors.Is(err, versionstore.ErrWriteMetadataOutcomeUnknown) ||
			isVisibleMutationWarning(err) {
			t.Fatalf("runtime error=%v", err)
		}
		assertWriteTransactionRuntimeTargetAbsent(t, fs, "/metadata-before.txt")
		assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
		assertWriteTransactionRuntimeJournalEmpty(t, fs)
	})
}

func TestWriteTransactionRuntimeCommittedSyncFailureRetainsOldStageUntilRetry(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	targetPath := filepath.Join(fs.config.FilesRoot, "durability.txt")
	if err := os.WriteFile(targetPath, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	persistentSyncErr := errors.New("persistent journal sync failure")
	failDirectorySync := false
	originalJournalHook := writeTransactionJournalFaultHook
	originalDirectorySync := writeTransactionJournalDirectorySync
	t.Cleanup(func() {
		writeTransactionJournalFaultHook = originalJournalHook
		writeTransactionJournalDirectorySync = originalDirectorySync
	})
	writeTransactionJournalDirectorySync = func(dir *os.File) error {
		if failDirectorySync {
			return persistentSyncErr
		}
		return originalDirectorySync(dir)
	}
	writeTransactionJournalFaultHook = func(point string) error {
		if point == "checkpoint:committed:directory_sync" {
			failDirectorySync = true
			return errors.New("injected initial committed sync failure")
		}
		return nil
	}
	store := newWriteTransactionRuntimeTestStore(fs)

	source, stagePath, err := runWriteTransactionRuntimeTest(
		t,
		context.Background(),
		fs,
		"/durability.txt",
		"new content",
		writeFileTransactionOptions{forceVersion: true},
		store,
		nil,
	)
	if !errors.Is(err, ErrWriteRecoveryRequired) ||
		!errors.Is(err, persistentSyncErr) ||
		isVisibleMutationWarning(err) {
		t.Fatalf("runtime error=%v", err)
	}
	if !source.retained {
		t.Fatal("journal-owned source was not retained")
	}
	assertWriteTransactionRuntimeFileContent(t, fs, "/durability.txt", "new content")
	oldStage, stageErr := fs.internalRootHandle.ReadFile(stagePath)
	if stageErr != nil || string(oldStage) != "old content" {
		t.Fatalf("old stage=%q err=%v", oldStage, stageErr)
	}
	if operations, scanErr := fs.writeTransactionJournal.Scan(); scanErr == nil ||
		!errors.Is(scanErr, persistentSyncErr) || operations != nil {
		t.Fatalf("scan crossed failed sync barrier: operations=%+v err=%v", operations, scanErr)
	}

	failDirectorySync = false
	writeTransactionJournalFaultHook = originalJournalHook
	report, err := fs.recoverWriteTransactionsWithStore(
		context.Background(),
		fs.writeTransactionJournal,
		store,
	)
	if err != nil || report.RolledForward != 1 || report.RolledBack != 0 {
		t.Fatalf("recovery report=%+v err=%v", report, err)
	}
	assertWriteTransactionRuntimeFileContent(t, fs, "/durability.txt", "new content")
	assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
	assertWriteTransactionRuntimeJournalEmpty(t, fs)
}

func TestWriteTransactionRuntimePreparedStageOwnershipSurvivesBlockedRecovery(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	store := newWriteTransactionRuntimeTestStore(fs)
	recoveryErr := errors.New("recovery is temporarily unavailable")
	originalRuntimeHook := writeTransactionRuntimeFaultHook
	originalRecoveryHook := writeTransactionRecoveryFaultHook
	t.Cleanup(func() {
		writeTransactionRuntimeFaultHook = originalRuntimeHook
		writeTransactionRecoveryFaultHook = originalRecoveryHook
	})
	writeTransactionRuntimeFaultHook = func(point string) error {
		if point == "after-prepared" {
			return errors.New("stop after prepared")
		}
		return nil
	}
	writeTransactionRecoveryFaultHook = func(point string) error {
		if strings.HasSuffix(point, ":before") {
			return recoveryErr
		}
		return nil
	}

	source, stagePath, err := runWriteTransactionRuntimeTest(
		t,
		context.Background(),
		fs,
		"/prepared-owned.txt",
		"prepared content",
		writeFileTransactionOptions{},
		store,
		nil,
	)
	if !errors.Is(err, ErrWriteRecoveryRequired) ||
		!errors.Is(err, recoveryErr) ||
		!errors.Is(err, errWriteTransactionRuntimeFaultInjected) ||
		isVisibleMutationWarning(err) {
		t.Fatalf("runtime error=%v", err)
	}
	if !source.retained || source.rel != stagePath {
		t.Fatalf("journal-owned source state: rel=%q retained=%v", source.rel, source.retained)
	}
	data, readErr := fs.internalRootHandle.ReadFile(stagePath)
	if readErr != nil || string(data) != "prepared content" {
		t.Fatalf("prepared stage=%q err=%v", data, readErr)
	}
	operations, scanErr := fs.writeTransactionJournal.Scan()
	if scanErr != nil || len(operations) != 1 ||
		operations[0].Decision != WriteTransactionDecisionRollback {
		t.Fatalf("prepared operations=%+v err=%v", operations, scanErr)
	}

	writeTransactionRuntimeFaultHook = originalRuntimeHook
	writeTransactionRecoveryFaultHook = originalRecoveryHook
	report, err := fs.recoverWriteTransactionsWithStore(
		context.Background(),
		fs.writeTransactionJournal,
		store,
	)
	if err != nil || report.RolledBack != 1 || report.RolledForward != 0 {
		t.Fatalf("recovery report=%+v err=%v", report, err)
	}
	assertWriteTransactionRuntimeTargetAbsent(t, fs, "/prepared-owned.txt")
	assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
	assertWriteTransactionRuntimeJournalEmpty(t, fs)
}

func TestWriteTransactionRuntimeMissingParentFailsClosed(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	_, stagePath, err := runWriteTransactionRuntimeTest(
		t,
		context.Background(),
		fs,
		"/missing/target.txt",
		"new content",
		writeFileTransactionOptions{},
		nil,
		nil,
	)
	if !errors.Is(err, ErrNotDir) {
		t.Fatalf("runtime error=%v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(fs.config.FilesRoot, "missing")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing parent was created: %v", statErr)
	}
	assertWriteTransactionRuntimeStageAbsent(t, fs, stagePath)
	assertWriteTransactionRuntimeJournalEmpty(t, fs)
}

func TestWriteFileMissingParentFailsBeforeBodyAndRaceLeavesNoResidue(t *testing.T) {
	t.Run("pre-body rejection", func(t *testing.T) {
		fs := setupStandaloneFileSystem(t)
		reads := 0
		err := fs.WriteFile(
			context.Background(),
			"/missing/target.txt",
			writeTransactionRuntimeReaderFunc(func([]byte) (int, error) {
				reads++
				return 0, errors.New("request body must not be read")
			}),
		)
		if !errors.Is(err, ErrNotDir) {
			t.Fatalf("WriteFile error=%v", err)
		}
		if reads != 0 {
			t.Fatalf("request body reads=%d, want 0", reads)
		}
		if _, statErr := os.Lstat(
			filepath.Join(fs.config.FilesRoot, "missing"),
		); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("missing parent was created: %v", statErr)
		}
		entries, readErr := os.ReadDir(filepath.Join(fs.config.InternalRoot, writeStagingDir))
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("staging entries=%v err=%v", entries, readErr)
		}
		assertWriteTransactionRuntimeJournalEmpty(t, fs)
	})

	t.Run("parent removed while body is staged", func(t *testing.T) {
		fs := setupStandaloneFileSystem(t)
		parentPath := filepath.Join(fs.config.FilesRoot, "race-parent")
		if err := os.Mkdir(parentPath, 0o755); err != nil {
			t.Fatal(err)
		}
		body := []byte("request content")
		read := false
		err := fs.WriteFile(
			context.Background(),
			"/race-parent/target.txt",
			writeTransactionRuntimeReaderFunc(func(buffer []byte) (int, error) {
				if read {
					return 0, io.EOF
				}
				read = true
				if removeErr := os.Remove(parentPath); removeErr != nil {
					return 0, removeErr
				}
				return copy(buffer, body), io.EOF
			}),
		)
		if !errors.Is(err, ErrNotDir) {
			t.Fatalf("WriteFile error=%v", err)
		}
		if !read {
			t.Fatal("request body was not staged before the parent race")
		}
		if _, statErr := os.Lstat(parentPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("raced parent remains: %v", statErr)
		}
		entries, readErr := os.ReadDir(filepath.Join(fs.config.InternalRoot, writeStagingDir))
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("staging entries=%v err=%v", entries, readErr)
		}
		assertWriteTransactionRuntimeJournalEmpty(t, fs)
	})
}

func TestSnapshotWriteTransactionCreatedDirectoriesCapturesLogicalPathsAndBase(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	created, err := fs.workspace.PrepareWriteParent(
		context.Background(),
		"/alpha/beta/target.txt",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = fs.workspace.CleanupCreatedDirs(context.Background(), created)
		_ = workspace.ReleaseCreatedDirs(created)
	}()

	directories, base, err := fs.snapshotWriteTransactionCreatedDirectories(created)
	if err != nil {
		t.Fatal(err)
	}
	if len(directories) != 2 ||
		directories[0].Path != "/alpha/beta" ||
		directories[0].RelativePath != "alpha/beta" ||
		directories[1].Path != "/alpha" ||
		directories[1].RelativePath != "alpha" {
		t.Fatalf("directories=%+v", directories)
	}
	filesBinding, err := inspectWriteTransactionRootBinding(fs.filesRootHandle, ".", false)
	if err != nil {
		t.Fatal(err)
	}
	if base == nil ||
		base.RelativePath != "." ||
		base.PersistentIdentity != filesBinding.PersistentIdentity ||
		base.Mode != filesBinding.Mode {
		t.Fatalf("base=%+v files=%+v", base, filesBinding)
	}
}

func TestWriteTransactionRuntimeLockedRecoveryDoesNotReacquireMutation(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fs.closeMu.RLock()
	defer fs.closeMu.RUnlock()
	fs.writeStagingMu.RLock()
	defer fs.writeStagingMu.RUnlock()
	targetSnapshot, err := fs.captureWriteTarget(ctx, "/locked.txt")
	if err != nil {
		t.Fatal(err)
	}
	source, err := fs.stageWriteReader(
		ctx,
		strings.NewReader("locked content"),
		defaultMaxWriteSize,
		writeSourceStagePrefix,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer source.discard()

	mutationHeld := false
	originalHook := writeTransactionRuntimeFaultHook
	t.Cleanup(func() { writeTransactionRuntimeFaultHook = originalHook })
	writeTransactionRuntimeFaultHook = func(point string) error {
		if point != "after-prepared" {
			return nil
		}
		if fs.mu.TryLock() {
			fs.mu.Unlock()
			return errors.New("mutation lock was not held")
		}
		mutationHeld = true
		return errors.New("force locked recovery")
	}

	result := make(chan error, 1)
	go func() {
		result <- fs.runWriteTransactionRuntimeLockedWithStore(
			ctx,
			"/locked.txt",
			source,
			writeFileTransactionOptions{},
			targetSnapshot,
			newWriteTransactionRuntimeTestStore(fs),
		)
	}()
	select {
	case err := <-result:
		if !errors.Is(err, errWriteTransactionRuntimeFaultInjected) {
			t.Fatalf("runtime error=%v", err)
		}
	case <-ctx.Done():
		t.Fatal("runtime recovery deadlocked while the mutation lease was held")
	}
	if !mutationHeld {
		t.Fatal("runtime hook did not observe the mutation lock")
	}
	assertWriteTransactionRuntimeTargetAbsent(t, fs, "/locked.txt")
	assertWriteTransactionRuntimeJournalEmpty(t, fs)
}
