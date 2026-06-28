//go:build !linux && !darwin

package storage

import "fmt"

func currentDeleteMountPoints() ([]string, error) {
	return nil, fmt.Errorf("mount table inspection is unsupported on this platform")
}
