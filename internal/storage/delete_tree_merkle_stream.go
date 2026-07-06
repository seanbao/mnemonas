package storage

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"path"
	"strings"
)

type deleteTreeMerkleStreamFrameV3 struct {
	path          string
	name          string
	hasher        hash.Hash
	childCount    uint64
	lastChildName string
}

// deleteTreeMerkleStreamV3 incrementally builds one target token from the
// name-sorted depth-first preorder produced by workspace.WalkStrict.
type deleteTreeMerkleStreamV3 struct {
	rootPath      string
	frames        []deleteTreeMerkleStreamFrameV3
	rootDigest    [sha256.Size]byte
	nodeCount     uint64
	started       bool
	finished      bool
	hasRootDigest bool
	err           error
}

func newDeleteTreeMerkleStreamV3(rootPath string) (*deleteTreeMerkleStreamV3, error) {
	if !strings.HasPrefix(rootPath, "/") {
		return nil, fmt.Errorf("invalid delete tree stream root path %q: path is not absolute", rootPath)
	}
	normalized, err := normalizeStorageWorkspacePath(rootPath)
	if err != nil || normalized != rootPath {
		return nil, fmt.Errorf("invalid delete tree stream root path %q: path is not canonical", rootPath)
	}
	return &deleteTreeMerkleStreamV3{rootPath: rootPath}, nil
}

func (stream *deleteTreeMerkleStreamV3) add(info FileInfo) error {
	if stream == nil {
		return fmt.Errorf("invalid delete tree stream: stream is nil")
	}
	if stream.finished {
		return fmt.Errorf("invalid delete tree stream: stream is already finished")
	}
	if stream.err != nil {
		return stream.err
	}
	if err := validateDeleteTreeMerkleEntryV3(info); err != nil {
		return stream.fail(fmt.Errorf("invalid delete tree stream entry %q: %w", info.Path, err))
	}
	if !deleteTreeMerklePathWithinRootV3(stream.rootPath, info.Path) {
		return stream.fail(fmt.Errorf("invalid delete tree stream: entry %q is outside root %q", info.Path, stream.rootPath))
	}

	if !stream.started {
		if info.Path != stream.rootPath {
			return stream.fail(fmt.Errorf("invalid delete tree stream: first entry %q does not match root %q", info.Path, stream.rootPath))
		}
		stream.started = true
		stream.nodeCount = 1
		if info.IsDir {
			stream.frames = append(stream.frames, newDeleteTreeMerkleStreamFrameV3(info))
		} else {
			stream.rootDigest = finishDeleteTreeMerkleDigestV3(newDeleteTreeMerkleNodeHasherV3(info))
			stream.hasRootDigest = true
		}
		return nil
	}

	if info.Path == stream.rootPath {
		return stream.fail(fmt.Errorf("invalid delete tree stream: duplicate root entry %q", info.Path))
	}
	if stream.hasRootDigest {
		return stream.fail(fmt.Errorf("invalid delete tree stream: file root %q is followed by entry %q", stream.rootPath, info.Path))
	}

	parentPath := path.Dir(info.Path)
	for len(stream.frames) > 0 && stream.frames[len(stream.frames)-1].path != parentPath {
		if err := stream.closeTopDirectory(); err != nil {
			return stream.fail(err)
		}
	}
	if len(stream.frames) == 0 || stream.frames[len(stream.frames)-1].path != parentPath {
		return stream.fail(fmt.Errorf("invalid delete tree stream: direct parent %q of entry %q is missing or already closed", parentPath, info.Path))
	}

	parent := &stream.frames[len(stream.frames)-1]
	if parent.lastChildName != "" && info.Name <= parent.lastChildName {
		return stream.fail(fmt.Errorf("invalid delete tree stream: child %q of directory %q is not in strict name order after %q", info.Name, parent.path, parent.lastChildName))
	}
	parent.lastChildName = info.Name
	stream.nodeCount++
	if info.IsDir {
		stream.frames = append(stream.frames, newDeleteTreeMerkleStreamFrameV3(info))
		return nil
	}

	digest := finishDeleteTreeMerkleDigestV3(newDeleteTreeMerkleNodeHasherV3(info))
	writeDeleteTreeMerkleChildV3(parent.hasher, info.Name, digest)
	parent.childCount++
	return nil
}

func (stream *deleteTreeMerkleStreamV3) finish() (string, error) {
	if stream == nil {
		return "", fmt.Errorf("invalid delete tree stream: stream is nil")
	}
	if stream.finished {
		return "", fmt.Errorf("invalid delete tree stream: stream is already finished")
	}
	stream.finished = true
	if stream.err != nil {
		return "", stream.err
	}
	if !stream.started {
		return "", stream.fail(fmt.Errorf("invalid delete tree stream: no entries were added"))
	}

	for len(stream.frames) > 0 {
		if err := stream.closeTopDirectory(); err != nil {
			return "", stream.fail(err)
		}
	}
	if !stream.hasRootDigest {
		return "", stream.fail(fmt.Errorf("invalid delete tree stream: root digest is unavailable"))
	}
	return deleteTreeTargetTokenV3(stream.rootPath, stream.rootDigest, stream.nodeCount), nil
}

func newDeleteTreeMerkleStreamFrameV3(info FileInfo) deleteTreeMerkleStreamFrameV3 {
	return deleteTreeMerkleStreamFrameV3{
		path:   info.Path,
		name:   info.Name,
		hasher: newDeleteTreeMerkleNodeHasherV3(info),
	}
}

func (stream *deleteTreeMerkleStreamV3) closeTopDirectory() error {
	if len(stream.frames) == 0 {
		return fmt.Errorf("invalid delete tree stream: directory stack is empty")
	}

	last := len(stream.frames) - 1
	frame := stream.frames[last]
	stream.frames = stream.frames[:last]
	writeDeleteTreeMerkleUint64V3(frame.hasher, frame.childCount)
	digest := finishDeleteTreeMerkleDigestV3(frame.hasher)

	if len(stream.frames) == 0 {
		if frame.path != stream.rootPath {
			return fmt.Errorf("invalid delete tree stream: directory %q closed without its direct parent", frame.path)
		}
		stream.rootDigest = digest
		stream.hasRootDigest = true
		return nil
	}

	parent := &stream.frames[len(stream.frames)-1]
	if path.Dir(frame.path) != parent.path {
		return fmt.Errorf("invalid delete tree stream: direct parent %q of directory %q is missing", path.Dir(frame.path), frame.path)
	}
	writeDeleteTreeMerkleChildV3(parent.hasher, frame.name, digest)
	parent.childCount++
	return nil
}

func (stream *deleteTreeMerkleStreamV3) fail(err error) error {
	if stream.err == nil {
		stream.err = err
	}
	return stream.err
}
