package favorites

import (
	"errors"
	"fmt"
	"os"
)

const favoritesStoreRecoveryMarkerSuffix = ".recovery-required"

var favoritesStoreRecoveryMarkerBody = []byte("mnemonas favorites store recovery required\n")

func favoritesStoreRecoveryMarkerPath(storePath string) string {
	return storePath + favoritesStoreRecoveryMarkerSuffix
}

func favoritesStoreRecoveryMarkerExists(storePath string) (bool, error) {
	_, err := readRegisteredFavoritesStoreFile(favoritesStoreRecoveryMarkerPath(storePath))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("read favorites store recovery marker: %w", err)
}

func persistFavoritesStoreRecoveryMarker(storePath string) error {
	if err := writeRegisteredFavoritesStoreFileAtomically(favoritesStoreRecoveryMarkerPath(storePath), favoritesStoreRecoveryMarkerBody); err != nil {
		return fmt.Errorf("persist favorites store recovery marker: %w", err)
	}
	return nil
}
