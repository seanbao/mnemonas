//go:build !linux && !darwin

package workspace

import "os"

func platformDeleteIdentity(os.FileInfo) (platformFileIdentity, bool) {
	return platformFileIdentity{}, false
}
