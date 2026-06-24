package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestDecodePersistedUserStoreRejectsIncompatibleSchemas(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		messagePart string
	}{
		{
			name:        "legacy array",
			payload:     `[]`,
			messagePart: "versioned JSON object",
		},
		{
			name:        "missing version",
			payload:     `{"users":[]}`,
			messagePart: "missing schema_version",
		},
		{
			name:        "obsolete version",
			payload:     `{"schema_version":0,"users":[]}`,
			messagePart: "obsolete",
		},
		{
			name:        "unknown version",
			payload:     `{"schema_version":2,"users":[]}`,
			messagePart: "unsupported",
		},
		{
			name:        "invalid version type",
			payload:     `{"schema_version":"1","users":[]}`,
			messagePart: "must be an integer",
		},
		{
			name:        "missing users array",
			payload:     `{"schema_version":1}`,
			messagePart: "missing users array",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			users, err := decodePersistedUserStore([]byte(test.payload))
			if !errors.Is(err, errUserStoreSchema) {
				t.Fatalf("decodePersistedUserStore() error = %v, want errUserStoreSchema", err)
			}
			if !strings.Contains(err.Error(), test.messagePart) {
				t.Fatalf("decodePersistedUserStore() error = %v, want %q", err, test.messagePart)
			}
			if users != nil {
				t.Fatalf("decodePersistedUserStore() users = %+v, want nil", users)
			}
		})
	}
}

func TestDecodePersistedUserStoreRequiresCredentialLifecycleFields(t *testing.T) {
	tests := []struct {
		name        string
		userFields  string
		messagePart string
	}{
		{
			name:        "missing must change password",
			userFields:  `"credential_version":1`,
			messagePart: `"must_change_password"`,
		},
		{
			name:        "null must change password",
			userFields:  `"must_change_password":null,"credential_version":1`,
			messagePart: `"must_change_password"`,
		},
		{
			name:        "missing credential version",
			userFields:  `"must_change_password":false`,
			messagePart: `"credential_version"`,
		},
		{
			name:        "null credential version",
			userFields:  `"must_change_password":false,"credential_version":null`,
			messagePart: `"credential_version"`,
		},
		{
			name:        "zero credential version",
			userFields:  `"must_change_password":false,"credential_version":0`,
			messagePart: "invalid credential_version 0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := `{"schema_version":1,"users":[{"id":"admin","username":"admin","password_hash":"hash","role":"admin","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","home_dir":"/",` + test.userFields + `}]}`
			users, err := decodePersistedUserStore([]byte(payload))
			if !errors.Is(err, errUserStoreSchema) {
				t.Fatalf("decodePersistedUserStore() error = %v, want errUserStoreSchema", err)
			}
			if !strings.Contains(err.Error(), test.messagePart) {
				t.Fatalf("decodePersistedUserStore() error = %v, want %q", err, test.messagePart)
			}
			if users != nil {
				t.Fatalf("decodePersistedUserStore() users = %+v, want nil", users)
			}
		})
	}
}

func TestNewUserStorePersistsVersionedBootstrapAndReloads(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	store, bootstrapPassword, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	if bootstrapPassword == "" {
		t.Fatal("expected bootstrap password")
	}
	member, err := store.Create("member", "member-password-123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create(member) error: %v", err)
	}
	if member.CredentialVersion != 1 {
		t.Fatalf("member credential version = %d, want 1", member.CredentialVersion)
	}

	data, err := os.ReadFile(usersFile)
	if err != nil {
		t.Fatalf("ReadFile(users.json) error: %v", err)
	}
	var document struct {
		SchemaVersion int               `json:"schema_version"`
		Users         []json.RawMessage `json:"users"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("Unmarshal(users.json) error: %v", err)
	}
	if document.SchemaVersion != userStoreSchemaVersion {
		t.Fatalf("schema version = %d, want %d", document.SchemaVersion, userStoreSchemaVersion)
	}
	if len(document.Users) != 2 {
		t.Fatalf("persisted users = %d, want 2", len(document.Users))
	}
	for index, rawUser := range document.Users {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawUser, &fields); err != nil {
			t.Fatalf("Unmarshal(user %d) error: %v", index, err)
		}
		for _, field := range []string{"must_change_password", "credential_version"} {
			if _, ok := fields[field]; !ok {
				t.Fatalf("persisted user %d omitted %s: %s", index, field, rawUser)
			}
		}
	}

	reloaded, generatedPassword, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore(reload) error: %v", err)
	}
	if generatedPassword != "" {
		t.Fatalf("reload generated unexpected bootstrap password %q", generatedPassword)
	}
	admin, err := reloaded.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	if !admin.MustChangePassword || admin.CredentialVersion != 1 {
		t.Fatalf("reloaded bootstrap credential lifecycle = %+v", admin)
	}
	reloadedMember, err := reloaded.GetByUsername("member")
	if err != nil {
		t.Fatalf("GetByUsername(member) error: %v", err)
	}
	if reloadedMember.MustChangePassword || reloadedMember.CredentialVersion != 1 {
		t.Fatalf("reloaded member credential lifecycle = %+v", reloadedMember)
	}
}

func TestNewUserStoreRejectsMissingCredentialLifecycleFieldsWithoutRecovery(t *testing.T) {
	tests := []struct {
		name       string
		userFields string
	}{
		{
			name:       "must change password",
			userFields: `"credential_version":1`,
		},
		{
			name:       "credential version",
			userFields: `"must_change_password":false`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			usersFile := filepath.Join(dir, "users.json")
			payload := []byte(`{"schema_version":1,"users":[{"id":"admin","username":"admin","password_hash":"hash","role":"admin","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","home_dir":"/",` + test.userFields + `}]}`)
			if err := os.WriteFile(usersFile, payload, 0600); err != nil {
				t.Fatalf("WriteFile(users.json) error: %v", err)
			}

			store, password, err := NewUserStore(usersFile)
			if !errors.Is(err, errUserStoreSchema) {
				t.Fatalf("NewUserStore() error = %v, want errUserStoreSchema", err)
			}
			if store != nil || password != "" {
				t.Fatalf("NewUserStore() = (%+v, %q), want no initialized store", store, password)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "initial-password.txt")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("initial password stat error = %v, want not exist", statErr)
			}
			persisted, readErr := os.ReadFile(usersFile)
			if readErr != nil {
				t.Fatalf("ReadFile(users.json) error: %v", readErr)
			}
			if !bytes.Equal(persisted, payload) {
				t.Fatalf("users file changed after rejected load")
			}
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatalf("ReadDir() error: %v", readErr)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "users.json.corrupt.") {
					t.Fatalf("schema error was recovered as corruption: %s", entry.Name())
				}
			}
		})
	}
}

func TestNewUserStoreRejectsLegacyBootstrapStateWithoutDeletingInitialPassword(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")
	bootstrapPassword := "legacy-bootstrap-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(bootstrapPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error: %v", err)
	}
	legacyUsers := []byte(`[{"id":"legacy-admin","username":"admin","password_hash":"` + string(hash) + `","role":"admin","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","home_dir":"/"}]`)
	initialPassword := []byte("MnemoNAS Initial Admin Password\nUsername: admin\nPassword: " + bootstrapPassword + "\n")
	if err := os.WriteFile(usersFile, legacyUsers, 0600); err != nil {
		t.Fatalf("WriteFile(legacy users) error: %v", err)
	}
	if err := os.WriteFile(passwordFile, initialPassword, 0600); err != nil {
		t.Fatalf("WriteFile(initial password) error: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		store, generatedPassword, err := NewUserStore(usersFile)
		if !errors.Is(err, errUserStoreSchema) {
			t.Fatalf("NewUserStore() attempt %d error = %v, want errUserStoreSchema", attempt, err)
		}
		if store != nil {
			t.Fatalf("NewUserStore() attempt %d returned store %+v; login must remain unreachable", attempt, store)
		}
		if generatedPassword != "" {
			t.Fatalf("NewUserStore() attempt %d generated password %q", attempt, generatedPassword)
		}
		persistedPassword, readErr := os.ReadFile(passwordFile)
		if readErr != nil {
			t.Fatalf("ReadFile(initial password) attempt %d error: %v", attempt, readErr)
		}
		if !bytes.Equal(persistedPassword, initialPassword) {
			t.Fatalf("initial password changed on attempt %d: got %q, want %q", attempt, persistedPassword, initialPassword)
		}
		persistedUsers, readErr := os.ReadFile(usersFile)
		if readErr != nil {
			t.Fatalf("ReadFile(legacy users) attempt %d error: %v", attempt, readErr)
		}
		if !bytes.Equal(persistedUsers, legacyUsers) {
			t.Fatalf("legacy users changed on attempt %d", attempt)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "users.json.corrupt.") {
			t.Fatalf("schema mismatch was silently recovered as corruption: %s", entry.Name())
		}
	}
}
