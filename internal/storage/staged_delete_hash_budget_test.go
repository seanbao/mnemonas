//go:build linux || darwin

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/seanbao/mnemonas/internal/versionstore"
)

func installStagedDeleteHashCounters(t *testing.T, fs *FileSystem) (*atomic.Int64, *atomic.Int64, *atomic.Int64) {
	t.Helper()

	var liveHashes atomic.Int64
	var sourceHashes atomic.Int64
	var destinationHashes atomic.Int64
	fs.hashDeleteTargetFile = func(ctx context.Context, name string) (string, error) {
		liveHashes.Add(1)
		return fs.hashWorkspaceFile(ctx, name)
	}
	fs.hashStagedDeleteSourceFile = func(ctx context.Context, file *os.File) (string, error) {
		sourceHashes.Add(1)
		return hashOpenWorkspaceFileContext(ctx, file)
	}
	fs.hashStagedTrashContentFile = func(ctx context.Context, file *os.File) (string, error) {
		destinationHashes.Add(1)
		return hashOpenWorkspaceFileContext(ctx, file)
	}

	return &liveHashes, &sourceHashes, &destinationHashes
}

func prepareObservedDeleteForTest(t *testing.T, fs *FileSystem, targetPath string) DeleteIntentSnapshot {
	t.Helper()

	ctx := context.Background()
	observed, err := fs.workspace.Stat(ctx, targetPath)
	if err != nil {
		t.Fatalf("Stat(%s) error: %v", targetPath, err)
	}
	if observed.DeleteIdentityToken == "" {
		t.Fatalf("Stat(%s) returned an empty delete identity token", targetPath)
	}
	intent, err := fs.PrepareObservedDeleteIntents(ctx, []ObservedDeleteTarget{{
		Path:                  targetPath,
		ObservedIdentityToken: observed.DeleteIdentityToken,
	}}, nil)
	if err != nil {
		t.Fatalf("PrepareObservedDeleteIntents(%s) error: %v", targetPath, err)
	}
	if len(intent.Targets) != 1 || intent.Targets[0].Token == "" {
		t.Fatalf("PrepareObservedDeleteIntents(%s) targets = %+v, want one tokenized target", targetPath, intent.Targets)
	}
	return intent
}

func commitPreparedDeleteForTest(fs *FileSystem, intent DeleteIntentSnapshot) error {
	return fs.DeleteWithExpectedPolicyAndTarget(
		context.Background(),
		intent.Targets[0].Path,
		DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token},
		intent.Targets[0].Token,
		nil,
	)
}

func TestFileSystemStagedTrashDeleteConfirmedFileHashBudget(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/trash-hash-budget.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("budget")); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", targetPath, err)
	}
	liveHashes, sourceHashes, destinationHashes := installStagedDeleteHashCounters(t, fs)

	intent := prepareObservedDeleteForTest(t, fs, targetPath)
	if intent.Policy.Mode != DeleteModeTrash {
		t.Fatalf("prepared delete mode = %q, want %q", intent.Policy.Mode, DeleteModeTrash)
	}
	if err := commitPreparedDeleteForTest(fs, intent); err != nil {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget(%s) error: %v", targetPath, err)
	}

	if got := liveHashes.Load(); got != 2 {
		t.Errorf("live content hash count = %d, want 2", got)
	}
	if got := sourceHashes.Load(); got != 4 {
		t.Errorf("staged source content hash count = %d, want 4", got)
	}
	if got := destinationHashes.Load(); got != 1 {
		t.Errorf("trash destination content hash count = %d, want 1", got)
	}
	if got := liveHashes.Load() + sourceHashes.Load() + destinationHashes.Load(); got != 7 {
		t.Errorf("total delete content hash count = %d, want 7", got)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].OriginalPath != targetPath {
		t.Fatalf("ListTrash() = %+v, %v; want committed item for %s", items, err, targetPath)
	}
}

func TestFileSystemStagedPermanentDeleteConfirmedFileHashBudget(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	fs.UpdateTrashSettings(false, 30, 0)
	targetPath := "/permanent-hash-budget.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("budget")); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", targetPath, err)
	}
	liveHashes, sourceHashes, destinationHashes := installStagedDeleteHashCounters(t, fs)

	intent := prepareObservedDeleteForTest(t, fs, targetPath)
	if intent.Policy.Mode != DeleteModePermanent {
		t.Fatalf("prepared delete mode = %q, want %q", intent.Policy.Mode, DeleteModePermanent)
	}
	if err := commitPreparedDeleteForTest(fs, intent); err != nil {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget(%s) error: %v", targetPath, err)
	}

	if got := liveHashes.Load(); got != 2 {
		t.Errorf("live content hash count = %d, want 2", got)
	}
	if got := sourceHashes.Load(); got != 3 {
		t.Errorf("staged source content hash count = %d, want 3", got)
	}
	if got := destinationHashes.Load(); got != 0 {
		t.Errorf("trash destination content hash count = %d, want 0", got)
	}
	if got := liveHashes.Load() + sourceHashes.Load() + destinationHashes.Load(); got != 5 {
		t.Errorf("total delete content hash count = %d, want 5", got)
	}
	if _, err := fs.Stat(ctx, targetPath); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat(%s) error = %v, want ErrNotFound", targetPath, err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("ListTrash() = %+v, %v; want empty", items, err)
	}
}

func rewriteSameLengthAndRestoreModTime(path string, content []byte) error {
	before, err := os.Stat(path)
	if err != nil {
		return err
	}
	if int64(len(content)) != before.Size() {
		return errors.New("replacement content length does not match the existing file")
	}
	if err := os.WriteFile(path, content, before.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(path, before.ModTime(), before.ModTime())
}

func TestFileSystemStagedTrashDeleteRejectsPostHashDriftBeforeBusinessCommit(t *testing.T) {
	type testCase struct {
		name           string
		directory      bool
		sourceMutation bool
		mutate         func(sourceStagePath, trashContentPath string) (string, string, error)
	}
	tests := []testCase{
		{
			name: "source content with restored mtime",
			mutate: func(sourceStagePath, _ string) (string, string, error) {
				return sourceStagePath, "changed!", rewriteSameLengthAndRestoreModTime(sourceStagePath, []byte("changed!"))
			},
			sourceMutation: true,
		},
		{
			name: "destination content with restored mtime",
			mutate: func(_, trashContentPath string) (string, string, error) {
				return trashContentPath, "changed!", rewriteSameLengthAndRestoreModTime(trashContentPath, []byte("changed!"))
			},
		},
		{
			name:      "new source descendant",
			directory: true,
			mutate: func(sourceStagePath, _ string) (string, string, error) {
				injectedPath := filepath.Join(sourceStagePath, "injected.bin")
				return injectedPath, "unknown", os.WriteFile(injectedPath, []byte("unknown"), 0o600)
			},
			sourceMutation: true,
		},
		{
			name:      "new destination descendant",
			directory: true,
			mutate: func(_, trashContentPath string) (string, string, error) {
				injectedPath := filepath.Join(trashContentPath, "injected.bin")
				return injectedPath, "unknown", os.WriteFile(injectedPath, []byte("unknown"), 0o600)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			ctx := context.Background()
			targetPath := "/post-hash-file.bin"
			baselinePath := targetPath
			if testCase.directory {
				targetPath = "/post-hash-tree"
				baselinePath = targetPath + "/baseline.bin"
				if err := fs.Mkdir(ctx, targetPath); err != nil {
					t.Fatalf("Mkdir(%s) error: %v", targetPath, err)
				}
			}
			if err := fs.WriteFile(ctx, baselinePath, strings.NewReader("original")); err != nil {
				t.Fatalf("WriteFile(%s) error: %v", baselinePath, err)
			}
			intent := prepareObservedDeleteForTest(t, fs, targetPath)

			var metadataCalls atomic.Int64
			originalAddTrashMetadata := fs.addTrashMetadata
			fs.addTrashMetadata = func(ctx context.Context, item *versionstore.TrashItem) error {
				metadataCalls.Add(1)
				return originalAddTrashMetadata(ctx, item)
			}

			var sourceStagePath string
			var trashContentPath string
			var preservedPath string
			var preservedContent string
			originalHook := afterDeleteTrashContentHash
			afterDeleteTrashContentHash = func(logicalName, sourcePath, destinationPath string) error {
				if logicalName != targetPath {
					return nil
				}
				sourceStagePath = sourcePath
				trashContentPath = destinationPath
				var err error
				preservedPath, preservedContent, err = testCase.mutate(sourcePath, destinationPath)
				return err
			}
			t.Cleanup(func() { afterDeleteTrashContentHash = originalHook })

			err := commitPreparedDeleteForTest(fs, intent)
			if !errors.Is(err, ErrDeleteTargetChanged) {
				t.Fatalf("DeleteWithExpectedPolicyAndTarget(%s) error = %v, want ErrDeleteTargetChanged", targetPath, err)
			}
			if sourceStagePath == "" || trashContentPath == "" || preservedPath == "" {
				t.Fatalf("post-hash hook paths = source %q, destination %q, preserved %q", sourceStagePath, trashContentPath, preservedPath)
			}
			if got := metadataCalls.Load(); got != 0 {
				t.Fatalf("trash metadata commit calls = %d, want 0", got)
			}
			items, listErr := fs.ListTrash(ctx)
			if listErr != nil || len(items) != 0 {
				t.Fatalf("ListTrash() after rejected delete = %+v, %v; want empty", items, listErr)
			}

			if testCase.sourceMutation {
				if testCase.directory {
					preservedPath = filepath.Join(fs.workspace.FullPath(targetPath), filepath.Base(preservedPath))
				} else {
					preservedPath = fs.workspace.FullPath(targetPath)
				}
			}
			preserved, readErr := os.ReadFile(preservedPath)
			if readErr != nil || string(preserved) != preservedContent {
				t.Fatalf("preserved unknown content at %s = %q, %v; want %q", preservedPath, preserved, readErr, preservedContent)
			}
			if testCase.sourceMutation {
				if _, statErr := fs.Stat(ctx, targetPath); statErr != nil {
					t.Fatalf("Stat(%s) after source drift error = %v, want same-inode source restored without replacement", targetPath, statErr)
				}
				if testCase.directory {
					baseline, readErr := os.ReadFile(fs.workspace.FullPath(baselinePath))
					if readErr != nil || string(baseline) != "original" {
						t.Fatalf("restored baseline %s = %q, %v; want original", baselinePath, baseline, readErr)
					}
				}
				return
			}
			baseline, readErr := os.ReadFile(fs.workspace.FullPath(baselinePath))
			if readErr != nil || string(baseline) != "original" {
				t.Fatalf("restored baseline %s = %q, want original", baselinePath, baseline)
			}
		})
	}
}
