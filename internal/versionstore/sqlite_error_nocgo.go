//go:build !cgo
// +build !cgo

package versionstore

func isRecoverableVersionStoreSQLiteError(error) bool {
	return false
}
