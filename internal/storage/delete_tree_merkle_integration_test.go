package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/workspace"
)

func TestDeleteTreeTokenV3_PrepareAndCommitUseSameToken(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	const targetPath = "/v3-token.txt"
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("v3 token"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	intent, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", err)
	}
	if len(intent.Targets) != 1 {
		t.Fatalf("prepared targets = %d, want 1", len(intent.Targets))
	}
	wantToken, err := deleteTreeTokenV3(intent.Targets[0].Snapshot)
	if err != nil {
		t.Fatalf("deleteTreeTokenV3() error: %v", err)
	}
	if wantToken == "" || intent.Targets[0].Token != wantToken {
		t.Fatalf("prepared token = %q, want v3 token %q", intent.Targets[0].Token, wantToken)
	}

	if intent.Targets[0].Snapshot.Root.DeleteIdentityToken == "" {
		t.Skip("platform deletion identity is unavailable; v3 token preparation remains covered")
	}
	if err := fs.DeleteWithExpectedPolicyAndTarget(
		ctx,
		targetPath,
		DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token},
		intent.Targets[0].Token,
		nil,
	); err != nil {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget() error: %v", err)
	}
	if _, err := fs.Stat(ctx, targetPath); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted target status = %v, want ErrNotFound", err)
	}
}

func TestDeleteTreeTokenV3_AllowsEmptyPlatformIdentity(t *testing.T) {
	entry := FileInfo{
		Path:        "/identity-unavailable.txt",
		Name:        "identity-unavailable.txt",
		Size:        7,
		Mode:        0o640,
		ModTime:     time.Unix(1_700_000_000, 123).UTC(),
		ContentHash: strings.Repeat("a", 64),
	}
	snapshot := DeleteTargetSnapshot{Root: entry, Entries: []FileInfo{entry}}

	token, err := deleteTreeTokenV3(snapshot)
	if err != nil {
		t.Fatalf("deleteTreeTokenV3() with empty identity error: %v", err)
	}
	if token == "" {
		t.Fatal("deleteTreeTokenV3() returned an empty token")
	}
}

func TestDeleteTreeTokenV3_ModeDriftChangesTokenAndRejectsCommit(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	const targetPath = "/mode-drift.txt"
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("mode drift"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	hostPath := fs.workspace.FullPath(targetPath)
	if err := os.Chmod(hostPath, 0o600); err != nil {
		t.Skipf("chmod is unavailable: %v", err)
	}

	before, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents(before) error: %v", err)
	}
	if got := before.Targets[0].Snapshot.Root.Mode.Perm(); got != 0o600 {
		t.Fatalf("prepared mode = %#o, want %#o", got, os.FileMode(0o600))
	}
	if err := os.Chmod(hostPath, 0o640); err != nil {
		t.Skipf("chmod update is unavailable: %v", err)
	}
	after, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents(after) error: %v", err)
	}
	if got := after.Targets[0].Snapshot.Root.Mode.Perm(); got != 0o640 {
		t.Fatalf("updated prepared mode = %#o, want %#o", got, os.FileMode(0o640))
	}
	if before.Targets[0].Token == "" || after.Targets[0].Token == "" || before.Targets[0].Token == after.Targets[0].Token {
		t.Fatalf("mode drift tokens = %q and %q, want distinct non-empty tokens", before.Targets[0].Token, after.Targets[0].Token)
	}

	err = fs.DeleteWithExpectedPolicyAndTarget(
		ctx,
		targetPath,
		DeletePolicyExpectation{Mode: before.Policy.Mode, Token: before.Policy.Token},
		before.Targets[0].Token,
		nil,
	)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget() error = %v, want ErrDeleteTargetChanged", err)
	}
	if _, err := fs.Stat(ctx, targetPath); err != nil {
		t.Fatalf("mode-drift target was mutated: %v", err)
	}
}

func TestFileSystem_StagedDeleteRejectsModeDrift(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	const targetPath = "/staged-mode-drift.txt"
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("staged mode drift"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	intent, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", err)
	}
	if intent.Targets[0].Snapshot.Root.DeleteIdentityToken == "" {
		t.Skip("platform deletion identity is unavailable")
	}

	release := fs.beginMutation()
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	target, err := fs.stageDeleteTargetLocked(ctx, targetPath, intent.Targets[0].Snapshot, nil)
	if err != nil {
		t.Fatalf("stageDeleteTargetLocked() error: %v", err)
	}
	defer target.close()

	originalWalk := walkStorageDeleteTree
	t.Cleanup(func() { walkStorageDeleteTree = originalWalk })
	walkStorageDeleteTree = func(ctx context.Context, ws *workspace.Workspace, root string, fn workspace.WalkFunc) error {
		return originalWalk(ctx, ws, root, func(path string, info *workspace.FileInfo) error {
			if path == root && info != nil {
				changed := *info
				changed.Mode ^= 0o040
				info = &changed
			}
			return fn(path, info)
		})
	}
	_, snapshotErr := fs.snapshotStagedDeleteLocked(ctx, target)
	walkStorageDeleteTree = originalWalk
	if !errors.Is(snapshotErr, ErrDeleteTargetChanged) {
		t.Fatalf("snapshotStagedDeleteLocked() error = %v, want ErrDeleteTargetChanged", snapshotErr)
	}
	if rollbackErr := fs.rollbackStagedDeleteLocked(target, nil); rollbackErr != nil {
		t.Fatalf("rollbackStagedDeleteLocked() error: %v", rollbackErr)
	}
	release()
	released = true
	if _, err := fs.Stat(ctx, targetPath); err != nil {
		t.Fatalf("staged mode-drift target was not restored: %v", err)
	}
}
