package storage

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path"
	"sort"
	"strings"
)

const (
	deleteTreeFileDomainV3   = "mnemonas-delete-file-v3"
	deleteTreeDirDomainV3    = "mnemonas-delete-dir-v3"
	deleteTreeTargetDomainV3 = "mnemonas-delete-target-v3"
)

type deleteTreeMerkleNodeV3 struct {
	info     FileInfo
	children []*deleteTreeMerkleNodeV3
}

// deleteTreeTokenV3 returns a deterministic Merkle token for one complete,
// canonical deletion snapshot.
func deleteTreeTokenV3(snapshot DeleteTargetSnapshot) (string, error) {
	root, nodeCount, err := buildDeleteTreeMerkleV3(snapshot)
	if err != nil {
		return "", err
	}

	rootDigest, err := root.digest()
	if err != nil {
		return "", err
	}
	return deleteTreeTargetTokenV3(snapshot.Root.Path, rootDigest, uint64(nodeCount)), nil
}

func deleteTreeTargetTokenV3(rootPath string, rootDigest [sha256.Size]byte, nodeCount uint64) string {
	targetHasher := sha256.New()
	writeDeleteTreeMerkleStringV3(targetHasher, deleteTreeTargetDomainV3)
	writeDeleteTreeMerkleStringV3(targetHasher, rootPath)
	writeDeleteTreeMerkleBytesV3(targetHasher, rootDigest[:])
	writeDeleteTreeMerkleUint64V3(targetHasher, nodeCount)
	return hex.EncodeToString(targetHasher.Sum(nil))
}

func buildDeleteTreeMerkleV3(snapshot DeleteTargetSnapshot) (*deleteTreeMerkleNodeV3, int, error) {
	if len(snapshot.Entries) == 0 {
		return nil, 0, fmt.Errorf("invalid delete tree snapshot: manifest is empty")
	}
	if err := validateDeleteTreeMerkleEntryV3(snapshot.Root); err != nil {
		return nil, 0, fmt.Errorf("invalid delete tree snapshot root: %w", err)
	}

	rootPath := snapshot.Root.Path
	nodes := make(map[string]*deleteTreeMerkleNodeV3, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		if err := validateDeleteTreeMerkleEntryV3(entry); err != nil {
			return nil, 0, fmt.Errorf("invalid delete tree snapshot entry %q: %w", entry.Path, err)
		}
		if !deleteTreeMerklePathWithinRootV3(rootPath, entry.Path) {
			return nil, 0, fmt.Errorf("invalid delete tree snapshot: entry %q is outside root %q", entry.Path, rootPath)
		}
		if _, exists := nodes[entry.Path]; exists {
			return nil, 0, fmt.Errorf("invalid delete tree snapshot: duplicate entry path %q", entry.Path)
		}
		nodes[entry.Path] = &deleteTreeMerkleNodeV3{info: entry}
	}

	root, ok := nodes[rootPath]
	if !ok {
		return nil, 0, fmt.Errorf("invalid delete tree snapshot: root entry %q is missing", rootPath)
	}
	if !sameDeleteTreeMerkleTokenFieldsV3(snapshot.Root, root.info) {
		return nil, 0, fmt.Errorf("invalid delete tree snapshot: root entry %q does not match Root", rootPath)
	}

	for entryPath, node := range nodes {
		if entryPath == rootPath {
			continue
		}
		parentPath := path.Dir(entryPath)
		parent, ok := nodes[parentPath]
		if !ok {
			return nil, 0, fmt.Errorf("invalid delete tree snapshot: direct parent %q of entry %q is missing", parentPath, entryPath)
		}
		if !parent.info.IsDir {
			return nil, 0, fmt.Errorf("invalid delete tree snapshot: file entry %q has child %q", parentPath, entryPath)
		}
		parent.children = append(parent.children, node)
	}

	return root, len(nodes), nil
}

func validateDeleteTreeMerkleEntryV3(info FileInfo) error {
	if !strings.HasPrefix(info.Path, "/") {
		return fmt.Errorf("path %q is not absolute", info.Path)
	}
	normalized, err := normalizeStorageWorkspacePath(info.Path)
	if err != nil || normalized != info.Path {
		return fmt.Errorf("path %q is not canonical", info.Path)
	}
	expectedName := path.Base(info.Path)
	if info.Path == "/" {
		expectedName = "/"
	}
	if info.Name != expectedName {
		return fmt.Errorf("name %q does not match path basename %q", info.Name, expectedName)
	}
	if info.IsDir {
		if info.Mode.Type() != os.ModeDir {
			return fmt.Errorf("directory path %q has non-directory mode %v", info.Path, info.Mode)
		}
		return nil
	}
	if !info.Mode.IsRegular() {
		return fmt.Errorf("file path %q has non-regular mode %v", info.Path, info.Mode)
	}
	return nil
}

func deleteTreeMerklePathWithinRootV3(rootPath, entryPath string) bool {
	if entryPath == rootPath {
		return true
	}
	if rootPath == "/" {
		return strings.HasPrefix(entryPath, "/")
	}
	return strings.HasPrefix(entryPath, strings.TrimSuffix(rootPath, "/")+"/")
}

func sameDeleteTreeMerkleTokenFieldsV3(left, right FileInfo) bool {
	return left.Path == right.Path &&
		left.Name == right.Name &&
		left.IsDir == right.IsDir &&
		left.Mode == right.Mode &&
		left.Size == right.Size &&
		left.ModTime.UnixNano() == right.ModTime.UnixNano() &&
		left.DeleteIdentityToken == right.DeleteIdentityToken &&
		left.ContentHash == right.ContentHash
}

func (node *deleteTreeMerkleNodeV3) digest() ([sha256.Size]byte, error) {
	if node == nil {
		return [sha256.Size]byte{}, fmt.Errorf("invalid delete tree snapshot: Merkle node is nil")
	}
	hasher := newDeleteTreeMerkleNodeHasherV3(node.info)

	if node.info.IsDir {
		sort.Slice(node.children, func(i, j int) bool {
			return node.children[i].info.Name < node.children[j].info.Name
		})
		for _, child := range node.children {
			childDigest, err := child.digest()
			if err != nil {
				return [sha256.Size]byte{}, err
			}
			writeDeleteTreeMerkleChildV3(hasher, child.info.Name, childDigest)
		}
		writeDeleteTreeMerkleUint64V3(hasher, uint64(len(node.children)))
	}

	return finishDeleteTreeMerkleDigestV3(hasher), nil
}

func newDeleteTreeMerkleNodeHasherV3(info FileInfo) hash.Hash {
	hasher := sha256.New()
	if info.IsDir {
		writeDeleteTreeMerkleStringV3(hasher, deleteTreeDirDomainV3)
	} else {
		writeDeleteTreeMerkleStringV3(hasher, deleteTreeFileDomainV3)
	}
	writeDeleteTreeMerkleUint64V3(hasher, uint64(info.Mode))
	writeDeleteTreeMerkleUint64V3(hasher, uint64(info.Size))
	writeDeleteTreeMerkleUint64V3(hasher, uint64(info.ModTime.UnixNano()))
	writeDeleteTreeMerkleStringV3(hasher, info.DeleteIdentityToken)
	writeDeleteTreeMerkleStringV3(hasher, info.ContentHash)
	return hasher
}

func writeDeleteTreeMerkleChildV3(hasher hash.Hash, name string, digest [sha256.Size]byte) {
	writeDeleteTreeMerkleStringV3(hasher, name)
	writeDeleteTreeMerkleBytesV3(hasher, digest[:])
}

func finishDeleteTreeMerkleDigestV3(hasher hash.Hash) [sha256.Size]byte {
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest
}

func writeDeleteTreeMerkleStringV3(hasher hash.Hash, value string) {
	writeDeleteTreeMerkleBytesV3(hasher, []byte(value))
}

func writeDeleteTreeMerkleBytesV3(hasher hash.Hash, value []byte) {
	writeDeleteTreeMerkleUint64V3(hasher, uint64(len(value)))
	_, _ = hasher.Write(value)
}

func writeDeleteTreeMerkleUint64V3(hasher hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = hasher.Write(encoded[:])
}
