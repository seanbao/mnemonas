// Package tls provides TLS certificate management for MnemoNAS
package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	errCertFileSymlink = errors.New("TLS certificate file path must not be a symlink")
	errKeyFileSymlink  = errors.New("TLS private key file path must not be a symlink")
)

// Config holds TLS configuration
type Config struct {
	Enabled      bool   // Enable HTTPS
	CertFile     string // Path to certificate file
	KeyFile      string // Path to private key file
	AutoGenerate bool   // Auto-generate self-signed certificate if missing
	CertDir      string // Directory to store generated certificates
}

// Manager handles TLS certificate operations
type Manager struct {
	config Config
}

// NewManager creates a new TLS manager
func NewManager(cfg Config) *Manager {
	return &Manager{config: cfg}
}

// GetTLSConfig returns a TLS configuration for the server
func (m *Manager) GetTLSConfig() (*tls.Config, error) {
	if !m.config.Enabled {
		return nil, nil
	}

	cert, err := m.loadOrGenerateCert()
	if err != nil {
		return nil, fmt.Errorf("failed to load certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		},
	}, nil
}

// loadOrGenerateCert loads existing certificate or generates a new one
func (m *Manager) loadOrGenerateCert() (tls.Certificate, error) {
	certFile := m.config.CertFile
	keyFile := m.config.KeyFile

	// Use default paths if not specified
	if certFile == "" && m.config.CertDir != "" {
		certFile = filepath.Join(m.config.CertDir, "server.crt")
		keyFile = filepath.Join(m.config.CertDir, "server.key")
	}

	// Try to load existing certificate
	if certFile != "" && keyFile != "" {
		if err := validateTLSFilePath(certFile, errCertFileSymlink, "certificate file"); err != nil {
			return tls.Certificate{}, err
		}
		if err := validateTLSFilePath(keyFile, errKeyFileSymlink, "private key file"); err != nil {
			return tls.Certificate{}, err
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err == nil {
			return cert, nil
		}

		// If auto-generate is disabled, return the error
		if !m.config.AutoGenerate {
			return tls.Certificate{}, fmt.Errorf("failed to load certificate: %w", err)
		}
	}

	// Auto-generate is enabled, generate new certificate
	if !m.config.AutoGenerate {
		return tls.Certificate{}, errors.New("TLS enabled but no certificate provided and auto_generate is false")
	}

	return m.generateSelfSignedCert(certFile, keyFile)
}

// generateSelfSignedCert generates a new self-signed certificate
func (m *Manager) generateSelfSignedCert(certFile, keyFile string) (tls.Certificate, error) {
	// Generate private key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Generate serial number
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Certificate valid for 1 year
	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour)

	// Get local IP addresses for SAN
	ipAddresses := getLocalIPs()

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"MnemoNAS"},
			CommonName:   "MnemoNAS Server",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "mnemonas", "mnemonas.local"},
		IPAddresses:           ipAddresses,
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode certificate and key to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	// Save to files if paths are provided
	if certFile != "" && keyFile != "" {
		if err := m.saveCertFiles(certFile, keyFile, certPEM, keyPEM); err != nil {
			return tls.Certificate{}, fmt.Errorf("failed to save certificate files: %w", err)
		}
	}

	// Parse and return the certificate
	return tls.X509KeyPair(certPEM, keyPEM)
}

// saveCertFiles saves certificate and key to files
func (m *Manager) saveCertFiles(certFile, keyFile string, certPEM, keyPEM []byte) error {
	if err := writeTLSFile(certFile, certPEM, 0644, errCertFileSymlink, ".tls-cert-*.tmp", "certificate file"); err != nil {
		return err
	}
	if err := writeTLSFile(keyFile, keyPEM, 0600, errKeyFileSymlink, ".tls-key-*.tmp", "private key file"); err != nil {
		return err
	}

	return nil
}

func validateTLSFilePath(path string, symlinkErr error, label string) error {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return fmt.Errorf("failed to resolve %s path: %w", label, err)
		}
		cleaned = absPath
	}

	root := filepath.VolumeName(cleaned) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(cleaned, root)
	if trimmed == "" {
		info, err := os.Lstat(cleaned)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("failed to stat %s: %w", label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return symlinkErr
		}
		return nil
	}

	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" {
			continue
		}

		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("failed to stat %s: %w", label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return symlinkErr
		}
	}
	return nil
}

func writeTLSFile(path string, data []byte, mode os.FileMode, symlinkErr error, pattern, label string) error {
	if err := validateTLSFilePath(path, symlinkErr, label); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create %s directory: %w", label, err)
	}

	tmpFile, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("failed to create temp %s: %w", label, err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(mode); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to set temp %s permissions: %w", label, err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write %s: %w", label, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to sync %s: %w", label, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp %s: %w", label, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace %s: %w", label, err)
	}
	cleanup = false
	return nil
}

// GetCertificateInfo returns information about the current certificate
func (m *Manager) GetCertificateInfo() (*CertInfo, error) {
	if !m.config.Enabled {
		return nil, errors.New("TLS not enabled")
	}

	certFile := m.config.CertFile
	if certFile == "" && m.config.CertDir != "" {
		certFile = filepath.Join(m.config.CertDir, "server.crt")
	}

	if certFile == "" {
		return nil, errors.New("no certificate file configured")
	}
	if err := validateTLSFilePath(certFile, errCertFileSymlink, "certificate file"); err != nil {
		return nil, err
	}

	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return &CertInfo{
		Subject:      cert.Subject.String(),
		Issuer:       cert.Issuer.String(),
		NotBefore:    cert.NotBefore,
		NotAfter:     cert.NotAfter,
		DNSNames:     cert.DNSNames,
		IPAddresses:  ipStrings(cert.IPAddresses),
		SerialNumber: cert.SerialNumber.String(),
		SelfSigned:   cert.Issuer.String() == cert.Subject.String(),
	}, nil
}

// CertInfo contains certificate information
type CertInfo struct {
	Subject      string    `json:"subject"`
	Issuer       string    `json:"issuer"`
	NotBefore    time.Time `json:"not_before"`
	NotAfter     time.Time `json:"not_after"`
	DNSNames     []string  `json:"dns_names"`
	IPAddresses  []string  `json:"ip_addresses"`
	SerialNumber string    `json:"serial_number"`
	SelfSigned   bool      `json:"self_signed"`
}

// getLocalIPs returns local IP addresses for certificate SAN
func getLocalIPs() []net.IP {
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil || ipnet.IP.To16() != nil {
				ips = append(ips, ipnet.IP)
			}
		}
	}

	return ips
}

// ipStrings converts IP addresses to strings
func ipStrings(ips []net.IP) []string {
	result := make([]string, len(ips))
	for i, ip := range ips {
		result[i] = ip.String()
	}
	return result
}
