//go:build linux || darwin

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestFileSystemDeleteIdentityPropagatesToReadsAndPreparedTargets(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/item.bin", bytes.NewReader([]byte("item"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	statInfo, err := fs.Stat(ctx, "/item.bin")
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if !validDeleteIdentityToken(statInfo.DeleteIdentityToken) {
		t.Fatalf("Stat() identity token = %q", statInfo.DeleteIdentityToken)
	}
	entries, err := fs.ReadDir(ctx, "/")
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	if len(entries) != 1 || entries[0].DeleteIdentityToken != statInfo.DeleteIdentityToken {
		t.Fatalf("ReadDir() identity = %+v, want %q", entries, statInfo.DeleteIdentityToken)
	}
	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/item.bin"}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", err)
	}
	if len(intent.Targets) != 1 || intent.Targets[0].DeleteIdentityToken != statInfo.DeleteIdentityToken {
		t.Fatalf("prepared target identity = %+v, want %q", intent.Targets, statInfo.DeleteIdentityToken)
	}
}

func TestFileSystemPrepareObservedDeleteIntentsRejectsIdentityDriftBeforeHashOrTraversal(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/child.bin", bytes.NewReader([]byte("child"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	info, err := fs.Stat(ctx, "/tree")
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	wrongToken := strings.Repeat("a", sha256.Size*2)
	if wrongToken == info.DeleteIdentityToken {
		wrongToken = strings.Repeat("b", sha256.Size*2)
	}

	var authorized []string
	hashCalls := 0
	fs.hashDeleteTargetFile = func(context.Context, string) (string, error) {
		hashCalls++
		return "", errors.New("unexpected hash")
	}
	_, err = fs.PrepareObservedDeleteIntents(ctx, []ObservedDeleteTarget{{
		Path:                  "/tree",
		ObservedIdentityToken: wrongToken,
	}}, func(targetPath string) error {
		authorized = append(authorized, targetPath)
		return nil
	})
	var changedErr *DeleteIdentityChangedError
	if !errors.As(err, &changedErr) || !errors.Is(err, ErrDeleteTargetChanged) || changedErr.Path != "/tree" {
		t.Fatalf("PrepareObservedDeleteIntents() error = %v, want identity change for /tree", err)
	}
	if !slices.Equal(authorized, []string{"/tree"}) {
		t.Fatalf("authorized paths = %v, want root only", authorized)
	}
	if hashCalls != 0 {
		t.Fatalf("identity drift hash calls = %d, want 0", hashCalls)
	}
}

func TestFileSystemPrepareObservedDeleteIntentsAuthorizesBeforeIdentityComparison(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/item.bin", bytes.NewReader([]byte("item"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	errDenied := errors.New("denied")
	_, err := fs.PrepareObservedDeleteIntents(ctx, []ObservedDeleteTarget{{
		Path:                  "/item.bin",
		ObservedIdentityToken: strings.Repeat("a", sha256.Size*2),
	}}, func(string) error {
		return errDenied
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("PrepareObservedDeleteIntents() error = %v, want authorization denial", err)
	}
}

func TestFileSystemPrepareObservedDeleteIntentsAcceptsCurrentIdentity(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/item.bin", bytes.NewReader([]byte("item"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	info, err := fs.Stat(ctx, "/item.bin")
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	intent, err := fs.PrepareObservedDeleteIntents(ctx, []ObservedDeleteTarget{{
		Path:                  "/item.bin",
		ObservedIdentityToken: info.DeleteIdentityToken,
	}}, nil)
	if err != nil {
		t.Fatalf("PrepareObservedDeleteIntents() error: %v", err)
	}
	if len(intent.Targets) != 1 || intent.Targets[0].DeleteIdentityToken != info.DeleteIdentityToken || !validDeleteIdentityToken(intent.Targets[0].Token) {
		t.Fatalf("observed intent = %+v", intent)
	}
}

func TestFileSystemPrepareObservedDeleteIntentsRejectsInvalidTargets(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	validToken := strings.Repeat("a", sha256.Size*2)
	tests := []struct {
		name    string
		targets []ObservedDeleteTarget
	}{
		{name: "empty"},
		{name: "missing token", targets: []ObservedDeleteTarget{{Path: "/item"}}},
		{name: "short token", targets: []ObservedDeleteTarget{{Path: "/item", ObservedIdentityToken: validToken[:len(validToken)-1]}}},
		{name: "uppercase token", targets: []ObservedDeleteTarget{{Path: "/item", ObservedIdentityToken: strings.ToUpper(validToken)}}},
		{name: "duplicate", targets: []ObservedDeleteTarget{{Path: "/item", ObservedIdentityToken: validToken}, {Path: "/item/", ObservedIdentityToken: validToken}}},
		{name: "nested", targets: []ObservedDeleteTarget{{Path: "/item", ObservedIdentityToken: validToken}, {Path: "/item/child", ObservedIdentityToken: validToken}}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := fs.PrepareObservedDeleteIntents(context.Background(), testCase.targets, nil)
			if !errors.Is(err, ErrInvalidDeleteIntent) {
				t.Fatalf("PrepareObservedDeleteIntents() error = %v, want ErrInvalidDeleteIntent", err)
			}
		})
	}
}
