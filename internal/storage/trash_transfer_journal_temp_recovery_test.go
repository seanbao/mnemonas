package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const trashTransferJournalTempNameForTest = ".trash-transfer-journal-0123456789abcdef.tmp"

func TestFileSystem_RecoverTrashTransfersRemovesOwnedJournalPublishTemp(t *testing.T) {
	fs := setupFileSystem(t)
	tempRel, tempAbs := writeTrashTransferJournalTempForTest(t, fs, trashTransferJournalTempNameForTest, []byte(`{"version":`))

	originalSync := syncTrashTransferJournalTempCleanupDir
	synced := false
	syncTrashTransferJournalTempCleanupDir = func(dir *os.File) error {
		if _, err := fs.trashRootHandle.Lstat(tempRel); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("journal temp still exists during directory sync: %v", err)
		}
		synced = true
		return originalSync(dir)
	}
	t.Cleanup(func() { syncTrashTransferJournalTempCleanupDir = originalSync })

	report, err := fs.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if len(report.UntrackedPaths) != 0 || !synced {
		t.Fatalf("RecoverTrashTransfers() report = %+v, synced=%t", report, synced)
	}
	if _, err := os.Lstat(tempAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(cleaned journal temp) error = %v, want os.ErrNotExist", err)
	}
}

func TestFileSystem_RecoverTrashTransfersDoesNotGeneralizeJournalTempCleanup(t *testing.T) {
	for _, name := range []string{
		".trash-transfer-journal-0123456789abcde.tmp",
		".trash-transfer-journal-0123456789abcdef0.tmp",
		".trash-transfer-journal-0123456789abcdeF.tmp",
		".trash-transfer-journal-0123456789abcdef.tmp.bak",
		".storage-copy-0123456789abcdef.tmp",
		"notes.tmp",
	} {
		t.Run(name, func(t *testing.T) {
			fs := setupFileSystem(t)
			_, tempAbs := writeTrashTransferJournalTempForTest(t, fs, name, []byte("unknown"))

			report, err := fs.RecoverTrashTransfers(context.Background())
			if !errors.Is(err, ErrTrashRecoveryRequired) {
				t.Fatalf("RecoverTrashTransfers() error = %v, want ErrTrashRecoveryRequired", err)
			}
			requireTrashTransferUntrackedPathForTest(t, report, tempAbs)
			if _, err := os.Lstat(tempAbs); err != nil {
				t.Fatalf("Lstat(retained unknown temp) error: %v", err)
			}
		})
	}
}

func TestFileSystem_RecoverTrashTransfersRejectsNonRegularJournalPublishTemp(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		create func(string) error
	}{
		{name: "directory", create: func(path string) error { return os.Mkdir(path, 0o700) }},
		{name: "symlink", create: func(path string) error { return os.Symlink("missing-target", path) }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.name == "symlink" && runtime.GOOS == "windows" {
				t.Skip("symlink creation requires additional Windows privileges")
			}
			fs := setupFileSystem(t)
			if err := fs.ensureTrashTransferJournalDir(); err != nil {
				t.Fatalf("ensureTrashTransferJournalDir() error: %v", err)
			}
			tempAbs := filepath.Join(fs.trashRoot, trashTransferJournalDir, trashTransferJournalTempNameForTest)
			if err := testCase.create(tempAbs); err != nil {
				t.Fatalf("create journal temp %s: %v", testCase.name, err)
			}

			report, err := fs.RecoverTrashTransfers(context.Background())
			if !errors.Is(err, ErrTrashRecoveryRequired) {
				t.Fatalf("RecoverTrashTransfers() error = %v, want ErrTrashRecoveryRequired", err)
			}
			requireTrashTransferUntrackedPathForTest(t, report, tempAbs)
			if _, err := os.Lstat(tempAbs); err != nil {
				t.Fatalf("Lstat(retained non-regular temp) error: %v", err)
			}
		})
	}
}

func TestFileSystem_RecoverTrashTransfersRejectsReplacedJournalPublishTemp(t *testing.T) {
	fs := setupFileSystem(t)
	_, tempAbs := writeTrashTransferJournalTempForTest(t, fs, trashTransferJournalTempNameForTest, []byte("owned partial journal"))

	originalHook := beforeTrashTransferJournalTempRemoval
	hookCalled := false
	beforeTrashTransferJournalTempRemoval = func(path string) error {
		hookCalled = true
		if path != tempAbs {
			t.Fatalf("temp removal hook path = %q, want %q", path, tempAbs)
		}
		if err := os.Remove(tempAbs); err != nil {
			return err
		}
		return os.WriteFile(tempAbs, []byte("replacement"), 0o600)
	}
	t.Cleanup(func() { beforeTrashTransferJournalTempRemoval = originalHook })

	report, err := fs.RecoverTrashTransfers(context.Background())
	if !hookCalled || !errors.Is(err, ErrTrashRecoveryRequired) || !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("RecoverTrashTransfers() = %+v, %v, hookCalled=%t; want replacement rejection", report, err, hookCalled)
	}
	requireTrashTransferUntrackedPathForTest(t, report, tempAbs)
	data, readErr := os.ReadFile(tempAbs)
	if readErr != nil || string(data) != "replacement" {
		t.Fatalf("replacement journal temp = %q, %v", data, readErr)
	}
}

func TestFileSystem_RecoverTrashTransfersBlocksWhenJournalTempCleanupSyncFails(t *testing.T) {
	fs := setupFileSystem(t)
	tempRel, tempAbs := writeTrashTransferJournalTempForTest(t, fs, trashTransferJournalTempNameForTest, []byte("owned partial journal"))

	originalSync := syncTrashTransferJournalTempCleanupDir
	syncErr := errors.New("journal temp cleanup directory sync failed")
	syncTrashTransferJournalTempCleanupDir = func(dir *os.File) error {
		if _, err := fs.trashRootHandle.Lstat(tempRel); errors.Is(err, os.ErrNotExist) {
			return syncErr
		}
		return originalSync(dir)
	}
	t.Cleanup(func() { syncTrashTransferJournalTempCleanupDir = originalSync })

	report, err := fs.RecoverTrashTransfers(context.Background())
	if !errors.Is(err, ErrTrashRecoveryRequired) || !errors.Is(err, syncErr) {
		t.Fatalf("RecoverTrashTransfers() = %+v, %v, want cleanup sync failure", report, err)
	}
	if _, err := os.Lstat(tempAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(unlinked journal temp) error = %v, want os.ErrNotExist", err)
	}
	if fs.trashMutationBlocked == nil {
		t.Fatal("journal temp cleanup sync failure did not gate Trash mutations")
	}
}

func writeTrashTransferJournalTempForTest(t *testing.T, fs *FileSystem, name string, data []byte) (string, string) {
	t.Helper()
	if err := fs.ensureTrashTransferJournalDir(); err != nil {
		t.Fatalf("ensureTrashTransferJournalDir() error: %v", err)
	}
	rel := filepath.Join(trashTransferJournalDir, name)
	abs := filepath.Join(fs.trashRoot, rel)
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		t.Fatalf("WriteFile(journal temp) error: %v", err)
	}
	return rel, abs
}

func requireTrashTransferUntrackedPathForTest(t *testing.T, report TrashTransferRecoveryReport, want string) {
	t.Helper()
	for _, path := range report.UntrackedPaths {
		if path == want {
			return
		}
	}
	t.Fatalf("RecoverTrashTransfers() untracked paths = %v, want %q", report.UntrackedPaths, want)
}
