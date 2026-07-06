package storage

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestDeleteTreeMerkleStreamV3MatchesSnapshotToken(t *testing.T) {
	snapshot := deleteTreeMerkleFixture()
	want, err := deleteTreeTokenV3(snapshot)
	if err != nil {
		t.Fatalf("deleteTreeTokenV3() error: %v", err)
	}

	stream, err := newDeleteTreeMerkleStreamV3(snapshot.Root.Path)
	if err != nil {
		t.Fatalf("newDeleteTreeMerkleStreamV3() error: %v", err)
	}
	for _, entry := range snapshot.Entries {
		if err := stream.add(entry); err != nil {
			t.Fatalf("add(%q) error: %v", entry.Path, err)
		}
	}
	got, err := stream.finish()
	if err != nil {
		t.Fatalf("finish() error: %v", err)
	}
	if got != want {
		t.Fatalf("finish() = %q, want snapshot token %q", got, want)
	}
	const known = "fc30ed6a31fbb9bcde2c06061a9995bbde5b698bfe543d49b40d2c5fcaa485f0"
	if got != known {
		t.Fatalf("finish() = %q, want known vector %q", got, known)
	}
}

func TestDeleteTreeMerkleStreamV3AcceptsWalkStrictPreorder(t *testing.T) {
	root := deleteTreeStreamDirectory("/root", "root")
	directory := deleteTreeStreamDirectory("/root/a", "a")
	entries := []FileInfo{
		root,
		directory,
		deleteTreeStreamFile("/root/a/z.txt", "z.txt"),
		deleteTreeStreamFile("/root/a-archive.txt", "a-archive.txt"),
	}
	snapshot := DeleteTargetSnapshot{Root: root, Entries: entries}
	want, err := deleteTreeTokenV3(snapshot)
	if err != nil {
		t.Fatalf("deleteTreeTokenV3() error: %v", err)
	}

	stream, err := newDeleteTreeMerkleStreamV3(root.Path)
	if err != nil {
		t.Fatalf("newDeleteTreeMerkleStreamV3() error: %v", err)
	}
	for _, entry := range entries {
		if err := stream.add(entry); err != nil {
			t.Fatalf("add(%q) error: %v", entry.Path, err)
		}
	}
	got, err := stream.finish()
	if err != nil {
		t.Fatalf("finish() error: %v", err)
	}
	if got != want {
		t.Fatalf("finish() = %q, want %q", got, want)
	}
}

func TestDeleteTreeMerkleStreamV3ClosesMultipleDirectoryFrames(t *testing.T) {
	root := deleteTreeStreamDirectory("/root", "root")
	entries := []FileInfo{
		root,
		deleteTreeStreamDirectory("/root/a", "a"),
		deleteTreeStreamDirectory("/root/a/b", "b"),
		deleteTreeStreamFile("/root/a/b/z.txt", "z.txt"),
		deleteTreeStreamDirectory("/root/c", "c"),
		deleteTreeStreamFile("/root/c/d.txt", "d.txt"),
		deleteTreeStreamFile("/root/e.txt", "e.txt"),
	}
	want, err := deleteTreeTokenV3(DeleteTargetSnapshot{Root: root, Entries: entries})
	if err != nil {
		t.Fatalf("deleteTreeTokenV3() error: %v", err)
	}

	stream, err := newDeleteTreeMerkleStreamV3(root.Path)
	if err != nil {
		t.Fatalf("newDeleteTreeMerkleStreamV3() error: %v", err)
	}
	for _, entry := range entries {
		if err := stream.add(entry); err != nil {
			t.Fatalf("add(%q) error: %v", entry.Path, err)
		}
	}
	got, err := stream.finish()
	if err != nil {
		t.Fatalf("finish() error: %v", err)
	}
	if got != want {
		t.Fatalf("finish() = %q, want %q", got, want)
	}
}

func TestDeleteTreeMerkleStreamV3HandlesFileAndEmptyDirectoryRoots(t *testing.T) {
	tests := []struct {
		name string
		root FileInfo
	}{
		{name: "file", root: deleteTreeStreamFile("/item.bin", "item.bin")},
		{name: "empty directory", root: deleteTreeStreamDirectory("/empty", "empty")},
		{name: "workspace root", root: deleteTreeStreamDirectory("/", "/")},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot := DeleteTargetSnapshot{Root: testCase.root, Entries: []FileInfo{testCase.root}}
			want, err := deleteTreeTokenV3(snapshot)
			if err != nil {
				t.Fatalf("deleteTreeTokenV3() error: %v", err)
			}
			stream, err := newDeleteTreeMerkleStreamV3(testCase.root.Path)
			if err != nil {
				t.Fatalf("newDeleteTreeMerkleStreamV3() error: %v", err)
			}
			if err := stream.add(testCase.root); err != nil {
				t.Fatalf("add(root) error: %v", err)
			}
			got, err := stream.finish()
			if err != nil {
				t.Fatalf("finish() error: %v", err)
			}
			if got != want {
				t.Fatalf("finish() = %q, want %q", got, want)
			}
		})
	}
}

func TestNewDeleteTreeMerkleStreamV3RejectsInvalidRootPath(t *testing.T) {
	for _, rootPath := range []string{"", "root", "/root/", "/root//nested", "/root/../outside"} {
		t.Run(strings.ReplaceAll(rootPath, "/", "_"), func(t *testing.T) {
			if _, err := newDeleteTreeMerkleStreamV3(rootPath); err == nil {
				t.Fatalf("newDeleteTreeMerkleStreamV3(%q) error = nil, want validation error", rootPath)
			}
		})
	}
}

func TestDeleteTreeMerkleStreamV3RejectsMalformedPreorder(t *testing.T) {
	rootDir := deleteTreeStreamDirectory("/root", "root")
	rootFile := deleteTreeStreamFile("/root", "root")
	tests := []struct {
		name     string
		rootPath string
		entries  []FileInfo
	}{
		{name: "root mismatch", rootPath: "/root", entries: []FileInfo{deleteTreeStreamFile("/root/child.txt", "child.txt")}},
		{name: "duplicate root", rootPath: "/root", entries: []FileInfo{rootDir, rootDir}},
		{
			name:     "siblings out of order",
			rootPath: "/root",
			entries: []FileInfo{
				rootDir,
				deleteTreeStreamFile("/root/b.txt", "b.txt"),
				deleteTreeStreamFile("/root/a.txt", "a.txt"),
			},
		},
		{
			name:     "duplicate sibling",
			rootPath: "/root",
			entries: []FileInfo{
				rootDir,
				deleteTreeStreamFile("/root/a.txt", "a.txt"),
				deleteTreeStreamFile("/root/a.txt", "a.txt"),
			},
		},
		{
			name:     "missing direct parent",
			rootPath: "/root",
			entries: []FileInfo{
				rootDir,
				deleteTreeStreamFile("/root/missing/child.txt", "child.txt"),
			},
		},
		{
			name:     "root file followed by entry",
			rootPath: "/root",
			entries: []FileInfo{
				rootFile,
				deleteTreeStreamFile("/root/child.txt", "child.txt"),
			},
		},
		{
			name:     "closed subtree reentry",
			rootPath: "/root",
			entries: []FileInfo{
				rootDir,
				deleteTreeStreamDirectory("/root/a", "a"),
				deleteTreeStreamFile("/root/a/z.txt", "z.txt"),
				deleteTreeStreamFile("/root/b.txt", "b.txt"),
				deleteTreeStreamFile("/root/a/y.txt", "y.txt"),
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			stream, err := newDeleteTreeMerkleStreamV3(testCase.rootPath)
			if err != nil {
				t.Fatalf("newDeleteTreeMerkleStreamV3() error: %v", err)
			}
			var addErr error
			for _, entry := range testCase.entries {
				if err := stream.add(entry); err != nil {
					addErr = err
					break
				}
			}
			if addErr == nil {
				t.Fatal("add() error = nil, want malformed preorder error")
			}
			if _, err := stream.finish(); err == nil {
				t.Fatal("finish() after malformed preorder error = nil")
			}
		})
	}
}

func TestDeleteTreeMerkleStreamV3RejectsEmptyAndFinishedStreams(t *testing.T) {
	stream, err := newDeleteTreeMerkleStreamV3("/root")
	if err != nil {
		t.Fatalf("newDeleteTreeMerkleStreamV3() error: %v", err)
	}
	if _, err := stream.finish(); err == nil {
		t.Fatal("finish(empty) error = nil, want validation error")
	}
	if err := stream.add(deleteTreeStreamFile("/root", "root")); err == nil {
		t.Fatal("add() after finish error = nil")
	}

	stream, err = newDeleteTreeMerkleStreamV3("/file")
	if err != nil {
		t.Fatalf("newDeleteTreeMerkleStreamV3(file) error: %v", err)
	}
	if err := stream.add(deleteTreeStreamFile("/file", "file")); err != nil {
		t.Fatalf("add(file) error: %v", err)
	}
	if _, err := stream.finish(); err != nil {
		t.Fatalf("finish(file) error: %v", err)
	}
	if _, err := stream.finish(); err == nil {
		t.Fatal("second finish() error = nil")
	}
	if err := stream.add(deleteTreeStreamFile("/file", "file")); err == nil {
		t.Fatal("add() after successful finish error = nil")
	}
}

func deleteTreeStreamDirectory(entryPath, name string) FileInfo {
	return FileInfo{
		Path:                entryPath,
		Name:                name,
		IsDir:               true,
		Mode:                os.ModeDir | 0o750,
		Size:                4096,
		ModTime:             time.Unix(1_700_000_000, 123),
		DeleteIdentityToken: "directory-identity",
	}
}

func deleteTreeStreamFile(entryPath, name string) FileInfo {
	return FileInfo{
		Path:                entryPath,
		Name:                name,
		Mode:                0o640,
		Size:                7,
		ModTime:             time.Unix(1_700_000_001, 456),
		DeleteIdentityToken: "file-identity",
		ContentHash:         "file-content-hash",
	}
}
