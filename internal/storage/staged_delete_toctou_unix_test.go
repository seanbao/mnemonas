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
	if stagePath == "" || residual.StagePath == "" || residual.StagePath == stagePath {
		t.Fatalf("recovery residual path = %q, unknown replacement path = %q, error = %v, cause = %v", residual.StagePath, stagePath, err, residual.err)
	}
	if len(residual.InspectionPaths) != 1 || residual.InspectionPaths[0] != stagePath {
		t.Fatalf("inspection paths = %v, want unknown stage %q", residual.InspectionPaths, stagePath)
	}
	current, statErr := os.Stat(stagePath)
	if statErr != nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("post-rename replacement was moved or removed: info=%v err=%v", current, statErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath(targetPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("logical path unexpectedly contains the replacement: %v", statErr)
	}
	recovered, readErr := os.ReadFile(residual.StagePath)
	if readErr != nil || string(recovered) != "captured" {
		t.Fatalf("recovered captured content = %q, %v; want captured", recovered, readErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash after post-rename replacement = %+v, %v; want empty", items, listErr)
	}
}

func TestFileSystemStagedDeleteReportsUnverifiedWitnessRecoveryForInspection(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/unverified-witness-recovery.bin"
	replacementPath := "/unverified-witness-replacement.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("captured")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := os.WriteFile(fs.workspace.FullPath(replacementPath), []byte("replacement"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}

	var stagePath string
	originalRenameHook := afterDeleteLeafRename
	afterDeleteLeafRename = func(sourceRel, targetRel string) error {
		if sourceRel != storageWorkspaceRelativeName(targetPath) {
			return nil
		}
		stagePath = filepath.Join(fs.workspace.Root(), targetRel)
		return os.Rename(fs.workspace.FullPath(replacementPath), stagePath)
	}
	t.Cleanup(func() { afterDeleteLeafRename = originalRenameHook })
	fs.hashDeleteWitnessRecoveryFile = func(*os.File) (string, error) {
		return "unverified", nil
	}

	err := fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.As(err, &residual) {
		t.Fatalf("Delete() error = %v, want target change with inspection residual", err)
	}
	if residual.StagePath != "" {
		t.Fatalf("verified recovery path = %q, want empty", residual.StagePath)
	}
	if len(residual.InspectionPaths) != 2 || residual.InspectionPaths[0] != stagePath {
		t.Fatalf("inspection paths = %v, want unknown stage first and recovery attempt second", residual.InspectionPaths)
	}
	if _, statErr := os.Stat(residual.InspectionPaths[1]); statErr != nil {
		t.Fatalf("Stat(recovery attempt) error: %v", statErr)
	}
	message := residual.Error()
	for _, inspectionPath := range residual.InspectionPaths {
		if !strings.Contains(message, inspectionPath) {
			t.Fatalf("residual error %q does not identify inspection path %q", message, inspectionPath)
		}
	}
}

func TestFileSystemStagedDeleteReportsUnsyncedWitnessRecoveryForInspection(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/unsynced-witness-recovery.bin"
	replacementPath := "/unsynced-witness-replacement.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("captured")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := os.WriteFile(fs.workspace.FullPath(replacementPath), []byte("replacement"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}

	var stagePath string
	originalRenameHook := afterDeleteLeafRename
	afterDeleteLeafRename = func(sourceRel, targetRel string) error {
		if sourceRel != storageWorkspaceRelativeName(targetPath) {
			return nil
		}
		stagePath = filepath.Join(fs.workspace.Root(), targetRel)
		return os.Rename(fs.workspace.FullPath(replacementPath), stagePath)
	}
	t.Cleanup(func() { afterDeleteLeafRename = originalRenameHook })
	wantErr := errors.New("recovery parent sync failed")
	originalSyncHook := syncDeleteWitnessRecoveryDir
	syncDeleteWitnessRecoveryDir = func(*os.Root, string, string) error { return wantErr }
	t.Cleanup(func() { syncDeleteWitnessRecoveryDir = originalSyncHook })

	err := fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.Is(err, wantErr) || !errors.As(err, &residual) {
		t.Fatalf("Delete() error = %v, want target change with unsynced recovery residual", err)
	}
	if residual.StagePath != "" {
		t.Fatalf("verified recovery path = %q, want empty", residual.StagePath)
	}
	if len(residual.InspectionPaths) != 2 || residual.InspectionPaths[0] != stagePath {
		t.Fatalf("inspection paths = %v, want unknown stage first and unsynced recovery second", residual.InspectionPaths)
	}
	if _, statErr := os.Stat(residual.InspectionPaths[1]); statErr != nil {
		t.Fatalf("Stat(unsynced recovery) error: %v", statErr)
	}
	message := residual.Error()
	for _, inspectionPath := range residual.InspectionPaths {
		if !strings.Contains(message, inspectionPath) {
			t.Fatalf("residual error %q does not identify inspection path %q", message, inspectionPath)
		}
	}
}

func TestFileSystemStagedDeleteDoesNotMisidentifyEmptyDirectoryReplacementAsWitness(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/post-rename-directory"
	replacementPath := "/post-rename-directory-new"
	if err := fs.Mkdir(ctx, targetPath); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}
	if err := os.Mkdir(fs.workspace.FullPath(replacementPath), 0o700); err != nil {
		t.Fatalf("Mkdir(replacement) error: %v", err)
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
		if err := os.Remove(stagePath); err != nil {
			return err
		}
		return os.Rename(fs.workspace.FullPath(replacementPath), stagePath)
	}
	t.Cleanup(func() { afterDeleteLeafRename = originalHook })

	err = fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.As(err, &residual) {
		t.Fatalf("Delete() error = %v, want target change and manual recovery residual", err)
	}
	if stagePath == "" || residual.StagePath != "" {
		t.Fatalf("verified recovery residual path = %q, unknown replacement path = %q; want no misidentified path", residual.StagePath, stagePath)
	}
	if len(residual.InspectionPaths) != 1 || residual.InspectionPaths[0] != stagePath {
		t.Fatalf("inspection paths = %v, want unknown stage %q", residual.InspectionPaths, stagePath)
	}
	current, statErr := os.Stat(stagePath)
	if statErr != nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("post-rename directory replacement = %v, %v; want retained", current, statErr)
	}
}

func TestFileSystemStagedDeleteRollsBackWhenPublishedCopyHashFails(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/published-copy-hash-failure.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	wantErr := errors.New("published copy hash failed")
	fs.hashStorageCopiedFile = func(*os.File) (string, error) {
		return "", wantErr
	}

	err := fs.Delete(ctx, targetPath)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Delete() error = %v, want published-copy hash failure", err)
	}
	var residual *copiedDestinationResidualError
	if errors.As(err, &residual) {
		t.Fatalf("Delete() retained copied destination at %q after successful cleanup", residual.path)
	}
	content, readErr := os.ReadFile(fs.workspace.FullPath(targetPath))
	if readErr != nil || string(content) != "intended" {
		t.Fatalf("rolled-back source content = %q, %v; want intended", content, readErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("ListTrash() after hash failure = %+v, %v; want empty", items, listErr)
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

func TestFileSystemStagedTrashDeleteRetainsSourceWhenCopiedContentChangesBeforeRemoval(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/trash-copy-before-removal.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}

	var stagePath string
	originalStageHook := afterDeleteStageCapture
	afterDeleteStageCapture = func(logicalPath, capturedStagePath string) error {
		if logicalPath == targetPath {
			stagePath = capturedStagePath
		}
		return nil
	}
	t.Cleanup(func() { afterDeleteStageCapture = originalStageHook })

	var copiedPath string
	originalCopyHook := afterDeleteTrashCopy
	afterDeleteTrashCopy = func(logicalPath, destinationPath string) error {
		if logicalPath == targetPath {
			copiedPath = destinationPath
		}
		return nil
	}
	t.Cleanup(func() { afterDeleteTrashCopy = originalCopyHook })

	fs.SetPathChangeHooks(nil, func(_ context.Context, logicalPath string) (*PathDeleteHookResult, error) {
		if logicalPath != targetPath || copiedPath == "" {
			return nil, nil
		}
		replacementPath := copiedPath + ".replacement"
		if err := os.WriteFile(replacementPath, []byte("replaced"), 0o600); err != nil {
			return nil, err
		}
		return nil, os.Rename(replacementPath, copiedPath)
	})

	err := fs.Delete(ctx, targetPath)
	var cleanup *DeleteCleanupWarningError
	var residual *DeleteStageResidualError
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.As(err, &cleanup) || !errors.As(err, &residual) {
		t.Fatalf("Delete() error = %v, want committed cleanup warning with retained source", err)
	}
	if stagePath == "" || residual.StagePath == "" {
		t.Fatalf("source residual path = %q, captured stage path = %q", residual.StagePath, stagePath)
	}
	staged, readErr := os.ReadFile(residual.StagePath)
	if readErr != nil || string(staged) != "intended" {
		t.Fatalf("retained staged source = %q, %v; want intended", staged, readErr)
	}
	if _, statErr := os.Stat(stagePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("pre-quarantine stage still exists: %v", statErr)
	}
	replacement, readErr := os.ReadFile(copiedPath)
	if readErr != nil || string(replacement) != "replaced" {
		t.Fatalf("replaced trash content = %q, %v; want retained unknown content", replacement, readErr)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil || len(items) != 1 || filepath.Join(fs.trashRoot, items[0].ID, "content") != copiedPath {
		t.Fatalf("trash metadata after copied-content drift = %+v, %v", items, listErr)
	}
}

func TestFileSystemRestoreFromTrashRejectsRegularSourceReplacementBeforeCopy(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-copy-source-race.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := fs.Delete(ctx, targetPath); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	trashContentPath := filepath.Join(fs.trashRoot, item.ID, "content")
	originalPath := trashContentPath + ".original"
	replacementPath := trashContentPath + ".replacement"
	if err := os.WriteFile(replacementPath, []byte("replaced"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	replacementInfo, err := os.Stat(replacementPath)
	if err != nil {
		t.Fatalf("Stat(replacement) error: %v", err)
	}

	originalHook := afterStorageCopySourceStat
	swapped := false
	afterStorageCopySourceStat = func(sourcePath string) error {
		if swapped || filepath.Clean(sourcePath) != filepath.Clean(trashContentPath) {
			return nil
		}
		swapped = true
		if err := os.Rename(trashContentPath, originalPath); err != nil {
			return err
		}
		return os.Rename(replacementPath, trashContentPath)
	}
	t.Cleanup(func() { afterStorageCopySourceStat = originalHook })

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("RestoreFromTrash() error = %v, want ErrDeleteTargetChanged", err)
	}
	if !swapped {
		t.Fatal("restore source replacement hook was not called")
	}
	if _, statErr := fs.Stat(ctx, targetPath); !errors.Is(statErr, ErrNotFound) {
		t.Fatalf("restored target after rejected source replacement = %v, want not found", statErr)
	}
	current, statErr := os.Stat(trashContentPath)
	if statErr != nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement trash source = %v, %v; want retained", current, statErr)
	}
	original, readErr := os.ReadFile(originalPath)
	if readErr != nil || string(original) != "intended" {
		t.Fatalf("original trash source = %q, %v; want intended", original, readErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after rejected restore = %+v, %v; want original item", items, err)
	}
}

func TestFileSystemRestoreFromTrashRejectsPublishedContentMutationBeforeIdentityBaseline(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-publish-content-race.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := fs.Delete(ctx, targetPath); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	trashContentPath := filepath.Join(fs.trashRoot, item.ID, "content")
	destinationPath := fs.workspace.FullPath(targetPath)

	originalHook := afterCopiedFilePublish
	hookCalled := false
	afterCopiedFilePublish = func(_, copiedPath string) error {
		if filepath.Clean(copiedPath) != filepath.Clean(destinationPath) {
			return nil
		}
		hookCalled = true
		return rewriteSameLengthAndRestoreModTime(copiedPath, []byte("changed!"))
	}
	t.Cleanup(func() { afterCopiedFilePublish = originalHook })

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("RestoreFromTrash() error = %v, want ErrDeleteTargetChanged", err)
	}
	var residual *copiedDestinationResidualError
	if !errors.As(err, &residual) || residual.path != destinationPath {
		t.Fatalf("RestoreFromTrash() error = %v, want copied destination residual for %q", err, destinationPath)
	}
	if !hookCalled {
		t.Fatal("published content mutation hook was not called")
	}
	mutated, readErr := os.ReadFile(destinationPath)
	if readErr != nil || string(mutated) != "changed!" {
		t.Fatalf("mutated published destination = %q, %v; want retained", mutated, readErr)
	}
	source, readErr := os.ReadFile(trashContentPath)
	if readErr != nil || string(source) != "intended" {
		t.Fatalf("trash source after rejected publication = %q, %v; want intended", source, readErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after rejected publication = %+v, %v; want original item", items, err)
	}
}

func TestFileSystemRestoreFromTrashDoesNotRemoveReusedCopyTempName(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-temp-name-reuse.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := fs.Delete(ctx, targetPath); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]

	originalRandomRead := storageRandomRead
	storageRandomRead = func(p []byte) (int, error) {
		for i := range p {
			p[i] = 0xab
		}
		return len(p), nil
	}
	t.Cleanup(func() { storageRandomRead = originalRandomRead })
	tempPath := filepath.Join(fs.workspace.Root(), ".storage-copy-abababababababab.tmp")
	originalHook := afterCopiedFilePublish
	afterCopiedFilePublish = func(_, copiedPath string) error {
		if filepath.Clean(copiedPath) != filepath.Clean(fs.workspace.FullPath(targetPath)) {
			return nil
		}
		return os.WriteFile(tempPath, []byte("external"), 0o600)
	}
	t.Cleanup(func() { afterCopiedFilePublish = originalHook })

	if err := fs.RestoreFromTrash(ctx, item.ID); err != nil {
		t.Fatalf("RestoreFromTrash() error: %v", err)
	}
	content, readErr := os.ReadFile(tempPath)
	if readErr != nil || string(content) != "external" {
		t.Fatalf("reused temp name content = %q, %v; want external object retained", content, readErr)
	}
}

func TestFileSystemRestoreFromTrashToRetainsReplacementUnderCreatedParent(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	originalPath := "/restore-created-parent-source.bin"
	newPath := "/created-parent/restored.bin"
	if err := fs.WriteFile(ctx, originalPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.Delete(ctx, originalPath); err != nil {
		t.Fatalf("Delete(source) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	destinationPath := fs.workspace.FullPath(newPath)
	originalHook := afterCopiedFilePublish
	afterCopiedFilePublish = func(_, copiedPath string) error {
		if filepath.Clean(copiedPath) != filepath.Clean(destinationPath) {
			return nil
		}
		return rewriteSameLengthAndRestoreModTime(copiedPath, []byte("changed!"))
	}
	t.Cleanup(func() { afterCopiedFilePublish = originalHook })

	err = fs.RestoreFromTrashTo(ctx, item.ID, newPath)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("RestoreFromTrashTo() error = %v, want ErrDeleteTargetChanged", err)
	}
	var residual *copiedDestinationResidualError
	if !errors.As(err, &residual) || residual.path != destinationPath {
		t.Fatalf("RestoreFromTrashTo() error = %v, want destination residual %q", err, destinationPath)
	}
	content, readErr := os.ReadFile(destinationPath)
	if readErr != nil || string(content) != "changed!" {
		t.Fatalf("replacement destination = %q, %v; want retained", content, readErr)
	}
	if info, statErr := os.Stat(filepath.Dir(destinationPath)); statErr != nil || !info.IsDir() {
		t.Fatalf("created parent after rejected cleanup = %v, %v; want retained directory", info, statErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after rejected restore = %+v, %v; want original item", items, err)
	}
}

func TestFileSystemRestoreFromTrashTreatsCleanupSyncFailureAsHardFailureWithoutResidual(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-cleanup-sync-failure.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := fs.Delete(ctx, targetPath); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	trashContentPath := filepath.Join(fs.trashRoot, item.ID, "content")
	destinationPath := fs.workspace.FullPath(targetPath)

	wantPublishErr := errors.New("published destination parent sync failed")
	wantCleanupErr := errors.New("destination cleanup parent sync failed")
	originalSync := syncManagedStorageDir
	syncCalls := 0
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		syncCalls++
		switch syncCalls {
		case 1:
			return wantPublishErr
		case 2:
			return wantCleanupErr
		default:
			return originalSync(root, relName, absPath)
		}
	}
	t.Cleanup(func() { syncManagedStorageDir = originalSync })

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, wantPublishErr) || !errors.Is(err, wantCleanupErr) {
		t.Fatalf("RestoreFromTrash() error = %v, want publication and cleanup sync failures", err)
	}
	if isVisibleMutationWarning(err) {
		t.Fatalf("RestoreFromTrash() error = %v, want hard uncommitted failure", err)
	}
	var residual *copiedDestinationResidualError
	if errors.As(err, &residual) {
		t.Fatalf("RestoreFromTrash() reported nonexistent destination residual %q", residual.path)
	}
	if _, statErr := os.Stat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restored destination still exists after cleanup: %v", statErr)
	}
	content, readErr := os.ReadFile(trashContentPath)
	if readErr != nil || string(content) != "intended" {
		t.Fatalf("trash source after hard failure = %q, %v; want intended", content, readErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after hard failure = %+v, %v; want original item", items, err)
	}
}

func TestFileSystemRestoreFromTrashRetainsUnexpectedItemDirectoryEntry(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-item-residue.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := fs.Delete(ctx, targetPath); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	itemDir := filepath.Join(fs.trashRoot, item.ID)
	unexpectedPath := filepath.Join(itemDir, "unexpected.bin")

	originalSync := syncManagedStorageDir
	injected := false
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if !injected && filepath.Clean(absPath) == filepath.Clean(itemDir) {
			injected = true
			if err := os.WriteFile(unexpectedPath, []byte("retain"), 0o600); err != nil {
				return err
			}
		}
		return originalSync(root, relName, absPath)
	}
	t.Cleanup(func() { syncManagedStorageDir = originalSync })

	if err := fs.RestoreFromTrash(ctx, item.ID); err != nil {
		t.Fatalf("RestoreFromTrash() error: %v", err)
	}
	if !injected {
		t.Fatal("unexpected item entry was not injected")
	}
	content, readErr := os.ReadFile(unexpectedPath)
	if readErr != nil || string(content) != "retain" {
		t.Fatalf("unexpected item entry = %q, %v; want retained", content, readErr)
	}
	restored, readErr := os.ReadFile(fs.workspace.FullPath(targetPath))
	if readErr != nil || string(restored) != "intended" {
		t.Fatalf("restored content = %q, %v; want intended", restored, readErr)
	}
	if items, err = fs.ListTrash(ctx); err != nil || len(items) != 0 {
		t.Fatalf("ListTrash() after restore = %+v, %v; want empty metadata", items, err)
	}
}

func TestFileSystemRestoreFromTrashToRetainsReplacedItemDirectoryLeaf(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	originalPath := "/restore-item-leaf-source.bin"
	newPath := "/restore-item-leaf-target.bin"
	if err := fs.WriteFile(ctx, originalPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.Delete(ctx, originalPath); err != nil {
		t.Fatalf("Delete(source) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	itemPath := filepath.Join(fs.trashRoot, item.ID)
	retainedItemPath := itemPath + ".retained"

	originalRemoveMetadata := fs.removeTrashMetadata
	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		if err := originalRemoveMetadata(ctx, id); err != nil {
			return err
		}
		if err := os.Rename(itemPath, retainedItemPath); err != nil {
			return err
		}
		return os.WriteFile(itemPath, []byte("external"), 0o600)
	}

	if err := fs.RestoreFromTrashTo(ctx, item.ID, newPath); err != nil {
		t.Fatalf("RestoreFromTrashTo() error: %v", err)
	}
	replacement, readErr := os.ReadFile(itemPath)
	if readErr != nil || string(replacement) != "external" {
		t.Fatalf("replacement item leaf = %q, %v; want retained", replacement, readErr)
	}
	if info, statErr := os.Stat(retainedItemPath); statErr != nil || !info.IsDir() {
		t.Fatalf("original item directory = %v, %v; want retained", info, statErr)
	}
	restored, readErr := os.ReadFile(fs.workspace.FullPath(newPath))
	if readErr != nil || string(restored) != "intended" {
		t.Fatalf("restored content = %q, %v; want intended", restored, readErr)
	}
}

func TestFileSystemRestoreFromTrashRejectsRegularSourceReplacementBeforeRemoval(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-remove-source-race.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := fs.Delete(ctx, targetPath); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	trashContentPath := filepath.Join(fs.trashRoot, item.ID, "content")
	originalPath := trashContentPath + ".original"
	replacementPath := trashContentPath + ".replacement"
	if err := os.WriteFile(replacementPath, []byte("replaced"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	replacementInfo, err := os.Stat(replacementPath)
	if err != nil {
		t.Fatalf("Stat(replacement) error: %v", err)
	}

	originalIsolationHook := beforeCopiedFileSourceIsolation
	swapped := false
	beforeCopiedFileSourceIsolation = func(sourcePath, _ string) error {
		if !swapped && filepath.Clean(sourcePath) == filepath.Clean(trashContentPath) {
			swapped = true
			if err := os.Rename(trashContentPath, originalPath); err != nil {
				return err
			}
			if err := os.Rename(replacementPath, trashContentPath); err != nil {
				return err
			}
		}
		return nil
	}
	t.Cleanup(func() { beforeCopiedFileSourceIsolation = originalIsolationHook })

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("RestoreFromTrash() error = %v, want ErrDeleteTargetChanged", err)
	}
	if !swapped {
		t.Fatal("restore source replacement hook was not called")
	}
	if _, statErr := fs.Stat(ctx, targetPath); !errors.Is(statErr, ErrNotFound) {
		t.Fatalf("restored target after rejected source replacement = %v, want not found", statErr)
	}
	current, statErr := os.Stat(trashContentPath)
	if statErr != nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement trash source = %v, %v; want retained", current, statErr)
	}
	original, readErr := os.ReadFile(originalPath)
	if readErr != nil || string(original) != "intended" {
		t.Fatalf("original trash source = %q, %v; want intended", original, readErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after rejected restore = %+v, %v; want original item", items, err)
	}
}

func TestFileSystemRestoreFromTrashRetainsSourceWhenPublishedDestinationChanges(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-published-destination-race.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := fs.Delete(ctx, targetPath); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	trashContentPath := filepath.Join(fs.trashRoot, item.ID, "content")
	destinationPath := fs.workspace.FullPath(targetPath)
	publishedPath := destinationPath + ".published"
	replacementPath := destinationPath + ".replacement"
	if err := os.WriteFile(replacementPath, []byte("external destination"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	replacementInfo, err := os.Stat(replacementPath)
	if err != nil {
		t.Fatalf("Stat(replacement) error: %v", err)
	}

	originalHook := afterCopiedFileSourceIsolation
	hookCalled := false
	afterCopiedFileSourceIsolation = func(sourcePath, _ string, copiedPath string) error {
		if filepath.Clean(sourcePath) != filepath.Clean(trashContentPath) || filepath.Clean(copiedPath) != filepath.Clean(destinationPath) {
			return nil
		}
		hookCalled = true
		if err := os.Rename(destinationPath, publishedPath); err != nil {
			return err
		}
		return os.Rename(replacementPath, destinationPath)
	}
	t.Cleanup(func() { afterCopiedFileSourceIsolation = originalHook })

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("RestoreFromTrash() error = %v, want ErrDeleteTargetChanged", err)
	}
	if !hookCalled {
		t.Fatal("published destination replacement hook was not called")
	}
	currentDestination, statErr := os.Stat(destinationPath)
	if statErr != nil || !os.SameFile(replacementInfo, currentDestination) {
		t.Fatalf("replacement destination = %v, %v; want retained", currentDestination, statErr)
	}
	published, readErr := os.ReadFile(publishedPath)
	if readErr != nil || string(published) != "intended" {
		t.Fatalf("published copy moved by test hook = %q, %v; want intended", published, readErr)
	}
	trashSource, readErr := os.ReadFile(trashContentPath)
	if readErr != nil || string(trashSource) != "intended" {
		t.Fatalf("trash source after destination drift = %q, %v; want intended", trashSource, readErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after destination drift = %+v, %v; want original item", items, err)
	}
}

func TestFileSystemRestoreFromTrashReportsBothResidualsWhenIsolatedSourceChanges(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-isolated-source-drift.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := fs.Delete(ctx, targetPath); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	trashContentPath := filepath.Join(fs.trashRoot, item.ID, "content")
	destinationPath := fs.workspace.FullPath(targetPath)
	var isolatedPath string

	originalHook := afterCopiedFileSourceIsolation
	afterCopiedFileSourceIsolation = func(sourcePath, capturedPath, copiedPath string) error {
		if filepath.Clean(sourcePath) != filepath.Clean(trashContentPath) || filepath.Clean(copiedPath) != filepath.Clean(destinationPath) {
			return nil
		}
		isolatedPath = capturedPath
		return rewriteSameLengthAndRestoreModTime(capturedPath, []byte("changed!"))
	}
	t.Cleanup(func() { afterCopiedFileSourceIsolation = originalHook })

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("RestoreFromTrash() error = %v, want ErrDeleteTargetChanged", err)
	}
	var sourceResidual *copiedSourceResidualError
	if !errors.As(err, &sourceResidual) || sourceResidual.path != isolatedPath || isolatedPath == "" {
		t.Fatalf("RestoreFromTrash() error = %v, want source residual %q", err, isolatedPath)
	}
	var destinationResidual *copiedDestinationResidualError
	if !errors.As(err, &destinationResidual) || destinationResidual.path != destinationPath {
		t.Fatalf("RestoreFromTrash() error = %v, want destination residual %q", err, destinationPath)
	}
	isolated, readErr := os.ReadFile(isolatedPath)
	if readErr != nil || string(isolated) != "changed!" {
		t.Fatalf("isolated source = %q, %v; want changed content retained", isolated, readErr)
	}
	destination, readErr := os.ReadFile(destinationPath)
	if readErr != nil || string(destination) != "intended" {
		t.Fatalf("published destination = %q, %v; want verified copy retained", destination, readErr)
	}
	if _, statErr := os.Stat(trashContentPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("original trash content path unexpectedly exists: %v", statErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after residual failure = %+v, %v; want original item", items, err)
	}
}

func TestFileSystemRestoreFromTrashRetainsDestinationWhenSourceRollbackSyncFails(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-source-rollback-sync.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := fs.Delete(ctx, targetPath); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	trashContentPath := filepath.Join(fs.trashRoot, item.ID, "content")
	destinationPath := fs.workspace.FullPath(targetPath)
	wantCause := errors.New("abort after source isolation")
	wantSyncErr := errors.New("source rollback parent sync failed")

	originalSync := syncManagedStorageDir
	originalHook := afterCopiedFileSourceIsolation
	afterCopiedFileSourceIsolation = func(sourcePath, _ string, copiedPath string) error {
		if filepath.Clean(sourcePath) != filepath.Clean(trashContentPath) || filepath.Clean(copiedPath) != filepath.Clean(destinationPath) {
			return nil
		}
		syncManagedStorageDir = func(*os.Root, string, string) error { return wantSyncErr }
		return wantCause
	}
	t.Cleanup(func() {
		afterCopiedFileSourceIsolation = originalHook
		syncManagedStorageDir = originalSync
	})

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, wantCause) || !errors.Is(err, wantSyncErr) {
		t.Fatalf("RestoreFromTrash() error = %v, want abort and rollback sync failures", err)
	}
	var sourceResidual *copiedSourceResidualError
	if !errors.As(err, &sourceResidual) || sourceResidual.path != trashContentPath {
		t.Fatalf("RestoreFromTrash() error = %v, want source residual %q", err, trashContentPath)
	}
	var destinationResidual *copiedDestinationResidualError
	if !errors.As(err, &destinationResidual) || destinationResidual.path != destinationPath {
		t.Fatalf("RestoreFromTrash() error = %v, want destination residual %q", err, destinationPath)
	}
	source, readErr := os.ReadFile(trashContentPath)
	if readErr != nil || string(source) != "intended" {
		t.Fatalf("restored trash source = %q, %v; want intended", source, readErr)
	}
	destination, readErr := os.ReadFile(destinationPath)
	if readErr != nil || string(destination) != "intended" {
		t.Fatalf("retained destination = %q, %v; want intended", destination, readErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after rollback warning = %+v, %v; want original item", items, err)
	}
}

func TestFileSystemRestoreFromTrashDoesNotRemoveReplacedDestinationDuringSourceRollback(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-cleanup-destination-race.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := fs.Delete(ctx, targetPath); err != nil {
		t.Fatalf("Delete(target) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	item := items[0]
	trashContentPath := filepath.Join(fs.trashRoot, item.ID, "content")
	originalTrashPath := trashContentPath + ".original"
	replacementTrashPath := trashContentPath + ".replacement"
	if err := os.WriteFile(replacementTrashPath, []byte("replacement source"), 0o600); err != nil {
		t.Fatalf("WriteFile(source replacement) error: %v", err)
	}

	destinationPath := fs.workspace.FullPath(targetPath)
	copiedDestinationPath := destinationPath + ".copied"
	replacementDestinationPath := destinationPath + ".replacement"
	if err := os.WriteFile(replacementDestinationPath, []byte("external destination"), 0o600); err != nil {
		t.Fatalf("WriteFile(destination replacement) error: %v", err)
	}
	replacementDestinationInfo, err := os.Stat(replacementDestinationPath)
	if err != nil {
		t.Fatalf("Stat(destination replacement) error: %v", err)
	}

	originalIsolationHook := beforeCopiedFileSourceIsolation
	beforeCopiedFileSourceIsolation = func(sourcePath, _ string) error {
		if filepath.Clean(sourcePath) == filepath.Clean(trashContentPath) {
			if err := os.Rename(trashContentPath, originalTrashPath); err != nil {
				return err
			}
			if err := os.Rename(replacementTrashPath, trashContentPath); err != nil {
				return err
			}
		}
		return nil
	}
	t.Cleanup(func() { beforeCopiedFileSourceIsolation = originalIsolationHook })

	originalCleanupHook := beforeCopiedFileDestinationCleanup
	cleanupHookCalled := false
	beforeCopiedFileDestinationCleanup = func(path string) error {
		if filepath.Clean(path) != filepath.Clean(destinationPath) {
			return nil
		}
		cleanupHookCalled = true
		if err := os.Rename(destinationPath, copiedDestinationPath); err != nil {
			return err
		}
		return os.Rename(replacementDestinationPath, destinationPath)
	}
	t.Cleanup(func() { beforeCopiedFileDestinationCleanup = originalCleanupHook })

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("RestoreFromTrash() error = %v, want ErrDeleteTargetChanged", err)
	}
	if !cleanupHookCalled {
		t.Fatal("copied destination cleanup hook was not called")
	}
	currentDestination, statErr := os.Stat(destinationPath)
	if statErr != nil || !os.SameFile(replacementDestinationInfo, currentDestination) {
		t.Fatalf("replacement destination = %v, %v; want retained", currentDestination, statErr)
	}
	copied, readErr := os.ReadFile(copiedDestinationPath)
	if readErr != nil || string(copied) != "intended" {
		t.Fatalf("published copy moved by test hook = %q, %v; want intended", copied, readErr)
	}
	trashReplacement, readErr := os.ReadFile(trashContentPath)
	if readErr != nil || string(trashReplacement) != "replacement source" {
		t.Fatalf("replacement trash source = %q, %v; want retained", trashReplacement, readErr)
	}
	originalTrash, readErr := os.ReadFile(originalTrashPath)
	if readErr != nil || string(originalTrash) != "intended" {
		t.Fatalf("original trash source = %q, %v; want intended", originalTrash, readErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after rejected cleanup = %+v, %v; want original item", items, err)
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
