//go:build linux || darwin

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStagedDeleteCheckedRegularFileVerifiersRejectSameInodeContentDrift(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "content.bin")
	original := []byte("intended")
	mutated := []byte("modified")
	if err := os.WriteFile(filePath, original, 0o600); err != nil {
		t.Fatalf("WriteFile(original) error: %v", err)
	}
	expectedInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Stat(original) error: %v", err)
	}
	expectedHash := computeHash(original)

	target := &stagedDeleteTarget{logicalName: "/tree"}
	manifest := map[string]stagedDeleteRemovalEntry{
		"/tree/nested.bin": {
			info: expectedInfo,
			hash: expectedHash,
		},
	}
	trashContent := &stagedTrashContent{
		entries: map[string]stagedTrashContentEntry{
			"nested.bin": {
				info: expectedInfo,
				hash: expectedHash,
			},
		},
	}
	verifiers := []struct {
		name   string
		verify func(*os.File, os.FileInfo) error
	}{
		{
			name: "staged delete source",
			verify: func(file *os.File, info os.FileInfo) error {
				return verifyQuarantinedDeleteRegularFile(
					context.Background(),
					target,
					manifest,
					filepath.Join(deleteQuarantineContentName, "nested.bin"),
					file,
					info,
				)
			},
		},
		{
			name: "staged trash rollback copy",
			verify: func(file *os.File, info os.FileInfo) error {
				expected, ok := stagedTrashDiscardEntry(trashContent, filepath.Join(deleteQuarantineContentName, "nested.bin"))
				if !ok {
					return ErrDeleteTargetChanged
				}
				return verifyCheckedDeleteRegularFile(context.Background(), expected.info, expected.info.Size(), expected.hash, file, info)
			},
		},
	}

	runVerifier := func(t *testing.T, verify func(*os.File, os.FileInfo) error) error {
		t.Helper()
		file, err := os.Open(filePath)
		if err != nil {
			t.Fatalf("Open(content) error: %v", err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			t.Fatalf("Stat(content handle) error: %v", err)
		}
		return verify(file, info)
	}
	for _, verifier := range verifiers {
		t.Run(verifier.name+"/accepts expected content", func(t *testing.T) {
			if err := runVerifier(t, verifier.verify); err != nil {
				t.Fatalf("verify(expected content) error: %v", err)
			}
		})
	}

	if err := os.WriteFile(filePath, mutated, expectedInfo.Mode().Perm()); err != nil {
		t.Fatalf("WriteFile(same inode mutation) error: %v", err)
	}
	if err := os.Chtimes(filePath, expectedInfo.ModTime(), expectedInfo.ModTime()); err != nil {
		t.Fatalf("Chtimes(restore modtime) error: %v", err)
	}
	mutatedInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Stat(mutated) error: %v", err)
	}
	if !os.SameFile(expectedInfo, mutatedInfo) {
		t.Fatal("same-inode mutation replaced the file")
	}
	if mutatedInfo.Size() != expectedInfo.Size() || !mutatedInfo.ModTime().Equal(expectedInfo.ModTime()) {
		t.Fatalf("same-inode mutation metadata = size %d mtime %v, want size %d mtime %v", mutatedInfo.Size(), mutatedInfo.ModTime(), expectedInfo.Size(), expectedInfo.ModTime())
	}

	for _, verifier := range verifiers {
		t.Run(verifier.name+"/rejects content drift", func(t *testing.T) {
			if err := runVerifier(t, verifier.verify); !errors.Is(err, ErrDeleteTargetChanged) {
				t.Fatalf("verify(mutated content) error = %v, want ErrDeleteTargetChanged", err)
			}
		})
	}
}
