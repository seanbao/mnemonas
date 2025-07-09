package favorites

import "os"

// SetSyncFavoritesStoreRootDirForTest overrides the final favorites-directory
// sync hook and returns a restore function for test cleanup.
func SetSyncFavoritesStoreRootDirForTest(fn func(root *os.Root) error) func() {
	previous := syncFavoritesStoreRootDir
	if fn == nil {
		syncFavoritesStoreRootDir = syncFavoritesRootDir
	} else {
		syncFavoritesStoreRootDir = fn
	}
	return func() {
		syncFavoritesStoreRootDir = previous
	}
}
