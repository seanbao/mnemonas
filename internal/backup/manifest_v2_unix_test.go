//go:build unix

package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCopySourceTreeRejectsStableSpecialPermissionBits(t *testing.T) {
	tests := []struct {
		name     string
		mode     os.FileMode
		makePath func(t *testing.T, source string) string
	}{
		{
			name: "directory sticky bit",
			mode: 0o700 | os.ModeSticky,
			makePath: func(t *testing.T, source string) string {
				t.Helper()
				path := filepath.Join(source, "special-directory")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("Mkdir(special directory) error: %v", err)
				}
				return path
			},
		},
		{
			name: "regular file setuid bit",
			mode: 0o600 | os.ModeSetuid,
			makePath: func(t *testing.T, source string) string {
				t.Helper()
				path := filepath.Join(source, "special-file")
				if err := os.WriteFile(path, []byte("special mode"), 0o600); err != nil {
					t.Fatalf("WriteFile(special file) error: %v", err)
				}
				return path
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := secureBackupTestTempDir(t)
			source := filepath.Join(tmpDir, "source")
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatalf("Mkdir(source) error: %v", err)
			}
			specialPath := tt.makePath(t, source)
			if err := os.Chmod(specialPath, tt.mode); err != nil {
				t.Skipf("special permission bit unavailable: %v", err)
			}
			info, err := os.Lstat(specialPath)
			if err != nil {
				t.Fatalf("Lstat(special path) error: %v", err)
			}
			wantSpecialBits := tt.mode & specialPermissionBits
			if info.Mode()&wantSpecialBits != wantSpecialBits {
				t.Skipf("filesystem did not retain special permission bits: got %v, want %v", info.Mode()&specialPermissionBits, wantSpecialBits)
			}

			_, _, _, err = copySourceTree(context.Background(), source, filepath.Join(tmpDir, "snapshot", "data"), nil)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("copySourceTree() error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestCreateNamedRestoreTargetRejectsReplaceableWritableAncestor(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	shared := filepath.Join(tmpDir, "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatalf("Mkdir(shared) error: %v", err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatalf("Chmod(shared) error: %v", err)
	}

	target := filepath.Join(shared, "restore-target")
	_, err := createNamedRestoreTarget(target, ".partial-test")
	if !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrBackupTargetLockUnsafeAncestor) {
		t.Fatalf("createNamedRestoreTarget() error = %v, want ErrUnsafePath and ErrBackupTargetLockUnsafeAncestor", err)
	}
	if _, statErr := os.Lstat(target + ".partial-test"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore staging target stat error = %v, want not exist", statErr)
	}
}
