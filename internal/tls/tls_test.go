package tls

import (
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
