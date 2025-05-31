//go:build !unix

package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/seanbao/mnemonas/internal/rootio"
)

func openLogOutputFile(cleanPath string) (*os.File, error) {
	rootPath := filepath.VolumeName(cleanPath) + string(filepath.Separator)
	relPath := strings.TrimPrefix(cleanPath, rootPath)
	if relPath == "" {
		return nil, errLogOutputSymlink
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	if err := rootio.MkdirAllNoFollow(root, filepath.Dir(relPath), 0o755); err != nil {
		if rootio.IsSymlinkError(err) {
			return nil, errLogOutputSymlink
		}
		return nil, err
	}
	afterOpenLogOutputParent()
	file, err := rootio.OpenFileNoFollow(root, relPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if rootio.IsSymlinkError(err) {
		return nil, errLogOutputSymlink
	}
	return file, err
}
