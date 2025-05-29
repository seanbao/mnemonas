//go:build !unix

package main

import (
	"os"
	"path/filepath"
)

func openLogOutputFile(cleanPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
		return nil, err
	}
	if err := validateLogOutputPath(cleanPath); err != nil {
		return nil, err
	}
	afterOpenLogOutputParent()
	return os.OpenFile(cleanPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}
