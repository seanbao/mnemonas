//go:build !unix

package backup

import "errors"

func restoreAvailableBytes(string) (int64, error) {
	return 0, errors.New("restore capacity check is unsupported on this platform")
}
