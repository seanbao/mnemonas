package tls

import (
	"crypto/tls"
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
