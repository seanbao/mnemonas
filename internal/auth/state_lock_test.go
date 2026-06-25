//go:build unix

package auth

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const authStateLockHelperUsersFileEnv = "MNEMONAS_TEST_AUTH_STATE_LOCK_USERS_FILE"

func privateAuthStateTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		t.Fatalf("Abs(temp directory) error = %v", err)
	}
	for current := dir; current != tempRoot; current = filepath.Dir(current) {
		if err := os.Chmod(current, 0o700); err != nil {
			t.Fatalf("Chmod(%s) error = %v", current, err)
		}
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	return dir
}

func TestAcquireStateLockLifecycle(t *testing.T) {
	usersFile := filepath.Join(privateAuthStateTestDir(t), "auth", "users.json")
	lock, err := AcquireStateLock(usersFile)
	if err != nil {
		t.Fatalf("AcquireStateLock() error = %v", err)
	}

	expectedPath := filepath.Join(filepath.Dir(usersFile), authStateLockFileName)
	if lock.Path() != expectedPath {
		t.Fatalf("Path() = %q, want %q", lock.Path(), expectedPath)
	}
	info, err := os.Lstat(expectedPath)
	if err != nil {
		t.Fatalf("Lstat(lock file) error = %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("lock file mode = %v, want regular file", info.Mode())
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("lock file permissions = %o, want 600", info.Mode().Perm())
	}

	contender, err := AcquireStateLock(usersFile)
	if contender != nil {
		_ = contender.Close()
		t.Fatal("second AcquireStateLock() returned a lock while the first was held")
	}
	if !errors.Is(err, ErrAuthStateLockHeld) {
		t.Fatalf("second AcquireStateLock() error = %v, want ErrAuthStateLockHeld", err)
	}

	if err := lock.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	reacquired, err := AcquireStateLock(usersFile)
	if err != nil {
		t.Fatalf("AcquireStateLock() after Close error = %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatalf("reacquired Close() error = %v", err)
	}
}

func TestAcquireStateLockRejectsCrossProcessContender(t *testing.T) {
	dir := privateAuthStateTestDir(t)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod(auth directory) error = %v", err)
	}
	usersFile := filepath.Join(dir, "users.json")
	lock, err := AcquireStateLock(usersFile)
	if err != nil {
		t.Fatalf("AcquireStateLock() error = %v", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	cmd := exec.Command(os.Args[0], "-test.run=^TestAuthStateLockHelperProcess$")
	cmd.Env = append(os.Environ(), authStateLockHelperUsersFileEnv+"="+usersFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper process error = %v\n%s", err, output)
	}
}

func TestAuthStateLockHelperProcess(t *testing.T) {
	usersFile := os.Getenv(authStateLockHelperUsersFileEnv)
	if usersFile == "" {
		t.Skip("helper process only")
	}

	lock, err := AcquireStateLock(usersFile)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("AcquireStateLock() unexpectedly acquired a cross-process lock")
	}
	if !errors.Is(err, ErrAuthStateLockHeld) {
		t.Fatalf("AcquireStateLock() error = %v, want ErrAuthStateLockHeld", err)
	}
}

func TestAcquireStateLockRejectsSymlinks(t *testing.T) {
	t.Run("users file", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target-users.json")
		if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
			t.Fatalf("WriteFile(target) error = %v", err)
		}
		usersFile := filepath.Join(dir, "users.json")
		if err := os.Symlink(target, usersFile); err != nil {
			t.Fatalf("Symlink(users file) error = %v", err)
		}

		lock, err := AcquireStateLock(usersFile)
		if lock != nil {
			_ = lock.Close()
			t.Fatal("AcquireStateLock() returned a lock for a symlink users file")
		}
		if !errors.Is(err, ErrAuthStateLockUnsafePath) {
			t.Fatalf("AcquireStateLock() error = %v, want ErrAuthStateLockUnsafePath", err)
		}
	})

	t.Run("parent directory", func(t *testing.T) {
		dir := t.TempDir()
		realDir := filepath.Join(dir, "real-auth")
		if err := os.Mkdir(realDir, 0700); err != nil {
			t.Fatalf("Mkdir(real auth) error = %v", err)
		}
		linkedDir := filepath.Join(dir, "linked-auth")
		if err := os.Symlink(realDir, linkedDir); err != nil {
			t.Fatalf("Symlink(parent directory) error = %v", err)
		}

		lock, err := AcquireStateLock(filepath.Join(linkedDir, "users.json"))
		if lock != nil {
			_ = lock.Close()
			t.Fatal("AcquireStateLock() returned a lock through a symlink directory")
		}
		if !errors.Is(err, ErrAuthStateLockUnsafePath) {
			t.Fatalf("AcquireStateLock() error = %v, want ErrAuthStateLockUnsafePath", err)
		}
	})

	t.Run("lock file", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target-lock")
		if err := os.WriteFile(target, nil, 0600); err != nil {
			t.Fatalf("WriteFile(target lock) error = %v", err)
		}
		lockPath := filepath.Join(dir, authStateLockFileName)
		if err := os.Symlink(target, lockPath); err != nil {
			t.Fatalf("Symlink(lock file) error = %v", err)
		}

		lock, err := AcquireStateLock(filepath.Join(dir, "users.json"))
		if lock != nil {
			_ = lock.Close()
			t.Fatal("AcquireStateLock() returned a symlink lock file")
		}
		if !errors.Is(err, ErrAuthStateLockUnsafePath) {
			t.Fatalf("AcquireStateLock() error = %v, want ErrAuthStateLockUnsafePath", err)
		}
	})
}

func TestAcquireStateLockRepairsPermissions(t *testing.T) {
	dir := privateAuthStateTestDir(t)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod(auth directory) error = %v", err)
	}
	lockPath := filepath.Join(dir, authStateLockFileName)
	if err := os.WriteFile(lockPath, nil, 0644); err != nil {
		t.Fatalf("WriteFile(lock) error = %v", err)
	}
	if err := os.Chmod(lockPath, 0644); err != nil {
		t.Fatalf("Chmod(lock) error = %v", err)
	}

	lock, err := AcquireStateLock(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("AcquireStateLock() error = %v", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("Stat(lock) error = %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("lock file permissions = %o, want 600", info.Mode().Perm())
	}
}
