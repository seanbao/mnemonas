package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/seanbao/mnemonas/internal/rootio"
)

type atomicWriteProbeReader struct {
	reads int
}

func (r *atomicWriteProbeReader) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("request body must not be read")
}

type atomicWriteTrackingReader struct {
	reader *strings.Reader
	bytes  int
}

func (r *atomicWriteTrackingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.bytes += n
	return n, err
}

func TestProbeAtomicWriteRenamesValidatesBothDirectionsAndLeavesNoResidue(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()

	var directions []string
	rename := func(sourceRoot *os.Root, sourceName string, targetRoot *os.Root, targetName string) error {
		switch {
		case sourceRoot == internalRoot && targetRoot == filesRoot:
			directions = append(directions, "internal-to-files")
		case sourceRoot == filesRoot && targetRoot == internalRoot:
			directions = append(directions, "files-to-internal")
		default:
			t.Fatalf("unexpected atomic rename roots for %q -> %q", sourceName, targetName)
		}
		return rootio.RenameLeafBetweenRootsNoReplace(sourceRoot, sourceName, targetRoot, targetName)
	}

	var exchanges []string
	exchange := func(sourceRoot *os.Root, sourceName string, targetRoot *os.Root, targetName string) error {
		switch {
		case sourceRoot == internalRoot && targetRoot == filesRoot:
			exchanges = append(exchanges, "internal-to-files")
		case sourceRoot == filesRoot && targetRoot == internalRoot:
			exchanges = append(exchanges, "files-to-internal")
		default:
			t.Fatalf("unexpected atomic exchange roots for %q <-> %q", sourceName, targetName)
		}
		return rootio.ExchangeLeavesBetweenRoots(sourceRoot, sourceName, targetRoot, targetName)
	}

	if err := probeAtomicWriteRenamesWith(filesRoot, internalRoot, rename, exchange); err != nil {
		t.Fatalf("probeAtomicWriteRenamesWith() error: %v", err)
	}
	if got, want := strings.Join(directions, ","), "internal-to-files,internal-to-files,files-to-internal,files-to-internal,internal-to-files,files-to-internal"; got != want {
		t.Fatalf("atomic rename directions = %q, want %q", got, want)
	}
	if got, want := strings.Join(exchanges, ","), "internal-to-files,files-to-internal"; got != want {
		t.Fatalf("atomic exchange directions = %q, want %q", got, want)
	}
	assertNoAtomicWriteRenameProbeResidue(t, filesRoot, internalRoot)
}

func TestProbeAtomicWriteRenamesReportsCrossMountAndLeavesNoResidue(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()

	err := probeAtomicWriteRenamesWith(
		filesRoot,
		internalRoot,
		func(*os.Root, string, *os.Root, string) error {
			return syscall.EXDEV
		},
		rootio.ExchangeLeavesBetweenRoots,
	)
	if !errors.Is(err, syscall.EXDEV) {
		t.Fatalf("probeAtomicWriteRenamesWith() error = %v, want EXDEV", err)
	}
	if !strings.Contains(err.Error(), "internal-to-files no-replace probe") {
		t.Fatalf("probeAtomicWriteRenamesWith() error = %v, want direction context", err)
	}
	assertNoAtomicWriteRenameProbeResidue(t, filesRoot, internalRoot)
}

func TestProbeAtomicWriteRenamesPreservesUnknownReplacement(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()

	var replacementRel string
	err := probeAtomicWriteRenamesWith(
		filesRoot,
		internalRoot,
		func(sourceRoot *os.Root, sourceName string, _ *os.Root, _ string) error {
			replacementRel = sourceName
			if err := sourceRoot.Remove(sourceName); err != nil {
				return err
			}
			replacement, err := rootio.OpenFileNoFollow(
				sourceRoot,
				sourceName,
				os.O_WRONLY|os.O_CREATE|os.O_EXCL,
				0o600,
			)
			if err != nil {
				return err
			}
			if _, err := replacement.Write([]byte("unknown")); err != nil {
				_ = replacement.Close()
				return err
			}
			if err := replacement.Close(); err != nil {
				return err
			}
			return syscall.EXDEV
		},
		rootio.ExchangeLeavesBetweenRoots,
	)
	if !errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
		t.Fatalf("probeAtomicWriteRenamesWith() error = %v, want ownership change", err)
	}
	data, readErr := internalRoot.ReadFile(replacementRel)
	if readErr != nil {
		t.Fatalf("ReadFile(preserved replacement) error: %v", readErr)
	}
	if got, want := string(data), "unknown"; got != want {
		t.Fatalf("preserved replacement = %q, want %q", got, want)
	}
}

func TestProbeAtomicWriteRenamesPreservesSameInodeRewriteAfterRenameFailure(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()

	var rewrittenRel string
	var beforeRewrite os.FileInfo
	err := probeAtomicWriteRenamesWith(
		filesRoot,
		internalRoot,
		func(sourceRoot *os.Root, sourceName string, _ *os.Root, _ string) error {
			rewrittenRel = sourceName
			var err error
			beforeRewrite, err = sourceRoot.Lstat(sourceName)
			if err != nil {
				return err
			}
			rewritten, err := rootio.OpenFileNoFollow(sourceRoot, sourceName, os.O_WRONLY|os.O_TRUNC, 0)
			if err != nil {
				return err
			}
			if _, err := rewritten.Write([]byte(strings.Repeat("x", atomicWriteRenameProbeNonceSize))); err != nil {
				_ = rewritten.Close()
				return err
			}
			if err := rewritten.Sync(); err != nil {
				_ = rewritten.Close()
				return err
			}
			if err := rewritten.Close(); err != nil {
				return err
			}
			return syscall.EXDEV
		},
		rootio.ExchangeLeavesBetweenRoots,
	)
	if !errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
		t.Fatalf("probeAtomicWriteRenamesWith() error = %v, want ownership change", err)
	}
	afterRewrite, statErr := internalRoot.Lstat(rewrittenRel)
	if statErr != nil {
		t.Fatalf("Lstat(preserved same-inode rewrite) error: %v", statErr)
	}
	if !os.SameFile(beforeRewrite, afterRewrite) {
		t.Fatal("same-inode rewrite was replaced instead of preserved")
	}
	data, readErr := internalRoot.ReadFile(rewrittenRel)
	if readErr != nil {
		t.Fatalf("ReadFile(preserved same-inode rewrite) error: %v", readErr)
	}
	if got, want := string(data), strings.Repeat("x", atomicWriteRenameProbeNonceSize); got != want {
		t.Fatalf("preserved same-inode rewrite = %q, want %q", got, want)
	}
}

func TestVerifyAtomicWriteRenameProbeIsolatedFileRejectsSameInodeContentDrift(t *testing.T) {
	_, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()

	probeFile, sourceRel, err := createStorageTempFile(
		internalRoot,
		writeStagingDir,
		atomicWriteRenameProbePrefix,
	)
	if err != nil {
		t.Fatalf("createStorageTempFile() error: %v", err)
	}
	owner := &atomicWriteRenameProbeFile{file: probeFile}
	defer owner.file.Close()
	slot := &atomicWriteRenameProbeSlot{
		root: internalRoot,
		rel:  sourceRel,
	}
	if err := initializeAtomicWriteRenameProbeFile(owner); err != nil {
		t.Fatalf("initializeAtomicWriteRenameProbeFile() error: %v", err)
	}
	if err := rebaselineAtomicWriteRenameProbeFile(slot, owner); err != nil {
		t.Fatalf("rebaselineAtomicWriteRenameProbeFile(initial) error: %v", err)
	}

	isolatedRel := filepath.Join(writeStagingDir, atomicWriteRenameProbePrefix+"isolated-test.tmp")
	if err := rootio.RenameLeafNoReplace(internalRoot, sourceRel, isolatedRel); err != nil {
		t.Fatalf("RenameLeafNoReplace(isolate) error: %v", err)
	}
	isolated, err := rootio.OpenRegularFileNoFollow(internalRoot, isolatedRel)
	if err != nil {
		t.Fatalf("OpenRegularFileNoFollow(isolated) error: %v", err)
	}
	defer isolated.Close()
	isolatedInfo, err := isolated.Stat()
	if err != nil {
		t.Fatalf("Stat(isolated) error: %v", err)
	}
	if err := verifyAtomicWriteRenameProbeIsolatedFile(owner, isolated, isolatedInfo); err != nil {
		t.Fatalf("verifyAtomicWriteRenameProbeIsolatedFile(stable) error: %v", err)
	}

	if _, err := owner.file.WriteAt([]byte(strings.Repeat("x", atomicWriteRenameProbeNonceSize)), 0); err != nil {
		t.Fatalf("WriteAt(same inode) error: %v", err)
	}
	if err := owner.file.Sync(); err != nil {
		t.Fatalf("Sync(same inode) error: %v", err)
	}
	isolatedPath := filepath.Join(internalRoot.Name(), isolatedRel)
	if err := os.Chtimes(isolatedPath, owner.evidence.modTime, owner.evidence.modTime); err != nil {
		t.Fatalf("Chtimes(same inode) error: %v", err)
	}
	driftedInfo, err := isolated.Stat()
	if err != nil {
		t.Fatalf("Stat(drifted isolated) error: %v", err)
	}
	if err := verifyAtomicWriteRenameProbeIsolatedFile(owner, isolated, driftedInfo); !errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
		t.Fatalf("verifyAtomicWriteRenameProbeIsolatedFile(drifted) error = %v, want ownership change", err)
	}
}

func TestFileSystemWriteFileRejectsNestedMountBeforeReadingBody(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	if err := fs.Mkdir(t.Context(), "/nested"); err != nil {
		t.Fatalf("Mkdir(nested) error: %v", err)
	}
	if err := os.WriteFile(fs.workspace.FullPath("/nested/existing.txt"), []byte("original"), 0o600); err != nil {
		t.Fatalf("WriteFile(existing setup) error: %v", err)
	}

	nestedMount := fs.workspace.FullPath("/nested")
	fs.readDeleteMountPoints = func() ([]string, error) {
		return []string{nestedMount}, nil
	}
	reader := &atomicWriteProbeReader{}
	err := fs.WriteFile(t.Context(), "/nested/existing.txt", reader)
	if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
		t.Fatalf("WriteFile(nested mount) error = %v, want ErrWriteAtomicRenameUnsupported", err)
	}
	if reader.reads != 0 {
		t.Fatalf("request body reads = %d, want 0", reader.reads)
	}
	data, readErr := os.ReadFile(fs.workspace.FullPath("/nested/existing.txt"))
	if readErr != nil {
		t.Fatalf("ReadFile(existing after rejection) error: %v", readErr)
	}
	if got, want := string(data), "original"; got != want {
		t.Fatalf("existing content after rejection = %q, want %q", got, want)
	}
	assertWriteStagingEmpty(t, fs)
}

func TestFileSystemWriteFileRejectsUnverifiableMountTableBeforeReadingBody(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	mountErr := errors.New("mount table unavailable")
	fs.readDeleteMountPoints = func() ([]string, error) {
		return nil, mountErr
	}
	reader := &atomicWriteProbeReader{}

	err := fs.WriteFile(t.Context(), "/new.txt", reader)
	if !errors.Is(err, ErrWriteAtomicRenameUnsupported) || !errors.Is(err, mountErr) {
		t.Fatalf("WriteFile(unverifiable mount table) error = %v, want layout and mount errors", err)
	}
	if reader.reads != 0 {
		t.Fatalf("request body reads = %d, want 0", reader.reads)
	}
	if _, statErr := os.Lstat(fs.workspace.FullPath("/new.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("new target Lstat error = %v, want not exist", statErr)
	}
	assertWriteStagingEmpty(t, fs)
}

func TestFileSystemWriteFileRechecksMountAfterStagingBeforePublish(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	if err := fs.Mkdir(t.Context(), "/nested"); err != nil {
		t.Fatalf("Mkdir(nested) error: %v", err)
	}
	targetPath := fs.workspace.FullPath("/nested/existing.txt")
	if err := os.WriteFile(targetPath, []byte("original"), 0o600); err != nil {
		t.Fatalf("WriteFile(existing setup) error: %v", err)
	}

	nestedMount := fs.workspace.FullPath("/nested")
	mountReads := 0
	fs.readDeleteMountPoints = func() ([]string, error) {
		mountReads++
		if mountReads == 1 {
			return []string{fs.workspace.Root()}, nil
		}
		return []string{fs.workspace.Root(), nestedMount}, nil
	}
	reader := &atomicWriteTrackingReader{reader: strings.NewReader("replacement")}

	err := fs.WriteFile(t.Context(), "/nested/existing.txt", reader)
	if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
		t.Fatalf("WriteFile(mount changed after staging) error = %v, want ErrWriteAtomicRenameUnsupported", err)
	}
	if reader.bytes != len("replacement") {
		t.Fatalf("request body bytes read = %d, want %d", reader.bytes, len("replacement"))
	}
	if mountReads < 2 {
		t.Fatalf("mount table reads = %d, want at least 2", mountReads)
	}
	data, readErr := os.ReadFile(targetPath)
	if readErr != nil {
		t.Fatalf("ReadFile(existing after rejection) error: %v", readErr)
	}
	if got, want := string(data), "original"; got != want {
		t.Fatalf("existing content after rejection = %q, want %q", got, want)
	}
	assertWriteStagingEmpty(t, fs)
}

func setupAtomicWriteRenameProbeRoots(t *testing.T) (*os.Root, *os.Root, func()) {
	t.Helper()

	base := t.TempDir()
	filesPath := filepath.Join(base, "files")
	internalPath := filepath.Join(base, ".mnemonas")
	if err := os.Mkdir(filesPath, 0o750); err != nil {
		t.Fatalf("Mkdir(files) error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(internalPath, writeStagingDir), 0o700); err != nil {
		t.Fatalf("MkdirAll(write staging) error: %v", err)
	}
	filesRoot, err := os.OpenRoot(filesPath)
	if err != nil {
		t.Fatalf("OpenRoot(files) error: %v", err)
	}
	internalRoot, err := os.OpenRoot(internalPath)
	if err != nil {
		_ = filesRoot.Close()
		t.Fatalf("OpenRoot(internal) error: %v", err)
	}
	return filesRoot, internalRoot, func() {
		if err := filesRoot.Close(); err != nil {
			t.Errorf("Close(files root) error: %v", err)
		}
		if err := internalRoot.Close(); err != nil {
			t.Errorf("Close(internal root) error: %v", err)
		}
	}
}

func assertNoAtomicWriteRenameProbeResidue(t *testing.T, filesRoot, internalRoot *os.Root) {
	t.Helper()
	for label, entries := range map[string][]os.DirEntry{
		"files root":             mustReadRootDir(t, filesRoot, "."),
		"internal write staging": mustReadRootDir(t, internalRoot, writeStagingDir),
	} {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), atomicWriteRenameProbePrefix) {
				t.Fatalf("%s retains atomic write rename probe %q", label, entry.Name())
			}
		}
	}
}

func mustReadRootDir(t *testing.T, root *os.Root, name string) []os.DirEntry {
	t.Helper()
	dir, err := rootio.OpenDirNoFollow(root, name)
	if err != nil {
		t.Fatalf("OpenDirNoFollow(%s) error: %v", name, err)
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadDir(%s) error: %v", name, err)
	}
	return entries
}

func assertWriteStagingEmpty(t *testing.T, fs *FileSystem) {
	t.Helper()
	entries := mustReadRootDir(t, fs.internalRootHandle, writeStagingDir)
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("write staging entries = %v, want empty", names)
	}
}
