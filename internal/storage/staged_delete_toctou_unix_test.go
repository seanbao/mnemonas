//go:build linux || darwin

package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createStagedDeleteReplacement(t *testing.T, targetHostPath, replacementHostPath string, content []byte) (os.FileInfo, os.FileInfo) {
	t.Helper()
	stableTime := time.Unix(1_700_000_000, 123_456_789)
	if err := os.Chtimes(targetHostPath, stableTime, stableTime); err != nil {
		t.Fatalf("Chtimes(target) error: %v", err)
	}
	targetInfo, err := os.Stat(targetHostPath)
	if err != nil {
		t.Fatalf("Stat(target) error: %v", err)
	}
	if err := os.WriteFile(replacementHostPath, content, targetInfo.Mode().Perm()); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	if err := os.Chtimes(replacementHostPath, targetInfo.ModTime(), targetInfo.ModTime()); err != nil {
		t.Fatalf("Chtimes(replacement) error: %v", err)
	}
	replacementInfo, err := os.Stat(replacementHostPath)
	if err != nil {
		t.Fatalf("Stat(replacement) error: %v", err)
	}
	if os.SameFile(targetInfo, replacementInfo) {
		t.Fatal("replacement unexpectedly shares target inode")
	}
	return targetInfo, replacementInfo
}

func TestFileSystemStagedDeleteRejectsReplacementBetweenWitnessAndCapture(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/witness-capture.bin"
	replacementPath := "/witness-capture-replacement.bin"
	content := []byte("same content")
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	targetHostPath := fs.workspace.FullPath(targetPath)
	replacementHostPath := fs.workspace.FullPath(replacementPath)
	_, replacementInfo := createStagedDeleteReplacement(t, targetHostPath, replacementHostPath, content)

	originalHook := afterDeleteWitnessOpen
	afterDeleteWitnessOpen = func(name string) error {
		if name == targetPath {
			return os.Rename(replacementHostPath, targetHostPath)
		}
		return nil
	}
	t.Cleanup(func() { afterDeleteWitnessOpen = originalHook })

	err := fs.Delete(ctx, targetPath)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("Delete() error = %v, want ErrDeleteTargetChanged", err)
	}
	var changed *DeleteTargetChangedError
	if !errors.As(err, &changed) || changed.Path != targetPath {
		t.Fatalf("Delete() error = %v, want typed target change for %q", err, targetPath)
	}
	current, statErr := os.Stat(targetHostPath)
	if statErr != nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement was not restored at original path: info=%v err=%v", current, statErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash after rejected capture = %+v, %v; want empty", items, listErr)
	}
}

func TestFileSystemStagedDeleteDoesNotReverseUnknownPostRenameReplacement(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/post-rename-replacement.bin"
	replacementPath := "/post-rename-replacement-new.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("captured")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := os.WriteFile(fs.workspace.FullPath(replacementPath), []byte("replacement"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	replacementInfo, err := os.Stat(fs.workspace.FullPath(replacementPath))
	if err != nil {
		t.Fatalf("Stat(replacement) error: %v", err)
	}

	var stagePath string
	originalHook := afterDeleteLeafRename
	afterDeleteLeafRename = func(sourceRel, targetRel string) error {
		if sourceRel != storageWorkspaceRelativeName(targetPath) {
			return nil
		}
		stagePath = filepath.Join(fs.workspace.Root(), targetRel)
		return os.Rename(fs.workspace.FullPath(replacementPath), stagePath)
	}
	t.Cleanup(func() { afterDeleteLeafRename = originalHook })

	err = fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.As(err, &residual) {
		t.Fatalf("Delete() error = %v, want target change and post-rename residual", err)
	}
	if stagePath == "" || residual.StagePath != stagePath {
		t.Fatalf("residual path = %q, stage path = %q", residual.StagePath, stagePath)
	}
	current, statErr := os.Stat(stagePath)
	if statErr != nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("post-rename replacement was moved or removed: info=%v err=%v", current, statErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath(targetPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("logical path unexpectedly contains the replacement: %v", statErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash after post-rename replacement = %+v, %v; want empty", items, listErr)
	}
}

func TestFileSystemStagedDeleteDoesNotConsumeNewOriginalPath(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		name := "trash"
		if permanent {
			name = "permanent"
		}
		t.Run(name, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			ctx := context.Background()
			if permanent {
				enabled := false
				fs.config.TrashEnabled = &enabled
			}
			targetPath := "/new-original-" + name + ".bin"
			if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
				t.Fatalf("WriteFile(target) error: %v", err)
			}
			newContent := []byte("new original")
			var stagePath string
			originalHook := afterDeleteStageCapture
			afterDeleteStageCapture = func(logicalPath, capturedStagePath string) error {
				if logicalPath != targetPath {
					return nil
				}
				stagePath = capturedStagePath
				return os.WriteFile(fs.workspace.FullPath(targetPath), newContent, 0o600)
			}
			t.Cleanup(func() { afterDeleteStageCapture = originalHook })

			if err := fs.Delete(ctx, targetPath); err != nil {
				t.Fatalf("Delete() error: %v", err)
			}
			data, err := os.ReadFile(fs.workspace.FullPath(targetPath))
			if err != nil || !bytes.Equal(data, newContent) {
				t.Fatalf("new original content = %q, %v", data, err)
			}
			if stagePath == "" {
				t.Fatal("stage capture hook was not called")
			}
			if _, err := os.Stat(stagePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("committed stage still exists: %v", err)
			}
			items, err := fs.ListTrash(ctx)
			if err != nil {
				t.Fatalf("ListTrash() error: %v", err)
			}
			if permanent && len(items) != 0 {
				t.Fatalf("permanent trash items = %d, want 0", len(items))
			}
			if !permanent && len(items) != 1 {
				t.Fatalf("trash items = %d, want 1", len(items))
			}
		})
	}
}

func TestFileSystemStagedDeleteRollbackPreservesOccupiedOriginalAndStage(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/occupied-rollback.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	originalInfo, err := os.Stat(fs.workspace.FullPath(targetPath))
	if err != nil {
		t.Fatalf("Stat(target) error: %v", err)
	}
	newContent := []byte("new original")
	var stagePath string
	originalHook := afterDeleteStageCapture
	afterDeleteStageCapture = func(logicalPath, capturedStagePath string) error {
		if logicalPath != targetPath {
			return nil
		}
		stagePath = capturedStagePath
		return os.WriteFile(fs.workspace.FullPath(targetPath), newContent, 0o600)
	}
	t.Cleanup(func() { afterDeleteStageCapture = originalHook })
	indexErr := errors.New("delete index failed")
	fs.deleteFileIndex = func(context.Context, string) error { return indexErr }

	err = fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	if !errors.Is(err, indexErr) || !errors.As(err, &residual) {
		t.Fatalf("Delete() error = %v, want index error and staged residual", err)
	}
	data, readErr := os.ReadFile(fs.workspace.FullPath(targetPath))
	if readErr != nil || !bytes.Equal(data, newContent) {
		t.Fatalf("occupied original content = %q, %v", data, readErr)
	}
	stagedInfo, statErr := os.Stat(stagePath)
	if statErr != nil || !os.SameFile(originalInfo, stagedInfo) {
		t.Fatalf("intended inode was not retained at stage: info=%v err=%v", stagedInfo, statErr)
	}
	if residual.StagePath != stagePath {
		t.Fatalf("residual stage path = %q, want %q", residual.StagePath, stagePath)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil || len(items) != 1 {
		t.Fatalf("trash after failed source rollback = %+v, %v; want retained recovery item", items, listErr)
	}
	trashContent := filepath.Join(fs.trashRoot, items[0].ID, "content")
	trashData, readTrashErr := os.ReadFile(trashContent)
	if readTrashErr != nil || string(trashData) != "intended" {
		t.Fatalf("retained trash recovery content = %q, %v", trashData, readTrashErr)
	}
}

func TestFileSystemStagedDeleteRejectsDirectoryDescendantReplacement(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/child.bin", strings.NewReader("child")); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}
	replacementPath := filepath.Join(fs.workspace.Root(), "replacement-child.bin")
	childPath := fs.workspace.FullPath("/tree/child.bin")
	_, replacementInfo := createStagedDeleteReplacement(t, childPath, replacementPath, []byte("child"))

	originalHook := afterDeleteStageCapture
	afterDeleteStageCapture = func(logicalPath, stagePath string) error {
		if logicalPath == "/tree" {
			return os.Rename(replacementPath, filepath.Join(stagePath, "child.bin"))
		}
		return nil
	}
	t.Cleanup(func() { afterDeleteStageCapture = originalHook })

	err := fs.Delete(ctx, "/tree")
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("Delete(tree) error = %v, want ErrDeleteTargetChanged", err)
	}
	current, statErr := os.Stat(childPath)
	if statErr != nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement descendant was not preserved: info=%v err=%v", current, statErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash after rejected descendant replacement = %+v, %v", items, listErr)
	}
}

func TestFileSystemStagedPermanentDeleteRejectsReplacementBeforePhysicalRemoval(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	enabled := false
	fs.config.TrashEnabled = &enabled
	targetPath := "/permanent-remove-race.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	var replacementInfo os.FileInfo
	var stagePath string
	originalHook := beforeDeleteStageRemoval
	beforeDeleteStageRemoval = func(logicalPath, capturedStagePath string) error {
		if logicalPath != targetPath {
			return nil
		}
		stagePath = capturedStagePath
		replacementPath := capturedStagePath + ".replacement"
		if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
			return err
		}
		var err error
		replacementInfo, err = os.Stat(replacementPath)
		if err != nil {
			return err
		}
		return os.Rename(replacementPath, capturedStagePath)
	}
	t.Cleanup(func() { beforeDeleteStageRemoval = originalHook })

	err := fs.Delete(ctx, targetPath)
	var cleanup *DeleteCleanupWarningError
	if !errors.As(err, &cleanup) || isVisibleMutationWarning(err) {
		t.Fatalf("Delete() error = %v, want committed cleanup warning", err)
	}
	current, statErr := os.Stat(stagePath)
	if statErr != nil || replacementInfo == nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement stage was removed: info=%v err=%v", current, statErr)
	}
	if _, err := os.Stat(fs.workspace.FullPath(targetPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logical path unexpectedly exists: %v", err)
	}
}

func TestFileSystemStagedTrashCopyRejectsStageSourceReplacement(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/trash-copy-race.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	var stagePath string
	originalCaptureHook := afterDeleteStageCapture
	afterDeleteStageCapture = func(logicalPath, capturedStagePath string) error {
		if logicalPath == targetPath {
			stagePath = capturedStagePath
		}
		return nil
	}
	t.Cleanup(func() { afterDeleteStageCapture = originalCaptureHook })
	var replacementInfo os.FileInfo
	originalCopyHook := afterStorageCopySourceStat
	afterStorageCopySourceStat = func(sourcePath string) error {
		if sourcePath != stagePath || stagePath == "" {
			return nil
		}
		replacementPath := stagePath + ".replacement"
		if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
			return err
		}
		var err error
		replacementInfo, err = os.Stat(replacementPath)
		if err != nil {
			return err
		}
		return os.Rename(replacementPath, stagePath)
	}
	t.Cleanup(func() { afterStorageCopySourceStat = originalCopyHook })

	err := fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.As(err, &residual) {
		t.Fatalf("Delete() error = %v, want target changed and staged residual", err)
	}
	current, statErr := os.Stat(stagePath)
	if statErr != nil || replacementInfo == nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement stage source was removed: info=%v err=%v", current, statErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash after rejected copied source = %+v, %v; want empty", items, listErr)
	}
}

func TestFileSystemStagedTrashCopyRetainsInjectedDestination(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/trash-destination-tree"); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-destination-tree/child.bin", strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}

	var copiedPath string
	originalHook := afterDeleteTrashCopy
	afterDeleteTrashCopy = func(logicalPath, destinationPath string) error {
		if logicalPath != "/trash-destination-tree" {
			return nil
		}
		copiedPath = destinationPath
		return os.WriteFile(filepath.Join(destinationPath, "injected.bin"), []byte("replacement"), 0o600)
	}
	t.Cleanup(func() { afterDeleteTrashCopy = originalHook })

	err := fs.Delete(ctx, "/trash-destination-tree")
	var residual *DeleteStageResidualError
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.As(err, &residual) {
		t.Fatalf("Delete() error = %v, want target change and destination residual", err)
	}
	if copiedPath == "" || residual.StagePath != copiedPath {
		t.Fatalf("destination residual path = %q, copied path = %q", residual.StagePath, copiedPath)
	}
	injected, readErr := os.ReadFile(filepath.Join(copiedPath, "injected.bin"))
	if readErr != nil || string(injected) != "replacement" {
		t.Fatalf("injected destination = %q, %v; want retained", injected, readErr)
	}
	if original, readErr := os.ReadFile(fs.workspace.FullPath("/trash-destination-tree/child.bin")); readErr != nil || string(original) != "intended" {
		t.Fatalf("rolled back source = %q, %v", original, readErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash metadata after rejected destination = %+v, %v; want empty", items, listErr)
	}
}

func TestFileSystemStagedTrashCopyRetainsReplacedDestination(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/trash-destination-replacement.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}

	var copiedPath string
	var replacementInfo os.FileInfo
	originalHook := afterDeleteTrashCopy
	afterDeleteTrashCopy = func(logicalPath, destinationPath string) error {
		if logicalPath != targetPath {
			return nil
		}
		copiedPath = destinationPath
		replacementPath := destinationPath + ".replacement"
		if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
			return err
		}
		var err error
		replacementInfo, err = os.Stat(replacementPath)
		if err != nil {
			return err
		}
		return os.Rename(replacementPath, destinationPath)
	}
	t.Cleanup(func() { afterDeleteTrashCopy = originalHook })

	err := fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.As(err, &residual) {
		t.Fatalf("Delete() error = %v, want target change and copied-content residual", err)
	}
	if copiedPath == "" || residual.StagePath != copiedPath {
		t.Fatalf("destination residual path = %q, copied path = %q", residual.StagePath, copiedPath)
	}
	current, statErr := os.Stat(copiedPath)
	if statErr != nil || replacementInfo == nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement destination was not retained: info=%v err=%v", current, statErr)
	}
	if original, readErr := os.ReadFile(fs.workspace.FullPath(targetPath)); readErr != nil || string(original) != "intended" {
		t.Fatalf("rolled back source = %q, %v", original, readErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash metadata after replaced destination = %+v, %v; want empty", items, listErr)
	}
}

func TestFileSystemStagedPermanentDeleteRetainsQuarantineAfterDescendantInjection(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	enabled := false
	fs.config.TrashEnabled = &enabled
	if err := fs.Mkdir(ctx, "/quarantine-tree"); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}

	var quarantineContentPath string
	originalHook := afterDeleteQuarantineCapture
	afterDeleteQuarantineCapture = func(logicalPath, contentPath string) error {
		if logicalPath != "/quarantine-tree" {
			return nil
		}
		quarantineContentPath = contentPath
		return os.WriteFile(filepath.Join(contentPath, "injected.bin"), []byte("replacement"), 0o600)
	}
	t.Cleanup(func() { afterDeleteQuarantineCapture = originalHook })

	err := fs.Delete(ctx, "/quarantine-tree")
	var residual *DeleteStageResidualError
	var cleanup *DeleteCleanupWarningError
	if !errors.As(err, &cleanup) || !errors.As(err, &residual) || isVisibleMutationWarning(err) {
		t.Fatalf("Delete() error = %v, want committed cleanup warning with residual", err)
	}
	if quarantineContentPath == "" || residual.StagePath != quarantineContentPath {
		t.Fatalf("quarantine residual path = %q, captured path = %q", residual.StagePath, quarantineContentPath)
	}
	injected, readErr := os.ReadFile(filepath.Join(quarantineContentPath, "injected.bin"))
	if readErr != nil || string(injected) != "replacement" {
		t.Fatalf("injected quarantine entry = %q, %v; want retained", injected, readErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/quarantine-tree")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("logical path unexpectedly exists: %v", statErr)
	}
}

func TestFileSystemStagedPermanentDeleteRetainsReplacedQuarantineContent(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	fs.UpdateTrashSettings(false, 30, 0)
	targetPath := "/quarantine-replacement.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}

	var quarantineContentPath string
	var replacementInfo os.FileInfo
	originalHook := afterDeleteQuarantineCapture
	afterDeleteQuarantineCapture = func(logicalPath, contentPath string) error {
		if logicalPath != targetPath {
			return nil
		}
		quarantineContentPath = contentPath
		replacementPath := contentPath + ".replacement"
		if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
			return err
		}
		var err error
		replacementInfo, err = os.Stat(replacementPath)
		if err != nil {
			return err
		}
		return os.Rename(replacementPath, contentPath)
	}
	t.Cleanup(func() { afterDeleteQuarantineCapture = originalHook })

	err := fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	var cleanup *DeleteCleanupWarningError
	if !errors.As(err, &cleanup) || !errors.As(err, &residual) || isVisibleMutationWarning(err) {
		t.Fatalf("Delete() error = %v, want committed cleanup residual", err)
	}
	if quarantineContentPath == "" || residual.StagePath != quarantineContentPath {
		t.Fatalf("quarantine residual path = %q, captured path = %q", residual.StagePath, quarantineContentPath)
	}
	current, statErr := os.Stat(quarantineContentPath)
	if statErr != nil || replacementInfo == nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement quarantine content was not retained: info=%v err=%v", current, statErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath(targetPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("logical path unexpectedly exists: %v", statErr)
	}
}

func TestFileSystemStagedTrashRollbackRetainsReplacedCanonicalCopyAndMetadata(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/trash-rollback-copy.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	indexErr := errors.New("delete index failed")
	fs.deleteFileIndex = func(context.Context, string) error { return indexErr }

	var copiedPath string
	originalHook := beforeTrashRollbackCapture
	beforeTrashRollbackCapture = func(logicalPath, contentPath string) error {
		if logicalPath != targetPath {
			return nil
		}
		copiedPath = contentPath
		replacement := contentPath + ".replacement"
		if err := os.WriteFile(replacement, []byte("replacement"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, contentPath)
	}
	t.Cleanup(func() { beforeTrashRollbackCapture = originalHook })

	err := fs.Delete(ctx, targetPath)
	if !errors.Is(err, indexErr) || !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("Delete() error = %v, want index error and copied-content drift", err)
	}
	if original, readErr := os.ReadFile(fs.workspace.FullPath(targetPath)); readErr != nil || string(original) != "intended" {
		t.Fatalf("rolled back source = %q, %v", original, readErr)
	}
	replacement, readErr := os.ReadFile(copiedPath)
	if readErr != nil || string(replacement) != "replacement" {
		t.Fatalf("replacement canonical copy = %q, %v; want retained", replacement, readErr)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil || len(items) != 1 {
		t.Fatalf("trash metadata after copied-content drift = %+v, %v; want retained item", items, listErr)
	}
	if got := filepath.Join(fs.trashRoot, items[0].ID, "content"); got != copiedPath {
		t.Fatalf("metadata content path = %q, want %q", got, copiedPath)
	}
}

func TestFileSystemStagedTrashRollbackRetainsPostCaptureReplacementAndMetadata(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/trash-rollback-post-capture.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	indexErr := errors.New("delete index failed")
	fs.deleteFileIndex = func(context.Context, string) error { return indexErr }

	var holdPath string
	var replacementInfo os.FileInfo
	originalHook := afterTrashRollbackCapture
	afterTrashRollbackCapture = func(logicalPath, capturedPath string) error {
		if logicalPath != targetPath {
			return nil
		}
		holdPath = capturedPath
		replacementPath := capturedPath + ".replacement"
		if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
			return err
		}
		var err error
		replacementInfo, err = os.Stat(replacementPath)
		if err != nil {
			return err
		}
		return os.Rename(replacementPath, capturedPath)
	}
	t.Cleanup(func() { afterTrashRollbackCapture = originalHook })

	err := fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	if !errors.Is(err, indexErr) || !errors.Is(err, ErrDeleteTargetChanged) || !errors.As(err, &residual) {
		t.Fatalf("Delete() error = %v, want index error and rollback-stage residual", err)
	}
	if original, readErr := os.ReadFile(fs.workspace.FullPath(targetPath)); readErr != nil || string(original) != "intended" {
		t.Fatalf("rolled back source = %q, %v", original, readErr)
	}
	if holdPath == "" || residual.StagePath != holdPath {
		t.Fatalf("rollback residual path = %q, hold path = %q", residual.StagePath, holdPath)
	}
	current, statErr := os.Stat(holdPath)
	if statErr != nil || replacementInfo == nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("post-capture replacement was not retained: info=%v err=%v", current, statErr)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil || len(items) != 1 {
		t.Fatalf("trash metadata after post-capture replacement = %+v, %v; want retained item", items, listErr)
	}
	canonicalPath := filepath.Join(fs.trashRoot, items[0].ID, "content")
	if _, statErr := os.Stat(canonicalPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("canonical content after post-capture replacement = %v, want absent", statErr)
	}
}

func TestFileSystemStagedDeleteRemovesDirectoryWithHardLinks(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/hardlinks"); err != nil {
		t.Fatalf("Mkdir(hardlinks) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/hardlinks/first.bin", strings.NewReader("shared")); err != nil {
		t.Fatalf("WriteFile(first.bin) error: %v", err)
	}
	if err := os.Link(fs.workspace.FullPath("/hardlinks/first.bin"), fs.workspace.FullPath("/hardlinks/second.bin")); err != nil {
		t.Fatalf("Link(second.bin) error: %v", err)
	}

	if err := fs.Delete(ctx, "/hardlinks"); err != nil {
		t.Fatalf("Delete(hardlinks) error: %v", err)
	}
	if _, err := fs.Stat(ctx, "/hardlinks"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hardlink directory after delete = %v, want not found", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("trash items = %+v, %v; want one", items, err)
	}
	for _, name := range []string{"first.bin", "second.bin"} {
		content, err := os.ReadFile(filepath.Join(fs.trashRoot, items[0].ID, "content", name))
		if err != nil || string(content) != "shared" {
			t.Fatalf("trash %s content = %q, %v", name, content, err)
		}
	}
}

func TestFileSystemStagedPermanentDeleteFinishesCleanupAfterRequestCancellation(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	fs.UpdateTrashSettings(false, 30, 0)
	if err := fs.Mkdir(ctx, "/cancel-after-commit"); err != nil {
		t.Fatalf("Mkdir(cancel-after-commit) error: %v", err)
	}
	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		cancel()
		return nil, nil
	})

	if err := fs.Delete(ctx, "/cancel-after-commit"); err != nil {
		t.Fatalf("Delete(cancel-after-commit) error: %v", err)
	}
	if _, err := fs.Stat(context.Background(), "/cancel-after-commit"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled committed target = %v, want not found", err)
	}
	stages, err := filepath.Glob(filepath.Join(fs.workspace.Root(), ".mnemonas-delete-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("staged paths after canceled committed delete = %v, %v; want none", stages, err)
	}
}
