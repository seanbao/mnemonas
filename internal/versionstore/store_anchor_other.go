//go:build !linux && !darwin

package versionstore

import (
	"fmt"
	"os"
	"runtime"
)

func anchoredVersionStorePath(dirHandle *os.File, name string) (string, error) {
	if dirHandle == nil || !validVersionStoreDBName(name) {
		return "", errInvalidStorePath
	}
	if _, err := dirHandle.Stat(); err != nil {
		return "", fmt.Errorf("validate anchored version store directory handle: %w", err)
	}
	return "", fmt.Errorf("%w: %s", errVersionStoreAnchor, runtime.GOOS)
}
