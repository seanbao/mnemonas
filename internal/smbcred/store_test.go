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

func TestStoreRejectsDuplicateUsername(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "smb-credentials.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := store.SetCredential("user-1", "Alice", "8846f7eaee8fb117ad06bdd830b7586c", true); err != nil {
		t.Fatalf("SetCredential(user-1) error: %v", err)
	}

	if _, err := store.SetCredential("user-2", "alice", "8846f7eaee8fb117ad06bdd830b7586c", true); !errors.Is(err, ErrDuplicateUsername) {
		t.Fatalf("SetCredential(user-2 duplicate username) error = %v, want ErrDuplicateUsername", err)
	}
	if _, ok := store.GetByUserID("user-2"); ok {
		t.Fatal("duplicate username write should not leave user-2 in memory")
	}
	got, ok := store.GetByUsername("ALICE")
	if !ok || got.UserID != "user-1" {
		t.Fatalf("username lookup after duplicate rejection = %+v, %v; want user-1", got, ok)
	}
}

func TestStoreRejectsUnicodeWhitespaceAndControlCharactersInNames(t *testing.T) {
	validHash := "8846f7eaee8fb117ad06bdd830b7586c"

	tests := []struct {
		name     string
		userID   string
		username string
	}{
		{
			name:     "user id unicode control",
			userID:   "user\u00811",
			username: "alice",
		},
		{
			name:     "username unicode control",
			userID:   "user-1",
			username: "ali\u0081ce",
		},
		{
			name:     "username unicode whitespace",
			userID:   "user-1",
			username: "ali\u00a0ce",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewStore(filepath.Join(t.TempDir(), "smb-credentials.json"))
			if err != nil {
				t.Fatalf("NewStore() error: %v", err)
			}

			if _, err := store.SetCredential(tt.userID, tt.username, validHash, true); err == nil {
				t.Fatal("SetCredential() error = nil, want invalid name error")
			}
		})
	}
}

func TestNewStoreRejectsDuplicatePersistedUsername(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "smb-credentials.json")
	data := `{"credentials":[` +
		`{"user_id":"user-1","username":"Alice","enabled":true,"nt_hash_hex":"8846f7eaee8fb117ad06bdd830b7586c"},` +
		`{"user_id":"user-2","username":"alice","enabled":true,"nt_hash_hex":"8846f7eaee8fb117ad06bdd830b7586c"}` +
		`]}`
	if err := os.WriteFile(filePath, []byte(data), credentialFileMode); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	if _, err := NewStore(filePath); !errors.Is(err, ErrDuplicateUsername) {
		t.Fatalf("NewStore() error = %v, want ErrDuplicateUsername", err)
	}
}

func TestStoreDisableRollsBackOnPersistenceFailure(t *testing.T) {
	tmpDir := t.TempDir()
	parentPath := filepath.Join(tmpDir, "credentials")
	filePath := filepath.Join(parentPath, "smb-credentials.json")
	store, err := NewStore(filePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := store.SetCredential("user-1", "Alice", "8846f7eaee8fb117ad06bdd830b7586c", true); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	realParent := filepath.Join(tmpDir, "credentials-real")
	if err := os.Rename(parentPath, realParent); err != nil {
		t.Fatalf("Rename(parent) error: %v", err)
	}
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.Mkdir(outsideDir, 0700); err != nil {
		t.Fatalf("Mkdir(outside) error: %v", err)
	}
	if err := os.Symlink(outsideDir, parentPath); err != nil {
		t.Fatalf("Symlink(parent) error: %v", err)
	}

	if err := store.Disable("user-1"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Disable() error = %v, want ErrUnsafePath", err)
	}
	credential, ok := store.GetByUserID("user-1")
	if !ok || !credential.Enabled {
		t.Fatalf("credential after failed disable = %+v, %v; want still enabled", credential, ok)
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
	if _, ok := store.GetByUserID("user-1"); ok {
		t.Fatal("failed SetPassword should not leave credential in memory")
	}
	if _, ok := store.GetByUsername("Alice"); ok {
		t.Fatal("failed SetPassword should not leave username index in memory")
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "smb-credentials.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside credential file stat error = %v, want ErrNotExist", err)
	}
}
