package workspace

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
)

type platformFileIdentity struct {
	deviceID uint64
	inode    uint64
	ctimeSec int64
	ctimeNS  int64
}

func deleteIdentityToken(info os.FileInfo) string {
	identity, ok := platformDeleteIdentity(info)
	if !ok {
		return ""
	}

	hasher := sha256.New()
	_, _ = hasher.Write([]byte("mnemonas-delete-identity-v1"))
	writeDeleteIdentityUint64(hasher, identity.deviceID)
	writeDeleteIdentityUint64(hasher, identity.inode)
	writeDeleteIdentityUint64(hasher, uint64(identity.ctimeSec))
	writeDeleteIdentityUint64(hasher, uint64(identity.ctimeNS))
	writeDeleteIdentityUint64(hasher, uint64(info.Mode()))
	writeDeleteIdentityUint64(hasher, uint64(info.Size()))
	writeDeleteIdentityUint64(hasher, uint64(info.ModTime().Unix()))
	writeDeleteIdentityUint64(hasher, uint64(info.ModTime().Nanosecond()))
	return hex.EncodeToString(hasher.Sum(nil))
}

// DeleteIdentityTokenForFileInfo returns the platform deletion identity for an
// already opened or rooted filesystem object.
func DeleteIdentityTokenForFileInfo(info os.FileInfo) string {
	return deleteIdentityToken(info)
}

// PersistentIdentityTokenForFileInfo returns an identity that remains stable
// when the same object is renamed within one filesystem. It intentionally
// excludes timestamps, permissions, and size so a recovery manifest can bind
// an object across journaled namespace changes and validate those fields
// separately.
func PersistentIdentityTokenForFileInfo(info os.FileInfo) string {
	identity, ok := platformDeleteIdentity(info)
	if !ok {
		return ""
	}

	hasher := sha256.New()
	_, _ = hasher.Write([]byte("mnemonas-persistent-file-identity-v1"))
	writeDeleteIdentityUint64(hasher, identity.deviceID)
	writeDeleteIdentityUint64(hasher, identity.inode)
	writeDeleteIdentityUint64(hasher, uint64(info.Mode()&os.ModeType))
	return hex.EncodeToString(hasher.Sum(nil))
}

func writeDeleteIdentityUint64(hasher interface{ Write([]byte) (int, error) }, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = hasher.Write(encoded[:])
}
