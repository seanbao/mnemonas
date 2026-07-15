package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/workspace"
)

var (
	// ErrStorageRootLockHeld reports that another FileSystem owns at least one
	// of the selected storage roots.
	ErrStorageRootLockHeld = errors.New("storage root lock is already held; another MnemoNAS process may be running")
	// ErrStorageRootLockUnsupported reports that the platform cannot provide
	// the required non-blocking directory-inode locks.
	ErrStorageRootLockUnsupported = errors.New("storage root locking is unsupported on this platform")
	errStorageRootLockChanged     = errors.New("storage root identity changed while acquiring its lifecycle lock")
)

type storageRootLifecycleLockSpec struct {
	label string
	path  string
	root  *os.Root
}

type storageRootLifecycleLockBinding struct {
	label string
	path  string
	root  *os.Root
}

type storageRootLifecycleLockEntry struct {
	identity string
	info     os.FileInfo
	file     *os.File
	bindings []storageRootLifecycleLockBinding
	locked   bool
}

type storageRootLifecycleLock struct {
	mu       sync.Mutex
	entries  []*storageRootLifecycleLockEntry
	closeErr error
	closed   bool
}

var storageRootLifecycleOwners = struct {
	sync.Mutex
	owners map[string]*storageRootLifecycleLock
}{
	owners: make(map[string]*storageRootLifecycleLock),
}

// storageRootLifecycleLockTestHook is nil outside tests. It provides
// deterministic fault points without weakening any production validation.
var storageRootLifecycleLockTestHook func(stage string) error

func acquireStorageRootLifecycleLock(specs ...storageRootLifecycleLockSpec) (*storageRootLifecycleLock, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("%w: no storage roots were provided", ErrStorageRootLockUnsupported)
	}

	lock := &storageRootLifecycleLock{}
	entryByIdentity := make(map[string]*storageRootLifecycleLockEntry, len(specs))
	cleanupCandidates := true
	defer func() {
		if cleanupCandidates {
			_ = closeStorageRootLifecycleLockEntries(lock.entries)
		}
	}()

	for _, spec := range specs {
		entry, err := inspectStorageRootLifecycleLockSpec(spec)
		if err != nil {
			return nil, err
		}

		binding := storageRootLifecycleLockBinding{
			label: spec.label,
			path:  spec.path,
			root:  spec.root,
		}
		if existing := entryByIdentity[entry.identity]; existing != nil {
			if !os.SameFile(existing.info, entry.info) {
				_ = entry.file.Close()
				return nil, fmt.Errorf("%s storage root %q: %w", spec.label, spec.path, errStorageRootLockChanged)
			}
			existing.bindings = append(existing.bindings, binding)
			if err := entry.file.Close(); err != nil {
				return nil, fmt.Errorf("close duplicate %s storage root lock handle: %w", spec.label, err)
			}
			continue
		}

		entry.bindings = append(entry.bindings, binding)
		entryByIdentity[entry.identity] = entry
		lock.entries = append(lock.entries, entry)
	}

	sort.Slice(lock.entries, func(i, j int) bool {
		return lock.entries[i].identity < lock.entries[j].identity
	})

	if err := runStorageRootLifecycleLockTestHook("validated"); err != nil {
		return nil, err
	}

	storageRootLifecycleOwners.Lock()
	for _, entry := range lock.entries {
		if storageRootLifecycleOwners.owners[entry.identity] != nil {
			storageRootLifecycleOwners.Unlock()
			return nil, fmt.Errorf("%w: %s", ErrStorageRootLockHeld, storageRootLifecycleLockEntryLabel(entry))
		}
	}
	for _, entry := range lock.entries {
		storageRootLifecycleOwners.owners[entry.identity] = lock
	}
	storageRootLifecycleOwners.Unlock()

	registered := true
	defer func() {
		if registered {
			unregisterStorageRootLifecycleLock(lock)
		}
	}()

	for _, entry := range lock.entries {
		if err := tryLockStorageRootDirectory(entry.file); err != nil {
			_ = unlockStorageRootLifecycleLockEntries(lock.entries)
			return nil, fmt.Errorf("acquire %s: %w", storageRootLifecycleLockEntryLabel(entry), err)
		}
		entry.locked = true
	}

	if err := runStorageRootLifecycleLockTestHook("locked"); err != nil {
		_ = unlockStorageRootLifecycleLockEntries(lock.entries)
		return nil, err
	}
	for _, entry := range lock.entries {
		for _, binding := range entry.bindings {
			if err := verifyStorageRootLifecycleLockBinding(entry, binding); err != nil {
				_ = unlockStorageRootLifecycleLockEntries(lock.entries)
				return nil, err
			}
		}
	}

	registered = false
	cleanupCandidates = false
	return lock, nil
}

func inspectStorageRootLifecycleLockSpec(spec storageRootLifecycleLockSpec) (*storageRootLifecycleLockEntry, error) {
	if spec.root == nil {
		return nil, fmt.Errorf("%s storage root %q: %w", spec.label, spec.path, errStorageRootLockChanged)
	}

	file, err := rootio.OpenDirNoFollow(spec.root, ".")
	if err != nil {
		return nil, fmt.Errorf("open %s storage root lock handle: %w", spec.label, mapStorageRootLifecycleLockPathError(err))
	}
	entry := &storageRootLifecycleLockEntry{file: file}
	if err := bindStorageRootLifecycleLockEntry(entry, spec); err != nil {
		_ = file.Close()
		return nil, err
	}
	return entry, nil
}

func bindStorageRootLifecycleLockEntry(entry *storageRootLifecycleLockEntry, spec storageRootLifecycleLockSpec) error {
	if entry == nil || entry.file == nil {
		return fmt.Errorf("%s storage root %q: %w", spec.label, spec.path, errStorageRootLockChanged)
	}

	heldInfo, err := entry.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect held %s storage root: %w", spec.label, err)
	}
	if err := verifyStorageRootLifecycleLockLocation(heldInfo, spec); err != nil {
		return err
	}

	identity := workspace.PersistentIdentityTokenForFileInfo(heldInfo)
	if identity == "" {
		return fmt.Errorf("%s storage root %q: %w", spec.label, spec.path, ErrStorageRootLockUnsupported)
	}
	entry.identity = identity
	entry.info = heldInfo
	return nil
}

func verifyStorageRootLifecycleLockBinding(
	entry *storageRootLifecycleLockEntry,
	binding storageRootLifecycleLockBinding,
) error {
	if entry == nil || entry.file == nil {
		return fmt.Errorf("%s storage root %q: %w", binding.label, binding.path, errStorageRootLockChanged)
	}
	heldInfo, err := entry.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect held %s storage root after lock: %w", binding.label, err)
	}
	if !os.SameFile(entry.info, heldInfo) ||
		workspace.PersistentIdentityTokenForFileInfo(heldInfo) != entry.identity {
		return fmt.Errorf("%s storage root %q: %w", binding.label, binding.path, errStorageRootLockChanged)
	}
	return verifyStorageRootLifecycleLockLocation(heldInfo, storageRootLifecycleLockSpec{
		label: binding.label,
		path:  binding.path,
		root:  binding.root,
	})
}

func verifyStorageRootLifecycleLockLocation(heldInfo os.FileInfo, spec storageRootLifecycleLockSpec) error {
	if heldInfo == nil || !heldInfo.IsDir() || heldInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s storage root %q: %w", spec.label, spec.path, errStorageRootLockChanged)
	}

	rootedInfo, err := spec.root.Lstat(".")
	if err != nil {
		return fmt.Errorf("inspect rooted %s storage root: %w", spec.label, mapStorageRootLifecycleLockPathError(err))
	}
	nominalInfo, err := os.Lstat(spec.path)
	if err != nil {
		return fmt.Errorf("inspect nominal %s storage root: %w", spec.label, mapStorageRootLifecycleLockPathError(err))
	}
	if nominalInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s storage root %q: %w", spec.label, spec.path, errStoragePathSymlink)
	}

	nominalHandle, err := rootio.OpenDirPathNoFollow(spec.path)
	if err != nil {
		return fmt.Errorf("open nominal %s storage root: %w", spec.label, mapStorageRootLifecycleLockPathError(err))
	}
	nominalOpenedInfo, statErr := nominalHandle.Stat()
	closeErr := nominalHandle.Close()
	if statErr != nil {
		return fmt.Errorf("inspect opened nominal %s storage root: %w", spec.label, statErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close opened nominal %s storage root: %w", spec.label, closeErr)
	}

	if !rootedInfo.IsDir() ||
		!nominalInfo.IsDir() ||
		!nominalOpenedInfo.IsDir() ||
		!os.SameFile(heldInfo, rootedInfo) ||
		!os.SameFile(heldInfo, nominalInfo) ||
		!os.SameFile(heldInfo, nominalOpenedInfo) {
		return fmt.Errorf("%s storage root %q: %w", spec.label, spec.path, errStorageRootLockChanged)
	}
	return nil
}

func verifyWorkspaceStorageRootLifecycleBinding(ws *workspace.Workspace, filesRoot *os.Root) error {
	if ws == nil || filesRoot == nil {
		return errStorageRootLockChanged
	}
	workspaceInfo, err := ws.Stat(context.Background(), "/")
	if err != nil {
		return fmt.Errorf("inspect workspace root binding: %w", err)
	}
	rootedInfo, err := filesRoot.Lstat(".")
	if err != nil {
		return fmt.Errorf("inspect files root binding: %w", err)
	}
	rootedIdentity := workspace.DeleteIdentityTokenForFileInfo(rootedInfo)
	if workspaceInfo.DeleteIdentityToken == "" || rootedIdentity == "" {
		return fmt.Errorf("files storage root %q: %w", ws.Root(), ErrStorageRootLockUnsupported)
	}
	if !workspaceInfo.IsDir ||
		!rootedInfo.IsDir() ||
		workspaceInfo.DeleteIdentityToken != rootedIdentity {
		return fmt.Errorf("files storage root %q: %w", ws.Root(), errStorageRootLockChanged)
	}
	return nil
}

func ensureStorageRootRelativeDirectory(root *os.Root, name string, perm os.FileMode) error {
	if root == nil {
		return errStorageRootLockChanged
	}
	created := false
	if err := rootio.MkdirNoFollow(root, name, perm); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return mapStorageRootLifecycleLockPathError(err)
	}

	dir, err := rootio.OpenDirNoFollow(root, name)
	if err != nil {
		return mapStorageRootLifecycleLockPathError(err)
	}
	info, statErr := dir.Stat()
	if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		_ = dir.Close()
		return errors.Join(errStorageRootLockChanged, statErr)
	}
	if info.Mode().Perm() != perm.Perm() ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		_ = dir.Close()
		return fmt.Errorf(
			"storage root relative directory %q has mode %04o, want %04o: %w",
			name,
			info.Mode().Perm(),
			perm.Perm(),
			errStorageRootLockChanged,
		)
	}
	if !created {
		return dir.Close()
	}

	rootDir, err := rootio.OpenDirNoFollow(root, ".")
	if err != nil {
		_ = dir.Close()
		return mapStorageRootLifecycleLockPathError(err)
	}
	syncErr := errors.Join(dir.Sync(), rootDir.Sync())
	closeErr := errors.Join(dir.Close(), rootDir.Close())
	return errors.Join(syncErr, closeErr)
}

func mapStorageRootLifecycleLockPathError(err error) error {
	if err == nil {
		return nil
	}
	if rootio.IsSymlinkError(err) {
		return errors.Join(errStoragePathSymlink, err)
	}
	return err
}

func runStorageRootLifecycleLockTestHook(stage string) error {
	if storageRootLifecycleLockTestHook == nil {
		return nil
	}
	return storageRootLifecycleLockTestHook(stage)
}

func storageRootLifecycleLockEntryLabel(entry *storageRootLifecycleLockEntry) string {
	if entry == nil || len(entry.bindings) == 0 {
		return "storage root"
	}
	binding := entry.bindings[0]
	return fmt.Sprintf("%s storage root %q", binding.label, binding.path)
}

func unlockStorageRootLifecycleLockEntries(entries []*storageRootLifecycleLockEntry) error {
	var unlockErr error
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry == nil || !entry.locked {
			continue
		}
		if err := unlockStorageRootDirectory(entry.file); err != nil {
			unlockErr = errors.Join(unlockErr, fmt.Errorf("release %s: %w", storageRootLifecycleLockEntryLabel(entry), err))
		}
		entry.locked = false
	}
	return unlockErr
}

func closeStorageRootLifecycleLockEntries(entries []*storageRootLifecycleLockEntry) error {
	var closeErr error
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry == nil || entry.file == nil {
			continue
		}
		if err := entry.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			closeErr = errors.Join(closeErr, fmt.Errorf("close %s: %w", storageRootLifecycleLockEntryLabel(entry), err))
		}
		entry.file = nil
	}
	return closeErr
}

func unregisterStorageRootLifecycleLock(lock *storageRootLifecycleLock) {
	if lock == nil {
		return
	}
	storageRootLifecycleOwners.Lock()
	defer storageRootLifecycleOwners.Unlock()
	for _, entry := range lock.entries {
		if entry != nil && storageRootLifecycleOwners.owners[entry.identity] == lock {
			delete(storageRootLifecycleOwners.owners, entry.identity)
		}
	}
}

func (lock *storageRootLifecycleLock) Validate() error {
	if lock == nil {
		return errStorageRootLockChanged
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.closed {
		return ErrFileSystemClosed
	}
	for _, entry := range lock.entries {
		for _, binding := range entry.bindings {
			if err := verifyStorageRootLifecycleLockBinding(entry, binding); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close releases every root lock in reverse order. It is safe to call more
// than once; later calls return the first close result.
func (lock *storageRootLifecycleLock) Close() error {
	if lock == nil {
		return nil
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.closed {
		return lock.closeErr
	}

	unlockErr := unlockStorageRootLifecycleLockEntries(lock.entries)
	closeErr := closeStorageRootLifecycleLockEntries(lock.entries)
	unregisterStorageRootLifecycleLock(lock)
	lock.closeErr = errors.Join(unlockErr, closeErr)
	lock.closed = true
	return lock.closeErr
}
