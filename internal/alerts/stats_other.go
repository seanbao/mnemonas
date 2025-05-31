//go:build !unix

package alerts

import "errors"

func (m *Monitor) getStats() (*StorageStats, error) {
	return nil, errors.New("storage stats are not available on this platform")
}
