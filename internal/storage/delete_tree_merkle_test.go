package storage

import (
	"os"
	"testing"
	"time"
)

func TestDeleteTreeTokenV3KnownVector(t *testing.T) {
	snapshot := deleteTreeMerkleFixture()

	got, err := deleteTreeTokenV3(snapshot)
	if err != nil {
		t.Fatalf("deleteTreeTokenV3() error: %v", err)
	}
	const want = "fc30ed6a31fbb9bcde2c06061a9995bbde5b698bfe543d49b40d2c5fcaa485f0"
	if got != want {
		t.Fatalf("deleteTreeTokenV3() = %q, want %q", got, want)
	}
}

func TestDeleteTreeTokenV3ChangesForEveryBoundField(t *testing.T) {
	base := deleteTreeMerkleFixture()
	want, err := deleteTreeTokenV3(base)
	if err != nil {
		t.Fatalf("deleteTreeTokenV3(base) error: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*DeleteTargetSnapshot)
	}{
		{
			name: "root path",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				for i := range snapshot.Entries {
					snapshot.Entries[i].Path = "/renamed" + snapshot.Entries[i].Path[len("/root"):]
					if snapshot.Entries[i].Path == "/renamed" {
						snapshot.Entries[i].Name = "renamed"
					}
				}
				snapshot.Root = snapshot.Entries[0]
			},
		},
		{
			name: "entry count",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries = append(snapshot.Entries, FileInfo{
					Path:                "/root/z.txt",
					Name:                "z.txt",
					Mode:                0o600,
					Size:                1,
					ModTime:             time.Unix(1_700_000_004, 444),
					DeleteIdentityToken: "z-id",
					ContentHash:         "z-hash",
				})
			},
		},
		{
			name: "entry path",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].Path = "/root/renamed.txt"
				snapshot.Entries[1].Name = "renamed.txt"
			},
		},
		{
			name: "type",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].IsDir = true
				snapshot.Entries[1].Mode = os.ModeDir | 0o640
			},
		},
		{
			name: "mode",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].Mode = 0o600
			},
		},
		{
			name: "mode special bits",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].Mode |= os.ModeSetuid
			},
		},
		{
			name: "size",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].Size++
			},
		},
		{
			name: "mtime nanoseconds",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].ModTime = snapshot.Entries[1].ModTime.Add(time.Nanosecond)
			},
		},
		{
			name: "delete identity",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].DeleteIdentityToken += "-changed"
			},
		},
		{
			name: "content hash",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].ContentHash += "-changed"
			},
		},
		{
			name: "directory content hash",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[2].ContentHash += "-changed"
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot := cloneDeleteTreeSnapshot(base)
			testCase.mutate(&snapshot)
			got, err := deleteTreeTokenV3(snapshot)
			if err != nil {
				t.Fatalf("deleteTreeTokenV3(mutated) error: %v", err)
			}
			if got == want {
				t.Fatalf("deleteTreeTokenV3(mutated) = %q, want token drift", got)
			}
		})
	}
}

func TestDeleteTreeTokenV3IgnoresManifestOrderAndVersioned(t *testing.T) {
	base := deleteTreeMerkleFixture()
	want, err := deleteTreeTokenV3(base)
	if err != nil {
		t.Fatalf("deleteTreeTokenV3(base) error: %v", err)
	}

	shuffled := cloneDeleteTreeSnapshot(base)
	shuffled.Entries[0], shuffled.Entries[3] = shuffled.Entries[3], shuffled.Entries[0]
	shuffled.Entries[1], shuffled.Entries[2] = shuffled.Entries[2], shuffled.Entries[1]
	for i := range shuffled.Entries {
		shuffled.Entries[i].Versioned = !shuffled.Entries[i].Versioned
	}
	// Root and its manifest entry intentionally disagree on Versioned because it
	// is not part of the deletion token contract.
	shuffled.Root.Versioned = false

	got, err := deleteTreeTokenV3(shuffled)
	if err != nil {
		t.Fatalf("deleteTreeTokenV3(shuffled) error: %v", err)
	}
	if got != want {
		t.Fatalf("deleteTreeTokenV3(shuffled) = %q, want %q", got, want)
	}
}

func TestDeleteTreeTokenV3DistinguishesTreeShapeAndEntryType(t *testing.T) {
	root := FileInfo{
		Path:                "/root",
		Name:                "root",
		IsDir:               true,
		Mode:                os.ModeDir | 0o750,
		Size:                4096,
		ModTime:             time.Unix(1_700_000_000, 1),
		DeleteIdentityToken: "root-id",
	}
	directory := func(entryPath, name string) FileInfo {
		return FileInfo{
			Path:                entryPath,
			Name:                name,
			IsDir:               true,
			Mode:                os.ModeDir | 0o700,
			Size:                4096,
			ModTime:             time.Unix(1_700_000_001, 2),
			DeleteIdentityToken: "dir-id",
		}
	}
	file := func(entryPath, name string) FileInfo {
		return FileInfo{
			Path:                entryPath,
			Name:                name,
			Mode:                0o600,
			Size:                3,
			ModTime:             time.Unix(1_700_000_002, 3),
			DeleteIdentityToken: "file-id",
			ContentHash:         "file-hash",
		}
	}

	left := DeleteTargetSnapshot{
		Root: root,
		Entries: []FileInfo{
			root,
			directory("/root/a", "a"),
			file("/root/a/bc", "bc"),
		},
	}
	right := DeleteTargetSnapshot{
		Root: root,
		Entries: []FileInfo{
			root,
			directory("/root/ab", "ab"),
			file("/root/ab/c", "c"),
		},
	}
	leftToken, err := deleteTreeTokenV3(left)
	if err != nil {
		t.Fatalf("deleteTreeTokenV3(left) error: %v", err)
	}
	rightToken, err := deleteTreeTokenV3(right)
	if err != nil {
		t.Fatalf("deleteTreeTokenV3(right) error: %v", err)
	}
	if leftToken == rightToken {
		t.Fatalf("different tree shapes share token %q", leftToken)
	}

	fileRoot := FileInfo{
		Path:                "/item",
		Name:                "item",
		Mode:                0o700,
		Size:                1,
		ModTime:             time.Unix(1_700_000_003, 4),
		DeleteIdentityToken: "same-id",
		ContentHash:         "same-hash",
	}
	dirRoot := fileRoot
	dirRoot.IsDir = true
	dirRoot.Mode = os.ModeDir | 0o700
	fileToken, err := deleteTreeTokenV3(DeleteTargetSnapshot{Root: fileRoot, Entries: []FileInfo{fileRoot}})
	if err != nil {
		t.Fatalf("deleteTreeTokenV3(file) error: %v", err)
	}
	dirToken, err := deleteTreeTokenV3(DeleteTargetSnapshot{Root: dirRoot, Entries: []FileInfo{dirRoot}})
	if err != nil {
		t.Fatalf("deleteTreeTokenV3(directory) error: %v", err)
	}
	if fileToken == dirToken {
		t.Fatalf("file and directory share token %q", fileToken)
	}
}

func TestDeleteTreeTokenV3AllowsEmptyIdentityAndContentHash(t *testing.T) {
	entry := FileInfo{
		Path:    "/empty",
		Name:    "empty",
		Mode:    0o600,
		ModTime: time.Unix(1_700_000_000, 0),
	}
	token, err := deleteTreeTokenV3(DeleteTargetSnapshot{Root: entry, Entries: []FileInfo{entry}})
	if err != nil {
		t.Fatalf("deleteTreeTokenV3() error: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("deleteTreeTokenV3() token length = %d, want 64: %q", len(token), token)
	}
}

func TestDeleteTreeTokenV3ValidatesRootSlashName(t *testing.T) {
	root := FileInfo{
		Path:    "/",
		Name:    "/",
		IsDir:   true,
		Mode:    os.ModeDir | 0o755,
		ModTime: time.Unix(1_700_000_000, 0),
	}
	if _, err := deleteTreeTokenV3(DeleteTargetSnapshot{Root: root, Entries: []FileInfo{root}}); err != nil {
		t.Fatalf("deleteTreeTokenV3(root slash) error: %v", err)
	}

	root.Name = ""
	if _, err := deleteTreeTokenV3(DeleteTargetSnapshot{Root: root, Entries: []FileInfo{root}}); err == nil {
		t.Fatal("deleteTreeTokenV3(root slash with empty name) error = nil, want validation error")
	}
}

func TestDeleteTreeTokenV3RejectsMalformedSnapshot(t *testing.T) {
	base := deleteTreeMerkleFixture()
	tests := []struct {
		name   string
		mutate func(*DeleteTargetSnapshot)
	}{
		{
			name: "empty manifest",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries = nil
			},
		},
		{
			name: "relative root",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Root.Path = "root"
				snapshot.Entries[0].Path = "root"
			},
		},
		{
			name: "non canonical root",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Root.Path = "/root/"
				snapshot.Entries[0].Path = "/root/"
			},
		},
		{
			name: "non canonical entry",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].Path = "/root//a.txt"
			},
		},
		{
			name: "missing root entry",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries = snapshot.Entries[1:]
			},
		},
		{
			name: "duplicate root entry",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries = append(snapshot.Entries, snapshot.Entries[0])
			},
		},
		{
			name: "root token field mismatch",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Root.ContentHash = "different-root-hash"
			},
		},
		{
			name: "name does not match basename",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].Name = "other.txt"
			},
		},
		{
			name: "entry outside root",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].Path = "/outside/a.txt"
			},
		},
		{
			name: "duplicate entry path",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				duplicate := snapshot.Entries[1]
				snapshot.Entries = append(snapshot.Entries, duplicate)
			},
		},
		{
			name: "missing direct parent",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries = append(snapshot.Entries, FileInfo{
					Path:    "/root/missing/child.txt",
					Name:    "child.txt",
					Mode:    0o600,
					ModTime: time.Unix(1_700_000_004, 5),
				})
			},
		},
		{
			name: "file has child",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries = append(snapshot.Entries, FileInfo{
					Path:    "/root/a.txt/child",
					Name:    "child",
					Mode:    0o600,
					ModTime: time.Unix(1_700_000_004, 6),
				})
			},
		},
		{
			name: "directory with regular mode",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[2].Mode = 0o755
			},
		},
		{
			name: "file with directory mode",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].Mode = os.ModeDir | 0o640
			},
		},
		{
			name: "special file mode",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[1].Mode = os.ModeSymlink | 0o777
			},
		},
		{
			name: "directory with mixed type mode",
			mutate: func(snapshot *DeleteTargetSnapshot) {
				snapshot.Entries[2].Mode = os.ModeDir | os.ModeSymlink | 0o755
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot := cloneDeleteTreeSnapshot(base)
			testCase.mutate(&snapshot)
			if _, err := deleteTreeTokenV3(snapshot); err == nil {
				t.Fatal("deleteTreeTokenV3(malformed) error = nil, want validation error")
			}
		})
	}
}

func deleteTreeMerkleFixture() DeleteTargetSnapshot {
	root := FileInfo{
		Path:                "/root",
		Name:                "root",
		IsDir:               true,
		Mode:                os.ModeDir | 0o750,
		Size:                4096,
		ModTime:             time.Unix(1_700_000_000, 111),
		DeleteIdentityToken: "root-id",
		ContentHash:         "root-hash",
	}
	return DeleteTargetSnapshot{
		Root: root,
		Entries: []FileInfo{
			root,
			{
				Path:                "/root/a.txt",
				Name:                "a.txt",
				Mode:                0o640,
				Size:                3,
				ModTime:             time.Unix(1_700_000_001, 222),
				DeleteIdentityToken: "a-id",
				ContentHash:         "a-hash",
			},
			{
				Path:                "/root/nested",
				Name:                "nested",
				IsDir:               true,
				Mode:                os.ModeDir | 0o755,
				Size:                4096,
				ModTime:             time.Unix(1_700_000_002, 333),
				DeleteIdentityToken: "nested-id",
				ContentHash:         "nested-hash",
			},
			{
				Path:                "/root/nested/b.bin",
				Name:                "b.bin",
				Mode:                0o600,
				Size:                2,
				ModTime:             time.Unix(1_700_000_003, 444),
				DeleteIdentityToken: "b-id",
				ContentHash:         "b-hash",
				Versioned:           true,
			},
		},
	}
}

func cloneDeleteTreeSnapshot(snapshot DeleteTargetSnapshot) DeleteTargetSnapshot {
	clone := snapshot
	clone.Entries = append([]FileInfo(nil), snapshot.Entries...)
	return clone
}
