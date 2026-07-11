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
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	writeManifestEvidenceTestFile(t, filepath.Join(source, "note.txt"), []byte("trusted backup content"))

	manager, err := newBackupTestManager(t, ManagerConfig{
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
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	writeManifestEvidenceTestFile(t, filepath.Join(source, "note.txt"), []byte("original snapshot bytes"))

	manager, err := newBackupTestManager(t, ManagerConfig{
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

func TestLocalRestoreSnapshotManifestRejectsSameSizeSemanticTampering(t *testing.T) {
	_, job, restore := newRestoreManifestEvidenceFixture(t)
	if _, _, _, err := localRestoreSnapshotManifest(job, restore); err != nil {
		t.Fatalf("localRestoreSnapshotManifest() baseline error: %v", err)
	}

	manifestData, err := os.ReadFile(restore.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest) error: %v", err)
	}
	tamperedData := append([]byte(nil), manifestData...)
	marker := []byte("MnemoNAS local directory snapshot")
	markerIndex := bytes.Index(tamperedData, marker)
	if markerIndex < 0 {
		t.Fatal("manifest description marker not found")
	}
	tamperedData[markerIndex] = 'm'
	if len(tamperedData) != len(manifestData) {
		t.Fatalf("tampered manifest size = %d, want unchanged size %d", len(tamperedData), len(manifestData))
	}
	if err := os.WriteFile(restore.ManifestPath, tamperedData, 0o600); err != nil {
		t.Fatalf("WriteFile(tampered manifest) error: %v", err)
	}

	if _, _, _, err := localRestoreSnapshotManifest(job, restore); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("localRestoreSnapshotManifest() error = %v, want ErrUnsafePath", err)
	}
}

func TestLocalRestoreSnapshotManifestRejectsSameSemanticByteTampering(t *testing.T) {
	_, job, restore := newRestoreManifestEvidenceFixture(t)
	manifestData, err := os.ReadFile(restore.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest) error: %v", err)
	}
	tamperedData := append([]byte(nil), manifestData...)
	indent := []byte("\n  \"version\"")
	indentIndex := bytes.Index(tamperedData, indent)
	if indentIndex < 0 {
		t.Fatal("manifest indentation marker not found")
	}
	tamperedData[indentIndex+1] = '\t'
	if len(tamperedData) != len(manifestData) {
		t.Fatalf("tampered manifest size = %d, want unchanged size %d", len(tamperedData), len(manifestData))
	}
	var before Manifest
	var after Manifest
	if err := json.Unmarshal(manifestData, &before); err != nil {
		t.Fatalf("Unmarshal(original manifest) error: %v", err)
	}
	if err := json.Unmarshal(tamperedData, &after); err != nil {
		t.Fatalf("Unmarshal(tampered manifest) error: %v", err)
	}
	beforeDigest, err := manifestSemanticDigest(before)
	if err != nil {
		t.Fatalf("manifestSemanticDigest(original) error: %v", err)
	}
	afterDigest, err := manifestSemanticDigest(after)
	if err != nil {
		t.Fatalf("manifestSemanticDigest(tampered) error: %v", err)
	}
	if beforeDigest != afterDigest {
		t.Fatalf("semantic digest changed: %q != %q", beforeDigest, afterDigest)
	}
	if err := os.WriteFile(restore.ManifestPath, tamperedData, 0o600); err != nil {
		t.Fatalf("WriteFile(tampered manifest) error: %v", err)
	}

	if _, _, _, err := localRestoreSnapshotManifest(job, restore); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("localRestoreSnapshotManifest() error = %v, want ErrUnsafePath", err)
	}
}

func TestLocalRestoreSnapshotManifestRejectsMismatchedRecordedManifestPath(t *testing.T) {
	_, job, restore := newRestoreManifestEvidenceFixture(t)
	restore.ManifestPath = filepath.Join(restore.SnapshotPath, "other-manifest.json")

	if _, _, _, err := localRestoreSnapshotManifest(job, restore); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("localRestoreSnapshotManifest() error = %v, want ErrUnsafePath", err)
	}
}

func TestRestoreManifestDigestPersistsButIsOmittedFromPublicRestoreResults(t *testing.T) {
	digest := "sha256:" + strings.Repeat("b", 64)
	restore := &RestoreResult{ManifestSize: 2345, ManifestDigest: digest}
	persistedPayload, err := json.Marshal(persistedState{Jobs: map[string]JobState{
		"home": {LastRestore: restore},
	}})
	if err != nil {
		t.Fatalf("Marshal(persisted state) error: %v", err)
	}
	if !bytes.Contains(persistedPayload, []byte(`"manifest_size":2345`)) || !bytes.Contains(persistedPayload, []byte(`"manifest_digest":"`+digest+`"`)) {
		t.Fatalf("persisted payload omitted restore manifest evidence: %s", persistedPayload)
	}

	rawRestore := cloneRestoreResultRaw(restore)
	if rawRestore.ManifestSize != restore.ManifestSize || rawRestore.ManifestDigest != restore.ManifestDigest {
		t.Fatalf("raw restore clone lost manifest evidence: %+v", rawRestore)
	}
	publicRestore := cloneRestoreResult(restore)
	if publicRestore.ManifestSize != 0 || publicRestore.ManifestDigest != "" {
		t.Fatalf("public restore exposed manifest evidence: %+v", publicRestore)
	}
	publicPayload, err := json.Marshal(publicRestore)
	if err != nil {
		t.Fatalf("Marshal(public restore) error: %v", err)
	}
	if bytes.Contains(publicPayload, []byte("manifest_size")) || bytes.Contains(publicPayload, []byte("manifest_digest")) || bytes.Contains(publicPayload, []byte(digest)) {
		t.Fatalf("public payload exposed restore manifest evidence: %s", publicPayload)
	}
}

func TestRestoreManifestRawEvidenceSurvivesManagerReload(t *testing.T) {
	manager, job, restore := newRestoreManifestEvidenceFixture(t)
	stateRoot := manager.root
	storageRoot := manager.storageRoot
	if err := manager.Close(); err != nil {
		t.Fatalf("Close(manager) error: %v", err)
	}

	reloaded, err := newBackupTestManager(t, ManagerConfig{
		Root:        stateRoot,
		StorageRoot: storageRoot,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager(reload) error: %v", err)
	}
	verify, err := reloaded.RunRestoreVerify(context.Background(), job.ID, RestoreVerifyOptions{TargetPath: restore.TargetPath})
	if err != nil {
		t.Fatalf("RunRestoreVerify(reloaded baseline) error: %v", err)
	}
	assertWarningsNotContain(t, verify.Warnings, "无法对照恢复时使用的本地备份快照")

	manifestData, err := os.ReadFile(restore.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest) error: %v", err)
	}
	tamperedData := append([]byte(nil), manifestData...)
	indent := []byte("\n  \"version\"")
	indentIndex := bytes.Index(tamperedData, indent)
	if indentIndex < 0 {
		t.Fatal("manifest indentation marker not found")
	}
	tamperedData[indentIndex+1] = '\t'
	if err := os.WriteFile(restore.ManifestPath, tamperedData, 0o600); err != nil {
		t.Fatalf("WriteFile(tampered manifest) error: %v", err)
	}
	verify, err = reloaded.RunRestoreVerify(context.Background(), job.ID, RestoreVerifyOptions{TargetPath: restore.TargetPath})
	if err != nil {
		t.Fatalf("RunRestoreVerify(reloaded tamper) error: %v", err)
	}
	assertWarningContains(t, verify.Warnings, "无法对照恢复时使用的本地备份快照")
}

func newRestoreManifestEvidenceFixture(t *testing.T) (*Manager, config.BackupJobConfig, *RestoreResult) {
	t.Helper()
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	writeManifestEvidenceTestFile(t, filepath.Join(source, "note.txt"), []byte("restore manifest evidence"))
	job := config.BackupJobConfig{
		ID:          "home",
		Name:        "Home backup",
		Type:        JobTypeLocal,
		Source:      source,
		Destination: destination,
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), job.ID); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if _, err := manager.RunRestore(context.Background(), job.ID, RestoreOptions{TargetPath: filepath.Join(tmpDir, "restore-target")}); err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}

	manager.mu.Lock()
	restore := cloneRestoreResultRaw(manager.state.Jobs[job.ID].LastRestore)
	manager.mu.Unlock()
	if restore == nil || restore.ManifestSize <= 0 || restore.ManifestDigest == "" || restore.ManifestPath == "" {
		t.Fatalf("persisted restore lacks trusted manifest evidence: %+v", restore)
	}
	return manager, job, restore
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
