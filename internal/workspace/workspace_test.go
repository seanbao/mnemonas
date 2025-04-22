package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
}

func TestWorkspace_Copy_ReturnsDirectorySyncErrorAfterRename(t *testing.T) {
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

	data, readErr := os.ReadFile(filepath.Join(w.Root(), "copied.txt"))
	if readErr != nil {
		t.Fatalf("expected copied file to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "copy content" {
		t.Fatalf("expected copied content to be preserved, got %q", string(data))
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
	return w
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
	readDirEntryInfo = func(entry os.DirEntry) (os.FileInfo, error) {
		if entry.Name() == "a.txt" {
			cancel()
		}
		return originalReadDirEntryInfo(entry)
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
	readDirEntryInfo = func(entry os.DirEntry) (os.FileInfo, error) {
		if entry.Name() == "broken.txt" {
			return nil, errors.New("stat failed")
		}
		return originalReadDirEntryInfo(entry)
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

	// Create some .tmp files
	tmpFile1 := filepath.Join(w.Root(), "test1.tmp")
	tmpFile2 := filepath.Join(w.Root(), "test2.tmp")
	os.WriteFile(tmpFile1, []byte("temp1"), 0644)
	os.WriteFile(tmpFile2, []byte("temp22"), 0644)

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
	if err := os.WriteFile(filepath.Join(blockedDir, "stuck.tmp"), []byte("temp"), 0644); err != nil {
		t.Fatalf("WriteFile(stuck.tmp) error: %v", err)
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
