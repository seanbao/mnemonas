//go:build darwin

package versionstore

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func anchoredVersionStorePath(dirHandle *os.File, name string) (string, error) {
	if dirHandle == nil || !validVersionStoreDBName(name) {
		return "", errInvalidStorePath
	}

	heldInfo, err := dirHandle.Stat()
	if err != nil {
		return "", fmt.Errorf("stat anchored version store directory handle: %w", err)
	}
	stat, ok := heldInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("%w: read Darwin directory identity", errVersionStoreAnchor)
	}
	var statfs unix.Statfs_t
	if err := unix.Fstatfs(int(dirHandle.Fd()), &statfs); err != nil {
		return "", fmt.Errorf("%w: inspect Darwin database filesystem: %w", errVersionStoreAnchor, err)
	}
	if statfs.Flags&unix.MNT_LOCAL == 0 {
		return "", fmt.Errorf("%w: SQLite WAL requires a local Darwin filesystem", errVersionStoreAnchor)
	}

	// Darwin does not support resolving children through /dev/fd/<fd>.
	// volfs addresses the held directory by its stable volume and file IDs,
	// so SQLite derives its database, WAL, and shared-memory paths beneath the
	// same directory even if the nominal directory path is replaced.
	volumeID := uint64(uint32(stat.Dev))
	directoryPath := fmt.Sprintf("/.vol/%d/%d", volumeID, stat.Ino)
	resolvedDir, err := os.Open(directoryPath)
	if err != nil {
		return "", fmt.Errorf("%w: open Darwin volfs directory: %w", errVersionStoreAnchor, err)
	}
	defer resolvedDir.Close()

	resolvedInfo, err := resolvedDir.Stat()
	if err != nil {
		return "", fmt.Errorf("%w: stat Darwin volfs directory: %w", errVersionStoreAnchor, err)
	}
	if !resolvedInfo.IsDir() || !os.SameFile(heldInfo, resolvedInfo) {
		return "", fmt.Errorf("%w: Darwin volfs identity mismatch", errVersionStoreAnchor)
	}

	return filepath.Join(directoryPath, name), nil
}
