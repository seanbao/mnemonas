//go:build linux || darwin

package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestFileSystem_DeleteWithExpectedPolicyAndTargetRejectsReplacementDuringHash(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetPath := "/delete-identity-race.bin"
	replacementPath := "/delete-identity-race-replacement.bin"
	content := []byte("same content")

	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	targetHostPath := fs.workspace.FullPath(targetPath)
	replacementHostPath := fs.workspace.FullPath(replacementPath)
	stableTime := time.Unix(1_700_000_000, 123_456_789)
	if err := os.Chtimes(targetHostPath, stableTime, stableTime); err != nil {
		t.Fatalf("Chtimes(target) error: %v", err)
	}
	targetOSInfo, err := os.Stat(targetHostPath)
	if err != nil {
		t.Fatalf("Stat(target) error: %v", err)
	}
	if err := os.WriteFile(replacementHostPath, content, targetOSInfo.Mode().Perm()); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	if err := os.Chtimes(replacementHostPath, targetOSInfo.ModTime(), targetOSInfo.ModTime()); err != nil {
		t.Fatalf("Chtimes(replacement) error: %v", err)
	}
	replacementOSInfo, err := os.Stat(replacementHostPath)
	if err != nil {
		t.Fatalf("Stat(replacement) error: %v", err)
	}
	if os.SameFile(targetOSInfo, replacementOSInfo) {
		t.Fatal("replacement unexpectedly shares the target inode")
	}
	if targetOSInfo.Mode() != replacementOSInfo.Mode() || targetOSInfo.Size() != replacementOSInfo.Size() || !targetOSInfo.ModTime().Equal(replacementOSInfo.ModTime()) {
		t.Fatalf("replacement metadata differs: target=%+v replacement=%+v", targetOSInfo, replacementOSInfo)
	}

	observed, err := fs.Stat(ctx, targetPath)
	if err != nil {
		t.Fatalf("FileSystem.Stat(target) error: %v", err)
	}
	intent, err := fs.PrepareObservedDeleteIntents(ctx, []ObservedDeleteTarget{{
		Path:                  targetPath,
		ObservedIdentityToken: observed.DeleteIdentityToken,
	}}, nil)
	if err != nil {
		t.Fatalf("PrepareObservedDeleteIntents() error: %v", err)
	}

	replaced := false
	fs.hashDeleteTargetFile = func(hashCtx context.Context, hashPath string) (string, error) {
		if hashPath == targetPath && !replaced {
			if err := os.Rename(replacementHostPath, targetHostPath); err != nil {
				return "", err
			}
			replaced = true
		}
		return fs.hashWorkspaceFile(hashCtx, hashPath)
	}

	err = fs.DeleteWithExpectedPolicyAndTarget(
		ctx,
		targetPath,
		DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token},
		intent.Targets[0].Token,
		nil,
	)
	if !errors.Is(err, ErrDeleteTargetChanged) {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget() error = %v, want ErrDeleteTargetChanged", err)
	}
	if !replaced {
		t.Fatal("delete target hash hook did not replace the target")
	}

	currentOSInfo, err := os.Stat(targetHostPath)
	if err != nil {
		t.Fatalf("replacement target was not preserved: %v", err)
	}
	if !os.SameFile(replacementOSInfo, currentOSInfo) {
		t.Fatal("target no longer identifies the replacement inode")
	}
	if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
		t.Fatalf("trash after rejected replacement delete = %+v, %v; want empty", items, err)
	}
}
