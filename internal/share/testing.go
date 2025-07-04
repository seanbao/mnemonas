package share

import "os"

// SetSyncShareStoreRootDirForTest overrides the final shares-directory sync hook
// and returns a restore function for test cleanup.
func SetSyncShareStoreRootDirForTest(fn func(root *os.Root) error) func() {
	previous := syncShareStoreRootDir
	if fn == nil {
		syncShareStoreRootDir = syncShareRootDir
	} else {
		syncShareStoreRootDir = fn
	}
	return func() {
		syncShareStoreRootDir = previous
	}
}
