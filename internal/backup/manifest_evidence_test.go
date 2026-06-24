package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/config"
)

func TestRunLocalBackupRejectsManifestReplacementAfterFinalize(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	writeManifestEvidenceTestFile(t, filepath.Join(source, "note.txt"), []byte("trusted backup content"))

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	originalHook := afterFinalizeLocalBackupSnapshot
	var finalizedPath string
	var hookErr error
	afterFinalizeLocalBackupSnapshot = func(snapshotPath string) {
		finalizedPath = snapshotPath
		manifestPath := filepath.Join(snapshotPath, manifestFileName)
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			hookErr = err
			return
		}
		replacementData := append([]byte(nil), manifestData...)
		marker := []byte("MnemoNAS local directory snapshot")
		markerIndex := bytes.Index(replacementData, marker)
		if markerIndex < 0 {
			hookErr = errors.New("manifest description marker not found")
			return
		}
		replacementData[markerIndex] = 'm'
		replacementPath := manifestPath + ".replacement"
		if err := os.WriteFile(replacementPath, replacementData, 0600); err != nil {
			hookErr = err
			return
		}
		if err := os.Rename(replacementPath, manifestPath); err != nil {
			hookErr = err
		}
	}
	t.Cleanup(func() { afterFinalizeLocalBackupSnapshot = originalHook })

	result, err := manager.RunJob(context.Background(), "home")
	if hookErr != nil {
		t.Fatalf("manifest replacement hook error: %v", hookErr)
	}
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunJob() error = %v, want ErrUnsafePath", err)
	}
	if result == nil || result.Status != StatusFailed {
		t.Fatalf("RunJob() result = %+v, want failed result", result)
	}
	if finalizedPath == "" {
		t.Fatal("expected finalized snapshot hook to run")
	}
	if _, statErr := os.Lstat(finalizedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("finalized snapshot stat error = %v, want removed snapshot", statErr)
	}
}

func TestTrustedManifestEvidenceRejectsCoordinatedManifestAndSnapshotTampering(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	writeManifestEvidenceTestFile(t, filepath.Join(source, "note.txt"), []byte("original snapshot bytes"))

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
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

	manager.mu.Lock()
	trustedRun := cloneRunResultRaw(manager.state.Jobs["home"].LastSuccessfulRun)
	manager.mu.Unlock()
	if trustedRun == nil || trustedRun.ManifestSize <= 0 || trustedRun.ManifestDigest == "" {
		t.Fatalf("persisted successful run lacks trusted manifest evidence: %+v", trustedRun)
	}

	manifestData, err := os.ReadFile(trustedRun.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest) error: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("Unmarshal(manifest) error: %v", err)
	}
	if len(manifest.Entries) != 1 {
		t.Fatalf("manifest entries = %d, want 1", len(manifest.Entries))
	}

	archivePath := filepath.Join(trustedRun.SnapshotPath, filepath.FromSlash(manifest.Entries[0].ArchivePath))
	originalContent, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("ReadFile(snapshot entry) error: %v", err)
	}
	tamperedContent := bytes.Repeat([]byte{'x'}, len(originalContent))
	if bytes.Equal(tamperedContent, originalContent) {
		tamperedContent[0] = 'y'
	}
	if err := os.WriteFile(archivePath, tamperedContent, 0600); err != nil {
		t.Fatalf("WriteFile(tampered snapshot entry) error: %v", err)
	}
	tamperedDigest := sha256.Sum256(tamperedContent)
	manifest.Entries[0].SHA256 = hex.EncodeToString(tamperedDigest[:])
	tamperedManifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(tampered manifest) error: %v", err)
	}
	if len(tamperedManifestData) != len(manifestData) {
		t.Fatalf("tampered manifest size = %d, want unchanged size %d", len(tamperedManifestData), len(manifestData))
	}
	replacementPath := trustedRun.ManifestPath + ".replacement"
	if err := os.WriteFile(replacementPath, tamperedManifestData, 0600); err != nil {
		t.Fatalf("WriteFile(tampered manifest) error: %v", err)
	}
	if err := os.Rename(replacementPath, trustedRun.ManifestPath); err != nil {
		t.Fatalf("Rename(tampered manifest) error: %v", err)
	}

	operations := []struct {
		name string
		run  func() error
	}{
		{
			name: "restore preview",
			run: func() error {
				_, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
					TargetPath: filepath.Join(tmpDir, "preview-target"),
				})
				return err
			},
		},
		{
			name: "restore",
			run: func() error {
				_, err := manager.RunRestore(context.Background(), "home", RestoreOptions{
					TargetPath: filepath.Join(tmpDir, "restore-target"),
				})
				return err
			},
		},
		{
			name: "restore drill",
			run: func() error {
				_, err := manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{KeepArtifact: true})
				return err
			},
		},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("operation error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestManifestDigestPersistsButIsOmittedFromPublicRunResults(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	run := &RunResult{ManifestSize: 1234, ManifestDigest: digest}
	persistedPayload, err := json.Marshal(persistedState{Jobs: map[string]JobState{
		"home": {LastSuccessfulRun: run},
	}})
	if err != nil {
		t.Fatalf("Marshal(persisted state) error: %v", err)
	}
	if !bytes.Contains(persistedPayload, []byte(`"manifest_size":1234`)) || !bytes.Contains(persistedPayload, []byte(`"manifest_digest":"`+digest+`"`)) {
		t.Fatalf("persisted payload omitted manifest evidence: %s", persistedPayload)
	}

	publicRun := cloneRunResult(run)
	if publicRun.ManifestSize != 0 || publicRun.ManifestDigest != "" {
		t.Fatalf("public run exposed manifest evidence: %+v", publicRun)
	}
	publicPayload, err := json.Marshal(publicRun)
	if err != nil {
		t.Fatalf("Marshal(public run) error: %v", err)
	}
	if bytes.Contains(publicPayload, []byte("manifest_size")) || bytes.Contains(publicPayload, []byte("manifest_digest")) || bytes.Contains(publicPayload, []byte(digest)) {
		t.Fatalf("public payload exposed manifest evidence: %s", publicPayload)
	}
}

func writeManifestEvidenceTestFile(t *testing.T, filePath string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0700); err != nil {
		t.Fatalf("MkdirAll(%s) error: %v", filepath.Dir(filePath), err)
	}
	if err := os.WriteFile(filePath, data, 0600); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", filePath, err)
	}
}
