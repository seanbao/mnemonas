//go:build !linux && !darwin

package backup

import "os"

func readinessFileChangeToken(os.FileInfo) (string, bool) {
	return "", false
}
