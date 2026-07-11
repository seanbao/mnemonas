package backup

import (
	"os"
	"path"
	"sort"
	"strings"
	"testing"
)

func secureBackupTestTempDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod(test temp dir) error: %v", err)
	}
	return dir
}

func newBackupTestManager(t testing.TB, cfg ManagerConfig) (*Manager, error) {
	t.Helper()
	manager, err := NewManager(cfg)
	if err == nil {
		t.Cleanup(func() {
			if closeErr := manager.Close(); closeErr != nil {
				t.Errorf("Close(test backup manager) error: %v", closeErr)
			}
		})
	}
	return manager, err
}

func testManifestDirectories(archivePaths ...string) []ManifestDirectory {
	directories := map[string]struct{}{"data": {}}
	for _, archivePath := range archivePaths {
		for current := strings.TrimSpace(archivePath); strings.HasPrefix(current, "data/"); current = path.Dir(current) {
			directories[current] = struct{}{}
			if current == "data" {
				break
			}
		}
	}

	paths := make([]string, 0, len(directories))
	for archivePath := range directories {
		paths = append(paths, archivePath)
	}
	sort.Strings(paths)

	result := make([]ManifestDirectory, 0, len(paths))
	for _, archivePath := range paths {
		result = append(result, ManifestDirectory{ArchivePath: archivePath, Mode: 0o700})
	}
	return result
}
