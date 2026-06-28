//go:build linux

package storage

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const bindMountDeleteHelperEnv = "MNEMONAS_BIND_MOUNT_DELETE_HELPER"

func TestFileSystem_DeleteRejectsRealBindMountWithoutSourceMutation(t *testing.T) {
	if os.Getenv(bindMountDeleteHelperEnv) == "1" {
		runBindMountDeleteHelper(t)
		return
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skipf("unshare is unavailable: %v", err)
	}

	command := exec.Command(
		"unshare",
		"--user",
		"--map-root-user",
		"--mount",
		"--propagation", "private",
		os.Args[0],
		"-test.run=^TestFileSystem_DeleteRejectsRealBindMountWithoutSourceMutation$",
		"-test.count=1",
	)
	command.Env = append(os.Environ(), bindMountDeleteHelperEnv+"=1")
	output, err := command.CombinedOutput()
	if err == nil {
		return
	}
	lowerOutput := strings.ToLower(string(output))
	if strings.Contains(lowerOutput, "operation not permitted") || strings.Contains(lowerOutput, "permission denied") {
		t.Skipf("user or mount namespaces are unavailable: %v: %s", err, output)
	}
	t.Fatalf("bind mount helper failed: %v\n%s", err, output)
}

func runBindMountDeleteHelper(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	sourceRoot := t.TempDir()
	sourceFile := filepath.Join(sourceRoot, "value.txt")
	if err := os.WriteFile(sourceFile, []byte("outside-value"), 0o600); err != nil {
		t.Fatalf("WriteFile(bind source) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tree/mounted"); err != nil {
		t.Fatalf("Mkdir(mounted) error: %v", err)
	}
	mountPoint := fs.workspace.FullPath("/tree/mounted")
	mountBindForDeleteTest(t, sourceRoot, mountPoint)
	assertBindMountUsesSameDevice(t, sourceRoot, mountPoint)

	for _, targetPath := range []string{"/tree", "/tree/mounted", "/tree/mounted/value.txt"} {
		_, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("PrepareDeleteIntents(%s bind mount) error = %v, want ErrNotRegular", targetPath, err)
		}
		err = fs.Delete(ctx, targetPath)
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("Delete(%s bind mount) error = %v, want ErrNotRegular", targetPath, err)
		}
		assertBindSourceUnchanged(t, sourceFile)
	}
	if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
		t.Fatalf("trash after rejected bind mount deletes = %+v, %v; want empty", items, err)
	}

	if err := unix.Unmount(mountPoint, 0); err != nil {
		t.Fatalf("Unmount(bind target before revalidation) error: %v", err)
	}
	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/tree"}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents(unmounted tree) error: %v", err)
	}
	if err := unix.Mount(sourceRoot, mountPoint, "", unix.MS_BIND, ""); err != nil {
		t.Fatalf("Mount(bind target for revalidation) error: %v", err)
	}
	err = fs.DeleteWithExpectedPolicyAndTarget(ctx, "/tree", DeletePolicyExpectation{
		Mode:  intent.Policy.Mode,
		Token: intent.Policy.Token,
	}, intent.Targets[0].Token, nil)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget(added bind mount) error = %v, want ErrNotRegular", err)
	}
	assertBindSourceUnchanged(t, sourceFile)
	if err := unix.Unmount(mountPoint, 0); err != nil {
		t.Fatalf("Unmount(bind target after revalidation) error: %v", err)
	}

	renameSource := fs.workspace.FullPath("/rename-tree")
	renameDestination := fs.workspace.FullPath("/renamed-tree")
	if err := os.MkdirAll(filepath.Join(renameSource, "mounted"), 0o755); err != nil {
		t.Fatalf("MkdirAll(rename source) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(renameSource, "mounted", "local.txt"), []byte("local"), 0o600); err != nil {
		t.Fatalf("WriteFile(rename local) error: %v", err)
	}
	originalMovePathRename := movePathRename
	mountedDuringRename := false
	movePathRename = func(root *os.Root, oldRel, newRel, oldPath, newPath string) error {
		if !mountedDuringRename && oldPath == renameSource && newPath == renameDestination {
			mountMovingBindForDeleteTest(
				t,
				sourceRoot,
				filepath.Join(renameSource, "mounted"),
				filepath.Join(renameDestination, "mounted"),
			)
			mountedDuringRename = true
		}
		return originalMovePathRename(root, oldRel, newRel, oldPath, newPath)
	}
	t.Cleanup(func() {
		movePathRename = originalMovePathRename
	})
	err = fs.movePath(renameSource, renameDestination)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("movePath(bind mounted during rename) error = %v, want ErrNotRegular", err)
	}
	if !mountedDuringRename {
		t.Fatal("same-root rename hook did not mount the nested bind source")
	}
	assertBindSourceUnchanged(t, sourceFile)
	if _, statErr := os.Stat(renameDestination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rename destination after mount-boundary rollback exists: %v", statErr)
	}
	if err := unix.Unmount(filepath.Join(renameSource, "mounted"), 0); err != nil {
		t.Fatalf("Unmount(rename bind target) error: %v", err)
	}

	if err := fs.Mkdir(ctx, "/trash-tree"); err != nil {
		t.Fatalf("Mkdir(trash-tree) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/trash-tree/mounted"); err != nil {
		t.Fatalf("Mkdir(trash mounted) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-tree/mounted/local.txt", strings.NewReader("local")); err != nil {
		t.Fatalf("WriteFile(trash local) error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-tree"); err != nil {
		t.Fatalf("Delete(trash-tree) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	trashMountPoint := filepath.Join(fs.trashRoot, items[0].ID, "content", "mounted")
	mountBindForDeleteTest(t, sourceRoot, trashMountPoint)
	err = fs.DeleteFromTrash(ctx, items[0].ID)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("DeleteFromTrash(bind mount) error = %v, want ErrNotRegular", err)
	}
	assertBindSourceUnchanged(t, sourceFile)
	if _, err := fs.GetTrashItem(ctx, items[0].ID); err != nil {
		t.Fatalf("GetTrashItem() after rejected bind mount deletion error: %v", err)
	}
	if err := unix.Unmount(trashMountPoint, 0); err != nil {
		t.Fatalf("Unmount(trash bind target) error: %v", err)
	}

	deletingDir := filepath.Join(fs.trashRoot, ".deleting")
	if err := os.MkdirAll(deletingDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(.deleting) error: %v", err)
	}
	externalDeletingDir := t.TempDir()
	externalSentinel := filepath.Join(externalDeletingDir, "sentinel.txt")
	if err := os.WriteFile(externalSentinel, []byte("outside-deleting"), 0o600); err != nil {
		t.Fatalf("WriteFile(.deleting sentinel) error: %v", err)
	}
	mountBindForDeleteTest(t, externalDeletingDir, deletingDir)
	err = fs.DeleteFromTrash(ctx, items[0].ID)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("DeleteFromTrash(bound .deleting) error = %v, want ErrNotRegular", err)
	}
	if _, err := fs.GetTrashItem(ctx, items[0].ID); err != nil {
		t.Fatalf("GetTrashItem() after bound .deleting rejection error: %v", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(fs.trashRoot, items[0].ID, "content", "mounted", "local.txt")); readErr != nil || string(data) != "local" {
		t.Fatalf("trash content after bound .deleting rejection = %q, %v", data, readErr)
	}
	entries, readErr := os.ReadDir(externalDeletingDir)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(externalSentinel) {
		t.Fatalf("external .deleting directory after rejected staging = %+v, %v; want sentinel only", entries, readErr)
	}
	if data, readErr := os.ReadFile(externalSentinel); readErr != nil || string(data) != "outside-deleting" {
		t.Fatalf("external .deleting sentinel after rejected staging = %q, %v", data, readErr)
	}
	if err := unix.Unmount(deletingDir, 0); err != nil {
		t.Fatalf("Unmount(.deleting bind target) error: %v", err)
	}

	if err := fs.WriteFile(ctx, "/copy-cleanup.txt", strings.NewReader("workspace-copy")); err != nil {
		t.Fatalf("WriteFile(copy cleanup source) error: %v", err)
	}
	externalCopyDir := t.TempDir()
	externalCopyContent := filepath.Join(externalCopyDir, "content")
	if err := os.WriteFile(externalCopyContent, []byte("outside-copy-cleanup"), 0o600); err != nil {
		t.Fatalf("WriteFile(copy cleanup sentinel) error: %v", err)
	}
	originalSyncManagedStorageDir := syncManagedStorageDir
	copySyncErr := errors.New("sync copied file failed")
	mountedCopyParent := ""
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if root == fs.trashRootHandle && mountedCopyParent == "" {
			if info, statErr := root.Lstat(filepath.Join(relName, "content")); statErr == nil && info.Mode().IsRegular() {
				mountBindForDeleteTest(t, externalCopyDir, absPath)
				mountedCopyParent = absPath
				return copySyncErr
			}
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})
	err = fs.Delete(ctx, "/copy-cleanup.txt")
	syncManagedStorageDir = originalSyncManagedStorageDir
	if !errors.Is(err, copySyncErr) || !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Delete(copy cleanup bind mount) error = %v, want sync error and ErrNotRegular", err)
	}
	if mountedCopyParent == "" {
		t.Fatal("copy cleanup bind mount hook was not reached")
	}
	if data, readErr := os.ReadFile(externalCopyContent); readErr != nil || string(data) != "outside-copy-cleanup" {
		t.Fatalf("external copied-destination sentinel after rejected cleanup = %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(fs.workspace.FullPath("/copy-cleanup.txt")); readErr != nil || string(data) != "workspace-copy" {
		t.Fatalf("workspace source after rejected copied-destination cleanup = %q, %v", data, readErr)
	}
	if err := unix.Unmount(mountedCopyParent, 0); err != nil {
		t.Fatalf("Unmount(copy cleanup bind target) error: %v", err)
	}

	if err := fs.Mkdir(ctx, "/permanent-rollback"); err != nil {
		t.Fatalf("Mkdir(permanent rollback parent) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/permanent-rollback/victim.txt", strings.NewReader("workspace-original")); err != nil {
		t.Fatalf("WriteFile(permanent rollback victim) error: %v", err)
	}
	externalRollbackDir := t.TempDir()
	externalRollbackVictim := filepath.Join(externalRollbackDir, "victim.txt")
	if err := os.WriteFile(externalRollbackVictim, []byte("outside-rollback"), 0o600); err != nil {
		t.Fatalf("WriteFile(permanent rollback sentinel) error: %v", err)
	}
	rollbackMountPoint := fs.workspace.FullPath("/permanent-rollback")
	originalDeleteFileIndex := fs.deleteFileIndex
	indexErr := errors.New("delete index failed")
	mountedRollbackParent := false
	fs.deleteFileIndex = func(context.Context, string) error {
		if !mountedRollbackParent {
			mountBindForDeleteTest(t, externalRollbackDir, rollbackMountPoint)
			mountedRollbackParent = true
		}
		return indexErr
	}
	t.Cleanup(func() {
		fs.deleteFileIndex = originalDeleteFileIndex
	})
	originalTrashEnabled := fs.config.TrashEnabled
	trashEnabled := false
	fs.config.TrashEnabled = &trashEnabled
	err = fs.Delete(ctx, "/permanent-rollback/victim.txt")
	fs.config.TrashEnabled = originalTrashEnabled
	fs.deleteFileIndex = originalDeleteFileIndex
	if !errors.Is(err, indexErr) || !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Delete(permanent rollback bind mount) error = %v, want index error and ErrNotRegular", err)
	}
	if !mountedRollbackParent {
		t.Fatal("permanent rollback bind mount hook was not reached")
	}
	if data, readErr := os.ReadFile(externalRollbackVictim); readErr != nil || string(data) != "outside-rollback" {
		t.Fatalf("external permanent-rollback sentinel = %q, %v", data, readErr)
	}
	if err := unix.Unmount(rollbackMountPoint, 0); err != nil {
		t.Fatalf("Unmount(permanent rollback bind target) error: %v", err)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/permanent-rollback/victim.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("permanent rollback target after rejected external write exists: %v", statErr)
	}

	if err := fs.WriteFile(ctx, "/after-real-bind-rejection.txt", strings.NewReader("ok")); err != nil {
		t.Fatalf("WriteFile() after rejected bind mount deletion error: %v", err)
	}
}

func mountBindForDeleteTest(t *testing.T, source, target string) {
	t.Helper()
	if err := unix.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		t.Fatalf("Mount(%s -> %s) error: %v", source, target, err)
	}
	t.Cleanup(func() {
		if err := unix.Unmount(target, unix.MNT_DETACH); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
			t.Errorf("Unmount(%s) cleanup error: %v", target, err)
		}
	})
}

func mountMovingBindForDeleteTest(t *testing.T, source string, candidateTargets ...string) {
	t.Helper()
	if len(candidateTargets) == 0 {
		t.Fatal("moving bind mount requires at least one target")
	}
	if err := unix.Mount(source, candidateTargets[0], "", unix.MS_BIND, ""); err != nil {
		t.Fatalf("Mount(%s -> %s) error: %v", source, candidateTargets[0], err)
	}
	t.Cleanup(func() {
		for i := len(candidateTargets) - 1; i >= 0; i-- {
			target := candidateTargets[i]
			if err := unix.Unmount(target, unix.MNT_DETACH); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
				t.Errorf("Unmount(%s) cleanup error: %v", target, err)
			}
		}
	})
}

func assertBindMountUsesSameDevice(t *testing.T, source, target string) {
	t.Helper()
	var sourceStat unix.Stat_t
	if err := unix.Stat(source, &sourceStat); err != nil {
		t.Fatalf("Stat(bind source) error: %v", err)
	}
	var targetStat unix.Stat_t
	if err := unix.Stat(target, &targetStat); err != nil {
		t.Fatalf("Stat(bind target) error: %v", err)
	}
	if sourceStat.Dev != targetStat.Dev {
		t.Fatalf("bind source dev = %d, target dev = %d; want same device", sourceStat.Dev, targetStat.Dev)
	}
}

func assertBindSourceUnchanged(t *testing.T, sourceFile string) {
	t.Helper()
	data, err := os.ReadFile(sourceFile)
	if err != nil || string(data) != "outside-value" {
		t.Fatalf("bind source after rejected deletion = %q, %v", data, err)
	}
}
