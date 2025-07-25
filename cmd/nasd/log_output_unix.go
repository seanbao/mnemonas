//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func openLogOutputFile(cleanPath string) (*os.File, error) {
	parentFD, err := openLogOutputParentDir(filepath.Dir(cleanPath))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = unix.Close(parentFD)
	}()

	afterOpenLogOutputParent()

	fd, err := unix.Openat(parentFD, filepath.Base(cleanPath), unix.O_CREAT|unix.O_APPEND|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil, mapLogOutputOpenError(err)
	}

	file := os.NewFile(uintptr(fd), cleanPath)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("failed to wrap log output file descriptor for %q", cleanPath)
	}
	return file, nil
}

func openLogOutputParentDir(dir string) (int, error) {
	cleanDir := filepath.Clean(dir)
	root := filepath.VolumeName(cleanDir) + string(filepath.Separator)
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, mapLogOutputOpenError(err)
	}

	trimmed := strings.TrimPrefix(cleanDir, root)
	if trimmed == "" {
		return fd, nil
	}

	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}

		nextFD, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(err, os.ErrNotExist) {
			if mkErr := unix.Mkdirat(fd, part, 0o755); mkErr != nil && !errors.Is(mkErr, os.ErrExist) {
				_ = unix.Close(fd)
				return -1, mapLogOutputOpenError(mkErr)
			}
			nextFD, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if err != nil {
			_ = unix.Close(fd)
			return -1, mapLogOutputOpenError(err)
		}

		_ = unix.Close(fd)
		fd = nextFD
	}

	return fd, nil
}

func mapLogOutputOpenError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.ELOOP) {
		return errLogOutputSymlink
	}
	return err
}
