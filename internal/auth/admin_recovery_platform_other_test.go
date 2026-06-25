//go:build !unix

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoverAdminPasswordRejectsUnsupportedPlatformBeforeFileAccess(t *testing.T) {
	usersPath := filepath.Join(t.TempDir(), "users.json")
	var stateLock *StateLock
	result, err := stateLock.RecoverAdminPassword(usersPath, "admin")
	if result != nil || !errors.Is(err, ErrAdminRecoveryUnsupported) {
		t.Fatalf("RecoverAdminPassword() result=%#v error=%v, want unsupported platform", result, err)
	}
	if _, err := os.Lstat(usersPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported recovery accessed or created users file: %v", err)
	}
}
