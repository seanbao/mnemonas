package smbcred

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNTHashHex(t *testing.T) {
	if got, want := NTHashHex("password"), "8846f7eaee8fb117ad06bdd830b7586c"; got != want {
		t.Fatalf("NTHashHex() = %s, want %s", got, want)
	}
}

func TestNormalizeNTHashHex(t *testing.T) {
	got, err := NormalizeNTHashHex("8846F7EAEE8FB117AD06BDD830B7586C")
	if err != nil {
		t.Fatalf("NormalizeNTHashHex() error: %v", err)
	}
	if got != "8846f7eaee8fb117ad06bdd830b7586c" {
		t.Fatalf("NormalizeNTHashHex() = %s, want lowercase hash", got)
	}

	if _, err := NormalizeNTHashHex("not-a-hash"); err == nil {
		t.Fatal("expected invalid hash error")
	}
}

func TestStorePersistsCredential(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), ".mnemonas", "smb-credentials.json")

	store, err := NewStore(filePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	credential, err := store.SetPassword("user-1", "Alice", "password", true)
	if err != nil {
		t.Fatalf("SetPassword() error: %v", err)
	}
	if !credential.Enabled {
		t.Fatal("credential should be enabled")
	}
	if credential.NTHashHex != "8846f7eaee8fb117ad06bdd830b7586c" {
		t.Fatalf("stored hash = %s, want NT hash", credential.NTHashHex)
	}

	reloaded, err := NewStore(filePath)
	if err != nil {
		t.Fatalf("NewStore(reload) error: %v", err)
	}
	got, ok := reloaded.GetByUserID("user-1")
	if !ok {
		t.Fatal("expected user ID lookup to find credential")
	}
	if got.Username != "Alice" || !got.Enabled {
		t.Fatalf("reloaded credential = %+v, want Alice enabled", got)
	}
	got, ok = reloaded.GetByUsername("alice")
	if !ok || got.UserID != "user-1" {
		t.Fatalf("username lookup = %+v, %v; want user-1", got, ok)
	}

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat credential file: %v", err)
	}
	if gotMode := info.Mode().Perm(); gotMode != credentialFileMode {
		t.Fatalf("credential file mode = %o, want %o", gotMode, credentialFileMode)
	}
}

func TestStoreDisable(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "smb-credentials.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := store.SetCredential("user-1", "alice", "8846f7eaee8fb117ad06bdd830b7586c", true); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}
	if err := store.Disable("user-1"); err != nil {
		t.Fatalf("Disable() error: %v", err)
	}
	credential, ok := store.GetByUserID("user-1")
	if !ok {
		t.Fatal("expected credential after disable")
	}
	if credential.Enabled {
		t.Fatal("credential should be disabled")
	}
}

func TestNewStoreRejectsSymlinkCredentialFile(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "outside.json")
	if err := os.WriteFile(targetPath, []byte(`{"credentials":[]}`), credentialFileMode); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	linkPath := filepath.Join(tmpDir, "smb-credentials.json")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	if _, err := NewStore(linkPath); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("NewStore() error = %v, want ErrUnsafePath", err)
	}
}

func TestStoreSetPasswordRejectsSymlinkCredentialParent(t *testing.T) {
	tmpDir := t.TempDir()
	parentPath := filepath.Join(tmpDir, "credentials")
	filePath := filepath.Join(parentPath, "smb-credentials.json")

	store, err := NewStore(filePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(outsideDir, 0700); err != nil {
		t.Fatalf("MkdirAll(outside) error: %v", err)
	}
	if err := os.Symlink(outsideDir, parentPath); err != nil {
		t.Fatalf("Symlink(parent) error: %v", err)
	}

	if _, err := store.SetPassword("user-1", "Alice", "password", true); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("SetPassword() error = %v, want ErrUnsafePath", err)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "smb-credentials.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside credential file stat error = %v, want ErrNotExist", err)
	}
}
