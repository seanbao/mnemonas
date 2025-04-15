//go:build cgo
// +build cgo

package versionstore

import (
	"errors"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func isRecoverableVersionStoreSQLiteError(err error) bool {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code == sqlite3.ErrCorrupt || sqliteErr.Code == sqlite3.ErrNotADB
	}
	return false
}
