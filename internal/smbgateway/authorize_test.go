package smbgateway

import (
	"errors"
	"testing"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
)

func TestResolvePathAllowsAdminOutsideHomeDir(t *testing.T) {
	resolved, err := ResolvePath(testShares(), "docs", "team/readme.md", &auth.User{
		ID:       "admin-1",
		Username: "admin",
		Role:     auth.RoleAdmin,
		HomeDir:  "/admins/admin",
	}, false)
	if err != nil {
		t.Fatalf("ResolvePath() error: %v", err)
	}
	if resolved.Path != "/shared/docs/team/readme.md" {
		t.Fatalf("resolved path = %q, want /shared/docs/team/readme.md", resolved.Path)
	}
	if !resolved.CanWrite {
		t.Fatal("admin should be able to write writable share")
	}
}

func TestResolvePathScopesNonAdminToHomeDir(t *testing.T) {
	user := &auth.User{
		ID:       "user-1",
		Username: "alice",
		Role:     auth.RoleUser,
		HomeDir:  "/shared/docs/alice",
	}

	resolved, err := ResolvePath(testShares(), "docs", "alice/file.txt", user, true)
	if err != nil {
		t.Fatalf("ResolvePath() error: %v", err)
	}
	if resolved.Path != "/shared/docs/alice/file.txt" {
		t.Fatalf("resolved path = %q, want scoped path", resolved.Path)
	}

	if _, err := ResolvePath(testShares(), "docs", "bob/file.txt", user, false); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ResolvePath(outside home) error = %v, want ErrAccessDenied", err)
	}
}

func TestResolvePathAllowsExplicitUser(t *testing.T) {
	resolved, err := ResolvePath(testShares(), "private", "/", &auth.User{
		ID:       "user-2",
		Username: "Bob",
		Role:     auth.RoleUser,
		HomeDir:  "/users/bob",
	}, false)
	if err != nil {
		t.Fatalf("ResolvePath() error: %v", err)
	}
	if resolved.Path != "/users/bob" {
		t.Fatalf("resolved path = %q, want /users/bob", resolved.Path)
	}
}

func TestResolvePathDeniesGuestWrite(t *testing.T) {
	guest := &auth.User{
		ID:       "guest-1",
		Username: "guest",
		Role:     auth.RoleGuest,
		HomeDir:  "/public",
	}

	resolved, err := ResolvePath(testShares(), "public", "readme.txt", guest, false)
	if err != nil {
		t.Fatalf("ResolvePath(read) error: %v", err)
	}
	if resolved.CanWrite {
		t.Fatal("guest should not have write capability")
	}

	if _, err := ResolvePath(testShares(), "public", "readme.txt", guest, true); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("ResolvePath(write) error = %v, want ErrReadOnly", err)
	}
}

func TestResolvePathRejectsTraversal(t *testing.T) {
	_, err := ResolvePath(testShares(), "docs", "../secret", &auth.User{
		ID:       "admin-1",
		Username: "admin",
		Role:     auth.RoleAdmin,
		HomeDir:  "/",
	}, false)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("ResolvePath() error = %v, want ErrInvalidPath", err)
	}
}

func TestResolvePathMissingShare(t *testing.T) {
	_, err := ResolvePath(testShares(), "missing", "/", &auth.User{
		ID:       "admin-1",
		Username: "admin",
		Role:     auth.RoleAdmin,
		HomeDir:  "/",
	}, false)
	if !errors.Is(err, ErrShareNotFound) {
		t.Fatalf("ResolvePath() error = %v, want ErrShareNotFound", err)
	}
}

func testShares() []config.SMBShareConfig {
	return []config.SMBShareConfig{
		{
			Name:         "docs",
			Path:         "/shared/docs",
			AllowedRoles: []string{"admin", "user"},
		},
		{
			Name:         "public",
			Path:         "/public",
			AllowedRoles: []string{"guest"},
		},
		{
			Name:         "private",
			Path:         "/users/bob",
			AllowedUsers: []string{"bob"},
		},
	}
}
