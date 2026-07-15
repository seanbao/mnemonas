//go:build unix && !linux && !darwin

package rootio

import "os"

func platformCheckedDirectorySystemMetadata(os.FileInfo) (checkedDirectorySystemMetadata, bool) {
	return checkedDirectorySystemMetadata{}, false
}
