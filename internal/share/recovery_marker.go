package share

import (
	"errors"
	"fmt"
	"os"
)

const shareStoreRecoveryMarkerSuffix = ".recovery-required"

var shareStoreRecoveryMarkerBody = []byte("mnemonas share store recovery required\n")

func shareStoreRecoveryMarkerPath(storePath string) string {
	return storePath + shareStoreRecoveryMarkerSuffix
}

func shareStoreRecoveryMarkerExists(storePath string) (bool, error) {
	_, err := readRegisteredShareStoreFile(shareStoreRecoveryMarkerPath(storePath))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("read share store recovery marker: %w", err)
}

func persistShareStoreRecoveryMarker(storePath string) error {
	if err := writeRegisteredShareStoreFileAtomically(shareStoreRecoveryMarkerPath(storePath), shareStoreRecoveryMarkerBody); err != nil {
		return fmt.Errorf("persist share store recovery marker: %w", err)
	}
	return nil
}
