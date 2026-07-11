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

	"github.com/seanbao/mnemonas/internal/versionstore"
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
	if !errors.Is(err, wantErr) || !errors.Is(err, ErrDeleteTargetChanged) || errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Delete() error = %v, want published-copy hash failure with safe rollback", err)
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
	if fs.trashMutationBlocked != nil {
		t.Fatalf("safe owned-container rollback left mutation gate: %v", fs.trashMutationBlocked)
	}
	fs.hashStorageCopiedFile = nil
	if _, recoveryErr := fs.RecoverTrashTransfers(ctx); recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after safe rollback error: %v", recoveryErr)
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

			deleteErr := fs.Delete(ctx, targetPath)
			if permanent && deleteErr != nil {
				t.Fatalf("Delete() error: %v", deleteErr)
			}
			var recovery *TrashTransferRecoveryRequiredError
			if !permanent {
				recovery = requireJournaledTrashRecovery(t, fs, deleteErr)
			}
			data, err := os.ReadFile(fs.workspace.FullPath(targetPath))
			if err != nil || !bytes.Equal(data, newContent) {
				t.Fatalf("new original content = %q, %v", data, err)
			}
			if stagePath == "" {
				t.Fatal("stage capture hook was not called")
			}
			if permanent {
				if _, err := os.Stat(stagePath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("committed stage still exists: %v", err)
				}
			} else {
				staged, readErr := os.ReadFile(stagePath)
				if readErr != nil || string(staged) != "intended" {
					t.Fatalf("committed source stage = %q, %v; want intended", staged, readErr)
				}
				if got := journaledTrashWorkspaceStagePath(t, fs, recovery); got != stagePath {
					t.Fatalf("recovery workspace stage = %q, want %q", got, stagePath)
				}
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
			if !permanent {
				requireJournaledTrashMutationGate(t, fs)
				if err := os.Remove(fs.workspace.FullPath(targetPath)); err != nil {
					t.Fatalf("Remove(new original before recovery) error: %v", err)
				}
				report, recoveryErr := fs.RecoverTrashTransfers(ctx)
				if recoveryErr != nil {
					t.Fatalf("RecoverTrashTransfers() after removing new original error: %v", recoveryErr)
				}
				if report.RolledForward != 1 || len(report.Blocked) != 0 {
					t.Fatalf("RecoverTrashTransfers() report = %+v, want one roll-forward", report)
				}
				if _, err := os.Stat(stagePath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("source stage after recovery exists: %v", err)
				}
			}
		})
	}
}

func TestFileSystemCommittedTrashDeletePreservesOccupiedOriginalAndStage(t *testing.T) {
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
	err = fs.Delete(ctx, targetPath)
	recovery := requireJournaledTrashRecovery(t, fs, err)
	data, readErr := os.ReadFile(fs.workspace.FullPath(targetPath))
	if readErr != nil || !bytes.Equal(data, newContent) {
		t.Fatalf("occupied original content = %q, %v", data, readErr)
	}
	stagedInfo, statErr := os.Stat(stagePath)
	if statErr != nil || !os.SameFile(originalInfo, stagedInfo) {
		t.Fatalf("intended inode was not retained at stage: info=%v err=%v", stagedInfo, statErr)
	}
	if got := journaledTrashWorkspaceStagePath(t, fs, recovery); got != stagePath {
		t.Fatalf("recovery workspace stage = %q, want %q", got, stagePath)
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
	requireJournaledTrashMutationGate(t, fs)
	if err := os.Remove(fs.workspace.FullPath(targetPath)); err != nil {
		t.Fatalf("Remove(occupied original before recovery) error: %v", err)
	}
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after occupied-path repair error: %v", recoveryErr)
	}
	if report.RolledForward != 1 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one roll-forward", report)
	}
	if _, statErr := os.Stat(stagePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("source stage after recovery exists: %v", statErr)
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
	var recoveryRequired *TrashTransferRecoveryRequiredError
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.Is(err, ErrTrashRecoveryRequired) ||
		!errors.As(err, &residual) || !errors.As(err, &recoveryRequired) {
		t.Fatalf("Delete() error = %v, want target changed, staged residual, and recovery gate", err)
	}
	current, statErr := os.Stat(stagePath)
	if statErr != nil || replacementInfo == nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement stage source was removed: info=%v err=%v", current, statErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash after rejected copied source = %+v, %v; want empty", items, listErr)
	}
	if residual.StagePath == "" {
		t.Fatalf("DeleteStageResidualError = %+v, want durable witness recovery path", residual)
	}
	recovered, readErr := os.ReadFile(residual.StagePath)
	if readErr != nil || string(recovered) != "intended" {
		t.Fatalf("witness recovery content = %q, %v; want intended", recovered, readErr)
	}
	foundRecoveryPath := false
	for _, inspectionPath := range recoveryRequired.InspectionPaths {
		if inspectionPath == residual.StagePath {
			foundRecoveryPath = true
			break
		}
	}
	if !foundRecoveryPath {
		t.Fatalf("recovery inspection paths = %v, want %s", recoveryRequired.InspectionPaths, residual.StagePath)
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
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.As(err, &residual) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Delete() error = %v, want target change, destination residual, and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	if copiedPath == "" || residual.StagePath != copiedPath {
		t.Fatalf("destination residual path = %q, copied path = %q", residual.StagePath, copiedPath)
	}
	injected, readErr := os.ReadFile(filepath.Join(copiedPath, "injected.bin"))
	if readErr != nil || string(injected) != "replacement" {
		t.Fatalf("injected destination = %q, %v; want retained", injected, readErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/trash-destination-tree")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("original source after fail-closed destination drift = %v, want os.ErrNotExist", statErr)
	}
	workspaceStage := journaledTrashWorkspaceStagePath(t, fs, recovery)
	if original, readErr := os.ReadFile(filepath.Join(workspaceStage, "child.bin")); readErr != nil || string(original) != "intended" {
		t.Fatalf("staged source = %q, %v", original, readErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash metadata after rejected destination = %+v, %v; want empty", items, listErr)
	}
	requireJournaledTrashMutationGate(t, fs)
	repairPreparedTrashTransferAndRecover(t, fs, recovery)
	if original, readErr := os.ReadFile(fs.workspace.FullPath("/trash-destination-tree/child.bin")); readErr != nil || string(original) != "intended" {
		t.Fatalf("source after repaired recovery = %q, %v", original, readErr)
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
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.As(err, &residual) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Delete() error = %v, want target change, copied-content residual, and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	if copiedPath == "" || residual.StagePath != copiedPath {
		t.Fatalf("destination residual path = %q, copied path = %q", residual.StagePath, copiedPath)
	}
	current, statErr := os.Stat(copiedPath)
	if statErr != nil || replacementInfo == nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement destination was not retained: info=%v err=%v", current, statErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath(targetPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("original source after fail-closed destination replacement = %v, want os.ErrNotExist", statErr)
	}
	workspaceStage := journaledTrashWorkspaceStagePath(t, fs, recovery)
	if original, readErr := os.ReadFile(workspaceStage); readErr != nil || string(original) != "intended" {
		t.Fatalf("staged source = %q, %v", original, readErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash metadata after replaced destination = %+v, %v; want empty", items, listErr)
	}
	requireJournaledTrashMutationGate(t, fs)
	repairPreparedTrashTransferAndRecover(t, fs, recovery)
	if original, readErr := os.ReadFile(fs.workspace.FullPath(targetPath)); readErr != nil || string(original) != "intended" {
		t.Fatalf("source after repaired recovery = %q, %v", original, readErr)
	}
}

func TestFileSystemJournaledTrashDeleteRetainsSourceWhenCanonicalContentChangesBeforeRemoval(t *testing.T) {
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

	var canonicalContentPath string
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(context.Context, string, string) ([]byte, error) {
			return []byte("participant"), nil
		},
		ApplyDelete: func(_ context.Context, _ string, logicalPath string, _ []byte, committed bool) error {
			if logicalPath != targetPath || !committed {
				return nil
			}
			entries, err := os.ReadDir(fs.trashRoot)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.IsDir() && entry.Name() != trashTransferJournalDir {
					canonicalContentPath = filepath.Join(fs.trashRoot, entry.Name(), "content")
					break
				}
			}
			if canonicalContentPath == "" {
				return errors.New("canonical Trash content was not published")
			}
			replacementPath := canonicalContentPath + ".replacement"
			if err := os.WriteFile(replacementPath, []byte("replaced"), 0o600); err != nil {
				return err
			}
			return os.Rename(replacementPath, canonicalContentPath)
		},
		RollbackDelete:        func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error { return nil },
	})

	err := fs.Delete(ctx, targetPath)
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Delete() error = %v, want committed canonical drift and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	if stagePath == "" || canonicalContentPath == "" {
		t.Fatalf("captured paths = source stage %q, canonical content %q", stagePath, canonicalContentPath)
	}
	if got := journaledTrashWorkspaceStagePath(t, fs, recovery); got != stagePath {
		t.Fatalf("recovery workspace stage = %q, want %q", got, stagePath)
	}
	staged, readErr := os.ReadFile(stagePath)
	if readErr != nil || string(staged) != "intended" {
		t.Fatalf("retained staged source = %q, %v; want intended", staged, readErr)
	}
	replacement, readErr := os.ReadFile(canonicalContentPath)
	if readErr != nil || string(replacement) != "replaced" {
		t.Fatalf("replaced trash content = %q, %v; want retained unknown content", replacement, readErr)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil || len(items) != 1 || filepath.Join(fs.trashRoot, items[0].ID, "content") != canonicalContentPath {
		t.Fatalf("trash metadata after copied-content drift = %+v, %v", items, listErr)
	}
	requireJournaledTrashMutationGate(t, fs)
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
	replacementPath := filepath.Join(t.TempDir(), "replacement")
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
		if swapped {
			return nil
		}
		if filepath.Clean(sourcePath) != filepath.Clean(trashContentPath) {
			t.Errorf("copy source path = %q, want %q", sourcePath, trashContentPath)
		}
		swapped = true
		if err := os.Rename(trashContentPath, originalPath); err != nil {
			return err
		}
		return os.Rename(replacementPath, trashContentPath)
	}
	t.Cleanup(func() { afterStorageCopySourceStat = originalHook })

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, want source drift and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
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
	requireJournaledTrashMutationGate(t, fs)
	if err := os.Remove(trashContentPath); err != nil {
		t.Fatalf("Remove(replacement source) error: %v", err)
	}
	if err := os.Rename(originalPath, trashContentPath); err != nil {
		t.Fatalf("Rename(original source back) error: %v", err)
	}
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after source repair error: %v", recoveryErr)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}
	for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
		if _, statErr := fs.trashRootHandle.Lstat(filepath.FromSlash(trashTransferJournalRel(recovery.OperationID, decision))); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("Lstat(%s journal) error = %v, want os.ErrNotExist", decision, statErr)
		}
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
	workspaceStagePath := ""
	afterCopiedFilePublish = func(_, copiedPath string) error {
		if hookCalled {
			return nil
		}
		hookCalled = true
		workspaceStagePath = copiedPath
		return rewriteSameLengthAndRestoreModTime(copiedPath, []byte("changed!"))
	}
	t.Cleanup(func() { afterCopiedFilePublish = originalHook })

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, ErrDeleteTargetChanged) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, want changed workspace stage and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	var residual *copiedDestinationResidualError
	if !errors.As(err, &residual) || residual.path != workspaceStagePath {
		t.Fatalf("RestoreFromTrash() error = %v, want copied stage residual for %q", err, workspaceStagePath)
	}
	if !hookCalled {
		t.Fatal("published content mutation hook was not called")
	}
	mutated, readErr := os.ReadFile(workspaceStagePath)
	if readErr != nil || string(mutated) != "changed!" {
		t.Fatalf("mutated workspace stage = %q, %v; want retained", mutated, readErr)
	}
	if got := journaledTrashWorkspaceStagePath(t, fs, recovery); got != filepath.Dir(workspaceStagePath) {
		t.Fatalf("recovery container = %q, want %q", got, filepath.Dir(workspaceStagePath))
	}
	if _, statErr := os.Stat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore destination exists after staged-copy rejection: %v", statErr)
	}
	source, readErr := os.ReadFile(trashContentPath)
	if readErr != nil || string(source) != "intended" {
		t.Fatalf("trash source after rejected publication = %q, %v; want intended", source, readErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after rejected publication = %+v, %v; want original item", items, err)
	}
	requireJournaledTrashMutationGate(t, fs)
	if err := os.Remove(workspaceStagePath); err != nil {
		t.Fatalf("Remove(inspected workspace stage) error: %v", err)
	}
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after removing unsafe stage error: %v", recoveryErr)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
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
	hookCalled := false
	afterCopiedFilePublish = func(_, copiedPath string) error {
		if hookCalled {
			return nil
		}
		hookCalled = true
		return os.WriteFile(tempPath, []byte("external"), 0o600)
	}
	t.Cleanup(func() { afterCopiedFilePublish = originalHook })

	if err := fs.RestoreFromTrash(ctx, item.ID); err != nil {
		t.Fatalf("RestoreFromTrash() error: %v", err)
	}
	if !hookCalled {
		t.Fatal("workspace stage publication hook was not called")
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
	hookCalled := false
	workspaceStagePath := ""
	afterCopiedFilePublish = func(_, copiedPath string) error {
		if hookCalled {
			return nil
		}
		hookCalled = true
		workspaceStagePath = copiedPath
		return os.WriteFile(destinationPath, []byte("changed!"), 0o600)
	}
	t.Cleanup(func() { afterCopiedFilePublish = originalHook })

	err = fs.RestoreFromTrashTo(ctx, item.ID, newPath)
	if !errors.Is(err, ErrAlreadyExists) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrashTo() error = %v, want occupied target and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	if !hookCalled {
		t.Fatal("workspace stage publication hook was not called")
	}
	content, readErr := os.ReadFile(destinationPath)
	if readErr != nil || string(content) != "changed!" {
		t.Fatalf("replacement destination = %q, %v; want retained", content, readErr)
	}
	staged, readErr := os.ReadFile(workspaceStagePath)
	if readErr != nil || string(staged) != "intended" {
		t.Fatalf("workspace stage = %q, %v; want intended restore content", staged, readErr)
	}
	wantStagePath := filepath.Join(filepath.Dir(destinationPath), ".mnemonas-trash-transfer-"+recovery.OperationID+".stage", "content")
	if filepath.Clean(workspaceStagePath) != filepath.Clean(wantStagePath) {
		t.Fatalf("workspace stage = %q, want %q", workspaceStagePath, wantStagePath)
	}
	if info, statErr := os.Stat(filepath.Dir(destinationPath)); statErr != nil || !info.IsDir() {
		t.Fatalf("created parent after rejected cleanup = %v, %v; want retained directory", info, statErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after rejected restore = %+v, %v; want original item", items, err)
	}
	requireJournaledTrashMutationGate(t, fs)
	if err := os.Remove(destinationPath); err != nil {
		t.Fatalf("Remove(external destination) error: %v", err)
	}
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after target removal error: %v", recoveryErr)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}
	if _, statErr := os.Stat(workspaceStagePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("workspace stage after recovery exists: %v", statErr)
	}
}

func TestFileSystemRestoreFromTrashRecoversCommittedSourceDirectorySyncFailure(t *testing.T) {
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
	trashItemPath := filepath.Join(fs.trashRoot, item.ID)
	destinationPath := fs.workspace.FullPath(targetPath)

	wantSyncErr := errors.New("Trash source parent sync failed")
	originalSync := syncManagedStorageDir
	syncFailed := false
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if !syncFailed && root == fs.trashRootHandle && relName == "." && filepath.Clean(absPath) == filepath.Clean(fs.trashRoot) {
			if _, itemErr := root.Lstat(item.ID); errors.Is(itemErr, os.ErrNotExist) {
				syncFailed = true
				return wantSyncErr
			}
		}
		return originalSync(root, relName, absPath)
	}
	t.Cleanup(func() { syncManagedStorageDir = originalSync })

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, wantSyncErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, want committed source sync failure and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	if !syncFailed {
		t.Fatal("Trash source parent sync failure was not injected")
	}
	content, readErr := os.ReadFile(destinationPath)
	if readErr != nil || string(content) != "intended" {
		t.Fatalf("committed restore destination = %q, %v; want intended", content, readErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("ListTrash() after committed restore = %+v, %v; want empty", items, err)
	}
	if _, statErr := os.Stat(trashItemPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Trash source after failed parent sync exists: %v", statErr)
	}
	operations, operationErr := fs.versions.ListTrashOperations(ctx)
	if operationErr != nil || len(operations) != 1 || operations[0].ID != recovery.OperationID {
		t.Fatalf("ListTrashOperations() = %+v, %v; want pending restore marker %s", operations, operationErr, recovery.OperationID)
	}
	requireJournaledTrashMutationGate(t, fs)
	syncManagedStorageDir = originalSync
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", recoveryErr)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || report.Completed != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one roll-forward", report)
	}
	operations, operationErr = fs.versions.ListTrashOperations(ctx)
	if operationErr != nil || len(operations) != 0 {
		t.Fatalf("ListTrashOperations() after recovery = %+v, %v; want empty", operations, operationErr)
	}
}

func TestFileSystemRestoreFromTrashRetainsUnexpectedItemDirectoryEntry(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-item-residue.bin"
	participantPayload := []byte(`{"participant":"unexpected-item-entry"}`)
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(context.Context, string, string) ([]byte, error) {
			return append([]byte(nil), participantPayload...), nil
		},
		ApplyDelete:           func(context.Context, string, string, []byte, bool) error { return nil },
		RollbackDelete:        func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error { return nil },
	})
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

	applyCalls := 0
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		CompleteRestore: completeRestoreParticipantForTest,
		ApplyRestore: func(_ context.Context, operationID, originalPath, restoredPath string, payload []byte) error {
			applyCalls++
			if !validTrashPurgeOperationID(operationID) || originalPath != targetPath || restoredPath != targetPath || !bytes.Equal(payload, participantPayload) {
				t.Errorf("ApplyRestore(%q, %q, %q, %q) received unexpected state", operationID, originalPath, restoredPath, payload)
			}
			if applyCalls == 1 {
				return os.WriteFile(unexpectedPath, []byte("retain"), 0o600)
			}
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})

	restoreErr := fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(restoreErr, ErrTrashRecoveryRequired) || !errors.Is(restoreErr, ErrDeleteTargetChanged) {
		t.Fatalf("RestoreFromTrash() error = %v, want unexpected source entry and recovery gate", restoreErr)
	}
	recovery := requireJournaledTrashRecovery(t, fs, restoreErr)
	if applyCalls != 1 {
		t.Fatalf("ApplyRestore calls = %d, want 1 before recovery", applyCalls)
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
		t.Fatalf("ListTrash() after committed restore = %+v, %v; want empty metadata", items, err)
	}
	if err := os.Remove(unexpectedPath); err != nil {
		t.Fatalf("Remove(unexpected item entry) error: %v", err)
	}
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after entry removal error: %v", recoveryErr)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one roll-forward", report)
	}
	if applyCalls != 2 {
		t.Fatalf("ApplyRestore calls = %d, want at-least-once replay", applyCalls)
	}
	if _, statErr := os.Stat(itemDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Trash item after recovery exists: %v", statErr)
	}
	if _, operationErr := fs.versions.GetTrashOperation(ctx, recovery.OperationID); !errors.Is(operationErr, versionstore.ErrNotFound) {
		t.Fatalf("GetTrashOperation() after recovery error = %v, want ErrNotFound", operationErr)
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

	originalCommit := fs.commitTrashRestore
	commitCalls := 0
	fs.commitTrashRestore = func(callCtx context.Context, committedItem *versionstore.TrashItem, destinationPath string, fileIndex []versionstore.FileIndexEntry, renameHistory bool, operation *versionstore.TrashOperation) error {
		commitCalls++
		if err := originalCommit(callCtx, committedItem, destinationPath, fileIndex, renameHistory, operation); err != nil {
			return err
		}
		if err := os.Rename(itemPath, retainedItemPath); err != nil {
			return err
		}
		return os.WriteFile(itemPath, []byte("external"), 0o600)
	}

	restoreErr := fs.RestoreFromTrashTo(ctx, item.ID, newPath)
	if !errors.Is(restoreErr, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrashTo() error = %v, want replaced Trash item recovery gate", restoreErr)
	}
	recovery := requireJournaledTrashRecovery(t, fs, restoreErr)
	if commitCalls != 1 {
		t.Fatalf("commitTrashRestore calls = %d, want 1", commitCalls)
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
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("ListTrash() after committed restore = %+v, %v; want empty", items, listErr)
	}
	if err := os.Remove(itemPath); err != nil {
		t.Fatalf("Remove(replacement item leaf) error: %v", err)
	}
	if err := os.Rename(retainedItemPath, itemPath); err != nil {
		t.Fatalf("Rename(original item back) error: %v", err)
	}
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after item repair error: %v", recoveryErr)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one roll-forward", report)
	}
	if _, statErr := os.Stat(itemPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Trash source after recovery exists: %v", statErr)
	}
	if _, operationErr := fs.versions.GetTrashOperation(ctx, recovery.OperationID); !errors.Is(operationErr, versionstore.ErrNotFound) {
		t.Fatalf("GetTrashOperation() after recovery error = %v, want ErrNotFound", operationErr)
	}
}

func TestFileSystemRestoreFromTrashRejectsRegularSourceReplacementBeforeRemoval(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-remove-source-race.bin"
	participantPayload := []byte(`{"participant":"source-replacement"}`)
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(context.Context, string, string) ([]byte, error) {
			return append([]byte(nil), participantPayload...), nil
		},
		ApplyDelete:           func(context.Context, string, string, []byte, bool) error { return nil },
		RollbackDelete:        func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error { return nil },
	})
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
	replacementPath := filepath.Join(t.TempDir(), "replacement")
	if err := os.WriteFile(replacementPath, []byte("replaced"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	replacementInfo, err := os.Stat(replacementPath)
	if err != nil {
		t.Fatalf("Stat(replacement) error: %v", err)
	}

	participantErr := errors.New("restore participant stopped after replacing source")
	swapped := false
	applyCalls := 0
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		CompleteRestore: completeRestoreParticipantForTest,
		ApplyRestore: func(_ context.Context, operationID, originalLogicalPath, restoredPath string, payload []byte) error {
			applyCalls++
			if !validTrashPurgeOperationID(operationID) || originalLogicalPath != targetPath || restoredPath != targetPath || !bytes.Equal(payload, participantPayload) {
				t.Errorf("ApplyRestore(%q, %q, %q, %q) received unexpected state", operationID, originalLogicalPath, restoredPath, payload)
			}
			if !swapped {
				swapped = true
				if err := os.Rename(trashContentPath, originalPath); err != nil {
					return err
				}
				if err := os.Rename(replacementPath, trashContentPath); err != nil {
					return err
				}
				return participantErr
			}
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, participantErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, want participant failure and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	if !swapped {
		t.Fatal("restore participant did not replace the source")
	}
	if applyCalls != 1 {
		t.Fatalf("ApplyRestore calls = %d, want 1 before recovery", applyCalls)
	}
	if restored, readErr := os.ReadFile(fs.workspace.FullPath(targetPath)); readErr != nil || string(restored) != "intended" {
		t.Fatalf("committed restore destination = %q, %v; want intended", restored, readErr)
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
	if err != nil || len(items) != 0 {
		t.Fatalf("ListTrash() after committed restore = %+v, %v; want empty", items, err)
	}
	if err := os.Remove(trashContentPath); err != nil {
		t.Fatalf("Remove(replacement Trash content) error: %v", err)
	}
	if err := os.Rename(originalPath, trashContentPath); err != nil {
		t.Fatalf("Rename(original Trash content back) error: %v", err)
	}
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		CompleteRestore: completeRestoreParticipantForTest,
		ApplyRestore: func(_ context.Context, operationID, originalLogicalPath, restoredPath string, payload []byte) error {
			applyCalls++
			if operationID != recovery.OperationID || originalLogicalPath != targetPath || restoredPath != targetPath || !bytes.Equal(payload, participantPayload) {
				t.Errorf("replayed ApplyRestore(%q, %q, %q, %q) received unexpected state", operationID, originalLogicalPath, restoredPath, payload)
			}
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after source repair error: %v", recoveryErr)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one roll-forward", report)
	}
	if applyCalls != 2 {
		t.Fatalf("ApplyRestore calls = %d, want at-least-once replay", applyCalls)
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, item.ID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Trash source after recovery exists: %v", statErr)
	}
}

func TestFileSystemRestoreFromTrashRetainsSourceWhenPublishedDestinationChanges(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-published-destination-race.bin"
	participantPayload := []byte(`{"participant":"destination-replacement"}`)
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(context.Context, string, string) ([]byte, error) {
			return append([]byte(nil), participantPayload...), nil
		},
		ApplyDelete:           func(context.Context, string, string, []byte, bool) error { return nil },
		RollbackDelete:        func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error { return nil },
	})
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

	participantErr := errors.New("restore participant stopped after replacing destination")
	hookCalled := false
	applyCalls := 0
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		CompleteRestore: completeRestoreParticipantForTest,
		ApplyRestore: func(_ context.Context, operationID, originalPath, restoredPath string, payload []byte) error {
			applyCalls++
			if !validTrashPurgeOperationID(operationID) || originalPath != targetPath || restoredPath != targetPath || !bytes.Equal(payload, participantPayload) {
				t.Errorf("ApplyRestore(%q, %q, %q, %q) received unexpected state", operationID, originalPath, restoredPath, payload)
			}
			hookCalled = true
			if err := os.Rename(destinationPath, publishedPath); err != nil {
				return err
			}
			if err := os.Rename(replacementPath, destinationPath); err != nil {
				return err
			}
			return participantErr
		},
		RecoveryStateReliable: func() error { return nil },
	})

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, participantErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, want participant failure and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	if !hookCalled {
		t.Fatal("restore participant did not replace the destination")
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
	if applyCalls != 1 {
		t.Fatalf("ApplyRestore calls = %d, want 1 before recovery", applyCalls)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("ListTrash() after committed destination drift = %+v, %v; want empty", items, err)
	}
	firstReport, firstRecoveryErr := fs.RecoverTrashTransfers(ctx)
	if !errors.Is(firstRecoveryErr, ErrTrashRecoveryRequired) || len(firstReport.Blocked) == 0 {
		t.Fatalf("RecoverTrashTransfers() before destination repair = %+v, %v; want blocked recovery", firstReport, firstRecoveryErr)
	}
	if applyCalls != 1 {
		t.Fatalf("ApplyRestore calls after failed verification = %d, want 1", applyCalls)
	}
	if err := os.Remove(destinationPath); err != nil {
		t.Fatalf("Remove(replacement destination) error: %v", err)
	}
	if err := os.Rename(publishedPath, destinationPath); err != nil {
		t.Fatalf("Rename(intended destination back) error: %v", err)
	}
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		CompleteRestore: completeRestoreParticipantForTest,
		ApplyRestore: func(_ context.Context, operationID, originalPath, restoredPath string, payload []byte) error {
			applyCalls++
			if operationID != recovery.OperationID || originalPath != targetPath || restoredPath != targetPath || !bytes.Equal(payload, participantPayload) {
				t.Errorf("replayed ApplyRestore(%q, %q, %q, %q) received unexpected state", operationID, originalPath, restoredPath, payload)
			}
			return nil
		},
		RecoveryStateReliable: func() error { return nil },
	})
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after destination repair error: %v", recoveryErr)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one roll-forward", report)
	}
	if applyCalls != 2 {
		t.Fatalf("ApplyRestore calls = %d, want at-least-once replay", applyCalls)
	}
	if _, statErr := os.Stat(filepath.Join(fs.trashRoot, item.ID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Trash source after recovery exists: %v", statErr)
	}
}

func TestFileSystemRestoreFromTrashResolvesErrorReturnedAfterCommit(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-commit-resolution.bin"
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
	trashItemPath := filepath.Join(fs.trashRoot, item.ID)
	destinationPath := fs.workspace.FullPath(targetPath)
	originalCommit := fs.commitTrashRestore
	commitReturnedErr := errors.New("commit acknowledgement unavailable")
	commitCalls := 0
	fs.commitTrashRestore = func(callCtx context.Context, committedItem *versionstore.TrashItem, restoredPath string, fileIndex []versionstore.FileIndexEntry, renameHistory bool, operation *versionstore.TrashOperation) error {
		commitCalls++
		if err := originalCommit(callCtx, committedItem, restoredPath, fileIndex, renameHistory, operation); err != nil {
			return err
		}
		return commitReturnedErr
	}

	if err := fs.RestoreFromTrash(ctx, item.ID); err != nil {
		t.Fatalf("RestoreFromTrash() after resolvable commit error: %v", err)
	}
	if commitCalls != 1 {
		t.Fatalf("commitTrashRestore calls = %d, want 1", commitCalls)
	}
	destination, readErr := os.ReadFile(destinationPath)
	if readErr != nil || string(destination) != "intended" {
		t.Fatalf("restored destination = %q, %v; want intended", destination, readErr)
	}
	if _, statErr := os.Stat(trashItemPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Trash source after completed restore exists: %v", statErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("ListTrash() after completed restore = %+v, %v; want empty", items, err)
	}
	operations, operationErr := fs.versions.ListTrashOperations(ctx)
	if operationErr != nil || len(operations) != 0 {
		t.Fatalf("ListTrashOperations() after completed restore = %+v, %v; want empty", operations, operationErr)
	}
}

func TestFileSystemRestoreFromTrashRollsBackReadyCheckpointAfterCommitFailure(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/restore-clean-commit-failure.bin"
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
	wantCommitErr := errors.New("commit restore metadata failed")
	commitCalls := 0
	fs.commitTrashRestore = func(context.Context, *versionstore.TrashItem, string, []versionstore.FileIndexEntry, bool, *versionstore.TrashOperation) error {
		commitCalls++
		return wantCommitErr
	}

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, wantCommitErr) || errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, want clean commit failure without recovery gate", err)
	}
	if commitCalls != 1 {
		t.Fatalf("commitTrashRestore calls = %d, want 1", commitCalls)
	}
	source, readErr := os.ReadFile(trashContentPath)
	if readErr != nil || string(source) != "intended" {
		t.Fatalf("Trash source after rollback = %q, %v; want intended", source, readErr)
	}
	if _, statErr := os.Stat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore destination after clean rollback exists: %v", statErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after clean rollback = %+v, %v; want original item", items, err)
	}
	operations, operationErr := fs.versions.ListTrashOperations(ctx)
	if operationErr != nil || len(operations) != 0 {
		t.Fatalf("ListTrashOperations() after clean rollback = %+v, %v; want empty", operations, operationErr)
	}
	if err := fs.Mkdir(ctx, "/after-clean-restore-rollback"); err != nil {
		t.Fatalf("Mkdir() after clean rollback error: %v", err)
	}
}

func TestFileSystemRestoreFromTrashRetainsReplacedDestinationDuringPrecommitRollback(t *testing.T) {
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
	destinationPath := fs.workspace.FullPath(targetPath)
	intendedDestinationPath := destinationPath + ".intended"
	replacementDestinationPath := destinationPath + ".replacement"
	if err := os.WriteFile(replacementDestinationPath, []byte("external destination"), 0o600); err != nil {
		t.Fatalf("WriteFile(destination replacement) error: %v", err)
	}
	replacementDestinationInfo, err := os.Stat(replacementDestinationPath)
	if err != nil {
		t.Fatalf("Stat(destination replacement) error: %v", err)
	}

	commitErr := errors.New("commit failed after destination replacement")
	commitCalls := 0
	fs.commitTrashRestore = func(context.Context, *versionstore.TrashItem, string, []versionstore.FileIndexEntry, bool, *versionstore.TrashOperation) error {
		commitCalls++
		if err := os.Rename(destinationPath, intendedDestinationPath); err != nil {
			return err
		}
		if err := os.Rename(replacementDestinationPath, destinationPath); err != nil {
			return err
		}
		return commitErr
	}

	err = fs.RestoreFromTrash(ctx, item.ID)
	if !errors.Is(err, commitErr) || !errors.Is(err, ErrDeleteTargetChanged) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RestoreFromTrash() error = %v, want commit failure, destination drift, and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	if commitCalls != 1 {
		t.Fatalf("commitTrashRestore calls = %d, want 1", commitCalls)
	}
	currentDestination, statErr := os.Stat(destinationPath)
	if statErr != nil || !os.SameFile(replacementDestinationInfo, currentDestination) {
		t.Fatalf("replacement destination = %v, %v; want retained", currentDestination, statErr)
	}
	copied, readErr := os.ReadFile(intendedDestinationPath)
	if readErr != nil || string(copied) != "intended" {
		t.Fatalf("intended destination retained for repair = %q, %v", copied, readErr)
	}
	trashSource, readErr := os.ReadFile(trashContentPath)
	if readErr != nil || string(trashSource) != "intended" {
		t.Fatalf("Trash source after failed commit = %q, %v; want intended", trashSource, readErr)
	}
	items, err = fs.ListTrash(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash() after failed commit = %+v, %v; want original item", items, err)
	}
	operations, operationErr := fs.versions.ListTrashOperations(ctx)
	if operationErr != nil || len(operations) != 0 {
		t.Fatalf("ListTrashOperations() before commit = %+v, %v; want empty", operations, operationErr)
	}
	requireJournaledTrashMutationGate(t, fs)
	if err := os.Remove(destinationPath); err != nil {
		t.Fatalf("Remove(replacement destination) error: %v", err)
	}
	if err := os.Rename(intendedDestinationPath, destinationPath); err != nil {
		t.Fatalf("Rename(intended destination back) error: %v", err)
	}
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after destination repair error: %v", recoveryErr)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}
	if _, statErr := os.Stat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore destination after recovered rollback exists: %v", statErr)
	}
	for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
		if _, statErr := fs.trashRootHandle.Lstat(filepath.FromSlash(trashTransferJournalRel(recovery.OperationID, decision))); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("Lstat(%s journal) error = %v, want os.ErrNotExist", decision, statErr)
		}
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

func TestFileSystemJournaledTrashRollbackRetainsReplacedCanonicalCopyBeforeCommit(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/trash-rollback-copy.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	participantErr := errors.New("precommit participant failed")
	var canonicalContentPath string
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(context.Context, string, string) ([]byte, error) {
			return []byte("participant"), nil
		},
		ApplyDelete: func(_ context.Context, _ string, logicalPath string, _ []byte, committed bool) error {
			if logicalPath != targetPath || committed {
				return nil
			}
			entries, err := os.ReadDir(fs.trashRoot)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.IsDir() && entry.Name() != trashTransferJournalDir {
					canonicalContentPath = filepath.Join(fs.trashRoot, entry.Name(), "content")
					break
				}
			}
			if canonicalContentPath == "" {
				return errors.New("canonical Trash content was not published")
			}
			replacement := canonicalContentPath + ".replacement"
			if err := os.WriteFile(replacement, []byte("replacement"), 0o600); err != nil {
				return err
			}
			if err := os.Rename(replacement, canonicalContentPath); err != nil {
				return err
			}
			return participantErr
		},
		RollbackDelete:        func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete:        completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error { return nil },
	})

	err := fs.Delete(ctx, targetPath)
	if !errors.Is(err, participantErr) || !errors.Is(err, ErrDeleteTargetChanged) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Delete() error = %v, want precommit failure, copied-content drift, and recovery gate", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	workspaceStage := journaledTrashWorkspaceStagePath(t, fs, recovery)
	if original, readErr := os.ReadFile(workspaceStage); readErr != nil || string(original) != "intended" {
		t.Fatalf("staged source = %q, %v", original, readErr)
	}
	replacement, readErr := os.ReadFile(canonicalContentPath)
	if readErr != nil || string(replacement) != "replacement" {
		t.Fatalf("replacement canonical copy = %q, %v; want retained", replacement, readErr)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil || len(items) != 0 {
		t.Fatalf("trash metadata after precommit drift = %+v, %v; want empty", items, listErr)
	}
	requireJournaledTrashMutationGate(t, fs)
}

func TestFileSystemJournaledTrashRollbackRetainsReplicaWhenParticipantRollbackFails(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/trash-rollback-post-capture.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("intended")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	applyErr := errors.New("precommit participant failed")
	rollbackErr := errors.New("participant rollback failed")
	var canonicalContentPath string
	var replacementInfo os.FileInfo
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(context.Context, string, string) ([]byte, error) {
			return []byte("participant"), nil
		},
		ApplyDelete: func(_ context.Context, _ string, logicalPath string, _ []byte, committed bool) error {
			if logicalPath == targetPath && !committed {
				return applyErr
			}
			return nil
		},
		RollbackDelete: func(_ context.Context, _ string, logicalPath string, _ []byte) error {
			if logicalPath != targetPath {
				return nil
			}
			entries, err := os.ReadDir(fs.trashRoot)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.IsDir() && entry.Name() != trashTransferJournalDir {
					canonicalContentPath = filepath.Join(fs.trashRoot, entry.Name(), "content")
					break
				}
			}
			if canonicalContentPath == "" {
				return errors.New("canonical Trash content was not published")
			}
			replacementPath := canonicalContentPath + ".replacement"
			if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
				return err
			}
			replacementInfo, err = os.Stat(replacementPath)
			if err != nil {
				return err
			}
			if err := os.Rename(replacementPath, canonicalContentPath); err != nil {
				return err
			}
			return rollbackErr
		},
		CompleteDelete:        completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error { return nil },
	})

	err := fs.Delete(ctx, targetPath)
	if !errors.Is(err, applyErr) || !errors.Is(err, rollbackErr) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Delete() error = %v, want participant apply, rollback, and recovery errors", err)
	}
	requireJournaledTrashRecovery(t, fs, err)
	if original, readErr := os.ReadFile(fs.workspace.FullPath(targetPath)); readErr != nil || string(original) != "intended" {
		t.Fatalf("rolled back source = %q, %v", original, readErr)
	}
	current, statErr := os.Stat(canonicalContentPath)
	if statErr != nil || replacementInfo == nil || !os.SameFile(replacementInfo, current) {
		t.Fatalf("replacement canonical content was not retained: info=%v err=%v", current, statErr)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil || len(items) != 0 {
		t.Fatalf("trash metadata after failed participant rollback = %+v, %v; want empty", items, listErr)
	}
	requireJournaledTrashMutationGate(t, fs)
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
