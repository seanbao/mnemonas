//go:build linux

package versionstore

import (
	"fmt"
	"os"
)

func anchoredVersionStorePath(dirHandle *os.File, name string) (string, error) {
	if dirHandle == nil || !validVersionStoreDBName(name) {
		return "", errInvalidStorePath
	}
	heldInfo, err := dirHandle.Stat()
	if err != nil {
		return "", fmt.Errorf("validate anchored version store directory handle: %w", err)
	}

	directoryPath := fmt.Sprintf("/proc/self/fd/%d", dirHandle.Fd())
	resolvedInfo, err := os.Stat(directoryPath)
	if err != nil {
		return "", fmt.Errorf("%w: stat Linux procfs directory: %w", errVersionStoreAnchor, err)
	}
	if !resolvedInfo.IsDir() || !os.SameFile(heldInfo, resolvedInfo) {
		return "", fmt.Errorf("%w: Linux procfs identity mismatch", errVersionStoreAnchor)
	}
	return fmt.Sprintf("%s/%s", directoryPath, name), nil
}
