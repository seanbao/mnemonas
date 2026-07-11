//go:build !unix

package rootio

import (
	"errors"
	"os"
	"testing"
)

func TestRemoveAllFromDirNoFollowCheckedInPlaceIsUnsupported(t *testing.T) {
	err := RemoveAllFromDirNoFollowCheckedInPlace(nil, "target", func(string, os.FileInfo) error { return nil })
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("RemoveAllFromDirNoFollowCheckedInPlace() error = %v, want errors.ErrUnsupported", err)
	}
}
