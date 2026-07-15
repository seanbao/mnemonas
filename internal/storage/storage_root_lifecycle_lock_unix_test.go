//go:build linux || darwin

package storage

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/workspace"
)

type storageRootLifecycleLockTestRoot struct {
	label string
	path  string
	root  *os.Root
}

func TestEnsureStorageRootRelativeDirectoryRequiresExactPrivateMode(t *testing.T) {
	rootPath := t.TempDir()
	stagingPath := filepath.Join(rootPath, writeStagingDir)
	if err := os.Mkdir(stagingPath, 0o755); err != nil {
		t.Fatalf("Mkdir(write staging) error: %v", err)
	}
	if err := os.Chmod(stagingPath, 0o755); err != nil {
		t.Fatalf("Chmod(write staging) error: %v", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := ensureStorageRootRelativeDirectory(root, writeStagingDir, 0o700); !errors.Is(err, errStorageRootLockChanged) {
		t.Fatalf("ensureStorageRootRelativeDirectory(0755) error = %v, want errStorageRootLockChanged", err)
	}
	if err := os.Chmod(stagingPath, 0o700); err != nil {
		t.Fatalf("Chmod(write staging, 0700) error: %v", err)
	}
	if err := ensureStorageRootRelativeDirectory(root, writeStagingDir, 0o700); err != nil {
		t.Fatalf("ensureStorageRootRelativeDirectory(0700) error: %v", err)
	}
}

func TestStorageRootLifecycleLockSameProcessAndCloseReacquire(t *testing.T) {
	roots := openStorageRootLifecycleLockTestRoots(t, t.TempDir(), "first")
	specs := storageRootLifecycleLockTestSpecs(roots)

	first, err := acquireStorageRootLifecycleLock(specs...)
	if err != nil {
		t.Fatalf("first acquireStorageRootLifecycleLock() error: %v", err)
	}
	second, err := acquireStorageRootLifecycleLock(specs...)
	if second != nil || !errors.Is(err, ErrStorageRootLockHeld) {
		t.Fatalf("second acquire result lock=%#v error=%v, want ErrStorageRootLockHeld", second, err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("repeated first Close() error: %v", err)
	}
	reacquired, err := acquireStorageRootLifecycleLock(specs...)
	if err != nil {
		t.Fatalf("reacquire after Close error: %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatalf("reacquired Close() error: %v", err)
	}
}

func TestStorageRootLifecycleLockRejectsEverySharedRoot(t *testing.T) {
	base := t.TempDir()
	ownerRoots := openStorageRootLifecycleLockTestRoots(t, base, "owner")
	owner, err := acquireStorageRootLifecycleLock(storageRootLifecycleLockTestSpecs(ownerRoots)...)
	if err != nil {
		t.Fatalf("owner acquireStorageRootLifecycleLock() error: %v", err)
	}
	t.Cleanup(func() { _ = owner.Close() })

	tests := []struct {
		name       string
		sharedRole int
	}{
		{name: "files root", sharedRole: 0},
		{name: "internal root", sharedRole: 1},
		{name: "trash root", sharedRole: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contenderRoots := openStorageRootLifecycleLockTestRoots(t, base, "contender-"+strings.ReplaceAll(test.name, " ", "-"))
			_ = contenderRoots[test.sharedRole].root.Close()
			contenderRoots[test.sharedRole].path = ownerRoots[test.sharedRole].path
			var openErr error
			contenderRoots[test.sharedRole].root, openErr = os.OpenRoot(ownerRoots[test.sharedRole].path)
			if openErr != nil {
				t.Fatalf("OpenRoot(shared %s) error: %v", test.name, openErr)
			}

			lock, lockErr := acquireStorageRootLifecycleLock(storageRootLifecycleLockTestSpecs(contenderRoots)...)
			if lock != nil || !errors.Is(lockErr, ErrStorageRootLockHeld) {
				t.Fatalf("shared %s acquire result lock=%#v error=%v, want ErrStorageRootLockHeld", test.name, lock, lockErr)
			}
		})
	}

	independentRoots := openStorageRootLifecycleLockTestRoots(t, base, "independent")
	independent, err := acquireStorageRootLifecycleLock(storageRootLifecycleLockTestSpecs(independentRoots)...)
	if err != nil {
		t.Fatalf("independent acquireStorageRootLifecycleLock() error: %v", err)
	}
	if err := independent.Close(); err != nil {
		t.Fatalf("independent Close() error: %v", err)
	}
}

func TestStorageRootLifecycleLockDeduplicatesAliasesWithoutCreatingFiles(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatalf("Mkdir(shared) error: %v", err)
	}
	paths := []string{rootPath, filepath.Join(rootPath, "."), filepath.Clean(rootPath)}
	specs := make([]storageRootLifecycleLockSpec, 0, len(paths))
	for index, candidatePath := range paths {
		root, err := os.OpenRoot(candidatePath)
		if err != nil {
			t.Fatalf("OpenRoot(alias %d) error: %v", index, err)
		}
		t.Cleanup(func() { _ = root.Close() })
		specs = append(specs, storageRootLifecycleLockSpec{
			label: fmt.Sprintf("alias-%d", index),
			path:  candidatePath,
			root:  root,
		})
	}

	lock, err := acquireStorageRootLifecycleLock(specs...)
	if err != nil {
		t.Fatalf("acquireStorageRootLifecycleLock(aliases) error: %v", err)
	}
	if len(lock.entries) != 1 {
		t.Fatalf("deduplicated lock entry count = %d, want 1", len(lock.entries))
	}
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		t.Fatalf("ReadDir(shared) error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory lock created visible entries: %v", entries)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

func TestStorageRootLifecycleLockPartialOSFailureRollsBack(t *testing.T) {
	base := t.TempDir()
	var firstRoot, blockedRoot storageRootLifecycleLockTestRoot
	for attempt := 0; attempt < 32; attempt++ {
		candidateA := openStorageRootLifecycleLockTestRoot(t, base, fmt.Sprintf("candidate-a-%d", attempt))
		candidateB := openStorageRootLifecycleLockTestRoot(t, base, fmt.Sprintf("candidate-b-%d", attempt))
		identityA := storageRootLifecycleLockTestIdentity(t, candidateA)
		identityB := storageRootLifecycleLockTestIdentity(t, candidateB)
		if identityA < identityB {
			firstRoot, blockedRoot = candidateA, candidateB
			break
		}
		if identityB < identityA {
			firstRoot, blockedRoot = candidateB, candidateA
			break
		}
	}
	if firstRoot.root == nil || blockedRoot.root == nil {
		t.Fatal("failed to allocate storage roots with distinct ordered identities")
	}

	blockedHandle, err := os.Open(blockedRoot.path)
	if err != nil {
		t.Fatalf("Open(blocked root) error: %v", err)
	}
	t.Cleanup(func() { _ = blockedHandle.Close() })
	if err := unix.Flock(int(blockedHandle.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("Flock(blocked root) error: %v", err)
	}
	t.Cleanup(func() { _ = unix.Flock(int(blockedHandle.Fd()), unix.LOCK_UN) })

	lock, err := acquireStorageRootLifecycleLock(
		storageRootLifecycleLockSpec{label: firstRoot.label, path: firstRoot.path, root: firstRoot.root},
		storageRootLifecycleLockSpec{label: blockedRoot.label, path: blockedRoot.path, root: blockedRoot.root},
	)
	if lock != nil || !errors.Is(err, ErrStorageRootLockHeld) {
		t.Fatalf("partial acquire result lock=%#v error=%v, want ErrStorageRootLockHeld", lock, err)
	}

	reacquired, err := acquireStorageRootLifecycleLock(storageRootLifecycleLockSpec{
		label: firstRoot.label,
		path:  firstRoot.path,
		root:  firstRoot.root,
	})
	if err != nil {
		t.Fatalf("first root remained locked after partial failure: %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatalf("reacquired Close() error: %v", err)
	}
}

func TestStorageRootLifecycleLockRejectsPathReplacementBeforeAndAfterLock(t *testing.T) {
	for _, stage := range []string{"validated", "locked"} {
		t.Run(stage, func(t *testing.T) {
			parent := t.TempDir()
			rootPath := filepath.Join(parent, "root")
			if err := os.Mkdir(rootPath, 0o700); err != nil {
				t.Fatalf("Mkdir(root) error: %v", err)
			}
			root, err := os.OpenRoot(rootPath)
			if err != nil {
				t.Fatalf("OpenRoot(root) error: %v", err)
			}
			t.Cleanup(func() { _ = root.Close() })

			movedPath := filepath.Join(parent, "root-held")
			storageRootLifecycleLockTestHook = func(actualStage string) error {
				if actualStage != stage {
					return nil
				}
				if err := os.Rename(rootPath, movedPath); err != nil {
					return err
				}
				return os.Mkdir(rootPath, 0o700)
			}
			lock, lockErr := acquireStorageRootLifecycleLock(storageRootLifecycleLockSpec{
				label: "files",
				path:  rootPath,
				root:  root,
			})
			storageRootLifecycleLockTestHook = nil
			if lock != nil || !errors.Is(lockErr, errStorageRootLockChanged) {
				t.Fatalf("path replacement acquire result lock=%#v error=%v, want identity-changed error", lock, lockErr)
			}

			if err := os.Remove(rootPath); err != nil {
				t.Fatalf("Remove(replacement root) error: %v", err)
			}
			if err := os.Rename(movedPath, rootPath); err != nil {
				t.Fatalf("Rename(held root back) error: %v", err)
			}
			reacquired, err := acquireStorageRootLifecycleLock(storageRootLifecycleLockSpec{
				label: "files",
				path:  rootPath,
				root:  root,
			})
			if err != nil {
				t.Fatalf("reacquire after rejected replacement error: %v", err)
			}
			if err := reacquired.Close(); err != nil {
				t.Fatalf("reacquired Close() error: %v", err)
			}
		})
	}
}

func TestStorageRootLifecycleLockRejectsNominalSymlink(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	linkedRoot := filepath.Join(parent, "linked")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}
	root, err := os.OpenRoot(linkedRoot)
	if err != nil {
		t.Fatalf("OpenRoot(linked) error: %v", err)
	}
	defer root.Close()

	lock, err := acquireStorageRootLifecycleLock(storageRootLifecycleLockSpec{
		label: "files",
		path:  linkedRoot,
		root:  root,
	})
	if lock != nil || !errors.Is(err, errStoragePathSymlink) {
		t.Fatalf("symlink acquire result lock=%#v error=%v, want storage symlink error", lock, err)
	}
}

func TestFileSystemRootLifecycleLockPrecedesInitializationAndCloseIsIdempotent(t *testing.T) {
	base := t.TempDir()
	ownerCfg := storageRootLifecycleLockTestConfig(base, "owner")
	owner, err := New(ownerCfg)
	if err != nil {
		t.Fatalf("New(owner) error: %v", err)
	}

	contenderCfg := storageRootLifecycleLockTestConfig(base, "contender")
	contenderCfg.FilesRoot = ownerCfg.FilesRoot
	contender, err := New(contenderCfg)
	if contender != nil || !errors.Is(err, ErrStorageRootLockHeld) {
		t.Fatalf("New(contender) result fs=%#v error=%v, want ErrStorageRootLockHeld", contender, err)
	}
	if _, statErr := os.Stat(filepath.Join(contenderCfg.InternalRoot, "index.db")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("contender initialized versionstore before root lock, stat error = %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(contenderCfg.InternalRoot, writeStagingDir)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("contender initialized write staging before root lock, stat error = %v", statErr)
	}

	if err := owner.Close(); err != nil {
		t.Fatalf("owner Close() error: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("owner repeated Close() error: %v", err)
	}
	if _, err := owner.Stat(t.Context(), "/"); !errors.Is(err, ErrFileSystemClosed) {
		t.Fatalf("Stat() after Close error = %v, want ErrFileSystemClosed", err)
	}
	if err := owner.Mkdir(t.Context(), "/after-close"); !errors.Is(err, ErrFileSystemClosed) {
		t.Fatalf("Mkdir() after Close error = %v, want ErrFileSystemClosed", err)
	}
	if _, _, err := owner.AcquireGCLock(t.Context()); !errors.Is(err, ErrFileSystemClosed) {
		t.Fatalf("AcquireGCLock() after Close error = %v, want ErrFileSystemClosed", err)
	}
	if _, err := owner.RecoverTrashTransfers(t.Context()); !errors.Is(err, ErrFileSystemClosed) {
		t.Fatalf("RecoverTrashTransfers() after Close error = %v, want ErrFileSystemClosed", err)
	}
	if _, err := owner.RecoverTrashDeletions(t.Context()); !errors.Is(err, ErrFileSystemClosed) {
		t.Fatalf("RecoverTrashDeletions() after Close error = %v, want ErrFileSystemClosed", err)
	}
	contender, err = New(contenderCfg)
	if err != nil {
		t.Fatalf("New(contender after owner Close) error: %v", err)
	}
	if err := contender.Close(); err != nil {
		t.Fatalf("contender Close() error: %v", err)
	}
}

func TestFileSystemRootLifecycleLockNewFailureReleasesRoots(t *testing.T) {
	cfg := storageRootLifecycleLockTestConfig(t.TempDir(), "failure")
	injectedErr := errors.New("injected post-lock initialization failure")
	storageRootLifecycleLockTestHook = func(stage string) error {
		if stage == "new-acquired" {
			return injectedErr
		}
		return nil
	}
	fs, err := New(cfg)
	storageRootLifecycleLockTestHook = nil
	if fs != nil || !errors.Is(err, injectedErr) {
		t.Fatalf("New(injected failure) result fs=%#v error=%v, want injected error", fs, err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.InternalRoot, "index.db")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed New initialized versionstore, stat error = %v", statErr)
	}

	restarted, err := New(cfg)
	if err != nil {
		t.Fatalf("New(after injected failure) error: %v", err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("restarted Close() error: %v", err)
	}
}

func TestFileSystemRootLifecycleLockRejectsWorkspacePathReplacementBeforeLock(t *testing.T) {
	base := t.TempDir()
	cfg := storageRootLifecycleLockTestConfig(base, "workspace-replacement")
	movedFilesRoot := cfg.FilesRoot + "-held"
	storageRootLifecycleLockTestHook = func(stage string) error {
		if stage != "new-workspace-opened" {
			return nil
		}
		if err := os.Rename(cfg.FilesRoot, movedFilesRoot); err != nil {
			return err
		}
		return os.Mkdir(cfg.FilesRoot, 0o755)
	}
	t.Cleanup(func() { storageRootLifecycleLockTestHook = nil })

	fs, err := New(cfg)
	storageRootLifecycleLockTestHook = nil
	if fs != nil || !errors.Is(err, errStorageRootLockChanged) {
		t.Fatalf("New(workspace replacement) result fs=%#v error=%v, want identity-changed error", fs, err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.InternalRoot, "index.db")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected workspace replacement initialized versionstore, stat error = %v", statErr)
	}
	if err := os.Remove(cfg.FilesRoot); err != nil {
		t.Fatalf("Remove(replacement files root) error: %v", err)
	}
	if err := os.Rename(movedFilesRoot, cfg.FilesRoot); err != nil {
		t.Fatalf("Rename(held files root back) error: %v", err)
	}

	restarted, err := New(cfg)
	if err != nil {
		t.Fatalf("New(after workspace replacement rejection) error: %v", err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("restarted Close() error: %v", err)
	}
}

func TestFileSystemRootLifecycleLockRejectsInternalRootReplacementBeforeVersionStore(t *testing.T) {
	base := t.TempDir()
	cfg := storageRootLifecycleLockTestConfig(base, "internal-replacement")
	movedInternalRoot := cfg.InternalRoot + "-held"
	storageRootLifecycleLockTestHook = func(stage string) error {
		if stage != "new-before-versionstore" {
			return nil
		}
		if err := os.Rename(cfg.InternalRoot, movedInternalRoot); err != nil {
			return err
		}
		return os.Mkdir(cfg.InternalRoot, 0o700)
	}
	t.Cleanup(func() { storageRootLifecycleLockTestHook = nil })

	fs, err := New(cfg)
	storageRootLifecycleLockTestHook = nil
	if fs != nil || !errors.Is(err, errStorageRootLockChanged) {
		t.Fatalf("New(internal replacement) result fs=%#v error=%v, want identity-changed error", fs, err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.InternalRoot, "index.db")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("replacement internal root received index.db, stat error = %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.InternalRoot, writeStagingDir)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("replacement internal root received write staging, stat error = %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(movedInternalRoot, "index.db")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected initialization created index.db, stat error = %v", statErr)
	}

	if err := os.Remove(cfg.InternalRoot); err != nil {
		t.Fatalf("Remove(replacement internal root) error: %v", err)
	}
	if err := os.Rename(movedInternalRoot, cfg.InternalRoot); err != nil {
		t.Fatalf("Rename(held internal root back) error: %v", err)
	}
	restarted, err := New(cfg)
	if err != nil {
		t.Fatalf("New(after internal replacement rejection) error: %v", err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("restarted Close() error: %v", err)
	}
}

func TestFileSystemRootLifecycleLockRejectsRuntimeFilesRootReplacement(t *testing.T) {
	cfg := storageRootLifecycleLockTestConfig(t.TempDir(), "runtime-files-replacement")
	fs, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	movedFilesRoot := cfg.FilesRoot + "-held"
	if err := os.Rename(cfg.FilesRoot, movedFilesRoot); err != nil {
		t.Fatalf("Rename(files root) error: %v", err)
	}
	if err := os.Mkdir(cfg.FilesRoot, 0o755); err != nil {
		t.Fatalf("Mkdir(replacement files root) error: %v", err)
	}
	err = fs.WriteFile(t.Context(), "/must-not-publish.bin", strings.NewReader("content"))
	if !errors.Is(err, errStorageRootLockChanged) {
		t.Fatalf("WriteFile() error = %v, want storage root identity change", err)
	}
	for _, candidate := range []string{
		filepath.Join(cfg.FilesRoot, "must-not-publish.bin"),
		filepath.Join(movedFilesRoot, "must-not-publish.bin"),
	} {
		if _, statErr := os.Stat(candidate); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("rejected write published %s, stat error = %v", candidate, statErr)
		}
	}
}

func TestFileSystemCloseWaitsForMutationLeaseBeforeReleasingRootLock(t *testing.T) {
	cfg := storageRootLifecycleLockTestConfig(t.TempDir(), "close-lease")
	fs, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	lease, err := fs.AcquireMutationLease(t.Context())
	if err != nil {
		t.Fatalf("AcquireMutationLease() error: %v", err)
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- fs.Close() }()
	assertStorageRootLifecycleCloseBlocked(t, closeDone)
	if _, err := lease.Stat(t.Context(), "/"); err != nil {
		t.Fatalf("lease.Stat() while Close waits error: %v", err)
	}
	assertStorageRootLifecycleContenderHeld(t, cfg)

	lease.Release()
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	assertStorageRootLifecycleReacquired(t, cfg)
}

func TestFileSystemCloseWaitsForStatBeforeReleasingRootLock(t *testing.T) {
	cfg := storageRootLifecycleLockTestConfig(t.TempDir(), "close-stat")
	fs, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if err := fs.WriteFile(t.Context(), "/slow-stat.bin", strings.NewReader("content")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	fs.hashStatWorkspaceFile = func(context.Context, string) (string, error) {
		close(started)
		<-release
		return "hash", nil
	}
	statDone := make(chan error, 1)
	go func() {
		_, statErr := fs.Stat(t.Context(), "/slow-stat.bin")
		statDone <- statErr
	}()
	<-started

	closeDone := make(chan error, 1)
	go func() { closeDone <- fs.Close() }()
	assertStorageRootLifecycleCloseBlocked(t, closeDone)
	assertStorageRootLifecycleContenderHeld(t, cfg)
	close(release)
	if err := <-statDone; err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	assertStorageRootLifecycleReacquired(t, cfg)
}

func TestFileSystemCloseWaitsForStreamedWriteBeforeReleasingRootLock(t *testing.T) {
	cfg := storageRootLifecycleLockTestConfig(t.TempDir(), "close-write")
	fs, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- fs.WriteFile(t.Context(), "/slow-write.bin", &blockingOnceReader{
			started: started,
			release: release,
			data:    []byte("content"),
		})
	}()
	<-started

	closeDone := make(chan error, 1)
	go func() { closeDone <- fs.Close() }()
	assertStorageRootLifecycleCloseBlocked(t, closeDone)
	assertStorageRootLifecycleContenderHeld(t, cfg)
	close(release)
	if err := <-writeDone; err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	assertStorageRootLifecycleReacquired(t, cfg)
}

func TestFileSystemCloseWaitsForSearchBeforeReleasingRootLock(t *testing.T) {
	cfg := storageRootLifecycleLockTestConfig(t.TempDir(), "close-search")
	fs, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if err := fs.WriteFile(t.Context(), "/search-target.bin", strings.NewReader("content")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	searchDone := make(chan error, 1)
	go func() {
		_, searchErr := fs.SearchFiltered(t.Context(), "search-target", 10, func(*SearchResult) (bool, error) {
			close(started)
			<-release
			return true, nil
		})
		searchDone <- searchErr
	}()
	<-started

	closeDone := make(chan error, 1)
	go func() { closeDone <- fs.Close() }()
	assertStorageRootLifecycleCloseBlocked(t, closeDone)
	assertStorageRootLifecycleContenderHeld(t, cfg)
	close(release)
	if err := <-searchDone; err != nil {
		t.Fatalf("SearchFiltered() error: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	assertStorageRootLifecycleReacquired(t, cfg)
}

func TestFileSystemCloseWaitsForGCLockBeforeReleasingRootLock(t *testing.T) {
	cfg := storageRootLifecycleLockTestConfig(t.TempDir(), "close-gc")
	fs, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	_, release, err := fs.AcquireGCLock(t.Context())
	if err != nil {
		t.Fatalf("AcquireGCLock() error: %v", err)
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- fs.Close() }()
	assertStorageRootLifecycleCloseBlocked(t, closeDone)
	assertStorageRootLifecycleContenderHeld(t, cfg)
	release()
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	assertStorageRootLifecycleReacquired(t, cfg)
}

func TestFileSystemRootLifecycleLockCrossProcessSharedTrash(t *testing.T) {
	if os.Getenv("MNEMONAS_STORAGE_ROOT_LOCK_HELPER") == "1" {
		cfg := &Config{
			FilesRoot:    os.Getenv("MNEMONAS_STORAGE_ROOT_LOCK_FILES"),
			InternalRoot: os.Getenv("MNEMONAS_STORAGE_ROOT_LOCK_INTERNAL"),
			TrashRoot:    os.Getenv("MNEMONAS_STORAGE_ROOT_LOCK_TRASH"),
			Dataplane:    dataplane.NewClient("unused"),
		}
		fs, err := New(cfg)
		if err != nil {
			fmt.Printf("error:%v\n", err)
			return
		}
		fmt.Println("ready")
		_, _ = os.Stdin.Read(make([]byte, 1))
		_ = fs.Close()
		return
	}

	base := t.TempDir()
	helperCfg := storageRootLifecycleLockTestConfig(base, "helper")
	command := exec.Command(os.Args[0], "-test.run=^TestFileSystemRootLifecycleLockCrossProcessSharedTrash$")
	command.Env = append(os.Environ(),
		"MNEMONAS_STORAGE_ROOT_LOCK_HELPER=1",
		"MNEMONAS_STORAGE_ROOT_LOCK_FILES="+helperCfg.FilesRoot,
		"MNEMONAS_STORAGE_ROOT_LOCK_INTERNAL="+helperCfg.InternalRoot,
		"MNEMONAS_STORAGE_ROOT_LOCK_TRASH="+helperCfg.TrashRoot,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error: %v", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("helper Start() error: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = command.Wait()
	})

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read helper readiness error: %v; stderr=%s", err, stderr.String())
	}
	if strings.TrimSpace(line) != "ready" {
		t.Fatalf("helper readiness = %q, want ready; stderr=%s", line, stderr.String())
	}

	contenderCfg := storageRootLifecycleLockTestConfig(base, "cross-process-contender")
	contenderCfg.TrashRoot = helperCfg.TrashRoot
	contender, contenderErr := New(contenderCfg)
	if contender != nil || !errors.Is(contenderErr, ErrStorageRootLockHeld) {
		t.Fatalf("cross-process contender result fs=%#v error=%v, want ErrStorageRootLockHeld", contender, contenderErr)
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("close helper stdin error: %v", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("helper Wait() error: %v; stderr=%s", err, stderr.String())
	}
}

func openStorageRootLifecycleLockTestRoots(t *testing.T, base, prefix string) []storageRootLifecycleLockTestRoot {
	t.Helper()
	return []storageRootLifecycleLockTestRoot{
		openStorageRootLifecycleLockTestRoot(t, base, prefix+"-files"),
		openStorageRootLifecycleLockTestRoot(t, base, prefix+"-internal"),
		openStorageRootLifecycleLockTestRoot(t, base, prefix+"-trash"),
	}
}

func openStorageRootLifecycleLockTestRoot(t *testing.T, base, label string) storageRootLifecycleLockTestRoot {
	t.Helper()
	rootPath := filepath.Join(base, label)
	if err := os.MkdirAll(rootPath, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error: %v", label, err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot(%s) error: %v", label, err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return storageRootLifecycleLockTestRoot{label: label, path: rootPath, root: root}
}

func storageRootLifecycleLockTestSpecs(roots []storageRootLifecycleLockTestRoot) []storageRootLifecycleLockSpec {
	specs := make([]storageRootLifecycleLockSpec, 0, len(roots))
	for _, root := range roots {
		specs = append(specs, storageRootLifecycleLockSpec{
			label: root.label,
			path:  root.path,
			root:  root.root,
		})
	}
	return specs
}

func storageRootLifecycleLockTestIdentity(t *testing.T, root storageRootLifecycleLockTestRoot) string {
	t.Helper()
	info, err := root.root.Lstat(".")
	if err != nil {
		t.Fatalf("Lstat(%s) error: %v", root.label, err)
	}
	identity := workspace.PersistentIdentityTokenForFileInfo(info)
	if identity == "" {
		t.Fatalf("persistent identity for %s is unavailable", root.label)
	}
	return identity
}

func storageRootLifecycleLockTestConfig(base, prefix string) *Config {
	return &Config{
		FilesRoot:    filepath.Join(base, prefix+"-files"),
		InternalRoot: filepath.Join(base, prefix+"-internal"),
		TrashRoot:    filepath.Join(base, prefix+"-trash"),
		Dataplane:    dataplane.NewClient("unused"),
	}
}

func assertStorageRootLifecycleCloseBlocked(t *testing.T, closeDone <-chan error) {
	t.Helper()
	select {
	case err := <-closeDone:
		t.Fatalf("Close() completed before active storage operation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertStorageRootLifecycleContenderHeld(t *testing.T, cfg *Config) {
	t.Helper()
	contender, err := New(cfg)
	if contender != nil || !errors.Is(err, ErrStorageRootLockHeld) {
		if contender != nil {
			_ = contender.Close()
		}
		t.Fatalf("contender result fs=%#v error=%v, want ErrStorageRootLockHeld", contender, err)
	}
}

func assertStorageRootLifecycleReacquired(t *testing.T, cfg *Config) {
	t.Helper()
	reacquired, err := New(cfg)
	if err != nil {
		t.Fatalf("New(after Close) error: %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatalf("reacquired Close() error: %v", err)
	}
}
