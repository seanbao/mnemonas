//go:build unix

package backup

import (
	"errors"
	"testing"
)

func TestRestoreAvailableBytesFromStatfsValidatesBlockSizeAndSaturatesOverflow(t *testing.T) {
	available, err := restoreAvailableBytesFromStatfs(10, 4096)
	if err != nil {
		t.Fatalf("restoreAvailableBytesFromStatfs() error: %v", err)
	}
	if available != 40960 {
		t.Fatalf("available bytes = %d, want 40960", available)
	}

	for _, blockSize := range []int64{0, -4096} {
		if _, err := restoreAvailableBytesFromStatfs(10, blockSize); !errors.Is(err, errRestoreAvailableInvalidBlockSize) {
			t.Fatalf("block size %d error = %v, want invalid block size", blockSize, err)
		}
	}

	maxInt64 := int64(1<<63 - 1)
	available, err = restoreAvailableBytesFromStatfs(^uint64(0), 2)
	if err != nil {
		t.Fatalf("restoreAvailableBytesFromStatfs() overflow error: %v", err)
	}
	if available != maxInt64 {
		t.Fatalf("overflow available bytes = %d, want %d", available, maxInt64)
	}
}
