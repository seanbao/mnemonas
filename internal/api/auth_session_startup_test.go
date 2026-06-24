package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/auth"
)

func TestNewServerRejectsCorruptAuthSessionState(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	userStore, _, err := auth.NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	sessionFile := filepath.Join(dir, "auth-sessions.json")
	if err := os.WriteFile(sessionFile, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile(auth session state) error: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		AuthEnabled:    true,
		AuthUsersFile:  usersFile,
		AuthUserStore:  userStore,
		AuthJWTSecret:  "auth-session-startup-secret-32-bytes",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	})
	if err == nil {
		t.Fatalf("NewServer() = %+v, want corrupt auth session state failure", server)
	}
	if !strings.Contains(err.Error(), "failed to initialize auth session store") {
		t.Fatalf("NewServer() error = %q, want auth session initialization context", err)
	}
}
