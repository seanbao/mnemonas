package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func requireJournaledTrashRecovery(t *testing.T, fs *FileSystem, err error) *TrashTransferRecoveryRequiredError {
	t.Helper()

	if !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("operation error = %v, want ErrTrashRecoveryRequired", err)
	}
	var recovery *TrashTransferRecoveryRequiredError
	if !errors.As(err, &recovery) || recovery.OperationID == "" {
		t.Fatalf("operation error = %v, want identified TrashTransferRecoveryRequiredError", err)
	}
	preparedRel := filepath.FromSlash(trashTransferJournalRel(recovery.OperationID, trashTransferPrepared))
	if _, statErr := fs.trashRootHandle.Lstat(preparedRel); statErr != nil {
		t.Fatalf("Lstat(prepared transfer journal) error: %v", statErr)
	}
	if fs.trashMutationBlocked == nil {
		t.Fatal("journaled Trash recovery did not activate the global mutation gate")
	}
	return recovery
}

func journaledTrashWorkspaceStagePath(t *testing.T, fs *FileSystem, recovery *TrashTransferRecoveryRequiredError) string {
	t.Helper()

	wantBase := filepath.Base(storageWorkspaceRelativeName(trashTransferWorkspaceStagePath("/", recovery.OperationID)))
	for _, inspectionPath := range recovery.InspectionPaths {
		if filepath.Dir(inspectionPath) == fs.workspace.Root() && filepath.Base(inspectionPath) == wantBase {
			return inspectionPath
		}
	}
	t.Fatalf("Trash recovery inspection paths = %v, want workspace stage %q", recovery.InspectionPaths, wantBase)
	return ""
}

func requireJournaledTrashMutationGate(t *testing.T, fs *FileSystem) {
	t.Helper()

	blockedPath := "/blocked-by-trash-transfer-recovery"
	if err := fs.Mkdir(context.Background(), blockedPath); !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("Mkdir() during Trash recovery error = %v, want ErrTrashRecoveryRequired", err)
	}
	if _, err := os.Lstat(fs.workspace.FullPath(blockedPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(blocked mutation target) error = %v, want os.ErrNotExist", err)
	}
}

func repairPreparedTrashTransferAndRecover(t *testing.T, fs *FileSystem, recovery *TrashTransferRecoveryRequiredError) {
	t.Helper()

	privateStage := filepath.Join(fs.trashRoot, filepath.FromSlash(trashTransferItemStageRel(recovery.OperationID)))
	if err := os.RemoveAll(privateStage); err != nil {
		t.Fatalf("RemoveAll(private Trash stage) error: %v", err)
	}
	report, err := fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() after private-stage repair error: %v", err)
	}
	if report.RolledBack != 1 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}
}

func TestMountPointsFromMountInfoPreservesEscapedPathsAndBindMounts(t *testing.T) {
	mountInfo := []byte(strings.Join([]string{
		"21 1 8:1 / / rw,relatime - ext4 /dev/root rw",
		"22 21 8:1 /srv/source /srv/Mnemo\\040NAS/files/bind\\040same-device rw,relatime - ext4 /dev/root rw",
		"23 21 8:1 /srv/gone /srv/Mnemo\\040NAS/files/gone\\040(deleted) rw,relatime - ext4 /dev/root rw",
	}, "\n"))

	mountPoints, err := mountPointsFromMountInfo(mountInfo)
	if err != nil {
		t.Fatalf("mountPointsFromMountInfo() error: %v", err)
	}
	want := []string{"/", "/srv/Mnemo NAS/files/bind same-device", "/srv/Mnemo NAS/files/gone (deleted)"}
	if strings.Join(mountPoints, "|") != strings.Join(want, "|") {
		t.Fatalf("mountPointsFromMountInfo() = %q, want %q", mountPoints, want)
	}

	_, err = mountPointsFromMountInfo([]byte("22 21 8:1 / /srv/bad\\999path rw - ext4 /dev/root rw"))
	if err == nil {
		t.Fatal("mountPointsFromMountInfo(invalid escape) error = nil")
	}
	if _, err := mountPointsFromMountInfo([]byte("\n\t")); err == nil {
		t.Fatal("mountPointsFromMountInfo(empty) error = nil")
	}
}

func TestFileSystem_PrepareDeleteIntentsAuthorizesMountPointBeforeConflict(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tree/mounted"); err != nil {
		t.Fatalf("Mkdir(mounted) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/mounted/secret.txt", strings.NewReader("secret")); err != nil {
		t.Fatalf("WriteFile(secret) error: %v", err)
	}
	mountedPath := fs.workspace.FullPath("/tree/mounted")
	fs.readDeleteMountPoints = fixedDeleteMountPoints(mountedPath)

	errDenied := errors.New("mounted path denied")
	var authorized []string
	_, err := fs.PrepareDeleteIntents(ctx, []string{"/tree"}, func(targetPath string) error {
		authorized = append(authorized, targetPath)
		if targetPath == "/tree/mounted" {
			return errDenied
		}
		return nil
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("PrepareDeleteIntents(denied mount) error = %v, want access denial", err)
	}
	if got, want := strings.Join(authorized, "|"), "/tree|/tree/mounted"; got != want {
		t.Fatalf("authorized paths = %q, want %q", got, want)
	}

	authorized = nil
	_, err = fs.PrepareDeleteIntents(ctx, []string{"/tree"}, func(targetPath string) error {
		authorized = append(authorized, targetPath)
		return nil
	})
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("PrepareDeleteIntents(mount) error = %v, want ErrNotRegular", err)
	}
	if got, want := strings.Join(authorized, "|"), "/tree|/tree/mounted"; got != want {
		t.Fatalf("authorized paths = %q, want %q", got, want)
	}
	assertWorkspaceTreeAndTrashUnchanged(t, fs, "/tree/mounted/secret.txt", "secret")
}

func TestFileSystem_DeleteRejectsTargetsAcrossNestedMountBoundary(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tree/mounted"); err != nil {
		t.Fatalf("Mkdir(mounted) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/mounted/value.txt", strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(value) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints(fs.workspace.FullPath("/tree/mounted"))

	for _, targetPath := range []string{"/tree", "/tree/mounted", "/tree/mounted/value.txt"} {
		t.Run(strings.TrimPrefix(targetPath, "/"), func(t *testing.T) {
			_, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
			if !errors.Is(err, ErrNotRegular) {
				t.Fatalf("PrepareDeleteIntents(%s) error = %v, want ErrNotRegular", targetPath, err)
			}
			err = fs.Delete(ctx, targetPath)
			if !errors.Is(err, ErrNotRegular) {
				t.Fatalf("Delete(%s) error = %v, want ErrNotRegular", targetPath, err)
			}
			assertWorkspaceTreeAndTrashUnchanged(t, fs, "/tree/mounted/value.txt", "value")
		})
	}
}

func TestFileSystem_DeleteAllowsWorkspaceRootMount(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/root-mounted-filesystem.txt", strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints(fs.workspace.Root())

	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/root-mounted-filesystem.txt"}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents(workspace root mount) error: %v", err)
	}
	err = fs.DeleteWithExpectedPolicyAndTarget(ctx, "/root-mounted-filesystem.txt", DeletePolicyExpectation{
		Mode:  intent.Policy.Mode,
		Token: intent.Policy.Token,
	}, intent.Targets[0].Token, nil)
	if err != nil {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget(workspace root mount) error: %v", err)
	}
	if _, err := fs.Stat(ctx, "/root-mounted-filesystem.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat(deleted file) error = %v, want ErrNotFound", err)
	}
}

func TestFileSystem_DeleteMountBoundaryFailsClosedAndReleasesLock(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/keep.txt", strings.NewReader("keep")); err != nil {
		t.Fatalf("WriteFile(keep) error: %v", err)
	}
	fs.readDeleteMountPoints = func() ([]string, error) {
		return nil, errors.New("mount table unavailable")
	}

	var authorized []string
	_, err := fs.PrepareDeleteIntents(ctx, []string{"/tree"}, func(targetPath string) error {
		authorized = append(authorized, targetPath)
		return nil
	})
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("PrepareDeleteIntents(mount table failure) error = %v, want ErrNotRegular", err)
	}
	if got, want := strings.Join(authorized, "|"), "/tree"; got != want {
		t.Fatalf("authorized paths before mount table failure = %q, want %q", got, want)
	}

	err = fs.Delete(ctx, "/tree")
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Delete(mount table failure) error = %v, want ErrNotRegular", err)
	}
	assertWorkspaceTreeAndTrashUnchanged(t, fs, "/tree/keep.txt", "keep")
	if err := fs.WriteFile(ctx, "/after-mount-table-failure.txt", strings.NewReader("ok")); err != nil {
		t.Fatalf("WriteFile() after rejected deletion error: %v", err)
	}
}

func TestFileSystem_DeleteMountBoundaryRejectsInvalidProviderPaths(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		mountPoint string
	}{
		{name: "no entries", mountPoint: "<none>"},
		{name: "empty", mountPoint: ""},
		{name: "relative", mountPoint: "relative/mount"},
		{name: "nul", mountPoint: "/mnt/invalid\x00mount"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			ctx := context.Background()
			if err := fs.WriteFile(ctx, "/keep.txt", strings.NewReader("keep")); err != nil {
				t.Fatalf("WriteFile(keep) error: %v", err)
			}
			if testCase.mountPoint == "<none>" {
				fs.readDeleteMountPoints = func() ([]string, error) { return nil, nil }
			} else {
				fs.readDeleteMountPoints = fixedDeleteMountPoints(testCase.mountPoint)
			}

			_, err := fs.PrepareDeleteIntents(ctx, []string{"/keep.txt"}, nil)
			if !errors.Is(err, ErrNotRegular) {
				t.Fatalf("PrepareDeleteIntents(invalid mount path) error = %v, want ErrNotRegular", err)
			}
			assertWorkspaceTreeAndTrashUnchanged(t, fs, "/keep.txt", "keep")
		})
	}
}

func TestFileSystem_DeleteRevalidatesMountBoundaryBeforeTrashCopy(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tree/mounted"); err != nil {
		t.Fatalf("Mkdir(mounted) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/mounted/value.txt", strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(value) error: %v", err)
	}

	fs.readDeleteMountPoints = fixedDeleteMountPoints()
	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/tree"}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents(clean tree) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints(fs.workspace.FullPath("/tree/mounted"))
	deleteHookCalls := 0
	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		deleteHookCalls++
		return nil, nil
	})
	err = fs.DeleteWithExpectedPolicyAndTarget(ctx, "/tree", DeletePolicyExpectation{
		Mode:  intent.Policy.Mode,
		Token: intent.Policy.Token,
	}, intent.Targets[0].Token, nil)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget(added mount) error = %v, want ErrNotRegular", err)
	}
	if deleteHookCalls != 0 {
		t.Fatalf("delete hook calls = %d, want 0", deleteHookCalls)
	}
	assertWorkspaceTreeAndTrashUnchanged(t, fs, "/tree/mounted/value.txt", "value")
}

func TestFileSystem_DeleteRereadsMountBoundaryDuringActualDescription(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tree/mounted"); err != nil {
		t.Fatalf("Mkdir(mounted) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/mounted/value.txt", strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(value) error: %v", err)
	}

	reads := 0
	fs.readDeleteMountPoints = func() ([]string, error) {
		reads++
		if reads == 1 {
			return []string{"/"}, nil
		}
		return []string{fs.workspace.FullPath("/tree/mounted")}, nil
	}
	err := fs.Delete(ctx, "/tree")
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Delete(mount added before description) error = %v, want ErrNotRegular", err)
	}
	if reads < 2 {
		t.Fatalf("mount table reads = %d, want at least 2", reads)
	}
	assertWorkspaceTreeAndTrashUnchanged(t, fs, "/tree/mounted/value.txt", "value")
}

func TestFileSystem_PermanentDeleteRereadsMountBoundaryBeforeMutation(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/value.txt", strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(value) error: %v", err)
	}
	trashEnabled := false
	fs.config.TrashEnabled = &trashEnabled
	reads := 0
	fs.readDeleteMountPoints = func() ([]string, error) {
		reads++
		if reads < 3 {
			return []string{"/"}, nil
		}
		return []string{fs.workspace.FullPath("/tree")}, nil
	}
	deleteHookCalls := 0
	fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
		deleteHookCalls++
		return nil, nil
	})

	err := fs.Delete(ctx, "/tree/value.txt")
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Delete(permanent mount added before mutation) error = %v, want ErrNotRegular", err)
	}
	if reads < 3 {
		t.Fatalf("mount table reads = %d, want at least 3", reads)
	}
	if deleteHookCalls != 0 {
		t.Fatalf("delete hook calls = %d, want 0", deleteHookCalls)
	}
	data, readErr := os.ReadFile(fs.workspace.FullPath("/tree/value.txt"))
	if readErr != nil || string(data) != "value" {
		t.Fatalf("permanent target after rejected deletion = %q, %v", data, readErr)
	}
}

func TestFileSystem_PermanentDeleteRefusesRollbackIntoNewMountBoundary(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	trashEnabled := false
	fs.config.TrashEnabled = &trashEnabled
	if err := fs.Mkdir(ctx, "/parent"); err != nil {
		t.Fatalf("Mkdir(parent) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/parent/victim.txt", strings.NewReader("original")); err != nil {
		t.Fatalf("WriteFile(victim) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints()
	indexErr := errors.New("delete index failed")
	fs.deleteFileIndex = func(context.Context, string) error {
		fs.readDeleteMountPoints = fixedDeleteMountPoints(fs.workspace.FullPath("/parent"))
		return indexErr
	}

	err := fs.Delete(ctx, "/parent/victim.txt")
	if !errors.Is(err, indexErr) || !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Delete(permanent rollback mount) error = %v, want index error and ErrNotRegular", err)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath("/parent/victim.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("permanent rollback target exists after rejected external write: %v", statErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash after rejected permanent rollback = %+v, %v; want empty", items, listErr)
	}
}

func TestFileSystem_MovePathCleansCopiedDestinationWhenMountAppearsBeforeSourceRemoval(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	sourcePath := fs.workspace.FullPath("/tree")
	destinationPath := filepath.Join(fs.trashRoot, "copied-tree")
	if err := os.MkdirAll(filepath.Join(sourcePath, "mounted"), 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "mounted", "value.txt"), []byte("value"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	reads := 0
	fs.readDeleteMountPoints = func() ([]string, error) {
		reads++
		if _, err := os.Stat(filepath.Join(destinationPath, "mounted", "value.txt")); err == nil {
			return []string{filepath.Join(sourcePath, "mounted")}, nil
		}
		return []string{"/"}, nil
	}
	err := fs.movePath(sourcePath, destinationPath)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("movePath(mount added after copy) error = %v, want ErrNotRegular", err)
	}
	data, readErr := os.ReadFile(filepath.Join(sourcePath, "mounted", "value.txt"))
	if readErr != nil || string(data) != "value" {
		t.Fatalf("source after rejected move = %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("copied destination remains after rejected move: %v", statErr)
	}
	if reads < 5 {
		t.Fatalf("mount table reads = %d, want repeated source and destination checks", reads)
	}
}

func TestFileSystem_MovePathPreservesMountedDestinationDuringRollback(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	sourcePath := fs.workspace.FullPath("/tree")
	destinationPath := filepath.Join(fs.trashRoot, "race-parent", "copied-tree")
	if err := os.MkdirAll(filepath.Join(sourcePath, "mounted"), 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "mounted", "value.txt"), []byte("value"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	fs.readDeleteMountPoints = func() ([]string, error) {
		if _, err := os.Stat(filepath.Join(destinationPath, "mounted", "value.txt")); err == nil {
			return []string{
				filepath.Join(sourcePath, "mounted"),
				filepath.Join(destinationPath, "mounted"),
			}, nil
		}
		return []string{"/"}, nil
	}
	err := fs.movePath(sourcePath, destinationPath)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("movePath(destination mounted during rollback) error = %v, want ErrNotRegular", err)
	}
	for _, filePath := range []string{
		filepath.Join(sourcePath, "mounted", "value.txt"),
		filepath.Join(destinationPath, "mounted", "value.txt"),
	} {
		data, readErr := os.ReadFile(filePath)
		if readErr != nil || string(data) != "value" {
			t.Fatalf("preserved file %s = %q, %v; want value", filePath, data, readErr)
		}
	}
}

func TestFileSystem_MovePathRejectsNestedMountBeforeSameRootRename(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	sourcePath := fs.workspace.FullPath("/source")
	destinationPath := fs.workspace.FullPath("/destination")
	if err := os.MkdirAll(filepath.Join(sourcePath, "mounted"), 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "mounted", "value.txt"), []byte("value"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints(filepath.Join(sourcePath, "mounted"))

	err := fs.movePath(sourcePath, destinationPath)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("movePath(same-root nested mount) error = %v, want ErrNotRegular", err)
	}
	data, readErr := os.ReadFile(filepath.Join(sourcePath, "mounted", "value.txt"))
	if readErr != nil || string(data) != "value" {
		t.Fatalf("source after rejected same-root rename = %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination after rejected same-root rename exists: %v", statErr)
	}
}

func TestFileSystem_MovePathRollsBackSameRootRenameWhenMountAppears(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	sourcePath := fs.workspace.FullPath("/source")
	destinationPath := fs.workspace.FullPath("/destination")
	if err := os.MkdirAll(filepath.Join(sourcePath, "mounted"), 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "mounted", "value.txt"), []byte("value"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	fs.readDeleteMountPoints = func() ([]string, error) {
		if _, err := os.Stat(filepath.Join(destinationPath, "mounted")); err == nil {
			return []string{filepath.Join(destinationPath, "mounted")}, nil
		}
		return []string{"/"}, nil
	}

	err := fs.movePath(sourcePath, destinationPath)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("movePath(mount appeared during rename) error = %v, want ErrNotRegular", err)
	}
	data, readErr := os.ReadFile(filepath.Join(sourcePath, "mounted", "value.txt"))
	if readErr != nil || string(data) != "value" {
		t.Fatalf("source after mount-boundary rename rollback = %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination after mount-boundary rename rollback exists: %v", statErr)
	}
}

func TestFileSystem_MovePathRechecksMountBoundaryBeforeFailedRenameCopyFallback(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	sourcePath := fs.workspace.FullPath("/source.txt")
	destinationPath := fs.workspace.FullPath("/destination.txt")
	if err := os.WriteFile(sourcePath, []byte("value"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints()

	originalMovePathRename := movePathRename
	originalAfterStorageCopySourceStat := afterStorageCopySourceStat
	copyCalls := 0
	movePathRename = func(root *os.Root, oldRel, newRel, oldPath, newPath string) error {
		if oldPath == sourcePath && newPath == destinationPath {
			fs.readDeleteMountPoints = fixedDeleteMountPoints(destinationPath)
			return syscall.EXDEV
		}
		return originalMovePathRename(root, oldRel, newRel, oldPath, newPath)
	}
	afterStorageCopySourceStat = func(string) error {
		copyCalls++
		return nil
	}
	t.Cleanup(func() {
		movePathRename = originalMovePathRename
		afterStorageCopySourceStat = originalAfterStorageCopySourceStat
	})

	err := fs.movePath(sourcePath, destinationPath)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("movePath(mount appeared during failed rename) error = %v, want ErrNotRegular", err)
	}
	if copyCalls != 0 {
		t.Fatalf("copy source hook calls = %d, want 0", copyCalls)
	}
	data, readErr := os.ReadFile(sourcePath)
	if readErr != nil || string(data) != "value" {
		t.Fatalf("source after rejected fallback copy = %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination after rejected fallback copy exists: %v", statErr)
	}
}

func TestFileSystem_CopyDirRollbackPreservesDestinationMountBoundary(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	sourcePath := fs.workspace.FullPath("/source")
	destinationPath := filepath.Join(fs.trashRoot, "copy-parent", "destination")
	if err := os.MkdirAll(filepath.Join(sourcePath, "mounted"), 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(sourcePath, "mounted", "z-link")); err != nil {
		t.Fatalf("Symlink(source) error: %v", err)
	}
	fs.readDeleteMountPoints = func() ([]string, error) {
		mountPoint := filepath.Join(destinationPath, "mounted")
		if _, err := os.Stat(mountPoint); err == nil {
			return []string{mountPoint}, nil
		}
		return []string{"/"}, nil
	}

	srcRoot, srcRel, srcAbs, err := fs.resolveStoragePathRoot(sourcePath)
	if err != nil {
		t.Fatalf("resolveStoragePathRoot(source) error: %v", err)
	}
	dstRoot, dstRel, dstAbs, err := fs.resolveStoragePathRoot(destinationPath)
	if err != nil {
		t.Fatalf("resolveStoragePathRoot(destination) error: %v", err)
	}
	err = fs.copyDirBetweenRoots(srcRoot, srcRel, srcAbs, dstRoot, dstRel, dstAbs)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("copyDirBetweenRoots(mounted rollback) error = %v, want ErrNotRegular", err)
	}
	if info, statErr := os.Stat(filepath.Join(destinationPath, "mounted")); statErr != nil || !info.IsDir() {
		t.Fatalf("mounted destination residue after safe rollback = %v, %v; want directory", info, statErr)
	}
	if info, statErr := os.Lstat(filepath.Join(sourcePath, "mounted", "z-link")); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("source symlink after rejected copy = %v, %v", info, statErr)
	}
}

func TestFileSystem_CopyFileRechecksMountBoundaryAfterSourceStatHook(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	sourcePath := fs.workspace.FullPath("/source.txt")
	destinationPath := filepath.Join(fs.trashRoot, "copy-parent", "destination.txt")
	if err := os.WriteFile(sourcePath, []byte("value"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints()

	originalAfterStorageCopySourceStat := afterStorageCopySourceStat
	afterStorageCopySourceStat = func(srcAbs string) error {
		if srcAbs == sourcePath {
			fs.readDeleteMountPoints = fixedDeleteMountPoints(destinationPath)
		}
		return nil
	}
	t.Cleanup(func() {
		afterStorageCopySourceStat = originalAfterStorageCopySourceStat
	})

	err := fs.copyFile(sourcePath, destinationPath)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("copyFile(mount appeared after source stat) error = %v, want ErrNotRegular", err)
	}
	data, readErr := os.ReadFile(sourcePath)
	if readErr != nil || string(data) != "value" {
		t.Fatalf("source after rejected direct copy = %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination after rejected direct copy exists: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Dir(destinationPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination parent after rejected direct copy exists: %v", statErr)
	}
}

func TestFileSystem_CopyFilePreservesTempWhenCleanupBoundaryChanges(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	sourcePath := fs.workspace.FullPath("/source.txt")
	destinationPath := filepath.Join(fs.trashRoot, "copy-parent", "destination.txt")
	if err := os.WriteFile(sourcePath, []byte("value"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(destination parent) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints()

	originalCreateStorageCopyTempFile := createStorageCopyTempFile
	var tempPath string
	createStorageCopyTempFile = func(root *os.Root, parentName, prefix string) (*os.File, string, error) {
		file, rel, err := originalCreateStorageCopyTempFile(root, parentName, prefix)
		if err != nil {
			return nil, "", err
		}
		tempPath = filepath.Join(fs.trashRoot, rel)
		if err := file.Close(); err != nil {
			return nil, "", err
		}
		fs.readDeleteMountPoints = fixedDeleteMountPoints(filepath.Dir(destinationPath))
		return file, rel, nil
	}
	t.Cleanup(func() {
		createStorageCopyTempFile = originalCreateStorageCopyTempFile
	})

	err := fs.copyFile(sourcePath, destinationPath)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("copyFile(temp cleanup mount) error = %v, want ErrNotRegular", err)
	}
	if tempPath == "" {
		t.Fatal("copy temp path was not captured")
	}
	if _, statErr := os.Stat(tempPath); statErr != nil {
		t.Fatalf("copy temp residue after unsafe cleanup rejection error: %v", statErr)
	}
	if data, readErr := os.ReadFile(sourcePath); readErr != nil || string(data) != "value" {
		t.Fatalf("source after rejected temp cleanup = %q, %v", data, readErr)
	}
}

func TestFileSystem_CopyFilePreservesCreatedDirsWhenCleanupBoundaryChanges(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	sourcePath := fs.workspace.FullPath("/source.txt")
	destinationPath := filepath.Join(fs.trashRoot, "deep", "copy", "destination.txt")
	if err := os.WriteFile(sourcePath, []byte("value"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints()

	originalCreateStorageCopyTempFile := createStorageCopyTempFile
	tempErr := errors.New("temp create failed")
	createStorageCopyTempFile = func(*os.Root, string, string) (*os.File, string, error) {
		fs.readDeleteMountPoints = fixedDeleteMountPoints(filepath.Dir(destinationPath))
		return nil, "", tempErr
	}
	t.Cleanup(func() {
		createStorageCopyTempFile = originalCreateStorageCopyTempFile
	})

	err := fs.copyFile(sourcePath, destinationPath)
	if !errors.Is(err, tempErr) || errors.Is(err, ErrNotRegular) || !strings.Contains(err.Error(), "created directories retained") {
		t.Fatalf("copyFile(created-dir retention) error = %v, want temp error and retained-directory detail", err)
	}
	if info, statErr := os.Stat(filepath.Dir(destinationPath)); statErr != nil || !info.IsDir() {
		t.Fatalf("created destination directory after unsafe cleanup rejection = %v, %v", info, statErr)
	}
	if data, readErr := os.ReadFile(sourcePath); readErr != nil || string(data) != "value" {
		t.Fatalf("source after rejected created-dir cleanup = %q, %v", data, readErr)
	}
}

func TestFileSystem_DeleteToTrashPreservesPublishedCopyWhenCleanupBoundaryChanges(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/source.txt", strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints()

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncErr := errors.New("sync copied file failed")
	mountedParent := ""
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		contentRel := filepath.Join(relName, "content")
		if root == fs.trashRootHandle && mountedParent == "" {
			if info, err := root.Lstat(contentRel); err == nil && info.Mode().IsRegular() {
				mountedParent = absPath
				fs.readDeleteMountPoints = fixedDeleteMountPoints(absPath)
				return syncErr
			}
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	err := fs.Delete(ctx, "/source.txt")
	if !errors.Is(err, syncErr) || !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Delete(published copy cleanup mount) error = %v, want sync error and ErrNotRegular", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	var residual *DeleteStageResidualError
	if !errors.As(err, &residual) {
		t.Fatalf("Delete(published copy cleanup mount) error = %v, want copied-content residual", err)
	}
	if mountedParent == "" {
		t.Fatal("published trash copy did not reach sync hook")
	}
	if residual.StagePath != filepath.Join(mountedParent, "content") {
		t.Fatalf("copied-content residual path = %q, want %q", residual.StagePath, filepath.Join(mountedParent, "content"))
	}
	if _, statErr := os.Lstat(fs.workspace.FullPath("/source.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("original source after fail-closed copy = %v, want os.ErrNotExist", statErr)
	}
	workspaceStage := journaledTrashWorkspaceStagePath(t, fs, recovery)
	if data, readErr := os.ReadFile(workspaceStage); readErr != nil || string(data) != "value" {
		t.Fatalf("staged source after rejected published-copy cleanup = %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(filepath.Join(mountedParent, "content")); readErr != nil || string(data) != "value" {
		t.Fatalf("safe published-copy residue = %q, %v", data, readErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash metadata after failed copy = %+v, %v; want empty", items, listErr)
	}
	requireJournaledTrashMutationGate(t, fs)
	fs.readDeleteMountPoints = fixedDeleteMountPoints()
	repairPreparedTrashTransferAndRecover(t, fs, recovery)
	if data, readErr := os.ReadFile(fs.workspace.FullPath("/source.txt")); readErr != nil || string(data) != "value" {
		t.Fatalf("source after repaired recovery = %q, %v", data, readErr)
	}
}

func TestFileSystem_StagedDeleteRetainsTrashCopyWhenMountAppearsAfterPublish(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/post-publish-mount.txt"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints()

	var copiedPath string
	originalHook := afterDeleteTrashCopy
	afterDeleteTrashCopy = func(logicalPath, destinationPath string) error {
		if logicalPath == targetPath {
			copiedPath = destinationPath
			fs.readDeleteMountPoints = fixedDeleteMountPoints(destinationPath)
		}
		return nil
	}
	t.Cleanup(func() { afterDeleteTrashCopy = originalHook })

	err := fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	if !errors.Is(err, ErrNotRegular) || !errors.As(err, &residual) {
		t.Fatalf("Delete(post-publish mount) error = %v, want mount rejection and copied-content residual", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	if copiedPath == "" || residual.StagePath != copiedPath {
		t.Fatalf("copied-content residual path = %q, want %q", residual.StagePath, copiedPath)
	}
	if data, readErr := os.ReadFile(copiedPath); readErr != nil || string(data) != "value" {
		t.Fatalf("retained copied content = %q, %v", data, readErr)
	}
	if _, statErr := os.Lstat(fs.workspace.FullPath(targetPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("original source after fail-closed copy = %v, want os.ErrNotExist", statErr)
	}
	workspaceStage := journaledTrashWorkspaceStagePath(t, fs, recovery)
	if data, readErr := os.ReadFile(workspaceStage); readErr != nil || string(data) != "value" {
		t.Fatalf("staged source after post-publish mount = %q, %v", data, readErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash metadata after post-publish mount = %+v, %v; want empty", items, listErr)
	}
	requireJournaledTrashMutationGate(t, fs)
	fs.readDeleteMountPoints = fixedDeleteMountPoints()
	repairPreparedTrashTransferAndRecover(t, fs, recovery)
	if data, readErr := os.ReadFile(fs.workspace.FullPath(targetPath)); readErr != nil || string(data) != "value" {
		t.Fatalf("source after repaired recovery = %q, %v", data, readErr)
	}
}

func TestFileSystem_StagedPermanentDeleteRetainsQuarantineWhenMountAppearsBeforeRemoval(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	fs.UpdateTrashSettings(false, 30, 0)
	targetPath := "/quarantine-mount.txt"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints()

	var quarantineContentPath string
	originalHook := afterDeleteQuarantineCapture
	afterDeleteQuarantineCapture = func(logicalPath, contentPath string) error {
		if logicalPath == targetPath {
			quarantineContentPath = contentPath
			fs.readDeleteMountPoints = fixedDeleteMountPoints(contentPath)
		}
		return nil
	}
	t.Cleanup(func() { afterDeleteQuarantineCapture = originalHook })

	err := fs.Delete(ctx, targetPath)
	var residual *DeleteStageResidualError
	var cleanup *DeleteCleanupWarningError
	if !errors.Is(err, ErrNotRegular) || !errors.As(err, &cleanup) || !errors.As(err, &residual) {
		t.Fatalf("Delete(quarantine mount) error = %v, want committed cleanup residual", err)
	}
	if quarantineContentPath == "" || residual.StagePath != quarantineContentPath {
		t.Fatalf("quarantine residual path = %q, want %q", residual.StagePath, quarantineContentPath)
	}
	if data, readErr := os.ReadFile(quarantineContentPath); readErr != nil || string(data) != "value" {
		t.Fatalf("retained quarantine content = %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(fs.workspace.FullPath(targetPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("logical path unexpectedly exists: %v", statErr)
	}
}

func TestFileSystem_RestorePreservesPublishedCopyWhenCleanupBoundaryChanges(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/source.txt", strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.Delete(ctx, "/source.txt"); err != nil {
		t.Fatalf("Delete(source) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	if err := fs.Mkdir(ctx, "/restore-parent"); err != nil {
		t.Fatalf("Mkdir(restore parent) error: %v", err)
	}
	destinationPath := fs.workspace.FullPath("/restore-parent/restored.txt")
	fs.readDeleteMountPoints = fixedDeleteMountPoints()

	originalSyncManagedStorageDir := syncManagedStorageDir
	syncErr := errors.New("sync restored file failed")
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if root == fs.filesRootHandle && relName == "restore-parent" {
			if info, err := root.Lstat(filepath.Join(relName, "restored.txt")); err == nil && info.Mode().IsRegular() {
				fs.readDeleteMountPoints = fixedDeleteMountPoints(filepath.Dir(destinationPath))
				return syncErr
			}
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	err = fs.RestoreFromTrashTo(ctx, items[0].ID, "/restore-parent/restored.txt")
	if !errors.Is(err, syncErr) || !errors.Is(err, ErrNotRegular) {
		t.Fatalf("RestoreFromTrashTo(published copy cleanup mount) error = %v, want sync error and ErrNotRegular", err)
	}
	if data, readErr := os.ReadFile(destinationPath); readErr != nil || string(data) != "value" {
		t.Fatalf("safe restored-copy residue = %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(filepath.Join(fs.trashRoot, items[0].ID, "content")); readErr != nil || string(data) != "value" {
		t.Fatalf("trash source after failed restore = %q, %v", data, readErr)
	}
	if _, itemErr := fs.GetTrashItem(ctx, items[0].ID); itemErr != nil {
		t.Fatalf("trash metadata after failed restore error: %v", itemErr)
	}
}

func TestFileSystem_RollbackJournaledTrashDeleteRefusesReplicaCleanupAcrossNewMountBoundary(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/rollback.txt", strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(rollback source) error: %v", err)
	}
	fs.readDeleteMountPoints = fixedDeleteMountPoints()
	participantErr := errors.New("precommit participant failed")
	var canonicalContent string
	fs.SetTrashParticipantHooks(TrashParticipantHooks{
		PrepareDelete: func(context.Context, string, string) ([]byte, error) {
			return []byte("participant"), nil
		},
		ApplyDelete: func(_ context.Context, _ string, _ string, _ []byte, committed bool) error {
			if committed {
				return nil
			}
			entries, err := os.ReadDir(fs.trashRoot)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.IsDir() && entry.Name() != trashTransferJournalDir {
					canonicalContent = filepath.Join(fs.trashRoot, entry.Name(), "content")
					break
				}
			}
			if canonicalContent == "" {
				return errors.New("canonical Trash item was not published")
			}
			fs.readDeleteMountPoints = fixedDeleteMountPoints(canonicalContent)
			return participantErr
		},
		RollbackDelete: func(context.Context, string, string, []byte) error { return nil },
		CompleteDelete: completeDeleteParticipantForTest,
		RecoveryStateReliable: func() error {
			return nil
		},
	})

	err := fs.Delete(ctx, "/rollback.txt")
	if !errors.Is(err, participantErr) || !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Delete(journaled rollback mount) error = %v, want participant error and ErrNotRegular", err)
	}
	recovery := requireJournaledTrashRecovery(t, fs, err)
	workspaceStage := journaledTrashWorkspaceStagePath(t, fs, recovery)
	data, readErr := os.ReadFile(workspaceStage)
	if readErr != nil || string(data) != "value" {
		t.Fatalf("workspace stage after rejected replica cleanup = %q, %v", data, readErr)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil || len(items) != 0 {
		t.Fatalf("trash metadata after rejected precommit rollback = %+v, %v; want empty", items, listErr)
	}
	if data, readErr := os.ReadFile(canonicalContent); readErr != nil || string(data) != "value" {
		t.Fatalf("canonical replica after rejected cleanup = %q, %v", data, readErr)
	}
	requireJournaledTrashMutationGate(t, fs)
	fs.readDeleteMountPoints = fixedDeleteMountPoints()
	report, recoveryErr := fs.RecoverTrashTransfers(ctx)
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashTransfers() after mount repair error: %v", recoveryErr)
	}
	if report.RolledBack != 1 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashTransfers() report = %+v, want one rollback", report)
	}
	if data, readErr := os.ReadFile(fs.workspace.FullPath("/rollback.txt")); readErr != nil || string(data) != "value" {
		t.Fatalf("workspace source after repaired rollback = %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(canonicalContent); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("canonical replica after recovered rollback exists: %v", statErr)
	}
}

func TestFileSystem_DeleteFromTrashRejectsNestedMountBeforeStaging(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tree/mounted"); err != nil {
		t.Fatalf("Mkdir(mounted) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/mounted/value.txt", strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(value) error: %v", err)
	}
	if err := fs.Delete(ctx, "/tree"); err != nil {
		t.Fatalf("Delete(tree) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	itemRoot := filepath.Join(fs.trashRoot, items[0].ID)
	contentPath := filepath.Join(itemRoot, "content", "mounted")
	fs.readDeleteMountPoints = fixedDeleteMountPoints(contentPath)
	removeCalls := 0
	originalRemoveTrashPath := fs.removeTrashPath
	fs.removeTrashPath = func(target string) error {
		removeCalls++
		return originalRemoveTrashPath(target)
	}

	err = fs.DeleteFromTrash(ctx, items[0].ID)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("DeleteFromTrash(nested mount) error = %v, want ErrNotRegular", err)
	}
	if removeCalls != 0 {
		t.Fatalf("recursive trash removal calls = %d, want 0", removeCalls)
	}
	if _, err := fs.GetTrashItem(ctx, items[0].ID); err != nil {
		t.Fatalf("GetTrashItem() after rejected delete error: %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(contentPath, "value.txt"))
	if readErr != nil || string(data) != "value" {
		t.Fatalf("trash content after rejected delete = %q, %v", data, readErr)
	}
	if err := fs.WriteFile(ctx, "/after-trash-mount-rejection.txt", strings.NewReader("ok")); err != nil {
		t.Fatalf("WriteFile() after rejected trash delete error: %v", err)
	}
}

func TestFileSystem_WalkTrashItemRestorePathsAuthorizesMountPointBeforeConflict(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		mountedChild bool
		deniedPath   string
		wantVisited  string
	}{
		{name: "trash content root", deniedPath: "/tree", wantVisited: "/tree"},
		{name: "trash content descendant", mountedChild: true, deniedPath: "/tree/mounted", wantVisited: "/tree|/tree/mounted"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			ctx := context.Background()
			if err := fs.Mkdir(ctx, "/tree"); err != nil {
				t.Fatalf("Mkdir(tree) error: %v", err)
			}
			if err := fs.Mkdir(ctx, "/tree/mounted"); err != nil {
				t.Fatalf("Mkdir(mounted) error: %v", err)
			}
			if err := fs.WriteFile(ctx, "/tree/mounted/secret.txt", strings.NewReader("secret")); err != nil {
				t.Fatalf("WriteFile(secret) error: %v", err)
			}
			if err := fs.Delete(ctx, "/tree"); err != nil {
				t.Fatalf("Delete(tree) error: %v", err)
			}
			items, err := fs.ListTrash(ctx)
			if err != nil || len(items) != 1 {
				t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
			}
			mountPoint := filepath.Join(fs.trashRoot, items[0].ID, "content")
			if testCase.mountedChild {
				mountPoint = filepath.Join(mountPoint, "mounted")
			}
			fs.readDeleteMountPoints = fixedDeleteMountPoints(mountPoint)

			errDenied := errors.New("mounted restore path denied")
			var visited []string
			err = fs.WalkTrashItemRestorePaths(ctx, items[0].ID, func(restoredPath string, _ bool, _ int64) error {
				visited = append(visited, restoredPath)
				if restoredPath == testCase.deniedPath {
					return errDenied
				}
				return nil
			})
			if !errors.Is(err, errDenied) {
				t.Fatalf("WalkTrashItemRestorePaths(denied mount path) error = %v, want callback denial", err)
			}
			if got := strings.Join(visited, "|"); got != testCase.wantVisited {
				t.Fatalf("visited denied restore paths = %q, want %q", got, testCase.wantVisited)
			}

			visited = nil
			err = fs.WalkTrashItemRestorePaths(ctx, items[0].ID, func(restoredPath string, _ bool, _ int64) error {
				visited = append(visited, restoredPath)
				return nil
			})
			if !errors.Is(err, ErrNotRegular) {
				t.Fatalf("WalkTrashItemRestorePaths(mount path) error = %v, want ErrNotRegular", err)
			}
			if got := strings.Join(visited, "|"); got != testCase.wantVisited {
				t.Fatalf("visited mounted restore paths = %q, want %q", got, testCase.wantVisited)
			}
		})
	}
}

func fixedDeleteMountPoints(paths ...string) func() ([]string, error) {
	return func() ([]string, error) {
		if len(paths) == 0 {
			return []string{"/"}, nil
		}
		return append([]string(nil), paths...), nil
	}
}

func assertWorkspaceTreeAndTrashUnchanged(t *testing.T, fs *FileSystem, filePath, want string) {
	t.Helper()
	data, err := os.ReadFile(fs.workspace.FullPath(filePath))
	if err != nil || string(data) != want {
		t.Fatalf("workspace file %s after rejected deletion = %q, %v", filePath, data, err)
	}
	items, err := fs.ListTrash(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("trash after rejected deletion = %+v, %v; want empty", items, err)
	}
}
