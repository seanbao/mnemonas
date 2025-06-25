package tls

import (
	"bytes"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func loadTestCertificateDER(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read certificate %s: %v", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("failed to decode PEM certificate %s", path)
	}
	return block.Bytes
}

func generateTestTLSPair(t *testing.T, dir string) ([]byte, *CertInfo) {
	t.Helper()

	manager := NewManager(Config{
		Enabled:      true,
		AutoGenerate: true,
		CertDir:      dir,
	})
	if _, err := manager.GetTLSConfig(); err != nil {
		t.Fatalf("failed to generate certificate pair in %s: %v", dir, err)
	}
	info, err := manager.GetCertificateInfo()
	if err != nil {
		t.Fatalf("failed to read certificate info in %s: %v", dir, err)
	}
	return loadTestCertificateDER(t, filepath.Join(dir, "server.crt")), info
}

func TestNewManager(t *testing.T) {
	cfg := Config{
		Enabled:      true,
		AutoGenerate: true,
	}
	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("NewManager returned nil")
	}
}

func TestGetTLSConfig_Disabled(t *testing.T) {
	manager := NewManager(Config{Enabled: false})
	cfg, err := manager.GetTLSConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatal("expected nil TLS config when disabled")
	}
}

func TestGetTLSConfig_AutoGenerate(t *testing.T) {
	tempDir := t.TempDir()

	manager := NewManager(Config{
		Enabled:      true,
		AutoGenerate: true,
		CertDir:      tempDir,
	})

	cfg, err := manager.GetTLSConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil TLS config")
	}

	// Verify TLS version
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion TLS 1.2, got %d", cfg.MinVersion)
	}

	// Verify certificates were loaded
	if len(cfg.Certificates) == 0 {
		t.Fatal("expected at least one certificate")
	}

	// Verify certificate files were created
	certFile := filepath.Join(tempDir, "server.crt")
	keyFile := filepath.Join(tempDir, "server.key")

	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		t.Errorf("certificate file not created: %s", certFile)
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		t.Errorf("key file not created: %s", keyFile)
	}

	certInfo, err := os.Stat(certFile)
	if err != nil {
		t.Fatalf("failed to stat cert file: %v", err)
	}
	if certInfo.Mode().Perm() != 0644 {
		t.Fatalf("certificate permissions = %o, want 0644", certInfo.Mode().Perm())
	}

	keyInfo, err := os.Stat(keyFile)
	if err != nil {
		t.Fatalf("failed to stat key file: %v", err)
	}
	if keyInfo.Mode().Perm() != 0600 {
		t.Fatalf("key permissions = %o, want 0600", keyInfo.Mode().Perm())
	}
}

func TestGetTLSConfig_LoadExisting(t *testing.T) {
	tempDir := t.TempDir()

	// First, generate a certificate
	manager1 := NewManager(Config{
		Enabled:      true,
		AutoGenerate: true,
		CertDir:      tempDir,
	})

	_, err := manager1.GetTLSConfig()
	if err != nil {
		t.Fatalf("failed to generate certificate: %v", err)
	}

	// Now load the existing certificate
	manager2 := NewManager(Config{
		Enabled:      true,
		AutoGenerate: false,
		CertFile:     filepath.Join(tempDir, "server.crt"),
		KeyFile:      filepath.Join(tempDir, "server.key"),
	})

	cfg, err := manager2.GetTLSConfig()
	if err != nil {
		t.Fatalf("failed to load existing certificate: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil TLS config")
	}
}

func TestGetTLSConfig_MissingCertNoAutoGenerate(t *testing.T) {
	manager := NewManager(Config{
		Enabled:      true,
		AutoGenerate: false,
		CertFile:     "/nonexistent/cert.pem",
		KeyFile:      "/nonexistent/key.pem",
	})

	_, err := manager.GetTLSConfig()
	if err == nil {
		t.Fatal("expected error for missing certificate")
	}
}

func TestWriteTLSFile_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "server.crt")

	originalSyncTLSRootDir := syncTLSRootDir
	syncTLSRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncTLSRootDir = originalSyncTLSRootDir
	}()

	err := writeTLSFile(filePath, []byte("certificate"), 0644, errCertFileSymlink, ".tls-cert-*.tmp", "certificate file")
	if err == nil {
		t.Fatal("expected writeTLSFile() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync certificate file directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatalf("expected TLS file to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "certificate" {
		t.Fatalf("expected TLS file content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(filePath)
	if statErr != nil {
		t.Fatalf("expected TLS file to exist after sync failure, got %v", statErr)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("expected TLS file permissions 0644, got %o", info.Mode().Perm())
	}
}

func TestWriteTLSFile_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "nested", "tls", "server.crt")

	originalSyncTLSDir := syncTLSDir
	syncTLSDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncTLSDir = originalSyncTLSDir
	}()

	err := writeTLSFile(filePath, []byte("certificate"), 0644, errCertFileSymlink, ".tls-cert-*.tmp", "certificate file")
	if err == nil {
		t.Fatal("expected writeTLSFile() to fail when directory tree sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync certificate file directory tree") {
		t.Fatalf("expected directory tree sync error, got %v", err)
	}
	if _, statErr := os.Stat(filePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no TLS file to be created, got %v", statErr)
	}
}

func TestEnsureTLSDir_SyncsCreatedDirectoriesDeepestParentFirst(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "tls")

	originalSyncTLSDir := syncTLSDir
	var synced []string
	syncTLSDir = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}
	defer func() {
		syncTLSDir = originalSyncTLSDir
	}()

	if err := ensureTLSDir(nestedDir, 0755, "certificate file"); err != nil {
		t.Fatalf("ensureTLSDir() error: %v", err)
	}

	expected := []string{filepath.Join(tmpDir, "nested"), tmpDir}
	if !reflect.DeepEqual(synced, expected) {
		t.Fatalf("syncTLSDir() order = %#v, want %#v", synced, expected)
	}
}

func TestGetTLSConfig_RejectsSymlinkPaths(t *testing.T) {
	tempDir := t.TempDir()
	certTarget := filepath.Join(tempDir, "real-server.crt")
	keyTarget := filepath.Join(tempDir, "real-server.key")
	certLink := filepath.Join(tempDir, "server.crt")
	keyLink := filepath.Join(tempDir, "server.key")

	if err := os.WriteFile(certTarget, []byte("placeholder"), 0644); err != nil {
		t.Fatalf("failed to seed cert target: %v", err)
	}
	if err := os.WriteFile(keyTarget, []byte("placeholder"), 0600); err != nil {
		t.Fatalf("failed to seed key target: %v", err)
	}
	if err := os.Symlink(certTarget, certLink); err != nil {
		t.Fatalf("failed to create cert symlink: %v", err)
	}
	if err := os.Symlink(keyTarget, keyLink); err != nil {
		t.Fatalf("failed to create key symlink: %v", err)
	}

	manager := NewManager(Config{
		Enabled:      true,
		AutoGenerate: false,
		CertFile:     certLink,
		KeyFile:      keyLink,
	})

	_, err := manager.GetTLSConfig()
	if !errors.Is(err, errCertFileSymlink) {
		t.Fatalf("expected cert symlink rejection, got %v", err)
	}
}

func TestGetTLSConfig_RejectsSymlinkParentDirectory(t *testing.T) {
	tempDir := t.TempDir()
	realCertDir := filepath.Join(tempDir, "real-certs")
	if err := os.MkdirAll(realCertDir, 0755); err != nil {
		t.Fatalf("MkdirAll(real-certs) failed: %v", err)
	}

	generator := NewManager(Config{
		Enabled:      true,
		AutoGenerate: true,
		CertDir:      realCertDir,
	})
	if _, err := generator.GetTLSConfig(); err != nil {
		t.Fatalf("failed to generate real certificate: %v", err)
	}

	linkedCertDir := filepath.Join(tempDir, "linked-certs")
	if err := os.Symlink(realCertDir, linkedCertDir); err != nil {
		t.Fatalf("Symlink(linked-certs) failed: %v", err)
	}

	manager := NewManager(Config{
		Enabled:      true,
		AutoGenerate: false,
		CertFile:     filepath.Join(linkedCertDir, "server.crt"),
		KeyFile:      filepath.Join(linkedCertDir, "server.key"),
	})

	_, err := manager.GetTLSConfig()
	if !errors.Is(err, errCertFileSymlink) {
		t.Fatalf("expected parent-directory symlink rejection, got %v", err)
	}
}

func TestGetCertificateInfo(t *testing.T) {
	tempDir := t.TempDir()

	manager := NewManager(Config{
		Enabled:      true,
		AutoGenerate: true,
		CertDir:      tempDir,
	})

	// Generate certificate first
	_, err := manager.GetTLSConfig()
	if err != nil {
		t.Fatalf("failed to generate certificate: %v", err)
	}

	// Get certificate info
	info, err := manager.GetCertificateInfo()
	if err != nil {
		t.Fatalf("failed to get certificate info: %v", err)
	}

	// Verify info
	if info.Subject == "" {
		t.Error("expected non-empty subject")
	}
	if !info.SelfSigned {
		t.Error("expected self-signed certificate")
	}
	if len(info.DNSNames) == 0 {
		t.Error("expected at least one DNS name")
	}
	if info.NotAfter.IsZero() {
		t.Error("expected non-zero NotAfter")
	}
}

func TestGetCertificateInfo_Disabled(t *testing.T) {
	manager := NewManager(Config{Enabled: false})

	_, err := manager.GetCertificateInfo()
	if err == nil {
		t.Fatal("expected error when TLS is disabled")
	}
}

func TestGetCertificateInfo_RejectsSymlinkPath(t *testing.T) {
	tempDir := t.TempDir()
	certTarget := filepath.Join(tempDir, "real-server.crt")
	certLink := filepath.Join(tempDir, "server.crt")

	if err := os.WriteFile(certTarget, []byte("placeholder"), 0644); err != nil {
		t.Fatalf("failed to seed cert target: %v", err)
	}
	if err := os.Symlink(certTarget, certLink); err != nil {
		t.Fatalf("failed to create cert symlink: %v", err)
	}

	manager := NewManager(Config{
		Enabled:  true,
		CertFile: certLink,
	})

	_, err := manager.GetCertificateInfo()
	if !errors.Is(err, errCertFileSymlink) {
		t.Fatalf("expected cert symlink rejection, got %v", err)
	}
}

func TestGetTLSConfig_AutoGenerateRejectsSymlinkTargets(t *testing.T) {
	tempDir := t.TempDir()
	certTarget := filepath.Join(tempDir, "real-server.crt")
	keyTarget := filepath.Join(tempDir, "real-server.key")
	certLink := filepath.Join(tempDir, "server.crt")
	keyLink := filepath.Join(tempDir, "server.key")

	if err := os.WriteFile(certTarget, []byte("old-cert"), 0644); err != nil {
		t.Fatalf("failed to seed cert target: %v", err)
	}
	if err := os.WriteFile(keyTarget, []byte("old-key"), 0600); err != nil {
		t.Fatalf("failed to seed key target: %v", err)
	}
	if err := os.Symlink(certTarget, certLink); err != nil {
		t.Fatalf("failed to create cert symlink: %v", err)
	}
	if err := os.Symlink(keyTarget, keyLink); err != nil {
		t.Fatalf("failed to create key symlink: %v", err)
	}

	manager := NewManager(Config{
		Enabled:      true,
		AutoGenerate: true,
		CertFile:     certLink,
		KeyFile:      keyLink,
	})

	_, err := manager.GetTLSConfig()
	if !errors.Is(err, errCertFileSymlink) {
		t.Fatalf("expected cert symlink rejection, got %v", err)
	}

	certBytes, err := os.ReadFile(certTarget)
	if err != nil {
		t.Fatalf("failed to read cert target: %v", err)
	}
	if string(certBytes) != "old-cert" {
		t.Fatalf("expected cert target unchanged, got %q", string(certBytes))
	}
	keyBytes, err := os.ReadFile(keyTarget)
	if err != nil {
		t.Fatalf("failed to read key target: %v", err)
	}
	if string(keyBytes) != "old-key" {
		t.Fatalf("expected key target unchanged, got %q", string(keyBytes))
	}
}

func TestGetTLSConfig_AutoGenerateRejectsSymlinkCertDir(t *testing.T) {
	tempDir := t.TempDir()
	realCertDir := filepath.Join(tempDir, "real-certs")
	if err := os.MkdirAll(realCertDir, 0755); err != nil {
		t.Fatalf("MkdirAll(real-certs) failed: %v", err)
	}
	linkedCertDir := filepath.Join(tempDir, "linked-certs")
	if err := os.Symlink(realCertDir, linkedCertDir); err != nil {
		t.Fatalf("Symlink(linked-certs) failed: %v", err)
	}

	manager := NewManager(Config{
		Enabled:      true,
		AutoGenerate: true,
		CertDir:      linkedCertDir,
	})

	_, err := manager.GetTLSConfig()
	if !errors.Is(err, errCertFileSymlink) {
		t.Fatalf("expected cert dir symlink rejection, got %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(realCertDir, "server.crt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no generated cert in symlink target dir, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(realCertDir, "server.key")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no generated key in symlink target dir, got %v", statErr)
	}
}

func TestGetTLSConfig_LoadExisting_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	certDir := filepath.Join(baseDir, "certs")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(certDir, 0755); err != nil {
		t.Fatalf("failed to create cert dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	originalDER, _ := generateTestTLSPair(t, certDir)
	outsideDER, _ := generateTestTLSPair(t, outsideDir)

	manager := NewManager(Config{
		Enabled:      true,
		AutoGenerate: false,
		CertFile:     filepath.Join(certDir, "server.crt"),
		KeyFile:      filepath.Join(certDir, "server.key"),
	})

	originalHook := afterValidateTLSFilePath
	var hookErr error
	swapped := false
	afterValidateTLSFilePath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		backupDir := filepath.Join(baseDir, "certs-backup")
		if err := os.Rename(certDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, certDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateTLSFilePath = originalHook
	}()

	cfg, err := manager.GetTLSConfig()
	if hookErr != nil {
		t.Fatalf("afterValidateTLSFilePath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected TLS load to stay bound to the original directory, got %v", err)
	}
	if len(cfg.Certificates) == 0 || len(cfg.Certificates[0].Certificate) == 0 {
		t.Fatal("expected loaded certificate material")
	}
	if !bytes.Equal(cfg.Certificates[0].Certificate[0], originalDER) {
		t.Fatal("expected loaded certificate to remain bound to the original directory")
	}
	if bytes.Equal(cfg.Certificates[0].Certificate[0], outsideDER) {
		t.Fatal("expected loaded certificate to ignore the swapped symlink target")
	}
}

func TestGetTLSConfig_AutoGenerate_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	certDir := filepath.Join(baseDir, "certs")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	outsideCertPath := filepath.Join(outsideDir, "server.crt")
	outsideKeyPath := filepath.Join(outsideDir, "server.key")
	if err := os.WriteFile(outsideCertPath, []byte("outside-cert"), 0644); err != nil {
		t.Fatalf("failed to seed outside cert: %v", err)
	}
	if err := os.WriteFile(outsideKeyPath, []byte("outside-key"), 0600); err != nil {
		t.Fatalf("failed to seed outside key: %v", err)
	}

	manager := NewManager(Config{
		Enabled:      true,
		AutoGenerate: true,
		CertDir:      certDir,
	})

	originalHook := afterValidateTLSFilePath
	var hookErr error
	swapped := false
	afterValidateTLSFilePath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		backupDir := filepath.Join(baseDir, "certs-backup")
		if err := os.Rename(certDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, certDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateTLSFilePath = originalHook
	}()

	cfg, err := manager.GetTLSConfig()
	if hookErr != nil {
		t.Fatalf("afterValidateTLSFilePath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected TLS auto-generate to stay bound to the original directory, got %v", err)
	}
	if len(cfg.Certificates) == 0 {
		t.Fatal("expected generated certificate material")
	}

	certBytes, err := os.ReadFile(outsideCertPath)
	if err != nil {
		t.Fatalf("failed to read outside cert: %v", err)
	}
	if string(certBytes) != "outside-cert" {
		t.Fatalf("expected outside cert to remain unchanged, got %q", string(certBytes))
	}
	keyBytes, err := os.ReadFile(outsideKeyPath)
	if err != nil {
		t.Fatalf("failed to read outside key: %v", err)
	}
	if string(keyBytes) != "outside-key" {
		t.Fatalf("expected outside key to remain unchanged, got %q", string(keyBytes))
	}
	if _, statErr := os.Stat(filepath.Join(baseDir, "certs-backup", "server.crt")); statErr != nil {
		t.Fatalf("expected generated cert to remain in original directory inode, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(baseDir, "certs-backup", "server.key")); statErr != nil {
		t.Fatalf("expected generated key to remain in original directory inode, got %v", statErr)
	}
}

func TestGetCertificateInfo_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	certDir := filepath.Join(baseDir, "certs")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(certDir, 0755); err != nil {
		t.Fatalf("failed to create cert dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	_, originalInfo := generateTestTLSPair(t, certDir)
	_, outsideInfo := generateTestTLSPair(t, outsideDir)

	manager := NewManager(Config{
		Enabled:  true,
		CertFile: filepath.Join(certDir, "server.crt"),
	})

	originalHook := afterValidateTLSFilePath
	var hookErr error
	swapped := false
	afterValidateTLSFilePath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		backupDir := filepath.Join(baseDir, "certs-backup")
		if err := os.Rename(certDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, certDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateTLSFilePath = originalHook
	}()

	info, err := manager.GetCertificateInfo()
	if hookErr != nil {
		t.Fatalf("afterValidateTLSFilePath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected GetCertificateInfo to stay bound to the original directory, got %v", err)
	}
	if info.SerialNumber != originalInfo.SerialNumber {
		t.Fatalf("expected original certificate serial %s, got %s", originalInfo.SerialNumber, info.SerialNumber)
	}
	if info.SerialNumber == outsideInfo.SerialNumber {
		t.Fatal("expected GetCertificateInfo to ignore the swapped symlink target")
	}
}

func TestGetLocalIPs(t *testing.T) {
	ips := getLocalIPs()

	// Should always include localhost
	foundLocalhost := false
	for _, ip := range ips {
		if ip.IsLoopback() {
			foundLocalhost = true
			break
		}
	}

	if !foundLocalhost {
		t.Error("expected localhost in IP list")
	}
}
