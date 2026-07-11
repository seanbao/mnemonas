//go:build linux || darwin

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceDeleteIdentityTokenPropagatesAcrossMetadataReads(t *testing.T) {
	root := t.TempDir()
	w, err := New(root)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := os.WriteFile(filepath.Join(root, "item.bin"), []byte("item"), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	statInfo, err := w.Stat(context.Background(), "/item.bin")
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	assertDeleteIdentityToken(t, statInfo.DeleteIdentityToken)

	entries, err := w.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	if len(entries) != 1 || entries[0].DeleteIdentityToken != statInfo.DeleteIdentityToken {
		t.Fatalf("ReadDir() identity = %+v, want %q", entries, statInfo.DeleteIdentityToken)
	}

	var walkToken string
	if err := w.WalkStrict(context.Background(), "/item.bin", func(_ string, info *FileInfo) error {
		walkToken = info.DeleteIdentityToken
		return nil
	}); err != nil {
		t.Fatalf("WalkStrict() error: %v", err)
	}
	if walkToken != statInfo.DeleteIdentityToken {
		t.Fatalf("WalkStrict() identity = %q, want %q", walkToken, statInfo.DeleteIdentityToken)
	}
}

func TestWorkspaceDeleteIdentityTokenChangesForSameMetadataReplacement(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		create func(string) error
	}{
		{
			name: "file",
			create: func(targetPath string) error {
				return os.WriteFile(targetPath, []byte("same"), 0o600)
			},
		},
		{
			name: "directory",
			create: func(targetPath string) error {
				return os.Mkdir(targetPath, 0o700)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			w, err := New(root)
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}
			t.Cleanup(func() { _ = w.Close() })

			targetPath := filepath.Join(root, "target")
			replacementPath := filepath.Join(root, "replacement")
			if err := testCase.create(targetPath); err != nil {
				t.Fatalf("create target: %v", err)
			}
			if err := testCase.create(replacementPath); err != nil {
				t.Fatalf("create replacement: %v", err)
			}
			fixedTime := time.Unix(1_700_000_000, 123_456_789)
			for _, hostPath := range []string{targetPath, replacementPath} {
				if err := os.Chtimes(hostPath, fixedTime, fixedTime); err != nil {
					t.Fatalf("Chtimes(%s) error: %v", hostPath, err)
				}
			}

			before, err := w.Stat(context.Background(), "/target")
			if err != nil {
				t.Fatalf("Stat(before) error: %v", err)
			}
			asidePath := filepath.Join(root, "original")
			if err := os.Rename(targetPath, asidePath); err != nil {
				t.Fatalf("Rename(target aside) error: %v", err)
			}
			if err := os.Rename(replacementPath, targetPath); err != nil {
				t.Fatalf("Rename(replacement) error: %v", err)
			}
			after, err := w.Stat(context.Background(), "/target")
			if err != nil {
				t.Fatalf("Stat(after) error: %v", err)
			}

			if before.IsDir != after.IsDir || before.Mode != after.Mode || before.Size != after.Size || !before.ModTime.Equal(after.ModTime) {
				t.Fatalf("replacement metadata changed: before=%+v after=%+v", before, after)
			}
			assertDeleteIdentityToken(t, before.DeleteIdentityToken)
			assertDeleteIdentityToken(t, after.DeleteIdentityToken)
			if before.DeleteIdentityToken == after.DeleteIdentityToken {
				t.Fatalf("same-metadata replacement retained identity %q", before.DeleteIdentityToken)
			}
		})
	}
}

func TestPersistentIdentityTokenSurvivesRenameAndRejectsReplacement(t *testing.T) {
	root := t.TempDir()
	originalPath := filepath.Join(root, "original.bin")
	replacementPath := filepath.Join(root, "replacement.bin")
	if err := os.WriteFile(originalPath, []byte("same"), 0o600); err != nil {
		t.Fatalf("WriteFile(original) error: %v", err)
	}
	if err := os.WriteFile(replacementPath, []byte("same"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	originalInfo, err := os.Lstat(originalPath)
	if err != nil {
		t.Fatalf("Lstat(original) error: %v", err)
	}
	replacementInfo, err := os.Lstat(replacementPath)
	if err != nil {
		t.Fatalf("Lstat(replacement) error: %v", err)
	}
	originalToken := PersistentIdentityTokenForFileInfo(originalInfo)
	replacementToken := PersistentIdentityTokenForFileInfo(replacementInfo)
	assertDeleteIdentityToken(t, originalToken)
	assertDeleteIdentityToken(t, replacementToken)
	if originalToken == replacementToken {
		t.Fatalf("different objects share persistent identity %q", originalToken)
	}

	renamedPath := filepath.Join(root, "renamed.bin")
	if err := os.Rename(originalPath, renamedPath); err != nil {
		t.Fatalf("Rename() error: %v", err)
	}
	renamedInfo, err := os.Lstat(renamedPath)
	if err != nil {
		t.Fatalf("Lstat(renamed) error: %v", err)
	}
	if renamedToken := PersistentIdentityTokenForFileInfo(renamedInfo); renamedToken != originalToken {
		t.Fatalf("persistent identity after rename = %q, want %q", renamedToken, originalToken)
	}
}

func assertDeleteIdentityToken(t *testing.T, token string) {
	t.Helper()
	if len(token) != 64 {
		t.Fatalf("delete identity token length = %d, want 64: %q", len(token), token)
	}
	for _, character := range token {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			t.Fatalf("delete identity token is not lowercase hexadecimal: %q", token)
		}
	}
}
