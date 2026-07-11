package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
)

func TestCopySourceTreeRecordsExactDataDirectoryManifest(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "snapshot", "data")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "manifest directories")
	mustWriteFile(t, filepath.Join(source, "after-exclude", "skip.txt"), "excluded")
	if err := os.Mkdir(filepath.Join(source, "empty"), 0o700); err != nil {
		t.Fatalf("Mkdir(empty) error: %v", err)
	}
	for path, mode := range map[string]os.FileMode{
		source:                                 0o750,
		filepath.Join(source, "after-exclude"): 0o710,
		filepath.Join(source, "docs"):          0o705,
		filepath.Join(source, "empty"):         0o500,
	} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatalf("Chmod(%s) error: %v", path, err)
		}
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(source, "empty"), 0o700)
		_ = os.Chmod(source, 0o700)
	})

	entries, directories, totalBytes, err := copySourceTree(context.Background(), source, destination, []string{"after-exclude/skip.txt"})
	if err != nil {
		t.Fatalf("copySourceTree() error: %v", err)
	}
	wantDirectories := []ManifestDirectory{
		{ArchivePath: "data", Mode: 0o750},
		{ArchivePath: "data/after-exclude", Mode: 0o710},
		{ArchivePath: "data/docs", Mode: 0o705},
		{ArchivePath: "data/empty", Mode: 0o500},
	}
	sort.Slice(directories, func(i, j int) bool {
		return directories[i].ArchivePath < directories[j].ArchivePath
	})
	if !reflect.DeepEqual(directories, wantDirectories) {
		t.Fatalf("copySourceTree() directories = %#v, want %#v", directories, wantDirectories)
	}
	if len(entries) != 1 || entries[0].ArchivePath != "data/docs/note.txt" {
		t.Fatalf("copySourceTree() entries = %#v, want only data/docs/note.txt", entries)
	}
	if totalBytes != int64(len("manifest directories")) {
		t.Fatalf("copySourceTree() total bytes = %d, want %d", totalBytes, len("manifest directories"))
	}
	for _, directory := range directories {
		if directory.ArchivePath == "config" || strings.HasPrefix(directory.ArchivePath, "config/") {
			t.Fatalf("copySourceTree() recorded config directory: %#v", directory)
		}
	}
	for _, relPath := range []string{"after-exclude", "docs", "empty"} {
		info, err := os.Lstat(filepath.Join(destination, relPath))
		if err != nil || !info.IsDir() {
			t.Fatalf("copied directory %q info/error = %+v/%v", relPath, info, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(destination, "after-exclude", "skip.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("excluded destination file stat error = %v, want not exist", err)
	}
}

func TestValidateManifestEntriesRejectsInvalidDirectoryManifest(t *testing.T) {
	validDigest := strings.Repeat("a", 64)
	tests := []struct {
		name        string
		directories []ManifestDirectory
		entryPath   string
	}{
		{
			name: "backslash archive path",
			directories: []ManifestDirectory{
				{ArchivePath: "data", Mode: 0o700},
				{ArchivePath: `data\docs`, Mode: 0o700},
			},
			entryPath: "data/note.txt",
		},
		{
			name: "unsorted directories",
			directories: []ManifestDirectory{
				{ArchivePath: "data", Mode: 0o700},
				{ArchivePath: "data/z", Mode: 0o700},
				{ArchivePath: "data/a", Mode: 0o700},
			},
			entryPath: "data/a/note.txt",
		},
		{
			name: "duplicate directory",
			directories: []ManifestDirectory{
				{ArchivePath: "data", Mode: 0o700},
				{ArchivePath: "data", Mode: 0o700},
			},
			entryPath: "data/note.txt",
		},
		{
			name: "missing parent directory",
			directories: []ManifestDirectory{
				{ArchivePath: "data", Mode: 0o700},
				{ArchivePath: "data/docs/reports", Mode: 0o700},
			},
			entryPath: "data/note.txt",
		},
		{
			name: "special permission bit",
			directories: []ManifestDirectory{
				{ArchivePath: "data", Mode: 0o1700},
			},
			entryPath: "data/note.txt",
		},
		{
			name: "file parent directory not registered",
			directories: []ManifestDirectory{
				{ArchivePath: "data", Mode: 0o700},
			},
			entryPath: "data/docs/note.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := Manifest{
				Version:     manifestVersion,
				FileCount:   1,
				TotalBytes:  1,
				Directories: tt.directories,
				Entries: []ManifestEntry{
					{
						ArchivePath: tt.entryPath,
						Size:        1,
						Mode:        0o600,
						SHA256:      validDigest,
					},
				},
			}

			if err := validateManifestEntries(manifest); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("validateManifestEntries() error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestVerifyManifestFilesRejectsDirectoryManifestMismatch(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, root string)
		directories []ManifestDirectory
	}{
		{
			name: "extra directory",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "data", "injected"), 0o700); err != nil {
					t.Fatalf("MkdirAll(injected directory) error: %v", err)
				}
			},
			directories: testManifestDirectories(),
		},
		{
			name: "missing directory",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "data"), 0o700); err != nil {
					t.Fatalf("Mkdir(data) error: %v", err)
				}
			},
			directories: testManifestDirectories("data/missing"),
		},
		{
			name: "directory mode mismatch",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "data"), 0o700); err != nil {
					t.Fatalf("Mkdir(data) error: %v", err)
				}
				if err := os.Chmod(filepath.Join(root, "data"), 0o700); err != nil {
					t.Fatalf("Chmod(data) error: %v", err)
				}
			},
			directories: []ManifestDirectory{{ArchivePath: "data", Mode: 0o755}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := secureBackupTestTempDir(t)
			tt.setup(t, root)
			manifest := Manifest{
				Version:     manifestVersion,
				Directories: tt.directories,
				Entries:     []ManifestEntry{},
			}
			if err := writeJSONFile(filepath.Join(root, manifestFileName), manifest, 0o600); err != nil {
				t.Fatalf("writeJSONFile(manifest) error: %v", err)
			}

			_, _, err := verifyManifestFiles(context.Background(), root, manifest)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("verifyManifestFiles() error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestManifestTreeVerificationEnforcesLayoutManifestFile(t *testing.T) {
	manifest := Manifest{
		Version:     manifestVersion,
		Directories: testManifestDirectories(),
		Entries:     []ManifestEntry{},
	}

	t.Run("restored layout rejects manifest", func(t *testing.T) {
		root := secureBackupTestTempDir(t)
		if err := os.Mkdir(filepath.Join(root, "data"), 0o700); err != nil {
			t.Fatalf("Mkdir(data) error: %v", err)
		}
		if err := writeJSONFile(filepath.Join(root, manifestFileName), manifest, 0o600); err != nil {
			t.Fatalf("writeJSONFile(manifest) error: %v", err)
		}

		if _, _, err := verifyRestoredManifestFiles(context.Background(), root, manifest); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("verifyRestoredManifestFiles() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("snapshot layout requires manifest", func(t *testing.T) {
		root := secureBackupTestTempDir(t)
		if err := os.Mkdir(filepath.Join(root, "data"), 0o700); err != nil {
			t.Fatalf("Mkdir(data) error: %v", err)
		}

		if _, _, err := verifyManifestFiles(context.Background(), root, manifest); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("verifyManifestFiles() error = %v, want ErrUnsafePath", err)
		}
	})
}

func TestRestoreManifestDirectoriesAppliesManifestModes(t *testing.T) {
	targetRoot := filepath.Join(secureBackupTestTempDir(t), "restore-target")
	directories := []ManifestDirectory{
		{ArchivePath: "data", Mode: 0o750},
		{ArchivePath: "data/readonly", Mode: 0o500},
		{ArchivePath: "data/shared", Mode: 0o775},
	}

	directoryModes, err := restoreManifestDirectories(context.Background(), targetRoot, directories)
	if err != nil {
		t.Fatalf("restoreManifestDirectories() error: %v", err)
	}
	if err := applyDirectoryModesNoFollow(targetRoot, directoryModes, "restore target"); err != nil {
		t.Fatalf("applyDirectoryModesNoFollow() error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(targetRoot, "readonly"), 0o700)
	})

	assertPathMode(t, targetRoot, 0o750)
	assertPathMode(t, filepath.Join(targetRoot, "readonly"), 0o500)
	assertPathMode(t, filepath.Join(targetRoot, "shared"), 0o775)
}

func TestManager_ExplicitConfigRestoreRejectsReservedDataNamespaceConflict(t *testing.T) {
	tests := []struct {
		name        string
		setupSource func(t *testing.T, source string)
		assertData  func(t *testing.T, target string)
	}{
		{
			name: "directory",
			setupSource: func(t *testing.T, source string) {
				t.Helper()
				mustWriteFile(t, filepath.Join(source, ".mnemonas-restore", "data.txt"), "reserved directory data")
			},
			assertData: func(t *testing.T, target string) {
				t.Helper()
				assertFileContent(t, filepath.Join(target, ".mnemonas-restore", "data.txt"), "reserved directory data")
			},
		},
		{
			name: "config file name as user data",
			setupSource: func(t *testing.T, source string) {
				t.Helper()
				mustWriteFile(t, filepath.Join(source, ".mnemonas-restore", "config.toml"), "user data, not backup config")
			},
			assertData: func(t *testing.T, target string) {
				t.Helper()
				assertFileContent(t, filepath.Join(target, ".mnemonas-restore", "config.toml"), "user data, not backup config")
			},
		},
		{
			name: "regular file",
			setupSource: func(t *testing.T, source string) {
				t.Helper()
				mustWriteFile(t, filepath.Join(source, ".mnemonas-restore"), "reserved file data")
			},
			assertData: func(t *testing.T, target string) {
				t.Helper()
				assertFileContent(t, filepath.Join(target, ".mnemonas-restore"), "reserved file data")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := secureBackupTestTempDir(t)
			source := filepath.Join(tmpDir, "source")
			destination := filepath.Join(tmpDir, "backups")
			configPath := filepath.Join(tmpDir, "mnemonas.toml")
			tt.setupSource(t, source)
			mustWriteFile(t, configPath, "[server]\nport = 8080\n")

			manager, err := newBackupTestManager(t, ManagerConfig{
				Root:        filepath.Join(tmpDir, "state"),
				StorageRoot: source,
				ConfigPath:  configPath,
				Jobs: []config.BackupJobConfig{{
					ID:            "home",
					Name:          "Home backup",
					Type:          JobTypeLocal,
					Source:        source,
					Destination:   destination,
					IncludeConfig: true,
				}},
			})
			if err != nil {
				t.Fatalf("NewManager() error: %v", err)
			}
			run, err := manager.RunJob(context.Background(), "home")
			if err != nil {
				t.Fatalf("RunJob() error: %v", err)
			}
			manifest, err := readManifest(run.ManifestPath)
			if err != nil {
				t.Fatalf("readManifest() error: %v", err)
			}
			assertManifestHasConfigAndReservedDataPath(t, manifest)

			conflictingPreviewTarget := filepath.Join(tmpDir, "preview-conflict")
			if _, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
				TargetPath:    conflictingPreviewTarget,
				IncludeConfig: true,
			}); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("RunRestorePreview(include config) error = %v, want ErrUnsafePath", err)
			}
			conflictingRestoreTarget := filepath.Join(tmpDir, "restore-conflict")
			if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{
				TargetPath:    conflictingRestoreTarget,
				IncludeConfig: true,
			}); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("RunRestore(include config) error = %v, want ErrUnsafePath", err)
			}
			if _, err := os.Lstat(conflictingRestoreTarget); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("conflicting restore target stat error = %v, want not exist", err)
			}

			safeTarget := filepath.Join(tmpDir, "restore-data-only")
			if _, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: safeTarget}); err != nil {
				t.Fatalf("RunRestorePreview(data only) error: %v", err)
			}
			restore, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: safeTarget})
			if err != nil {
				t.Fatalf("RunRestore(data only) error: %v", err)
			}
			if restore.ConfigRestored {
				t.Fatalf("RunRestore(data only) restored config: %+v", restore)
			}
			tt.assertData(t, safeTarget)
			if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: filepath.Join(tmpDir, "restore-data-only-latest")}); err != nil {
				t.Fatalf("RunRestore(second data only) error: %v", err)
			}
			verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: safeTarget})
			if err != nil {
				t.Fatalf("RunRestoreVerify(data only) error: %v", err)
			}
			for _, warning := range verify.Warnings {
				if strings.Contains(warning, ".mnemonas-restore") {
					t.Fatalf("RunRestoreVerify(data only) emitted reserved data namespace warning %q; all warnings=%#v", warning, verify.Warnings)
				}
			}
		})
	}
}

func assertManifestHasConfigAndReservedDataPath(t *testing.T, manifest Manifest) {
	t.Helper()
	var hasConfig bool
	var hasReservedDataPath bool
	for _, directory := range manifest.Directories {
		if directory.ArchivePath == "data/.mnemonas-restore" {
			hasReservedDataPath = true
		}
	}
	for _, entry := range manifest.Entries {
		switch entry.ArchivePath {
		case "config/config.toml":
			hasConfig = true
		case "data/.mnemonas-restore":
			hasReservedDataPath = true
		}
	}
	if !hasConfig || !hasReservedDataPath {
		t.Fatalf("manifest config/reserved namespace evidence = %t/%t: %+v", hasConfig, hasReservedDataPath, manifest)
	}
}

func TestManager_RestoreVerifyRequiresRestoredConfigEvidence(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	configPath := filepath.Join(tmpDir, "mnemonas.toml")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "config verification")
	mustWriteFile(t, configPath, "[server]\nport = 8080\n")
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		ConfigPath:  configPath,
		Jobs: []config.BackupJobConfig{{
			ID:            "home",
			Type:          JobTypeLocal,
			Source:        source,
			Destination:   destination,
			IncludeConfig: true,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	t.Run("missing config file", func(t *testing.T) {
		target := filepath.Join(tmpDir, "missing-config")
		if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: target, IncludeConfig: true}); err != nil {
			t.Fatalf("RunRestore() error: %v", err)
		}
		if err := os.Remove(filepath.Join(target, ".mnemonas-restore", "config.toml")); err != nil {
			t.Fatalf("Remove(config) error: %v", err)
		}
		verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: target})
		if err != nil {
			t.Fatalf("RunRestoreVerify() error: %v", err)
		}
		assertWarningContains(t, verify.Warnings, "恢复目标缺少对照备份配置文件")
	})

	t.Run("config directory mode", func(t *testing.T) {
		target := filepath.Join(tmpDir, "config-mode")
		if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: target, IncludeConfig: true}); err != nil {
			t.Fatalf("RunRestore() error: %v", err)
		}
		if err := os.Chmod(filepath.Join(target, ".mnemonas-restore"), 0o755); err != nil {
			t.Fatalf("Chmod(config directory) error: %v", err)
		}
		verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: target})
		if err != nil {
			t.Fatalf("RunRestoreVerify() error: %v", err)
		}
		assertWarningContains(t, verify.Warnings, "恢复目标配置目录权限不匹配")
	})

	t.Run("case alias keeps matching restore config requirement", func(t *testing.T) {
		target := filepath.Join(tmpDir, "Case-Alias-Target")
		if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: target, IncludeConfig: true}); err != nil {
			t.Fatalf("RunRestore() error: %v", err)
		}
		alias := strings.ToLower(target)
		if alias == target {
			t.Fatal("case-alias fixture did not change path spelling")
		}
		if _, err := os.Lstat(alias); errors.Is(err, os.ErrNotExist) {
			t.Skip("filesystem is case-sensitive")
		} else if err != nil {
			t.Fatalf("Lstat(case alias) error: %v", err)
		}
		if err := os.Remove(filepath.Join(target, ".mnemonas-restore", "config.toml")); err != nil {
			t.Fatalf("Remove(config) error: %v", err)
		}
		verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: alias})
		if err != nil {
			t.Fatalf("RunRestoreVerify(case alias) error: %v", err)
		}
		if verify.TargetPath != target {
			t.Fatalf("RunRestoreVerify(case alias) target = %q, want bound restore target %q", verify.TargetPath, target)
		}
		assertWarningContains(t, verify.Warnings, "恢复目标缺少对照备份配置文件")
	})
}

func TestManager_LocalRetentionPinsLegacyAndRestoreReferencedSnapshots(t *testing.T) {
	t.Run("legacy v1 does not block v2 pruning", func(t *testing.T) {
		tmpDir := secureBackupTestTempDir(t)
		manager := newRetentionManifestV2TestManager(t, tmpDir, 1)
		current := time.Date(2026, 7, 16, 1, 0, 0, 0, time.UTC)
		manager.now = func() time.Time { return current }
		first, err := manager.RunJob(context.Background(), "home")
		if err != nil {
			t.Fatalf("RunJob(first) error: %v", err)
		}
		manifest, err := readManifest(first.ManifestPath)
		if err != nil {
			t.Fatalf("readManifest(first) error: %v", err)
		}
		manifest.Version = 1
		manifest.Directories = nil
		if err := writeJSONFile(first.ManifestPath, manifest, 0o600); err != nil {
			t.Fatalf("writeJSONFile(v1 manifest) error: %v", err)
		}

		current = current.Add(time.Minute)
		second, err := manager.RunJob(context.Background(), "home")
		if err != nil {
			t.Fatalf("RunJob(second) error: %v", err)
		}
		current = current.Add(time.Minute)
		third, err := manager.RunJob(context.Background(), "home")
		if err != nil {
			t.Fatalf("RunJob(third) error: %v", err)
		}
		if _, err := os.Lstat(first.SnapshotPath); err != nil {
			t.Fatalf("legacy snapshot was not pinned: %v", err)
		}
		if _, err := os.Lstat(second.SnapshotPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("older v2 snapshot stat error = %v, want not exist", err)
		}
		if _, err := os.Lstat(third.SnapshotPath); err != nil {
			t.Fatalf("current v2 snapshot missing: %v", err)
		}
		assertWarningContains(t, third.Warnings, "v1 本地快照")
	})

	t.Run("restore history and latest restore references pin snapshots", func(t *testing.T) {
		tmpDir := secureBackupTestTempDir(t)
		manager := newRetentionManifestV2TestManager(t, tmpDir, 1)
		current := time.Date(2026, 7, 16, 2, 0, 0, 0, time.UTC)
		manager.now = func() time.Time { return current }
		first, err := manager.RunJob(context.Background(), "home")
		if err != nil {
			t.Fatalf("RunJob(first) error: %v", err)
		}
		if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: filepath.Join(tmpDir, "restored-first")}); err != nil {
			t.Fatalf("RunRestore(first) error: %v", err)
		}
		current = current.Add(time.Minute)
		second, err := manager.RunJob(context.Background(), "home")
		if err != nil {
			t.Fatalf("RunJob(second) error: %v", err)
		}
		if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: filepath.Join(tmpDir, "restored-second")}); err != nil {
			t.Fatalf("RunRestore(second) error: %v", err)
		}
		current = current.Add(time.Minute)
		third, err := manager.RunJob(context.Background(), "home")
		if err != nil {
			t.Fatalf("RunJob(third) error: %v", err)
		}
		current = current.Add(time.Minute)
		fourth, err := manager.RunJob(context.Background(), "home")
		if err != nil {
			t.Fatalf("RunJob(fourth) error: %v", err)
		}
		if _, err := os.Lstat(first.SnapshotPath); err != nil {
			t.Fatalf("restore-history-referenced snapshot was not pinned: %v", err)
		}
		if _, err := os.Lstat(second.SnapshotPath); err != nil {
			t.Fatalf("latest-restore-referenced snapshot was not pinned: %v", err)
		}
		if _, err := os.Lstat(third.SnapshotPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unreferenced v2 snapshot stat error = %v, want not exist", err)
		}
		if _, err := os.Lstat(fourth.SnapshotPath); err != nil {
			t.Fatalf("current v2 snapshot missing: %v", err)
		}
	})
}

func TestRestoreTargetPathsMatchCaseAliasOnInsensitiveFilesystem(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	target := filepath.Join(tmpDir, "Case-Target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}
	alias := strings.ToLower(target)
	if _, err := os.Lstat(alias); errors.Is(err, os.ErrNotExist) {
		t.Skip("filesystem is case-sensitive")
	} else if err != nil {
		t.Fatalf("Lstat(case alias) error: %v", err)
	}
	if !restoreTargetPathsMatch(target, alias) {
		t.Fatalf("restoreTargetPathsMatch(%q, %q) = false on a case-insensitive filesystem", target, alias)
	}
}

func TestExplicitRestoreTreeRejectsCaseFoldedTopology(t *testing.T) {
	root := secureBackupTestTempDir(t)
	if err := os.Mkdir(filepath.Join(root, "A"), 0o700); err != nil {
		t.Fatalf("Mkdir(A) error: %v", err)
	}
	manifest := Manifest{
		Version: manifestVersion,
		Directories: []ManifestDirectory{
			{ArchivePath: "data", Mode: 0o700},
			{ArchivePath: "data/A", Mode: 0o700},
			{ArchivePath: "data/a", Mode: 0o700},
		},
		Entries: []ManifestEntry{},
	}
	if err := verifyExplicitRestoreTreeContents(context.Background(), root, manifest, false); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("verifyExplicitRestoreTreeContents() error = %v, want ErrUnsafePath", err)
	}

	configConflict := Manifest{
		Version:   manifestVersion,
		FileCount: 1,
		Directories: []ManifestDirectory{
			{ArchivePath: "data", Mode: 0o700},
			{ArchivePath: "data/.MNEMONAS-RESTORE", Mode: 0o700},
		},
		Entries: []ManifestEntry{{
			ArchivePath: "config/config.toml",
			Mode:        0o600,
			SHA256:      strings.Repeat("a", 64),
		}},
		ConfigPath: "/etc/mnemonas/config.toml",
	}
	if err := validateExplicitConfigRestoreNamespace(configConflict, true); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("validateExplicitConfigRestoreNamespace() error = %v, want ErrUnsafePath", err)
	}
}

func TestExplicitRestoreTreeAllowsDistinctCaseOnSensitiveFilesystem(t *testing.T) {
	root := secureBackupTestTempDir(t)
	upper := filepath.Join(root, "A")
	lower := filepath.Join(root, "a")
	if err := os.Mkdir(upper, 0o700); err != nil {
		t.Fatalf("Mkdir(A) error: %v", err)
	}
	if err := os.Mkdir(lower, 0o700); errors.Is(err, os.ErrExist) {
		t.Skip("filesystem is case-insensitive")
	} else if err != nil {
		t.Fatalf("Mkdir(a) error: %v", err)
	}
	upperInfo, upperErr := os.Lstat(upper)
	lowerInfo, lowerErr := os.Lstat(lower)
	if upperErr != nil || lowerErr != nil {
		t.Fatalf("Lstat(case-distinct directories) errors = %v/%v", upperErr, lowerErr)
	}
	if os.SameFile(upperInfo, lowerInfo) {
		t.Skip("filesystem folds case-distinct directory names")
	}
	manifest := Manifest{
		Version: manifestVersion,
		Directories: []ManifestDirectory{
			{ArchivePath: "data", Mode: 0o700},
			{ArchivePath: "data/A", Mode: 0o700},
			{ArchivePath: "data/a", Mode: 0o700},
		},
		Entries: []ManifestEntry{},
	}
	if err := verifyExplicitRestoreTreeContents(context.Background(), root, manifest, false); err != nil {
		t.Fatalf("verifyExplicitRestoreTreeContents() error: %v", err)
	}
}

func TestRestoreStagingIdentityRejectsDirectoryReplacement(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	target := filepath.Join(tmpDir, "restore-target")
	stage, err := createNamedRestoreTarget(target, ".partial-test")
	if err != nil {
		t.Fatalf("createNamedRestoreTarget() error: %v", err)
	}
	original := stage.Path + ".original"
	if err := os.Rename(stage.Path, original); err != nil {
		t.Fatalf("Rename(staging) error: %v", err)
	}
	if err := os.Mkdir(stage.Path, 0o700); err != nil {
		t.Fatalf("Mkdir(replacement) error: %v", err)
	}
	if err := installRestoreTarget(stage, target); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("installRestoreTarget() error = %v, want ErrUnsafePath", err)
	}
	if err := removeRestoreStagingTarget(stage, "restore staging target"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("removeRestoreStagingTarget() error = %v, want ErrUnsafePath", err)
	}
	if _, err := os.Lstat(stage.Path); err != nil {
		t.Fatalf("replacement staging directory was removed: %v", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", err)
	}
}

func TestManager_LocalRestoreAppliesWritableRootModeOnlyAtInstall(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "writable root mode")
	if err := os.Chmod(source, 0o777); err != nil {
		t.Fatalf("Chmod(source) error: %v", err)
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	target := filepath.Join(tmpDir, "restore-target")
	if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: target}); err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	assertPathMode(t, target, 0o777)
	assertFileContent(t, filepath.Join(target, "note.txt"), "writable root mode")
}

func newRetentionManifestV2TestManager(t *testing.T, tmpDir string, maxSnapshots int) *Manager {
	t.Helper()
	source := filepath.Join(tmpDir, "source")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "retention evidence")
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:           "home",
			Type:         JobTypeLocal,
			Source:       source,
			Destination:  filepath.Join(tmpDir, "backups"),
			MaxSnapshots: maxSnapshots,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	return manager
}

func assertWarningContains(t *testing.T, warnings []string, substring string) {
	t.Helper()
	for _, warning := range warnings {
		if strings.Contains(warning, substring) {
			return
		}
	}
	t.Fatalf("warnings = %#v, want entry containing %q", warnings, substring)
}
