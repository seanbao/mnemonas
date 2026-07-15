package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/rootio"
)

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(buffer []byte) (int, error) {
	return f(buffer)
}

func TestIsCreatedDirOwnershipChangedPreservesWrappedErrorSemantics(t *testing.T) {
	if IsCreatedDirOwnershipChanged(nil) {
		t.Fatal("IsCreatedDirOwnershipChanged(nil) = true, want false")
	}
	if IsCreatedDirOwnershipChanged(errors.New("unrelated")) {
		t.Fatal("IsCreatedDirOwnershipChanged(unrelated) = true, want false")
	}
	wrapped := fmt.Errorf("workspace cleanup failed: %w", errWorkspaceCreatedDirOwnershipChanged)
	if !IsCreatedDirOwnershipChanged(wrapped) {
		t.Fatalf("IsCreatedDirOwnershipChanged(%v) = false, want true", wrapped)
	}
	joined := errors.Join(errors.New("write failed"), wrapped)
	if !IsCreatedDirOwnershipChanged(joined) {
		t.Fatalf("IsCreatedDirOwnershipChanged(joined error) = false, want true")
	}
}

func TestVisibleMutationWarningWrapperPreservesErrorSemantics(t *testing.T) {
	baseErr := errors.New("sync failed")

	if got := WrapVisibleMutationWarning(nil); got != nil {
		t.Fatalf("WrapVisibleMutationWarning(nil) = %v, want nil", got)
	}
	warningErr := WrapVisibleMutationWarning(baseErr)
	if !IsVisibleMutationWarning(warningErr) {
		t.Fatalf("expected visible mutation warning, got %T", warningErr)
	}
	if !errors.Is(warningErr, baseErr) {
		t.Fatalf("expected warning to unwrap %v", baseErr)
	}
	if warningErr.Error() != baseErr.Error() {
		t.Fatalf("Error() = %q, want %q", warningErr.Error(), baseErr.Error())
	}
	if got := WrapVisibleMutationWarning(warningErr); got != warningErr {
		t.Fatal("expected existing warning to be reused")
	}
}

func TestSyncWorkspaceDirRejectsSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real")
	if err := os.Mkdir(realDir, 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	linkedDir := filepath.Join(tmpDir, "linked")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	if err := syncWorkspaceDir(linkedDir); !rootio.IsSymlinkError(err) {
		t.Fatalf("syncWorkspaceDir() error = %v, want symlink error", err)
	}
}

func TestWorkspace_WriteFile_ReturnsDirectorySyncErrorAfterRename(t *testing.T) {
	w := setupWorkspace(t)
	originalSyncWorkspaceDir := syncWorkspaceDir
	syncWorkspaceDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	}()

	err := w.WriteFile(context.Background(), "/durable.txt", []byte("content"))
	if err == nil {
		t.Fatal("expected WriteFile() to fail when parent directory sync fails")
	}
	if !IsVisibleMutationWarning(err) {
		t.Fatalf("expected visible mutation warning, got %v", err)
	}
	if !strings.Contains(err.Error(), "sync parent directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(filepath.Join(w.Root(), "durable.txt"))
	if readErr != nil {
		t.Fatalf("expected file to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "content" {
		t.Fatalf("expected written content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(filepath.Join(w.Root(), "durable.txt"))
	if statErr != nil {
		t.Fatalf("Stat(durable.txt) error: %v", statErr)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("expected durable.txt permissions 0644, got %o", info.Mode().Perm())
	}
}

func TestWorkspace_WriteFileFromReader_ReturnsDirectorySyncErrorAfterRename(t *testing.T) {
	w := setupWorkspace(t)
	originalSyncWorkspaceDir := syncWorkspaceDir
	syncWorkspaceDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	}()

	err := w.WriteFileFromReader(context.Background(), "/stream.txt", strings.NewReader("streamed content"))
	if err == nil {
		t.Fatal("expected WriteFileFromReader() to fail when parent directory sync fails")
	}
	if !IsVisibleMutationWarning(err) {
		t.Fatalf("expected visible mutation warning, got %v", err)
	}
	if !strings.Contains(err.Error(), "sync parent directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(filepath.Join(w.Root(), "stream.txt"))
	if readErr != nil {
		t.Fatalf("expected streamed file to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "streamed content" {
		t.Fatalf("expected streamed content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(filepath.Join(w.Root(), "stream.txt"))
	if statErr != nil {
		t.Fatalf("Stat(stream.txt) error: %v", statErr)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("expected stream.txt permissions 0644, got %o", info.Mode().Perm())
	}
}

func TestWorkspace_WriteFileFromReader_ReturnsCreatedDirectoryTreeSyncError(t *testing.T) {
	w := setupWorkspace(t)
	originalSyncWorkspaceDir := syncWorkspaceDir
	blockedDir := filepath.Join(w.Root(), "deep")
	syncWorkspaceDir = func(dir string) error {
		if dir == blockedDir {
			return errors.New("directory tree fsync failed")
		}
		return nil
	}
	defer func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	}()

	err := w.WriteFileFromReader(context.Background(), "/deep/path/stream.txt", strings.NewReader("streamed content"))
	if err == nil {
		t.Fatal("expected WriteFileFromReader() to fail when created directory tree sync fails")
	}
	if !IsVisibleMutationWarning(err) {
		t.Fatalf("expected visible mutation warning, got %v", err)
	}
	if !strings.Contains(err.Error(), "sync created directory tree") {
		t.Fatalf("expected created directory tree sync error, got %v", err)
	}

	data, readErr := os.ReadFile(filepath.Join(w.Root(), "deep", "path", "stream.txt"))
	if readErr != nil {
		t.Fatalf("expected streamed file to remain readable after directory tree sync failure, got %v", readErr)
	}
	if string(data) != "streamed content" {
		t.Fatalf("expected streamed content to be preserved, got %q", string(data))
	}
}

func TestWorkspace_WriteFileFromReader_CleansCreatedDirectoriesWhenWriteFailsBeforeRename(t *testing.T) {
	w := setupWorkspace(t)

	_, err := w.WriteFileFromReaderWithOptions(context.Background(), "/deep/path/toolarge.txt", strings.NewReader("too much data"), WriteFileOptions{MaxBytes: 4})
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(w.Root(), "deep", "path", "toolarge.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected file to remain absent after failed write, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(w.Root(), "deep", "path")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected nested directory to be removed after failed write, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(w.Root(), "deep")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected parent directory to be removed after failed write, got %v", statErr)
	}
}

func TestWorkspace_WriteFileFromReader_PreservesReplacedUnpublishedDirectory(t *testing.T) {
	w := setupWorkspace(t)
	targetPath := filepath.Join(w.Root(), "deep")
	displacedPath := filepath.Join(w.Root(), "displaced-created-directory")
	var replacementPath string
	replaced := false

	originalAfterWorkspaceCreatedDirTempOpen := afterWorkspaceCreatedDirTempOpen
	afterWorkspaceCreatedDirTempOpen = func(tempPath, publishPath string) error {
		if replaced || publishPath != targetPath {
			return nil
		}
		replaced = true
		replacementPath = tempPath
		if err := os.Rename(tempPath, displacedPath); err != nil {
			return err
		}
		return os.Mkdir(tempPath, 0o755)
	}
	t.Cleanup(func() {
		afterWorkspaceCreatedDirTempOpen = originalAfterWorkspaceCreatedDirTempOpen
	})

	_, err := w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/deep/path/replaced-temp.txt",
		strings.NewReader("content"),
		WriteFileOptions{},
	)
	if !errors.Is(err, errWorkspaceCreatedDirOwnershipChanged) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want created-directory ownership error", err)
	}
	if !replaced {
		t.Fatal("unpublished directory replacement hook was not called")
	}
	if base := filepath.Base(replacementPath); !strings.HasPrefix(base, ".workspace-dir-") || !strings.HasSuffix(base, ".tmp") {
		t.Fatalf("temporary directory name = %q, want unpredictable workspace directory name", base)
	}
	if info, statErr := os.Stat(replacementPath); statErr != nil || !info.IsDir() {
		t.Fatalf("replacement temporary directory = %v, %v; want preserved directory", info, statErr)
	}
	if info, statErr := os.Stat(displacedPath); statErr != nil || !info.IsDir() {
		t.Fatalf("displaced owned directory = %v, %v; want preserved directory", info, statErr)
	}
	if _, statErr := os.Stat(targetPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("published target after rejected replacement = %v, want absent", statErr)
	}
}

func TestWorkspace_WriteFileFromReader_PreservesReplacementAfterDirectoryPublish(t *testing.T) {
	w := setupWorkspace(t)
	targetPath := filepath.Join(w.Root(), "deep")
	displacedPath := filepath.Join(w.Root(), "displaced-published-directory")
	replaced := false
	var createdDirs []CreatedDir
	t.Cleanup(func() {
		_ = ReleaseCreatedDirs(createdDirs)
	})

	originalAfterWorkspaceCreatedDirPublish := afterWorkspaceCreatedDirPublish
	afterWorkspaceCreatedDirPublish = func(publishPath string) error {
		if replaced || publishPath != targetPath {
			return nil
		}
		replaced = true
		if err := os.Rename(publishPath, displacedPath); err != nil {
			return err
		}
		return os.Mkdir(publishPath, 0o755)
	}
	t.Cleanup(func() {
		afterWorkspaceCreatedDirPublish = originalAfterWorkspaceCreatedDirPublish
	})

	_, err := w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/deep/path/replaced-published.txt",
		strings.NewReader("content"),
		WriteFileOptions{CreatedDirs: &createdDirs},
	)
	if !IsCreatedDirOwnershipChanged(err) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want created-directory ownership error", err)
	}
	if !replaced {
		t.Fatal("published directory replacement hook was not called")
	}
	if len(createdDirs) != 1 {
		t.Fatalf("created directory evidence count = %d, want 1", len(createdDirs))
	}
	if createdDirs[0].Path != targetPath {
		t.Fatalf("created directory evidence path = %q, want %q", createdDirs[0].Path, targetPath)
	}
	heldInfo, heldStatErr := createdDirs[0].handle.Stat()
	if heldStatErr != nil {
		t.Fatalf("Stat(retained created directory handle) error: %v", heldStatErr)
	}
	if info, statErr := os.Stat(targetPath); statErr != nil || !info.IsDir() {
		t.Fatalf("replacement published directory = %v, %v; want preserved directory", info, statErr)
	}
	if info, statErr := os.Stat(displacedPath); statErr != nil || !info.IsDir() {
		t.Fatalf("displaced owned directory = %v, %v; want preserved directory", info, statErr)
	} else if !os.SameFile(heldInfo, info) {
		t.Fatal("retained created directory handle does not identify displaced owned directory")
	}
}

func TestWorkspace_CreatedDirEvidenceHandlesRemainHeldUntilReleased(t *testing.T) {
	w := setupWorkspace(t)
	var createdDirs []CreatedDir
	t.Cleanup(func() {
		_ = ReleaseCreatedDirs(createdDirs)
	})

	_, err := w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/deep/path/held-evidence.txt",
		strings.NewReader("content"),
		WriteFileOptions{CreatedDirs: &createdDirs},
	)
	if err != nil {
		t.Fatalf("WriteFileFromReaderWithOptions() error: %v", err)
	}
	if len(createdDirs) != 2 {
		t.Fatalf("created directory evidence count = %d, want 2", len(createdDirs))
	}
	for _, created := range createdDirs {
		if created.handle == nil {
			t.Fatalf("created directory %s has no held identity handle", created.Path)
		}
		if _, statErr := created.handle.Stat(); statErr != nil {
			t.Fatalf("Stat(created directory handle %s) error: %v", created.Path, statErr)
		}
	}

	if err := ReleaseCreatedDirs(createdDirs); err != nil {
		t.Fatalf("ReleaseCreatedDirs() error: %v", err)
	}
	for _, created := range createdDirs {
		if _, statErr := created.handle.Stat(); !errors.Is(statErr, os.ErrClosed) {
			t.Fatalf("Stat(released directory handle %s) error = %v, want os.ErrClosed", created.Path, statErr)
		}
	}
	if err := ReleaseCreatedDirs(createdDirs); err != nil {
		t.Fatalf("second ReleaseCreatedDirs() error: %v", err)
	}
}

func TestWorkspace_SnapshotCreatedDirsReturnsDeepestFirstDurableEvidence(t *testing.T) {
	w := setupWorkspace(t)
	createdDirs, err := w.PrepareWriteParent(
		context.Background(),
		"/deep/path/file.txt",
		nil,
	)
	if err != nil {
		t.Fatalf("PrepareWriteParent() error: %v", err)
	}
	t.Cleanup(func() {
		_ = w.CleanupCreatedDirs(context.Background(), createdDirs)
	})

	evidence, err := w.SnapshotCreatedDirs(createdDirs)
	if err != nil {
		t.Fatalf("SnapshotCreatedDirs() error: %v", err)
	}
	if len(evidence) != 2 {
		t.Fatalf("SnapshotCreatedDirs() count = %d, want 2", len(evidence))
	}
	for index, wantRelativePath := range []string{"deep/path", "deep"} {
		got := evidence[index]
		if got.RelativePath != wantRelativePath {
			t.Fatalf("evidence[%d].RelativePath = %q, want %q", index, got.RelativePath, wantRelativePath)
		}
		if got.Path != filepath.Join(w.Root(), filepath.FromSlash(wantRelativePath)) {
			t.Fatalf("evidence[%d].Path = %q", index, got.Path)
		}
		if got.PersistentIdentity == "" || got.DeleteIdentity == "" {
			t.Fatalf("evidence[%d] lacks stable identities: %+v", index, got)
		}
		if !got.Mode.IsDir() || got.Mode.Perm() != 0o755 {
			t.Fatalf("evidence[%d].Mode = %v, want directory 0755", index, got.Mode)
		}
	}
}

func TestWorkspace_SnapshotCreatedDirsRejectsNamespaceReplacement(t *testing.T) {
	w := setupWorkspace(t)
	createdDirs, err := w.PrepareWriteParent(
		context.Background(),
		"/owned/file.txt",
		nil,
	)
	if err != nil {
		t.Fatalf("PrepareWriteParent() error: %v", err)
	}
	t.Cleanup(func() {
		_ = ReleaseCreatedDirs(createdDirs)
	})

	ownedPath := filepath.Join(w.Root(), "owned")
	displacedPath := filepath.Join(w.Root(), "owned-held")
	if err := os.Rename(ownedPath, displacedPath); err != nil {
		t.Fatalf("Rename(owned, held) error: %v", err)
	}
	if err := os.Mkdir(ownedPath, 0o755); err != nil {
		t.Fatalf("Mkdir(replacement) error: %v", err)
	}
	if _, err := w.SnapshotCreatedDirs(createdDirs); !IsCreatedDirOwnershipChanged(err) {
		t.Fatalf("SnapshotCreatedDirs() error = %v, want ownership change", err)
	}
	if info, err := os.Stat(ownedPath); err != nil || !info.IsDir() {
		t.Fatalf("replacement directory = (%v, %v), want preserved", info, err)
	}
	if info, err := os.Stat(displacedPath); err != nil || !info.IsDir() {
		t.Fatalf("displaced owned directory = (%v, %v), want preserved", info, err)
	}
}

func TestWorkspace_WriteFileFromReader_RetainsSuspiciousCreatedDirEvidenceBeforeFilePublish(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(parentPath, displacedPath string) error
		verify func(t *testing.T, parentPath, displacedPath string, heldInfo os.FileInfo)
	}{
		{
			name: "replacement",
			mutate: func(parentPath, displacedPath string) error {
				if err := os.Rename(parentPath, displacedPath); err != nil {
					return err
				}
				return os.Mkdir(parentPath, 0o755)
			},
			verify: func(t *testing.T, parentPath, displacedPath string, heldInfo os.FileInfo) {
				t.Helper()
				replacementInfo, err := os.Stat(parentPath)
				if err != nil {
					t.Fatalf("Stat(replacement parent) error: %v", err)
				}
				if os.SameFile(heldInfo, replacementInfo) {
					t.Fatal("retained handle unexpectedly identifies replacement parent")
				}
				displacedInfo, err := os.Stat(displacedPath)
				if err != nil {
					t.Fatalf("Stat(displaced parent) error: %v", err)
				}
				if !os.SameFile(heldInfo, displacedInfo) {
					t.Fatal("retained handle does not identify displaced created parent")
				}
			},
		},
		{
			name: "mode drift",
			mutate: func(parentPath, _ string) error {
				return os.Chmod(parentPath, 0o700)
			},
			verify: func(t *testing.T, parentPath, _ string, heldInfo os.FileInfo) {
				t.Helper()
				currentInfo, err := os.Stat(parentPath)
				if err != nil {
					t.Fatalf("Stat(drifted parent) error: %v", err)
				}
				if !os.SameFile(heldInfo, currentInfo) {
					t.Fatal("retained handle does not identify drifted created parent")
				}
				if currentInfo.Mode().Perm() != 0o700 {
					t.Fatalf("drifted parent permissions = %o, want 700", currentInfo.Mode().Perm())
				}
			},
		},
		{
			name: "new child",
			mutate: func(parentPath, _ string) error {
				return os.WriteFile(filepath.Join(parentPath, "external-child"), []byte("external"), 0o600)
			},
			verify: func(t *testing.T, parentPath, _ string, heldInfo os.FileInfo) {
				t.Helper()
				currentInfo, err := os.Stat(parentPath)
				if err != nil {
					t.Fatalf("Stat(parent with new child) error: %v", err)
				}
				if !os.SameFile(heldInfo, currentInfo) {
					t.Fatal("retained handle does not identify parent with new child")
				}
				if _, err := os.Stat(filepath.Join(parentPath, "external-child")); err != nil {
					t.Fatalf("Stat(external child) error: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := setupWorkspace(t)
			parentPath := filepath.Join(w.Root(), "suspicious-parent")
			displacedPath := filepath.Join(w.Root(), "displaced-suspicious-parent")
			var createdDirs []CreatedDir
			t.Cleanup(func() {
				_ = ReleaseCreatedDirs(createdDirs)
			})
			stopErr := errors.New("stop before file publish")

			_, err := w.WriteFileFromReaderWithOptions(
				context.Background(),
				"/suspicious-parent/file.txt",
				strings.NewReader("content"),
				WriteFileOptions{
					CreatedDirs: &createdDirs,
					BeforePublish: func() error {
						if err := test.mutate(parentPath, displacedPath); err != nil {
							return err
						}
						return stopErr
					},
				},
			)
			if !errors.Is(err, stopErr) {
				t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want stop error", err)
			}
			if !IsCreatedDirOwnershipChanged(err) {
				t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want created-directory ownership change", err)
			}
			if len(createdDirs) != 1 {
				t.Fatalf("created directory evidence count = %d, want 1", len(createdDirs))
			}
			if createdDirs[0].Path != parentPath {
				t.Fatalf("created directory evidence path = %q, want %q", createdDirs[0].Path, parentPath)
			}
			heldInfo, statErr := createdDirs[0].handle.Stat()
			if statErr != nil {
				t.Fatalf("Stat(retained created directory handle) error: %v", statErr)
			}
			test.verify(t, parentPath, displacedPath, heldInfo)
			if _, statErr := os.Stat(filepath.Join(parentPath, "file.txt")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("file target before publish = %v, want absent", statErr)
			}

			if releaseErr := ReleaseCreatedDirs(createdDirs); releaseErr != nil {
				t.Fatalf("ReleaseCreatedDirs() error: %v", releaseErr)
			}
			if _, statErr := createdDirs[0].handle.Stat(); !errors.Is(statErr, os.ErrClosed) {
				t.Fatalf("Stat(released evidence handle) error = %v, want os.ErrClosed", statErr)
			}
		})
	}
}

func TestWorkspace_CleanupCreatedDirsRemovesOwnedTreeAndReleasesHandles(t *testing.T) {
	w := setupWorkspace(t)
	var createdDirs []CreatedDir
	t.Cleanup(func() {
		_ = ReleaseCreatedDirs(createdDirs)
	})

	_, err := w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/deep/path/cleanup-evidence.txt",
		strings.NewReader("content"),
		WriteFileOptions{CreatedDirs: &createdDirs},
	)
	if err != nil {
		t.Fatalf("WriteFileFromReaderWithOptions() error: %v", err)
	}
	if err := os.Remove(filepath.Join(w.Root(), "deep", "path", "cleanup-evidence.txt")); err != nil {
		t.Fatalf("Remove(published file) error: %v", err)
	}
	if err := w.CleanupCreatedDirs(context.Background(), createdDirs); err != nil {
		t.Fatalf("CleanupCreatedDirs() error: %v", err)
	}
	for _, created := range createdDirs {
		if _, statErr := created.handle.Stat(); !errors.Is(statErr, os.ErrClosed) {
			t.Fatalf("Stat(cleaned directory handle %s) error = %v, want os.ErrClosed", created.Path, statErr)
		}
		if _, statErr := os.Stat(created.Path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("Stat(cleaned directory %s) error = %v, want os.ErrNotExist", created.Path, statErr)
		}
	}
}

func TestWorkspace_WriteFileFromReader_UsesNoReplaceForCreatedDirectoryPublish(t *testing.T) {
	w := setupWorkspace(t)
	targetPath := filepath.Join(w.Root(), "concurrent-parent")
	publishedConcurrentTarget := false

	originalAfterWorkspaceCreatedDirTempOpen := afterWorkspaceCreatedDirTempOpen
	afterWorkspaceCreatedDirTempOpen = func(_, publishPath string) error {
		if publishedConcurrentTarget || publishPath != targetPath {
			return nil
		}
		publishedConcurrentTarget = true
		return os.Mkdir(publishPath, 0o700)
	}
	t.Cleanup(func() {
		afterWorkspaceCreatedDirTempOpen = originalAfterWorkspaceCreatedDirTempOpen
	})

	_, err := w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/concurrent-parent/file.txt",
		strings.NewReader("content"),
		WriteFileOptions{},
	)
	if err != nil {
		t.Fatalf("WriteFileFromReaderWithOptions() error: %v", err)
	}
	if !publishedConcurrentTarget {
		t.Fatal("concurrent target hook was not called")
	}
	info, statErr := os.Stat(targetPath)
	if statErr != nil {
		t.Fatalf("Stat(concurrent target) error: %v", statErr)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("concurrent target permissions = %o, want 700", info.Mode().Perm())
	}
	data, readErr := os.ReadFile(filepath.Join(targetPath, "file.txt"))
	if readErr != nil {
		t.Fatalf("ReadFile(published file) error: %v", readErr)
	}
	if got := string(data); got != "content" {
		t.Fatalf("published content = %q, want content", got)
	}
	matches, globErr := filepath.Glob(filepath.Join(w.Root(), ".workspace-dir-*.tmp"))
	if globErr != nil {
		t.Fatalf("Glob(workspace directory temp) error: %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("workspace directory temps remain after no-replace conflict: %v", matches)
	}
}

func TestWorkspace_WriteFileFromReader_PreservesReplacedCreatedDirectoryOnCleanup(t *testing.T) {
	w := setupWorkspace(t)
	replacementPath := filepath.Join(w.Root(), "deep", "path")
	displacedPath := filepath.Join(w.Root(), "deep", "owned-path")
	readerErr := errors.New("reader failed after directory replacement")
	replaced := false

	reader := readerFunc(func([]byte) (int, error) {
		if replaced {
			return 0, readerErr
		}
		replaced = true
		if err := os.Rename(replacementPath, displacedPath); err != nil {
			return 0, err
		}
		if err := os.Mkdir(replacementPath, 0o755); err != nil {
			return 0, err
		}
		return 0, readerErr
	})

	_, err := w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/deep/path/replaced-dir.txt",
		reader,
		WriteFileOptions{},
	)
	if !errors.Is(err, readerErr) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want reader failure", err)
	}
	if !errors.Is(err, errWorkspaceCreatedDirOwnershipChanged) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want created-directory ownership error", err)
	}

	info, statErr := os.Stat(replacementPath)
	if statErr != nil {
		t.Fatalf("Stat(replacement directory) error: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatalf("replacement path mode = %v, want directory", info.Mode())
	}
	entries, readDirErr := os.ReadDir(replacementPath)
	if readDirErr != nil {
		t.Fatalf("ReadDir(replacement directory) error: %v", readDirErr)
	}
	if len(entries) != 0 {
		t.Fatalf("replacement directory entries = %v, want empty replacement preserved", entries)
	}
	if displacedInfo, displacedStatErr := os.Stat(displacedPath); displacedStatErr != nil || !displacedInfo.IsDir() {
		t.Fatalf("displaced owned directory = %v, %v; want preserved directory", displacedInfo, displacedStatErr)
	}
}

func TestWorkspace_WriteFileFromReader_PreservesCreatedDirectoryChangedAfterFinalBinding(t *testing.T) {
	w := setupWorkspace(t)
	createdPath := filepath.Join(w.Root(), "deep", "path")
	originalBeforeWorkspaceCreatedDirRemoval := beforeWorkspaceCreatedDirRemoval
	changed := false
	beforeWorkspaceCreatedDirRemoval = func(path string) error {
		if path != createdPath {
			return nil
		}
		changed = true
		changedTime := time.Unix(1_700_000_000, 123_456_789)
		return os.Chtimes(path, changedTime, changedTime)
	}
	t.Cleanup(func() { beforeWorkspaceCreatedDirRemoval = originalBeforeWorkspaceCreatedDirRemoval })

	_, err := w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/deep/path/toolarge.txt",
		strings.NewReader("too much data"),
		WriteFileOptions{MaxBytes: 4},
	)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want ErrFileTooLarge", err)
	}
	if !errors.Is(err, errWorkspaceCreatedDirOwnershipChanged) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want created-directory ownership error", err)
	}
	if !changed {
		t.Fatal("created directory was not changed after its final identity binding")
	}
	info, statErr := os.Stat(createdPath)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("created directory after rejected cleanup = %v, %v; want preserved directory", info, statErr)
	}
}

func TestWorkspace_WriteFileFromReader_PublishNoReplacePreservesLateTarget(t *testing.T) {
	w := setupWorkspace(t)
	targetPath := filepath.Join(w.Root(), "late-target.txt")

	_, err := w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/late-target.txt",
		strings.NewReader("caller content"),
		WriteFileOptions{
			PublishNoReplace: true,
			BeforePublish: func() error {
				return os.WriteFile(targetPath, []byte("raced content"), 0o600)
			},
		},
	)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want ErrAlreadyExists", err)
	}
	data, readErr := os.ReadFile(targetPath)
	if readErr != nil {
		t.Fatalf("ReadFile(late target) error = %v", readErr)
	}
	if got := string(data); got != "raced content" {
		t.Fatalf("late target content = %q, want raced content", got)
	}
	entries, readDirErr := os.ReadDir(w.Root())
	if readDirErr != nil {
		t.Fatalf("ReadDir(workspace) error = %v", readDirErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".workspace-") {
			t.Fatalf("workspace temp remains after no-replace conflict: %s", entry.Name())
		}
	}
}

func TestWorkspace_WriteFileFromReader_RejectsReplacedTempWithoutDeletingReplacement(t *testing.T) {
	w := setupWorkspace(t)
	targetPath := filepath.Join(w.Root(), "target.txt")
	displacedPath := filepath.Join(w.Root(), "displaced-owned-temp")
	var replacementPath string
	publishedIdentity := ""

	_, err := w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/target.txt",
		strings.NewReader("caller content"),
		WriteFileOptions{
			PublishNoReplace:  true,
			PublishedIdentity: &publishedIdentity,
			BeforePublish: func() error {
				matches, globErr := filepath.Glob(filepath.Join(w.Root(), ".workspace-*.tmp"))
				if globErr != nil {
					return globErr
				}
				if len(matches) != 1 {
					return fmt.Errorf("workspace temp matches = %v, want exactly one", matches)
				}
				replacementPath = matches[0]
				if renameErr := os.Rename(replacementPath, displacedPath); renameErr != nil {
					return renameErr
				}
				return os.WriteFile(replacementPath, []byte("unknown replacement"), 0o600)
			},
		},
	)
	if !errors.Is(err, errWorkspaceTempOwnershipChanged) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want temp ownership change", err)
	}
	if publishedIdentity != "" {
		t.Fatalf("PublishedIdentity = %q, want empty before rename", publishedIdentity)
	}
	if _, statErr := os.Stat(targetPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("replaced temp was published: %v", statErr)
	}
	replacement, readErr := os.ReadFile(replacementPath)
	if readErr != nil {
		t.Fatalf("ReadFile(replacement) error = %v", readErr)
	}
	if got := string(replacement); got != "unknown replacement" {
		t.Fatalf("replacement content = %q, want unknown replacement", got)
	}
	displaced, readErr := os.ReadFile(displacedPath)
	if readErr != nil {
		t.Fatalf("ReadFile(displaced temp) error = %v", readErr)
	}
	if got := string(displaced); got != "caller content" {
		t.Fatalf("displaced temp content = %q, want caller content", got)
	}
}

func TestWorkspace_WriteFileFromReader_RejectsInPlaceTempMutation(t *testing.T) {
	w := setupWorkspace(t)
	probePath := filepath.Join(w.Root(), "identity-probe")
	if err := os.WriteFile(probePath, []byte("probe"), 0o600); err != nil {
		t.Fatalf("WriteFile(identity probe) error = %v", err)
	}
	probeInfo, err := os.Stat(probePath)
	if err != nil {
		t.Fatalf("Stat(identity probe) error = %v", err)
	}
	if DeleteIdentityTokenForFileInfo(probeInfo) == "" {
		t.Skip("platform does not expose deletion identity")
	}
	if err := os.Remove(probePath); err != nil {
		t.Fatalf("Remove(identity probe) error = %v", err)
	}

	targetPath := filepath.Join(w.Root(), "target.txt")
	_, err = w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/target.txt",
		strings.NewReader("caller content"),
		WriteFileOptions{
			PublishNoReplace: true,
			BeforePublish: func() error {
				matches, globErr := filepath.Glob(filepath.Join(w.Root(), ".workspace-*.tmp"))
				if globErr != nil {
					return globErr
				}
				if len(matches) != 1 {
					return fmt.Errorf("workspace temp matches = %v, want exactly one", matches)
				}
				tempInfo, statErr := os.Stat(matches[0])
				if statErr != nil {
					return statErr
				}
				originalToken := DeleteIdentityTokenForFileInfo(tempInfo)
				time.Sleep(2 * time.Millisecond)
				if writeErr := os.WriteFile(matches[0], []byte("tamper content"), tempInfo.Mode().Perm()); writeErr != nil {
					return writeErr
				}
				if timeErr := os.Chtimes(matches[0], tempInfo.ModTime(), tempInfo.ModTime()); timeErr != nil {
					return timeErr
				}
				mutatedInfo, statErr := os.Stat(matches[0])
				if statErr != nil {
					return statErr
				}
				if mutatedInfo.Mode() != tempInfo.Mode() || mutatedInfo.Size() != tempInfo.Size() ||
					!mutatedInfo.ModTime().Equal(tempInfo.ModTime()) {
					return errors.New("test mutation did not restore visible metadata")
				}
				if DeleteIdentityTokenForFileInfo(mutatedInfo) == originalToken {
					return errors.New("test mutation did not change deletion identity")
				}
				return nil
			},
		},
	)
	if !errors.Is(err, errWorkspaceTempOwnershipChanged) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want temp ownership change", err)
	}
	if _, statErr := os.Stat(targetPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("mutated temp was published: %v", statErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(w.Root(), ".workspace-*.tmp"))
	if globErr != nil {
		t.Fatalf("Glob(workspace temp) error = %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("owned mutated temp was not cleaned: %v", matches)
	}
}

func TestWorkspace_WriteFileFromReader_ReportsPublishedTargetIdentityDrift(t *testing.T) {
	w := setupWorkspace(t)
	targetPath := filepath.Join(w.Root(), "target.txt")
	displacedPath := filepath.Join(w.Root(), "displaced-published-temp")
	publishedIdentity := ""

	originalAfterWorkspaceFilePublish := afterWorkspaceFilePublish
	afterWorkspaceFilePublish = func() error {
		if err := os.Rename(targetPath, displacedPath); err != nil {
			return err
		}
		return os.WriteFile(targetPath, []byte("unknown replacement"), 0o600)
	}
	t.Cleanup(func() {
		afterWorkspaceFilePublish = originalAfterWorkspaceFilePublish
	})

	_, err := w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/target.txt",
		strings.NewReader("caller content"),
		WriteFileOptions{
			PublishNoReplace:  true,
			PublishedIdentity: &publishedIdentity,
		},
	)
	if !IsVisibleMutationWarning(err) || !errors.Is(err, errWorkspaceTempOwnershipChanged) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want visible ownership warning", err)
	}
	if publishedIdentity == "" {
		t.Fatal("PublishedIdentity is empty after rename")
	}
	replacement, readErr := os.ReadFile(targetPath)
	if readErr != nil {
		t.Fatalf("ReadFile(replacement target) error = %v", readErr)
	}
	if got := string(replacement); got != "unknown replacement" {
		t.Fatalf("replacement target content = %q, want unknown replacement", got)
	}
	displaced, readErr := os.ReadFile(displacedPath)
	if readErr != nil {
		t.Fatalf("ReadFile(displaced published temp) error = %v", readErr)
	}
	if got := string(displaced); got != "caller content" {
		t.Fatalf("displaced published content = %q, want caller content", got)
	}
}

func TestWorkspace_WriteFileFromReader_ReportsInPlacePublishedTargetMutation(t *testing.T) {
	w := setupWorkspace(t)
	probePath := filepath.Join(w.Root(), "identity-probe")
	if err := os.WriteFile(probePath, []byte("probe"), 0o600); err != nil {
		t.Fatalf("WriteFile(identity probe) error = %v", err)
	}
	probeInfo, err := os.Stat(probePath)
	if err != nil {
		t.Fatalf("Stat(identity probe) error = %v", err)
	}
	if DeleteIdentityTokenForFileInfo(probeInfo) == "" {
		t.Skip("platform does not expose deletion identity")
	}
	if err := os.Remove(probePath); err != nil {
		t.Fatalf("Remove(identity probe) error = %v", err)
	}

	targetPath := filepath.Join(w.Root(), "target.txt")
	publishedIdentity := ""
	originalAfterWorkspaceFilePublish := afterWorkspaceFilePublish
	afterWorkspaceFilePublish = func() error {
		targetInfo, statErr := os.Stat(targetPath)
		if statErr != nil {
			return statErr
		}
		originalToken := DeleteIdentityTokenForFileInfo(targetInfo)
		time.Sleep(2 * time.Millisecond)
		if writeErr := os.WriteFile(targetPath, []byte("tamper content"), targetInfo.Mode().Perm()); writeErr != nil {
			return writeErr
		}
		if timeErr := os.Chtimes(targetPath, targetInfo.ModTime(), targetInfo.ModTime()); timeErr != nil {
			return timeErr
		}
		mutatedInfo, statErr := os.Stat(targetPath)
		if statErr != nil {
			return statErr
		}
		if mutatedInfo.Mode() != targetInfo.Mode() || mutatedInfo.Size() != targetInfo.Size() ||
			!mutatedInfo.ModTime().Equal(targetInfo.ModTime()) {
			return errors.New("test mutation did not restore visible metadata")
		}
		if DeleteIdentityTokenForFileInfo(mutatedInfo) == originalToken {
			return errors.New("test mutation did not change deletion identity")
		}
		return nil
	}
	t.Cleanup(func() {
		afterWorkspaceFilePublish = originalAfterWorkspaceFilePublish
	})

	_, err = w.WriteFileFromReaderWithOptions(
		context.Background(),
		"/target.txt",
		strings.NewReader("caller content"),
		WriteFileOptions{
			PublishNoReplace:  true,
			PublishedIdentity: &publishedIdentity,
		},
	)
	if !IsVisibleMutationWarning(err) || !errors.Is(err, errWorkspaceTempOwnershipChanged) {
		t.Fatalf("WriteFileFromReaderWithOptions() error = %v, want visible ownership warning", err)
	}
	if publishedIdentity == "" {
		t.Fatal("PublishedIdentity is empty after rename")
	}
	mutated, readErr := os.ReadFile(targetPath)
	if readErr != nil {
		t.Fatalf("ReadFile(mutated target) error = %v", readErr)
	}
	if got := string(mutated); got != "tamper content" {
		t.Fatalf("mutated target content = %q, want tamper content", got)
	}
}

func TestWorkspace_Copy_RollsBackDestinationWhenDirectorySyncFails(t *testing.T) {
	w := setupWorkspace(t)
	if err := w.WriteFile(context.Background(), "/source.txt", []byte("copy content")); err != nil {
		t.Fatalf("WriteFile(source.txt) error: %v", err)
	}

	originalSyncWorkspaceDir := syncWorkspaceDir
	syncWorkspaceDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	}()

	err := w.Copy(context.Background(), "/source.txt", "/copied.txt")
	if err == nil {
		t.Fatal("expected Copy() to fail when parent directory sync fails")
	}
	if !strings.Contains(err.Error(), "sync parent directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(w.Root(), "copied.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected copied destination to be removed after sync failure, got %v", statErr)
	}
	data, readErr := os.ReadFile(filepath.Join(w.Root(), "source.txt"))
	if readErr != nil {
		t.Fatalf("expected source file to remain readable after copy rollback, got %v", readErr)
	}
	if string(data) != "copy content" {
		t.Fatalf("expected source content to remain unchanged, got %q", string(data))
	}
}

func TestWorkspace_RootMutationsAreRejected(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/source.txt", []byte("copy content")); err != nil {
		t.Fatalf("WriteFile(source.txt) error: %v", err)
	}

	if err := w.WriteFile(ctx, "/", []byte("blocked")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("WriteFile(/) error = %v, want ErrNotFound", err)
	}
	if err := w.WriteFileFromReader(ctx, "/", strings.NewReader("blocked")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("WriteFileFromReader(/) error = %v, want ErrNotFound", err)
	}
	if err := w.Mkdir(ctx, "/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Mkdir(/) error = %v, want ErrNotFound", err)
	}
	if err := w.Delete(ctx, "/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete(/) error = %v, want ErrNotFound", err)
	}
	if err := w.DeleteAll(ctx, "/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteAll(/) error = %v, want ErrNotFound", err)
	}
	if err := w.Rename(ctx, "/", "/renamed-root"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rename(/, /renamed-root) error = %v, want ErrNotFound", err)
	}
	if err := w.Copy(ctx, "/", "/copied-root"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Copy(/, /copied-root) error = %v, want ErrNotFound", err)
	}
	if err := w.Copy(ctx, "/source.txt", "/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Copy(/source.txt, /) error = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(w.Root(), "source.txt")); err != nil {
		t.Fatalf("expected source file to remain after rejected root mutations, got %v", err)
	}
}

type chunkedCancelReader struct {
	chunks [][]byte
	index  int
	cancel func()
}

func (r *chunkedCancelReader) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.index]
	r.index++
	if r.index == 1 && r.cancel != nil {
		r.cancel()
	}
	return copy(p, chunk), nil
}

func setupWorkspace(t *testing.T) *Workspace {
	tmpDir := t.TempDir()
	w, err := New(tmpDir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close() error: %v", err)
		}
	})
	return w
}

func TestWorkspace_StatAfterCloseReturnsErrClosed(t *testing.T) {
	w := setupWorkspace(t)

	if err := w.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	_, err := w.Stat(context.Background(), "/after-close.txt")
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Stat() after Close error = %v, want %v", err, os.ErrClosed)
	}
}

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()
	w, err := New(tmpDir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if w.Root() != tmpDir {
		t.Errorf("Root() = %s, want %s", w.Root(), tmpDir)
	}
}

func TestNew_SyncsCreatedDirectoriesDeepestParentFirst(t *testing.T) {
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "nested", "workspace", "root")

	originalSyncWorkspaceDir := syncWorkspaceDir
	var synced []string
	syncWorkspaceDir = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}
	defer func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	}()

	if _, err := New(root); err != nil {
		t.Fatalf("New() error: %v", err)
	}

	want := []string{
		filepath.Join(tmpDir, "nested", "workspace"),
		filepath.Join(tmpDir, "nested"),
		tmpDir,
	}
	if strings.Join(synced, "|") != strings.Join(want, "|") {
		t.Fatalf("synced directories = %v, want %v", synced, want)
	}
}

func TestNew_ReturnsDirectorySyncErrorWhenCreatingRoot(t *testing.T) {
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "nested", "workspace")

	originalSyncWorkspaceDir := syncWorkspaceDir
	syncWorkspaceDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	}()

	if _, err := New(root); err == nil {
		t.Fatal("expected New() to fail when workspace root directory tree sync fails")
	} else if !strings.Contains(err.Error(), "failed to sync directory") {
		t.Fatalf("expected directory sync failure, got %v", err)
	}
}

func TestNew_RejectsSymlinkRoot(t *testing.T) {
	tmpDir := t.TempDir()
	realRoot := filepath.Join(tmpDir, "real-root")
	if err := os.MkdirAll(realRoot, 0755); err != nil {
		t.Fatalf("MkdirAll(real-root) error: %v", err)
	}
	rootLink := filepath.Join(tmpDir, "root-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Fatalf("Symlink(root-link) error: %v", err)
	}

	_, err := New(rootLink)
	if !errors.Is(err, errWorkspaceRootSymlink) {
		t.Fatalf("expected symlink root rejection, got %v", err)
	}
}

func TestNew_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0755); err != nil {
		t.Fatalf("MkdirAll(real-parent) error: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked-parent) error: %v", err)
	}

	_, err := New(filepath.Join(linkedParent, "workspace"))
	if !errors.Is(err, errWorkspaceRootSymlink) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
}

func TestNew_DoesNotCreateRootThroughSymlinkParent(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0755); err != nil {
		t.Fatalf("MkdirAll(real-parent) error: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked-parent) error: %v", err)
	}

	workspaceRoot := filepath.Join(linkedParent, "workspace")
	if _, err := New(workspaceRoot); !errors.Is(err, errWorkspaceRootSymlink) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(realParent, "workspace")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace root created through symlink parent, stat error = %v", err)
	}
}

func TestCleanPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/test.txt", "/test.txt"},
		{"test.txt", "/test.txt"},
		{"./test.txt", "/test.txt"},
		{"//test.txt", "/test.txt"},
		{"/a/b/../c", "/a/c"},
		{"foo..txt", "/foo..txt"},
		{"/nested/foo..txt", "/nested/foo..txt"},
		{"../../etc/passwd", "/etc/passwd"},
		{"", "/"},
		{"/", "/"},
	}

	for _, tt := range tests {
		got := CleanPath(tt.input)
		if got != tt.want {
			t.Errorf("CleanPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWorkspace_WriteFile_AllowsDoubleDotInFileName(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/foo..txt", []byte("double dot")); err != nil {
		t.Fatalf("WriteFile(foo..txt) error: %v", err)
	}

	data, err := w.ReadFile(ctx, "/foo..txt")
	if err != nil {
		t.Fatalf("ReadFile(foo..txt) error: %v", err)
	}
	if string(data) != "double dot" {
		t.Fatalf("ReadFile(foo..txt) = %q, want %q", string(data), "double dot")
	}
}

func TestWorkspace_OperationsRejectTraversalLikeNames(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/safe"); err != nil {
		t.Fatalf("Mkdir(/safe) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/safe/existing.txt", []byte("safe")); err != nil {
		t.Fatalf("WriteFile(/safe/existing.txt) error: %v", err)
	}

	if _, err := w.Stat(ctx, "../safe/existing.txt"); err != ErrNotFound {
		t.Fatalf("Stat(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := w.Stat(ctx, "/safe/./existing.txt"); err != ErrNotFound {
		t.Fatalf("Stat(dot segment) error = %v, want ErrNotFound", err)
	}
	if _, err := w.ReadDir(ctx, "../safe"); err != ErrNotFound {
		t.Fatalf("ReadDir(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := w.ReadDir(ctx, "./safe"); err != ErrNotFound {
		t.Fatalf("ReadDir(dot segment) error = %v, want ErrNotFound", err)
	}
	if _, err := w.OpenFile(ctx, "../safe/existing.txt"); err != ErrNotFound {
		t.Fatalf("OpenFile(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := w.OpenFile(ctx, "/safe/./existing.txt"); err != ErrNotFound {
		t.Fatalf("OpenFile(dot segment) error = %v, want ErrNotFound", err)
	}
	if _, err := w.ReadFile(ctx, "../safe/existing.txt"); err != ErrNotFound {
		t.Fatalf("ReadFile(traversal) error = %v, want ErrNotFound", err)
	}
	if _, err := w.ReadFile(ctx, "./safe/existing.txt"); err != ErrNotFound {
		t.Fatalf("ReadFile(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := w.WriteFile(ctx, "../escape.txt", []byte("blocked")); err != ErrNotFound {
		t.Fatalf("WriteFile(traversal) error = %v, want ErrNotFound", err)
	}
	if err := w.WriteFile(ctx, "/safe/./blocked.txt", []byte("blocked")); err != ErrNotFound {
		t.Fatalf("WriteFile(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := w.WriteFileFromReader(ctx, "../escape-reader.txt", strings.NewReader("blocked")); err != ErrNotFound {
		t.Fatalf("WriteFileFromReader(traversal) error = %v, want ErrNotFound", err)
	}
	if err := w.WriteFileFromReader(ctx, "./safe/blocked-reader.txt", strings.NewReader("blocked")); err != ErrNotFound {
		t.Fatalf("WriteFileFromReader(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := w.Mkdir(ctx, "../escape-dir"); err != ErrNotFound {
		t.Fatalf("Mkdir(traversal) error = %v, want ErrNotFound", err)
	}
	if err := w.Mkdir(ctx, "/safe/./blocked-dir"); err != ErrNotFound {
		t.Fatalf("Mkdir(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := w.Delete(ctx, "../safe/existing.txt"); err != ErrNotFound {
		t.Fatalf("Delete(traversal) error = %v, want ErrNotFound", err)
	}
	if err := w.Delete(ctx, "/safe/./existing.txt"); err != ErrNotFound {
		t.Fatalf("Delete(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := w.DeleteAll(ctx, "../safe"); err != ErrNotFound {
		t.Fatalf("DeleteAll(traversal) error = %v, want ErrNotFound", err)
	}
	if err := w.DeleteAll(ctx, "./safe"); err != ErrNotFound {
		t.Fatalf("DeleteAll(dot segment) error = %v, want ErrNotFound", err)
	}
	if err := w.Rename(ctx, "../safe/existing.txt", "/safe/renamed.txt"); err != ErrNotFound {
		t.Fatalf("Rename(source traversal) error = %v, want ErrNotFound", err)
	}
	if err := w.Rename(ctx, "/safe/existing.txt", "../renamed.txt"); err != ErrNotFound {
		t.Fatalf("Rename(destination traversal) error = %v, want ErrNotFound", err)
	}
	if err := w.Rename(ctx, "/safe/./existing.txt", "/safe/renamed.txt"); err != ErrNotFound {
		t.Fatalf("Rename(source dot segment) error = %v, want ErrNotFound", err)
	}
	if err := w.Rename(ctx, "/safe/existing.txt", "/safe/./renamed.txt"); err != ErrNotFound {
		t.Fatalf("Rename(destination dot segment) error = %v, want ErrNotFound", err)
	}
	if err := w.Copy(ctx, "../safe/existing.txt", "/safe/copied.txt"); err != ErrNotFound {
		t.Fatalf("Copy(source traversal) error = %v, want ErrNotFound", err)
	}
	if err := w.Copy(ctx, "/safe/existing.txt", "../copied.txt"); err != ErrNotFound {
		t.Fatalf("Copy(destination traversal) error = %v, want ErrNotFound", err)
	}
	if err := w.Copy(ctx, "./safe/existing.txt", "/safe/copied.txt"); err != ErrNotFound {
		t.Fatalf("Copy(source dot segment) error = %v, want ErrNotFound", err)
	}
	if err := w.Copy(ctx, "/safe/existing.txt", "/safe/./copied.txt"); err != ErrNotFound {
		t.Fatalf("Copy(destination dot segment) error = %v, want ErrNotFound", err)
	}
	if err := w.Walk(ctx, "../safe", func(path string, info *FileInfo) error { return nil }); err != ErrNotFound {
		t.Fatalf("Walk(traversal) error = %v, want ErrNotFound", err)
	}
	if err := w.Walk(ctx, "/safe/.", func(path string, info *FileInfo) error { return nil }); err != ErrNotFound {
		t.Fatalf("Walk(dot segment) error = %v, want ErrNotFound", err)
	}
	if _, err := w.Stat(ctx, "/safe/existing\x00.txt"); err != ErrNotFound {
		t.Fatalf("Stat(NUL) error = %v, want ErrNotFound", err)
	}
	if err := w.WriteFile(ctx, "/safe/nul\x00.txt", []byte("blocked")); err != ErrNotFound {
		t.Fatalf("WriteFile(NUL) error = %v, want ErrNotFound", err)
	}
	if _, err := w.Stat(ctx, "/safe/existing\n.txt"); err != ErrNotFound {
		t.Fatalf("Stat(control character) error = %v, want ErrNotFound", err)
	}
	if err := w.WriteFile(ctx, "/safe/delete\x7f.txt", []byte("blocked")); err != ErrNotFound {
		t.Fatalf("WriteFile(delete control character) error = %v, want ErrNotFound", err)
	}
	if w.Exists(ctx, "../safe/existing.txt") {
		t.Fatal("expected Exists(traversal) to return false")
	}
	if w.Exists(ctx, "/safe/./existing.txt") {
		t.Fatal("expected Exists(dot segment) to return false")
	}
	if w.IsDir(ctx, "../safe") {
		t.Fatal("expected IsDir(traversal) to return false")
	}
	if w.IsDir(ctx, "./safe") {
		t.Fatal("expected IsDir(dot segment) to return false")
	}

	if data, err := w.ReadFile(ctx, "/safe/existing.txt"); err != nil {
		t.Fatalf("ReadFile(/safe/existing.txt) after traversal rejections error: %v", err)
	} else if string(data) != "safe" {
		t.Fatalf("ReadFile(/safe/existing.txt) = %q, want %q", string(data), "safe")
	}
	if w.Exists(ctx, "/escape.txt") {
		t.Fatal("expected traversal write not to create normalized /escape.txt")
	}
	if w.Exists(ctx, "/escape-reader.txt") {
		t.Fatal("expected traversal reader write not to create normalized /escape-reader.txt")
	}
	if w.Exists(ctx, "/safe/blocked.txt") {
		t.Fatal("expected dot-segment write not to create normalized /safe/blocked.txt")
	}
	if w.Exists(ctx, "/safe/blocked-reader.txt") {
		t.Fatal("expected dot-segment reader write not to create normalized /safe/blocked-reader.txt")
	}
	if w.Exists(ctx, "/escape-dir") {
		t.Fatal("expected traversal mkdir not to create normalized /escape-dir")
	}
	if w.Exists(ctx, "/safe/blocked-dir") {
		t.Fatal("expected dot-segment mkdir not to create normalized /safe/blocked-dir")
	}
	if w.Exists(ctx, "/safe/nul.txt") {
		t.Fatal("expected NUL write not to create normalized /safe/nul.txt")
	}
}

func TestWorkspace_WriteFile_ReadFile(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	content := []byte("hello world")

	err := w.WriteFile(ctx, "/test.txt", content)
	if err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	got, err := w.ReadFile(ctx, "/test.txt")
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}

	if string(got) != string(content) {
		t.Errorf("Content = %q, want %q", got, content)
	}
}

func TestWorkspace_Stat(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	w.WriteFile(ctx, "/file.txt", []byte("content"))

	info, err := w.Stat(ctx, "/file.txt")
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}

	if info.Name != "file.txt" {
		t.Errorf("Name = %s, want file.txt", info.Name)
	}
	if info.IsDir {
		t.Error("IsDir should be false for file")
	}
	if info.Size != 7 {
		t.Errorf("Size = %d, want 7", info.Size)
	}
}

func TestWorkspace_Stat_NotFound(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	_, err := w.Stat(ctx, "/nonexistent.txt")
	if err != ErrNotFound {
		t.Errorf("Stat() error = %v, want ErrNotFound", err)
	}
}

func TestWorkspace_Mkdir(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	err := w.Mkdir(ctx, "/testdir")
	if err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	info, err := w.Stat(ctx, "/testdir")
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}

	if !info.IsDir {
		t.Error("IsDir should be true for directory")
	}
}

func TestWorkspace_Mkdir_ReturnsDirectorySyncErrorAfterNestedCreate(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	originalSyncWorkspaceDir := syncWorkspaceDir
	syncCalls := 0
	syncWorkspaceDir = func(dir string) error {
		syncCalls++
		if syncCalls == 2 {
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	})

	err := w.Mkdir(ctx, "/nested/a/b")
	if err == nil {
		t.Fatal("expected Mkdir() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync directory") {
		t.Fatalf("expected directory sync failure in error, got %v", err)
	}
	if syncCalls < 2 {
		t.Fatalf("expected nested mkdir to sync more than one parent directory, got %d calls", syncCalls)
	}

	info, statErr := w.Stat(ctx, "/nested/a/b")
	if statErr != nil {
		t.Fatalf("Stat(/nested/a/b) after sync failure error: %v", statErr)
	}
	if !info.IsDir {
		t.Fatal("expected nested directory to remain present after sync failure")
	}
}

func TestWorkspace_Mkdir_AlreadyExists(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	w.Mkdir(ctx, "/existingdir")

	err := w.Mkdir(ctx, "/existingdir")
	if err != ErrAlreadyExists {
		t.Errorf("Mkdir() error = %v, want ErrAlreadyExists", err)
	}
}

func TestWorkspace_ReadDir(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	w.Mkdir(ctx, "/dir")
	w.WriteFile(ctx, "/dir/a.txt", []byte("a"))
	w.WriteFile(ctx, "/dir/b.txt", []byte("b"))
	w.Mkdir(ctx, "/dir/subdir")

	entries, err := w.ReadDir(ctx, "/dir")
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("ReadDir() returned %d entries, want 3", len(entries))
	}
}

func TestWorkspace_ReadDir_HidesSymlinkEntries(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/dir"); err != nil {
		t.Fatalf("Mkdir(dir) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/dir/visible.txt", []byte("visible")); err != nil {
		t.Fatalf("WriteFile(visible.txt) error: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0644); err != nil {
		t.Fatalf("WriteFile(outside.txt) error: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(w.Root(), "dir", "linked.txt")); err != nil {
		t.Fatalf("Symlink(linked.txt) error: %v", err)
	}

	entries, err := w.ReadDir(ctx, "/dir")
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ReadDir() returned %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].Name != "visible.txt" {
		t.Fatalf("ReadDir() entry = %q, want visible.txt", entries[0].Name)
	}
}

func TestWorkspace_ReadDirRejectsUnsafeEntryNames(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/dir"); err != nil {
		t.Fatalf("Mkdir(dir) error: %v", err)
	}

	unsafeNames := []string{
		"nested\\report.txt",
		"report\n2026.txt",
		"report\x7f.txt",
	}
	tested := 0
	for _, unsafeName := range unsafeNames {
		unsafePath := filepath.Join(w.Root(), "dir", unsafeName)
		if err := os.WriteFile(unsafePath, []byte("unsafe"), 0644); err != nil {
			t.Logf("skipping unsupported unsafe filename %q: %v", unsafeName, err)
			continue
		}
		tested++

		entries, err := w.ReadDir(ctx, "/dir")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("ReadDir() with unsafe entry %q error = %v, want ErrNotFound", unsafeName, err)
		}
		if entries != nil {
			t.Fatalf("expected no entries after unsafe entry %q, got %d", unsafeName, len(entries))
		}
		if err := os.Remove(unsafePath); err != nil {
			t.Fatalf("Remove(%q) error: %v", unsafeName, err)
		}
	}
	if tested == 0 {
		t.Skip("platform does not support unsafe filenames for this test")
	}
}

func TestWorkspace_ReadDir_ReturnsErrNotDirWhenPathIsFile(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	w.WriteFile(ctx, "/file.txt", []byte("content"))

	entries, err := w.ReadDir(ctx, "/file.txt")
	if err != ErrNotDir {
		t.Fatalf("ReadDir() error = %v, want ErrNotDir", err)
	}
	if entries != nil {
		t.Fatalf("expected no entries for file path, got %d", len(entries))
	}
}

func TestWorkspace_ReadDir_ReturnsContextCanceledBeforeRead(t *testing.T) {
	w := setupWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	entries, err := w.ReadDir(ctx, "/")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if entries != nil {
		t.Fatalf("expected no entries after cancellation, got %d", len(entries))
	}
}

func TestWorkspace_ReadDir_StopsWhenContextIsCanceledMidIteration(t *testing.T) {
	w := setupWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())

	if err := w.Mkdir(ctx, "/dir"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := w.WriteFile(ctx, "/dir/a.txt", []byte("a")); err != nil {
		t.Fatalf("WriteFile(a.txt) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/dir/b.txt", []byte("b")); err != nil {
		t.Fatalf("WriteFile(b.txt) error: %v", err)
	}

	originalReadDirEntryInfo := readDirEntryInfo
	readDirEntryInfo = func(root *os.Root, name string, entry os.DirEntry) (os.FileInfo, error) {
		if entry.Name() == "a.txt" {
			cancel()
		}
		return originalReadDirEntryInfo(root, name, entry)
	}
	defer func() {
		readDirEntryInfo = originalReadDirEntryInfo
	}()

	entries, err := w.ReadDir(ctx, "/dir")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled after mid-iteration cancel, got %v", err)
	}
	if entries != nil {
		t.Fatalf("expected no partial entries on cancellation, got %d", len(entries))
	}
}

func TestWorkspace_ReadDir_ReturnsEntryInfoError(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/dir"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := w.WriteFile(ctx, "/dir/ok.txt", []byte("ok")); err != nil {
		t.Fatalf("WriteFile(ok.txt) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/dir/broken.txt", []byte("broken")); err != nil {
		t.Fatalf("WriteFile(broken.txt) error: %v", err)
	}

	originalReadDirEntryInfo := readDirEntryInfo
	readDirEntryInfo = func(root *os.Root, name string, entry os.DirEntry) (os.FileInfo, error) {
		if entry.Name() == "broken.txt" {
			return nil, errors.New("stat failed")
		}
		return originalReadDirEntryInfo(root, name, entry)
	}
	defer func() {
		readDirEntryInfo = originalReadDirEntryInfo
	}()

	entries, err := w.ReadDir(ctx, "/dir")
	if err == nil {
		t.Fatal("expected ReadDir() to fail when entry info lookup fails")
	}
	if err.Error() != "stat failed" {
		t.Fatalf("expected stat failed error, got %v", err)
	}
	if entries != nil {
		t.Fatalf("expected no entries on entry-info failure, got %d", len(entries))
	}
}

func TestWorkspace_Delete(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	w.WriteFile(ctx, "/todelete.txt", []byte("delete me"))

	err := w.Delete(ctx, "/todelete.txt")
	if err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	_, err = w.Stat(ctx, "/todelete.txt")
	if err != ErrNotFound {
		t.Error("File should not exist after delete")
	}
}

func TestWorkspace_Delete_ReturnsDirectorySyncErrorAfterRemove(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()
	if err := w.WriteFile(ctx, "/delete-sync.txt", []byte("delete me")); err != nil {
		t.Fatalf("WriteFile(delete-sync.txt) error: %v", err)
	}

	originalSyncWorkspaceDir := syncWorkspaceDir
	syncWorkspaceDir = func(dir string) error {
		return errors.New("sync dir failed")
	}
	t.Cleanup(func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	})

	err := w.Delete(ctx, "/delete-sync.txt")
	if err == nil {
		t.Fatal("expected Delete() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync directory") {
		t.Fatalf("expected directory sync failure in error, got %v", err)
	}

	if _, statErr := w.Stat(ctx, "/delete-sync.txt"); statErr != ErrNotFound {
		t.Fatalf("expected file to remain deleted after sync failure, got %v", statErr)
	}
}

func TestWorkspace_Delete_NotFound(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	err := w.Delete(ctx, "/nonexistent.txt")
	if err != ErrNotFound {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

func TestWorkspace_Delete_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/parent-file", []byte("content")); err != nil {
		t.Fatalf("WriteFile(parent-file) error: %v", err)
	}

	err := w.Delete(ctx, "/parent-file/child.txt")
	if err != ErrNotDir {
		t.Fatalf("Delete() error = %v, want ErrNotDir", err)
	}
}

func TestWorkspace_DeleteAll(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	w.Mkdir(ctx, "/parentdir")
	w.WriteFile(ctx, "/parentdir/file.txt", []byte("x"))
	w.Mkdir(ctx, "/parentdir/child")
	w.WriteFile(ctx, "/parentdir/child/nested.txt", []byte("y"))

	err := w.DeleteAll(ctx, "/parentdir")
	if err != nil {
		t.Fatalf("DeleteAll() error: %v", err)
	}

	_, err = w.Stat(ctx, "/parentdir")
	if err != ErrNotFound {
		t.Error("Directory should not exist after DeleteAll")
	}
}

func TestWorkspace_DeleteAll_ReturnsDirectorySyncErrorAfterRemove(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()
	if err := w.Mkdir(ctx, "/deleteall-sync"); err != nil {
		t.Fatalf("Mkdir(deleteall-sync) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/deleteall-sync/file.txt", []byte("x")); err != nil {
		t.Fatalf("WriteFile(deleteall-sync/file.txt) error: %v", err)
	}

	originalSyncWorkspaceDir := syncWorkspaceDir
	syncWorkspaceDir = func(dir string) error {
		return errors.New("sync dir failed")
	}
	t.Cleanup(func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	})

	err := w.DeleteAll(ctx, "/deleteall-sync")
	if err == nil {
		t.Fatal("expected DeleteAll() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync directory") {
		t.Fatalf("expected directory sync failure in error, got %v", err)
	}

	if _, statErr := w.Stat(ctx, "/deleteall-sync"); statErr != ErrNotFound {
		t.Fatalf("expected path to remain deleted after sync failure, got %v", statErr)
	}
}

func TestWorkspace_DeleteAll_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/parent-file", []byte("content")); err != nil {
		t.Fatalf("WriteFile(parent-file) error: %v", err)
	}

	err := w.DeleteAll(ctx, "/parent-file/child")
	if err != ErrNotDir {
		t.Fatalf("DeleteAll() error = %v, want ErrNotDir", err)
	}
}

func TestWorkspace_Rename(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	w.WriteFile(ctx, "/oldname.txt", []byte("content"))

	err := w.Rename(ctx, "/oldname.txt", "/newname.txt")
	if err != nil {
		t.Fatalf("Rename() error: %v", err)
	}

	if w.Exists(ctx, "/oldname.txt") {
		t.Error("Old path should not exist")
	}
	if !w.Exists(ctx, "/newname.txt") {
		t.Error("New path should exist")
	}
}

func TestWorkspace_Rename_ReturnsDirectorySyncErrorAfterRename(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()
	if err := w.Mkdir(ctx, "/src-dir"); err != nil {
		t.Fatalf("Mkdir(src-dir) error: %v", err)
	}
	if err := w.Mkdir(ctx, "/dst-dir"); err != nil {
		t.Fatalf("Mkdir(dst-dir) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/src-dir/source.txt", []byte("content")); err != nil {
		t.Fatalf("WriteFile(source.txt) error: %v", err)
	}

	originalSyncWorkspaceDir := syncWorkspaceDir
	syncFailed := false
	syncWorkspaceDir = func(dir string) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	})

	err := w.Rename(ctx, "/src-dir/source.txt", "/dst-dir/renamed.txt")
	if err == nil {
		t.Fatal("expected Rename() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync directory") {
		t.Fatalf("expected sync failure in error, got %v", err)
	}

	data, readErr := w.ReadFile(ctx, "/src-dir/source.txt")
	if readErr != nil {
		t.Fatalf("ReadFile(source.txt) after rollback error: %v", readErr)
	}
	if string(data) != "content" {
		t.Fatalf("expected source content after rollback, got %q", string(data))
	}
	if _, statErr := w.Stat(ctx, "/dst-dir/renamed.txt"); statErr != ErrNotFound {
		t.Fatalf("expected destination to be absent after rollback, got %v", statErr)
	}
}

func TestWorkspace_Rename_ReturnsWarningWhenRollbackFailsAfterRename(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()
	if err := w.Mkdir(ctx, "/src-dir"); err != nil {
		t.Fatalf("Mkdir(src-dir) error: %v", err)
	}
	if err := w.Mkdir(ctx, "/dst-dir"); err != nil {
		t.Fatalf("Mkdir(dst-dir) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/src-dir/source.txt", []byte("content")); err != nil {
		t.Fatalf("WriteFile(source.txt) error: %v", err)
	}

	originalSyncWorkspaceDir := syncWorkspaceDir
	syncFailed := false
	var blockRollbackErr error
	syncWorkspaceDir = func(dir string) error {
		if !syncFailed {
			syncFailed = true
			blockRollbackErr = os.Mkdir(filepath.Join(w.Root(), "src-dir", "source.txt"), 0755)
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncWorkspaceDir = originalSyncWorkspaceDir
	})

	err := w.Rename(ctx, "/src-dir/source.txt", "/dst-dir/renamed.txt")
	if blockRollbackErr != nil {
		t.Fatalf("failed to arrange rollback conflict: %v", blockRollbackErr)
	}
	if !IsVisibleMutationWarning(err) {
		t.Fatalf("Rename() error = %v, want visible mutation warning", err)
	}
	if !strings.Contains(err.Error(), "failed to rollback renamed path") {
		t.Fatalf("expected rollback failure in warning, got %v", err)
	}

	data, readErr := w.ReadFile(ctx, "/dst-dir/renamed.txt")
	if readErr != nil {
		t.Fatalf("ReadFile(renamed.txt) after rollback failure error: %v", readErr)
	}
	if string(data) != "content" {
		t.Fatalf("expected destination content after rollback failure, got %q", string(data))
	}
	info, statErr := w.Stat(ctx, "/src-dir/source.txt")
	if statErr != nil {
		t.Fatalf("Stat(source.txt) rollback blocker error: %v", statErr)
	}
	if !info.IsDir {
		t.Fatal("expected rollback blocker to remain at original path as a directory")
	}
}

func TestWorkspace_Rename_ReturnsErrNotFoundWhenDestinationParentMissing(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/rename-source.txt", []byte("content")); err != nil {
		t.Fatalf("WriteFile(rename-source.txt) error: %v", err)
	}

	err := w.Rename(ctx, "/rename-source.txt", "/missing-parent/child.txt")
	if err != ErrNotFound {
		t.Fatalf("Rename() error = %v, want ErrNotFound", err)
	}
	if !w.Exists(ctx, "/rename-source.txt") {
		t.Fatal("source should remain after rejected rename")
	}
}

func TestWorkspace_Rename_DoesNotOverwriteTargetCreatedAfterPrecheck(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/rename-source.txt", []byte("source")); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	originalBeforeWorkspaceRename := beforeWorkspaceRename
	inserted := false
	beforeWorkspaceRename = func() error {
		if inserted {
			return nil
		}
		inserted = true
		return os.WriteFile(filepath.Join(w.Root(), "rename-dest.txt"), []byte("live target"), 0644)
	}
	t.Cleanup(func() {
		beforeWorkspaceRename = originalBeforeWorkspaceRename
	})

	err := w.Rename(ctx, "/rename-source.txt", "/rename-dest.txt")
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Rename() error = %v, want ErrAlreadyExists", err)
	}

	sourceData, readErr := os.ReadFile(filepath.Join(w.Root(), "rename-source.txt"))
	if readErr != nil {
		t.Fatalf("ReadFile(source) after rejected rename error: %v", readErr)
	}
	if string(sourceData) != "source" {
		t.Fatalf("source content = %q, want source", sourceData)
	}
	destData, readErr := os.ReadFile(filepath.Join(w.Root(), "rename-dest.txt"))
	if readErr != nil {
		t.Fatalf("ReadFile(dest) after rejected rename error: %v", readErr)
	}
	if string(destData) != "live target" {
		t.Fatalf("dest content = %q, want live target", destData)
	}
}

func TestWorkspace_Copy(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	original := []byte("copy me")
	w.WriteFile(ctx, "/source.txt", original)

	err := w.Copy(ctx, "/source.txt", "/dest.txt")
	if err != nil {
		t.Fatalf("Copy() error: %v", err)
	}

	if !w.Exists(ctx, "/source.txt") {
		t.Error("Source should still exist")
	}
	if !w.Exists(ctx, "/dest.txt") {
		t.Error("Destination should exist")
	}

	got, _ := w.ReadFile(ctx, "/dest.txt")
	if string(got) != string(original) {
		t.Errorf("Copied content = %q, want %q", got, original)
	}
}

func TestWorkspace_Copy_ReturnsContextCanceledAndCleansUpTempFile(t *testing.T) {
	w := setupWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())

	if err := w.WriteFile(context.Background(), "/source.txt", []byte("content")); err != nil {
		t.Fatalf("WriteFile(source.txt) error: %v", err)
	}

	originalCopyWorkspaceData := copyWorkspaceData
	copyWorkspaceData = func(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
		cancel()
		return 0, ctx.Err()
	}
	defer func() {
		copyWorkspaceData = originalCopyWorkspaceData
	}()

	err := w.Copy(ctx, "/source.txt", "/dest.txt")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(w.Root(), "dest.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no destination file after canceled copy, got %v", statErr)
	}
	if matches, globErr := filepath.Glob(filepath.Join(w.Root(), ".workspace-*.tmp")); globErr != nil {
		t.Fatalf("Glob(.workspace-*.tmp) error: %v", globErr)
	} else if len(matches) != 0 {
		t.Fatalf("expected no leftover temp files after canceled copy, got %v", matches)
	}
}

func TestWorkspace_Copy_DoesNotOverwriteFileCreatedDuringCopy(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/source.txt", []byte("source")); err != nil {
		t.Fatalf("WriteFile(source.txt) error: %v", err)
	}

	originalCopyWorkspaceData := copyWorkspaceData
	copyWorkspaceData = func(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
		n, err := io.Copy(dst, src)
		if err != nil {
			return n, err
		}
		if writeErr := os.WriteFile(filepath.Join(w.Root(), "dest.txt"), []byte("dest"), 0644); writeErr != nil {
			return n, writeErr
		}
		return n, nil
	}
	t.Cleanup(func() {
		copyWorkspaceData = originalCopyWorkspaceData
	})

	err := w.Copy(ctx, "/source.txt", "/dest.txt")
	if err != ErrAlreadyExists {
		t.Fatalf("Copy() error = %v, want ErrAlreadyExists", err)
	}

	destData, readErr := os.ReadFile(filepath.Join(w.Root(), "dest.txt"))
	if readErr != nil {
		t.Fatalf("ReadFile(dest.txt) error: %v", readErr)
	}
	if string(destData) != "dest" {
		t.Fatalf("destination content = %q, want %q", string(destData), "dest")
	}
	if !w.Exists(ctx, "/source.txt") {
		t.Fatal("source should remain after copy conflict")
	}
}

func TestWorkspace_Copy_CompletesWhenTempCleanupRetrySucceedsAfterLink(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/source.txt", []byte("source")); err != nil {
		t.Fatalf("WriteFile(source.txt) error: %v", err)
	}

	originalFinalizeWorkspaceCopyTemp := finalizeWorkspaceCopyTemp
	finalizeCalls := 0
	finalizeWorkspaceCopyTemp = func(root *os.Root, tmpPath string) error {
		finalizeCalls++
		if finalizeCalls == 1 {
			return errors.New("remove temp link failed")
		}
		return originalFinalizeWorkspaceCopyTemp(root, tmpPath)
	}
	t.Cleanup(func() {
		finalizeWorkspaceCopyTemp = originalFinalizeWorkspaceCopyTemp
	})

	err := w.Copy(ctx, "/source.txt", "/dest.txt")
	if err != nil {
		t.Fatalf("Copy() error = %v, want nil after successful temp cleanup retry", err)
	}
	if !w.Exists(ctx, "/dest.txt") {
		t.Fatal("destination should exist after temp cleanup retry")
	}
	destData, readErr := w.ReadFile(ctx, "/dest.txt")
	if readErr != nil {
		t.Fatalf("ReadFile(dest.txt) error: %v", readErr)
	}
	if string(destData) != "source" {
		t.Fatalf("destination content = %q, want %q", string(destData), "source")
	}
	if matches, globErr := filepath.Glob(filepath.Join(w.Root(), ".workspace-*.tmp")); globErr != nil {
		t.Fatalf("Glob(.workspace-*.tmp) error: %v", globErr)
	} else if len(matches) != 0 {
		t.Fatalf("expected no leftover temp files after finalize cleanup retry, got %v", matches)
	}
}

func TestWorkspace_Copy_ReturnsWarningWhenTempCleanupRemainsAfterLink(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/source.txt", []byte("source")); err != nil {
		t.Fatalf("WriteFile(source.txt) error: %v", err)
	}

	originalFinalizeWorkspaceCopyTemp := finalizeWorkspaceCopyTemp
	stuckTempRelPath := ""
	finalizeWorkspaceCopyTemp = func(root *os.Root, tmpPath string) error {
		stuckTempRelPath = tmpPath
		tmpAbsPath := filepath.Join(w.Root(), filepath.FromSlash(tmpPath))
		if err := os.Remove(tmpAbsPath); err != nil {
			return err
		}
		if err := os.Mkdir(tmpAbsPath, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmpAbsPath, "child"), []byte("stuck"), 0644); err != nil {
			return err
		}
		return errors.New("remove temp link failed")
	}
	t.Cleanup(func() {
		finalizeWorkspaceCopyTemp = originalFinalizeWorkspaceCopyTemp
	})

	err := w.Copy(ctx, "/source.txt", "/dest.txt")
	if !IsVisibleMutationWarning(err) {
		t.Fatalf("Copy() error = %v, want visible mutation warning", err)
	}
	if !strings.Contains(err.Error(), "failed to finalize copied file") {
		t.Fatalf("expected finalize cleanup warning in error, got %v", err)
	}
	destData, readErr := w.ReadFile(ctx, "/dest.txt")
	if readErr != nil {
		t.Fatalf("ReadFile(dest.txt) error: %v", readErr)
	}
	if string(destData) != "source" {
		t.Fatalf("destination content = %q, want %q", string(destData), "source")
	}
	if stuckTempRelPath == "" {
		t.Fatal("expected finalize hook to capture temp path")
	}
	if _, statErr := os.Stat(filepath.Join(w.Root(), filepath.FromSlash(stuckTempRelPath))); statErr != nil {
		t.Fatalf("expected stuck temp directory to remain for staging cleanup, got %v", statErr)
	}
}

func TestWorkspace_Copy_ReturnsErrNotFoundWhenDestinationParentMissing(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/copy-source.txt", []byte("content")); err != nil {
		t.Fatalf("WriteFile(copy-source.txt) error: %v", err)
	}

	err := w.Copy(ctx, "/copy-source.txt", "/missing-parent/child.txt")
	if err != ErrNotFound {
		t.Fatalf("Copy() error = %v, want ErrNotFound", err)
	}
	if w.Exists(ctx, "/missing-parent/child.txt") {
		t.Fatal("destination should remain absent after rejected copy")
	}
}

func TestWorkspace_Copy_ReturnsErrNotDirWhenSourceParentIsFile(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/parent-file", []byte("content")); err != nil {
		t.Fatalf("WriteFile(parent-file) error: %v", err)
	}

	err := w.Copy(ctx, "/parent-file/child.txt", "/dest.txt")
	if err != ErrNotDir {
		t.Fatalf("Copy() error = %v, want ErrNotDir", err)
	}
}

func TestWorkspace_Copy_ReturnsErrNotDirWhenDestinationParentIsFile(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/source.txt", []byte("content")); err != nil {
		t.Fatalf("WriteFile(source.txt) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/parent-file", []byte("content")); err != nil {
		t.Fatalf("WriteFile(parent-file) error: %v", err)
	}

	err := w.Copy(ctx, "/source.txt", "/parent-file/child.txt")
	if err != ErrNotDir {
		t.Fatalf("Copy() error = %v, want ErrNotDir", err)
	}
}

func TestWorkspace_WriteFileFromReader_StopsWhenContextIsCanceled(t *testing.T) {
	w := setupWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())

	err := w.WriteFileFromReader(ctx, "/stream.txt", &chunkedCancelReader{
		chunks: [][]byte{[]byte("hello "), []byte("world")},
		cancel: cancel,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(w.Root(), "stream.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no destination file after canceled write, got %v", statErr)
	}
}

func TestWorkspace_WriteFileFromReader_RejectsCancellationObservedAtEOFBeforeRename(t *testing.T) {
	w := setupWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	reads := 0
	reader := readerFunc(func(buffer []byte) (int, error) {
		reads++
		if reads == 1 {
			return copy(buffer, "complete payload"), nil
		}
		cancel()
		return 0, io.EOF
	})

	err := w.WriteFileFromReader(ctx, "/cancel-at-eof.txt", reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteFileFromReader() error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(filepath.Join(w.Root(), "cancel-at-eof.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("canceled write published destination: %v", statErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(w.Root(), ".workspace-*.tmp"))
	if globErr != nil {
		t.Fatalf("Glob() error = %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("canceled write left temp files: %v", matches)
	}
}

func TestWorkspace_Walk(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	w.Mkdir(ctx, "/walktest")
	w.WriteFile(ctx, "/walktest/a.txt", []byte("a"))
	w.Mkdir(ctx, "/walktest/sub")
	w.WriteFile(ctx, "/walktest/sub/b.txt", []byte("b"))

	var paths []string
	err := w.Walk(ctx, "/walktest", func(path string, info *FileInfo) error {
		paths = append(paths, path)
		return nil
	})

	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	if len(paths) != 4 {
		t.Errorf("Walk() visited %d paths, want 4: %v", len(paths), paths)
	}
}

func TestWorkspace_Walk_HidesSymlinkEntries(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/walktest"); err != nil {
		t.Fatalf("Mkdir(walktest) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/walktest/visible.txt", []byte("visible")); err != nil {
		t.Fatalf("WriteFile(visible.txt) error: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0644); err != nil {
		t.Fatalf("WriteFile(outside.txt) error: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(w.Root(), "walktest", "linked.txt")); err != nil {
		t.Fatalf("Symlink(linked.txt) error: %v", err)
	}

	var paths []string
	err := w.Walk(ctx, "/walktest", func(path string, info *FileInfo) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	want := "/walktest|/walktest/visible.txt"
	if got := strings.Join(paths, "|"); got != want {
		t.Fatalf("Walk() paths = %v, want %s", paths, want)
	}
}

func TestWorkspace_WalkStrict_RejectsSymlinkEntries(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/walktest"); err != nil {
		t.Fatalf("Mkdir(walktest) error: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o644); err != nil {
		t.Fatalf("WriteFile(outside.txt) error: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(w.Root(), "walktest", "linked.txt")); err != nil {
		t.Fatalf("Symlink(linked.txt) error: %v", err)
	}

	var paths []string
	err := w.WalkStrict(ctx, "/walktest", func(path string, info *FileInfo) error {
		paths = append(paths, path)
		return nil
	})
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("WalkStrict() error = %v, want ErrNotRegular", err)
	}
	if want := "/walktest|/walktest/linked.txt"; strings.Join(paths, "|") != want {
		t.Fatalf("WalkStrict() paths = %v, want %s", paths, want)
	}
}

func TestWorkspace_WalkStrict_RejectsRootSymlinkAfterCallback(t *testing.T) {
	w := setupWorkspace(t)
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o644); err != nil {
		t.Fatalf("WriteFile(outside.txt) error: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(w.Root(), "linked.txt")); err != nil {
		t.Fatalf("Symlink(linked.txt) error: %v", err)
	}

	var paths []string
	err := w.WalkStrict(context.Background(), "/linked.txt", func(path string, _ *FileInfo) error {
		paths = append(paths, path)
		return nil
	})
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("WalkStrict(root symlink) error = %v, want ErrNotRegular", err)
	}
	if got, want := strings.Join(paths, "|"), "/linked.txt"; got != want {
		t.Fatalf("WalkStrict(root symlink) paths = %q, want %q", got, want)
	}
}

func TestWorkspace_WalkRejectsUnsafeEntryNames(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/walktest"); err != nil {
		t.Fatalf("Mkdir(walktest) error: %v", err)
	}
	unsafeName := "nested\\report.txt"
	unsafePath := filepath.Join(w.Root(), "walktest", unsafeName)
	if err := os.WriteFile(unsafePath, []byte("unsafe"), 0644); err != nil {
		t.Skipf("platform does not support unsafe filename %q: %v", unsafeName, err)
	}

	var paths []string
	err := w.Walk(ctx, "/walktest", func(path string, info *FileInfo) error {
		paths = append(paths, path)
		return nil
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Walk() with unsafe entry error = %v, want ErrNotFound", err)
	}

	want := "/walktest"
	if got := strings.Join(paths, "|"); got != want {
		t.Fatalf("Walk() paths = %v, want %s", paths, want)
	}
}

func TestWorkspace_Walk_RejectsRootSymlinkInsertedAfterValidation(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/walktest"); err != nil {
		t.Fatalf("Mkdir(walktest) error: %v", err)
	}
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("outside"), 0644); err != nil {
		t.Fatalf("WriteFile(secret.txt) error: %v", err)
	}

	walkRoot := filepath.Join(w.Root(), "walktest")
	originalAfterValidateWorkspacePaths := afterValidateWorkspacePaths
	afterValidateWorkspacePaths = func() error {
		if err := os.RemoveAll(walkRoot); err != nil {
			return err
		}
		return os.Symlink(outsideDir, walkRoot)
	}
	t.Cleanup(func() {
		afterValidateWorkspacePaths = originalAfterValidateWorkspacePaths
		if info, err := os.Lstat(walkRoot); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(walkRoot); removeErr != nil {
				t.Errorf("Remove(walk root symlink) error: %v", removeErr)
			}
		}
	})

	var paths []string
	err := w.Walk(ctx, "/walktest", func(path string, info *FileInfo) error {
		paths = append(paths, path)
		return nil
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Walk() error = %v, want ErrNotFound", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected no paths for symlink root, got %v", paths)
	}
}

func TestWorkspace_Walk_ReturnsContextCanceledBeforeTraversal(t *testing.T) {
	w := setupWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Walk(ctx, "/", func(path string, info *FileInfo) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestWorkspace_Walk_StopsWhenContextCanceledDuringTraversal(t *testing.T) {
	w := setupWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())

	if err := w.Mkdir(ctx, "/walktest"); err != nil {
		t.Fatalf("Mkdir(walktest) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/walktest/a.txt", []byte("a")); err != nil {
		t.Fatalf("WriteFile(a.txt) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/walktest/b.txt", []byte("b")); err != nil {
		t.Fatalf("WriteFile(b.txt) error: %v", err)
	}

	visited := 0
	err := w.Walk(ctx, "/walktest", func(path string, info *FileInfo) error {
		visited++
		if path == "/walktest" {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if visited != 1 {
		t.Fatalf("expected traversal to stop after root when context is canceled, visited %d entries", visited)
	}
}

func TestWorkspace_Walk_PropagatesTraversalError(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/walktest"); err != nil {
		t.Fatalf("Mkdir(walktest) error: %v", err)
	}
	if err := w.Mkdir(ctx, "/walktest/blocked"); err != nil {
		t.Fatalf("Mkdir(blocked) error: %v", err)
	}

	blockedPath := filepath.Join(w.root, "walktest", "blocked")
	if err := os.Chmod(blockedPath, 0); err != nil {
		t.Fatalf("Chmod(blocked) error: %v", err)
	}
	defer func() {
		_ = os.Chmod(blockedPath, 0o755)
	}()

	err := w.Walk(ctx, "/walktest", func(path string, info *FileInfo) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected Walk() to propagate traversal error")
	}
}

func TestWorkspace_Walk_DoesNotFollowWorkspaceRootSymlinkInsertedAfterValidation(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/walktest"); err != nil {
		t.Fatalf("Mkdir(walktest) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/walktest/original.txt", []byte("original")); err != nil {
		t.Fatalf("WriteFile(original.txt) error: %v", err)
	}

	outsideRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(outsideRoot, "walktest"), 0755); err != nil {
		t.Fatalf("Mkdir(outside walktest) error: %v", err)
	}
	outsidePath := filepath.Join(outsideRoot, "walktest", "secret.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0644); err != nil {
		t.Fatalf("WriteFile(outside secret.txt) error: %v", err)
	}

	backupRoot := w.Root() + "-backup"
	originalAfterValidateWorkspacePaths := afterValidateWorkspacePaths
	afterValidateWorkspacePaths = func() error {
		if err := os.Rename(w.Root(), backupRoot); err != nil {
			return err
		}
		return os.Symlink(outsideRoot, w.Root())
	}
	t.Cleanup(func() {
		afterValidateWorkspacePaths = originalAfterValidateWorkspacePaths
		if info, err := os.Lstat(w.Root()); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(w.Root()); removeErr != nil {
				t.Errorf("Remove(workspace root symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupRoot); err == nil {
			if renameErr := os.Rename(backupRoot, w.Root()); renameErr != nil {
				t.Errorf("Rename(backup root) error: %v", renameErr)
			}
		}
	})

	var paths []string
	err := w.Walk(ctx, "/walktest", func(path string, info *FileInfo) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	foundOriginal := false
	for _, walkedPath := range paths {
		if walkedPath == "/walktest/original.txt" {
			foundOriginal = true
		}
		if walkedPath == "/walktest/secret.txt" {
			t.Fatalf("expected Walk() to stay on original workspace root, got outside path list %v", paths)
		}
	}
	if !foundOriginal {
		t.Fatalf("expected Walk() to visit original workspace file, got %v", paths)
	}

	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("ReadFile(outside secret.txt) error: %v", err)
	}
	if string(data) != "outside" {
		t.Fatalf("expected outside file unchanged, got %q", string(data))
	}
}

func TestWorkspace_OpenFile(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	w.WriteFile(ctx, "/toopen.txt", []byte("file content"))

	f, err := w.OpenFile(ctx, "/toopen.txt")
	if err != nil {
		t.Fatalf("OpenFile() error: %v", err)
	}
	defer f.Close()

	data := make([]byte, 12)
	n, _ := f.Read(data)
	if string(data[:n]) != "file content" {
		t.Errorf("Read content = %q, want 'file content'", data[:n])
	}
}

func TestWorkspace_Exists(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if w.Exists(ctx, "/check.txt") {
		t.Error("Exists() should be false for missing file")
	}

	w.WriteFile(ctx, "/check.txt", []byte("x"))

	if !w.Exists(ctx, "/check.txt") {
		t.Error("Exists() should be true after creation")
	}
}

func TestWorkspace_IsDir(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	w.WriteFile(ctx, "/file.txt", []byte("x"))
	w.Mkdir(ctx, "/dir")

	if w.IsDir(ctx, "/file.txt") {
		t.Error("IsDir() should be false for file")
	}
	if !w.IsDir(ctx, "/dir") {
		t.Error("IsDir() should be true for directory")
	}
}

func TestWorkspace_CleanupStaging(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	// Create workspace staging files plus a user tmp file that must be preserved.
	tmpFile1 := filepath.Join(w.Root(), ".workspace-test1.tmp")
	tmpFile2 := filepath.Join(w.Root(), ".workspace-test2.tmp")
	keepFile := filepath.Join(w.Root(), "keep.tmp")
	os.WriteFile(tmpFile1, []byte("temp1"), 0644)
	os.WriteFile(tmpFile2, []byte("temp22"), 0644)
	os.WriteFile(keepFile, []byte("keep"), 0644)

	files, bytes, err := w.CleanupStaging(ctx)
	if err != nil {
		t.Fatalf("CleanupStaging() error: %v", err)
	}

	if files != 2 {
		t.Errorf("CleanupStaging() files = %d, want 2", files)
	}
	if bytes != 11 {
		t.Errorf("CleanupStaging() bytes = %d, want 11", bytes)
	}
	if _, err := os.Stat(keepFile); err != nil {
		t.Fatalf("expected user tmp file to remain, got %v", err)
	}
}

func TestWorkspace_CleanupStaging_ReturnsContextCanceledBeforeWalk(t *testing.T) {
	w := setupWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	files, bytes, err := w.CleanupStaging(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if files != 0 || bytes != 0 {
		t.Fatalf("expected zero cleanup counts on canceled context, got files=%d bytes=%d", files, bytes)
	}
}

func TestWorkspace_CleanupStaging_PropagatesWalkError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based walk error is unreliable as root")
	}

	w := setupWorkspace(t)
	ctx := context.Background()

	blockedDir := filepath.Join(w.Root(), "blocked")
	if err := os.Mkdir(blockedDir, 0755); err != nil {
		t.Fatalf("Mkdir(blocked) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, ".workspace-stuck.tmp"), []byte("temp"), 0644); err != nil {
		t.Fatalf("WriteFile(.workspace-stuck.tmp) error: %v", err)
	}
	if err := os.Chmod(blockedDir, 0000); err != nil {
		t.Fatalf("Chmod(blocked) error: %v", err)
	}
	defer os.Chmod(blockedDir, 0755)

	_, _, err := w.CleanupStaging(ctx)
	if err == nil {
		t.Fatal("expected CleanupStaging() to return walk error")
	}
}

func TestWorkspace_CleanupStaging_DoesNotFollowWorkspaceRootSymlinkInsertedAfterValidation(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	originalTemp := filepath.Join(w.Root(), ".workspace-original.tmp")
	keepFile := filepath.Join(w.Root(), "keep.tmp")
	if err := os.WriteFile(originalTemp, []byte("inside"), 0644); err != nil {
		t.Fatalf("WriteFile(original staging) error: %v", err)
	}
	if err := os.WriteFile(keepFile, []byte("keep"), 0644); err != nil {
		t.Fatalf("WriteFile(keep.tmp) error: %v", err)
	}

	outsideRoot := t.TempDir()
	outsideTemp := filepath.Join(outsideRoot, ".workspace-outside.tmp")
	if err := os.WriteFile(outsideTemp, []byte("outside"), 0644); err != nil {
		t.Fatalf("WriteFile(outside staging) error: %v", err)
	}

	backupRoot := w.Root() + "-backup"
	originalAfterValidateWorkspacePaths := afterValidateWorkspacePaths
	afterValidateWorkspacePaths = func() error {
		if err := os.Rename(w.Root(), backupRoot); err != nil {
			return err
		}
		return os.Symlink(outsideRoot, w.Root())
	}
	t.Cleanup(func() {
		afterValidateWorkspacePaths = originalAfterValidateWorkspacePaths
		if info, err := os.Lstat(w.Root()); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(w.Root()); removeErr != nil {
				t.Errorf("Remove(workspace root symlink) error: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupRoot); err == nil {
			if renameErr := os.Rename(backupRoot, w.Root()); renameErr != nil {
				t.Errorf("Rename(backup root) error: %v", renameErr)
			}
		}
	})

	files, bytes, err := w.CleanupStaging(ctx)
	if err != nil {
		t.Fatalf("CleanupStaging() error: %v", err)
	}
	if files != 1 {
		t.Fatalf("CleanupStaging() files = %d, want 1", files)
	}
	if bytes != int64(len("inside")) {
		t.Fatalf("CleanupStaging() bytes = %d, want %d", bytes, len("inside"))
	}

	if _, err := os.Stat(filepath.Join(backupRoot, ".workspace-original.tmp")); !os.IsNotExist(err) {
		t.Fatalf("expected original staging file to be removed from anchored workspace, got %v", err)
	}
	keepData, err := os.ReadFile(filepath.Join(backupRoot, "keep.tmp"))
	if err != nil {
		t.Fatalf("ReadFile(keep.tmp) error: %v", err)
	}
	if string(keepData) != "keep" {
		t.Fatalf("expected keep.tmp to remain unchanged, got %q", string(keepData))
	}
	outsideData, err := os.ReadFile(outsideTemp)
	if err != nil {
		t.Fatalf("ReadFile(outside staging) error: %v", err)
	}
	if string(outsideData) != "outside" {
		t.Fatalf("expected outside staging file unchanged, got %q", string(outsideData))
	}
}

func TestWorkspace_FullPath(t *testing.T) {
	w := setupWorkspace(t)

	got := w.FullPath("/test.txt")
	want := filepath.Join(w.Root(), "test.txt")

	if got != want {
		t.Errorf("FullPath() = %s, want %s", got, want)
	}
}

func TestWorkspace_AtomicWrite(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	// Write large file to test atomic write
	data := make([]byte, 1024*1024) // 1MB
	for i := range data {
		data[i] = byte(i % 256)
	}

	err := w.WriteFile(ctx, "/large.bin", data)
	if err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	got, err := w.ReadFile(ctx, "/large.bin")
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}

	if len(got) != len(data) {
		t.Errorf("File size = %d, want %d", len(got), len(data))
	}

	// Verify no .tmp file left
	tmpPath := filepath.Join(w.Root(), "large.bin.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("Temp file should not exist after successful write")
	}
}

func TestWorkspace_OpenFile_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/open-parent", []byte("content")); err != nil {
		t.Fatalf("WriteFile(open-parent) error: %v", err)
	}

	reader, err := w.OpenFile(ctx, "/open-parent/child.txt")
	if err != ErrNotDir {
		t.Fatalf("OpenFile() error = %v, want ErrNotDir", err)
	}
	if reader != nil {
		t.Fatal("expected no reader for parent-not-directory path")
	}
}

func TestWorkspace_ReadFile_ReturnsErrNotDirWhenParentIsFile(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.WriteFile(ctx, "/read-parent", []byte("content")); err != nil {
		t.Fatalf("WriteFile(read-parent) error: %v", err)
	}

	data, err := w.ReadFile(ctx, "/read-parent/child.txt")
	if err != ErrNotDir {
		t.Fatalf("ReadFile() error = %v, want ErrNotDir", err)
	}
	if data != nil {
		t.Fatal("expected no data for parent-not-directory path")
	}
}

func TestWorkspace_ReadFile_RejectsSymlinkPath(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	targetPath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(targetPath, []byte("outside"), 0644); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	linkPath := filepath.Join(w.Root(), "linked.txt")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("Symlink(linked.txt) error: %v", err)
	}

	data, err := w.ReadFile(ctx, "/linked.txt")
	if err != ErrNotFound {
		t.Fatalf("ReadFile() error = %v, want ErrNotFound", err)
	}
	if data != nil {
		t.Fatal("expected no data for symlink-backed path")
	}
}

func TestWorkspace_WriteFile_RejectsSymlinkParent(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	outsideDir := t.TempDir()
	linkPath := filepath.Join(w.Root(), "linked")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	err := w.WriteFile(ctx, "/linked/child.txt", []byte("blocked"))
	if err != ErrNotFound {
		t.Fatalf("WriteFile() error = %v, want ErrNotFound", err)
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "child.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected symlink target file to remain absent, got %v", statErr)
	}
}

func TestWorkspace_WriteFile_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	safeDir := filepath.Join(w.Root(), "safe")
	if err := os.Mkdir(safeDir, 0755); err != nil {
		t.Fatalf("Mkdir(safe) error: %v", err)
	}
	outsideDir := t.TempDir()

	originalAfterValidateWorkspacePaths := afterValidateWorkspacePaths
	afterValidateWorkspacePaths = func() error {
		if err := os.Remove(safeDir); err != nil {
			return err
		}
		return os.Symlink(outsideDir, safeDir)
	}
	t.Cleanup(func() {
		afterValidateWorkspacePaths = originalAfterValidateWorkspacePaths
	})

	err := w.WriteFile(ctx, "/safe/child.txt", []byte("blocked"))
	if err != ErrNotFound {
		t.Fatalf("WriteFile() error = %v, want ErrNotFound", err)
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "child.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected external target file to remain absent, got %v", statErr)
	}
}

func TestWorkspace_WriteFileRejectsParentSymlinkInsertedAfterValidationInsideRoot(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	safeDir := filepath.Join(w.Root(), "safe")
	if err := os.Mkdir(safeDir, 0755); err != nil {
		t.Fatalf("Mkdir(safe) error: %v", err)
	}
	realDir := filepath.Join(w.Root(), "real")
	if err := os.Mkdir(realDir, 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}

	originalAfterValidateWorkspacePaths := afterValidateWorkspacePaths
	afterValidateWorkspacePaths = func() error {
		if err := os.Remove(safeDir); err != nil {
			return err
		}
		return os.Symlink("real", safeDir)
	}
	t.Cleanup(func() {
		afterValidateWorkspacePaths = originalAfterValidateWorkspacePaths
	})

	err := w.WriteFile(ctx, "/safe/child.txt", []byte("blocked"))
	if err != ErrNotFound {
		t.Fatalf("WriteFile() error = %v, want ErrNotFound", err)
	}
	if _, statErr := os.Stat(filepath.Join(realDir, "child.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected symlink target file to remain absent, got %v", statErr)
	}
}

func TestWorkspace_ReadFile_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	safeDir := filepath.Join(w.Root(), "safe")
	if err := os.Mkdir(safeDir, 0755); err != nil {
		t.Fatalf("Mkdir(safe) error: %v", err)
	}
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("outside secret"), 0644); err != nil {
		t.Fatalf("WriteFile(secret.txt) error: %v", err)
	}

	originalAfterValidateWorkspacePaths := afterValidateWorkspacePaths
	afterValidateWorkspacePaths = func() error {
		if err := os.Remove(safeDir); err != nil {
			return err
		}
		return os.Symlink(outsideDir, safeDir)
	}
	t.Cleanup(func() {
		afterValidateWorkspacePaths = originalAfterValidateWorkspacePaths
	})

	data, err := w.ReadFile(ctx, "/safe/secret.txt")
	if err != ErrNotFound {
		t.Fatalf("ReadFile() error = %v, want ErrNotFound", err)
	}
	if data != nil {
		t.Fatal("expected no data for post-validation symlink swap")
	}
}

func TestWorkspace_ReadFileRejectsFinalSymlinkInsertedAfterValidationInsideRoot(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	if err := w.Mkdir(ctx, "/safe"); err != nil {
		t.Fatalf("Mkdir(safe) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/safe/secret.txt", []byte("original")); err != nil {
		t.Fatalf("WriteFile(secret) error: %v", err)
	}
	if err := w.WriteFile(ctx, "/safe/linked.txt", []byte("linked")); err != nil {
		t.Fatalf("WriteFile(linked) error: %v", err)
	}

	secretPath := filepath.Join(w.Root(), "safe", "secret.txt")
	originalAfterValidateWorkspacePaths := afterValidateWorkspacePaths
	afterValidateWorkspacePaths = func() error {
		if err := os.Remove(secretPath); err != nil {
			return err
		}
		return os.Symlink("linked.txt", secretPath)
	}
	t.Cleanup(func() {
		afterValidateWorkspacePaths = originalAfterValidateWorkspacePaths
	})

	data, err := w.ReadFile(ctx, "/safe/secret.txt")
	if err != ErrNotFound {
		t.Fatalf("ReadFile() error = %v, want ErrNotFound", err)
	}
	if data != nil {
		t.Fatal("expected no data for final symlink swap inside root")
	}
}

func TestWorkspace_DeleteDoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	safeDir := filepath.Join(w.Root(), "safe")
	if err := os.Mkdir(safeDir, 0755); err != nil {
		t.Fatalf("Mkdir(safe) error: %v", err)
	}
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "child.txt"), []byte("outside"), 0644); err != nil {
		t.Fatalf("WriteFile(outside child) error: %v", err)
	}

	originalAfterValidateWorkspacePaths := afterValidateWorkspacePaths
	afterValidateWorkspacePaths = func() error {
		if err := os.Remove(safeDir); err != nil {
			return err
		}
		return os.Symlink(outsideDir, safeDir)
	}
	t.Cleanup(func() {
		afterValidateWorkspacePaths = originalAfterValidateWorkspacePaths
	})

	err := w.Delete(ctx, "/safe/child.txt")
	if err == nil {
		t.Fatal("expected Delete() to reject post-validation symlink swap")
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "child.txt")); statErr != nil {
		t.Fatalf("outside child was removed or became inaccessible: %v", statErr)
	}
}

func TestWorkspace_DeleteAllDoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	w := setupWorkspace(t)
	ctx := context.Background()

	safeDir := filepath.Join(w.Root(), "safe")
	if err := os.Mkdir(safeDir, 0755); err != nil {
		t.Fatalf("Mkdir(safe) error: %v", err)
	}
	outsideDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(outsideDir, "child"), 0755); err != nil {
		t.Fatalf("Mkdir(outside child) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "child", "secret.txt"), []byte("outside"), 0644); err != nil {
		t.Fatalf("WriteFile(outside secret) error: %v", err)
	}

	originalAfterValidateWorkspacePaths := afterValidateWorkspacePaths
	afterValidateWorkspacePaths = func() error {
		if err := os.Remove(safeDir); err != nil {
			return err
		}
		return os.Symlink(outsideDir, safeDir)
	}
	t.Cleanup(func() {
		afterValidateWorkspacePaths = originalAfterValidateWorkspacePaths
	})

	err := w.DeleteAll(ctx, "/safe/child")
	if err == nil {
		t.Fatal("expected DeleteAll() to reject post-validation symlink swap")
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "child", "secret.txt")); statErr != nil {
		t.Fatalf("outside child tree was removed or became inaccessible: %v", statErr)
	}
}

func TestCleanupTempPath_ReturnsJoinedErrorWhenCleanupFails(t *testing.T) {
	root := t.TempDir()
	tmpPath := filepath.Join(root, "stuck.tmp")
	if err := os.Mkdir(tmpPath, 0755); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpPath, "child"), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	operationErr := errors.New("write failed")
	err := cleanupTempPath(tmpPath, operationErr)
	if err == nil {
		t.Fatal("expected cleanup error")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected original error in joined error, got %v", err)
	}
	if !strings.Contains(err.Error(), "cleanup temp file") {
		t.Fatalf("expected cleanup context in joined error, got %v", err)
	}
	if !errors.Is(err, operationErr) {
		t.Fatalf("expected errors.Is to match original error, got %v", err)
	}
}
