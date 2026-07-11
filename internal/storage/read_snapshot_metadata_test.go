package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func TestFileSystem_StatMetadataSkipsContentHash(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	const filePath = "/metadata.txt"
	if err := fs.workspace.WriteFile(ctx, filePath, []byte("metadata only")); err != nil {
		t.Fatalf("workspace.WriteFile() error: %v", err)
	}

	hashCalls := 0
	fs.hashStatWorkspaceFile = func(context.Context, string) (string, error) {
		hashCalls++
		return "content-hash", nil
	}

	info, err := fs.StatMetadata(ctx, filePath)
	if err != nil {
		t.Fatalf("StatMetadata() error: %v", err)
	}
	if hashCalls != 0 {
		t.Fatalf("StatMetadata() hash calls = %d, want 0", hashCalls)
	}
	if info.ContentHash != "" {
		t.Fatalf("StatMetadata() ContentHash = %q, want empty", info.ContentHash)
	}
	if info.Size != int64(len("metadata only")) {
		t.Fatalf("StatMetadata() Size = %d, want %d", info.Size, len("metadata only"))
	}

	info, err = fs.Stat(ctx, filePath)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if hashCalls != 1 {
		t.Fatalf("Stat() hash calls = %d, want 1", hashCalls)
	}
	if info.ContentHash != "content-hash" {
		t.Fatalf("Stat() ContentHash = %q, want content-hash", info.ContentHash)
	}
}

func TestFileSystem_OpenFileSnapshotMetadataSkipsHashAndUsesSameHandle(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	const filePath = "/snapshot-metadata.bin"
	original := []byte("original snapshot bytes")
	if err := fs.workspace.WriteFile(ctx, filePath, original); err != nil {
		t.Fatalf("workspace.WriteFile() error: %v", err)
	}

	hashCalls := 0
	fs.hashOpenSnapshotFile = func(context.Context, *os.File) (string, error) {
		hashCalls++
		return "unexpected-hash", nil
	}

	file, info, err := fs.OpenFileSnapshotMetadata(ctx, filePath)
	if err != nil {
		t.Fatalf("OpenFileSnapshotMetadata() error: %v", err)
	}
	defer file.Close()
	if hashCalls != 0 {
		t.Fatalf("OpenFileSnapshotMetadata() hash calls = %d, want 0", hashCalls)
	}
	if info.ContentHash != "" {
		t.Fatalf("OpenFileSnapshotMetadata() ContentHash = %q, want empty", info.ContentHash)
	}

	if err := fs.workspace.WriteFile(ctx, filePath, []byte("replacement bytes")); err != nil {
		t.Fatalf("workspace.WriteFile(replacement) error: %v", err)
	}
	body, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(snapshot) error: %v", err)
	}
	if string(body) != string(original) {
		t.Fatalf("snapshot body = %q, want %q", body, original)
	}
}

func TestFileSystem_OpenFileSnapshotPassesRequestContextToHash(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	const filePath = "/snapshot-context.bin"
	if err := fs.workspace.WriteFile(context.Background(), filePath, []byte("hash me")); err != nil {
		t.Fatalf("workspace.WriteFile() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs.hashOpenSnapshotFile = func(gotCtx context.Context, file *os.File) (string, error) {
		cancel()
		return hashOpenWorkspaceFileContext(gotCtx, file)
	}

	file, info, err := fs.OpenFileSnapshot(ctx, filePath)
	if file != nil {
		_ = file.Close()
	}
	if info != nil {
		t.Fatalf("OpenFileSnapshot() info = %+v, want nil", info)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenFileSnapshot() error = %v, want context.Canceled", err)
	}
}
