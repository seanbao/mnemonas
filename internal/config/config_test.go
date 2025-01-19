// Package config provides configuration management for MnemoNAS
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg.Server.Port != 8080 {
		t.Errorf("Default port = %d, want 8080", cfg.Server.Port)
	}

	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Default host = %s, want 0.0.0.0", cfg.Server.Host)
	}

	if cfg.Storage.DataDir == "" {
		t.Error("Default data_dir should not be empty")
	}

	if cfg.DataPlane.GRPCAddress != "127.0.0.1:9090" {
		t.Errorf("Default gRPC address = %s, want 127.0.0.1:9090", cfg.DataPlane.GRPCAddress)
	}

	if cfg.WebDAV.Prefix != "/dav" {
		t.Errorf("Default WebDAV prefix = %s, want /dav", cfg.WebDAV.Prefix)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "Default is valid",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name:    "Invalid port zero",
			modify:  func(c *Config) { c.Server.Port = 0 },
			wantErr: true,
		},
		{
			name:    "Invalid port negative",
			modify:  func(c *Config) { c.Server.Port = -1 },
			wantErr: true,
		},
		{
			name:    "Invalid port too large",
			modify:  func(c *Config) { c.Server.Port = 70000 },
			wantErr: true,
		},
		{
			name:    "Empty data_dir",
			modify:  func(c *Config) { c.Storage.DataDir = "" },
			wantErr: true,
		},
		{
			name:    "Empty gRPC address",
			modify:  func(c *Config) { c.DataPlane.GRPCAddress = "" },
			wantErr: true,
		},
		{
			name: "Invalid CDC min >= avg",
			modify: func(c *Config) {
				c.DataPlane.CDC.MinChunkSize = 1024 * 1024
				c.DataPlane.CDC.AvgChunkSize = 512 * 1024
			},
			wantErr: true,
		},
		{
			name: "Invalid CDC avg >= max",
			modify: func(c *Config) {
				c.DataPlane.CDC.AvgChunkSize = 5 * 1024 * 1024
				c.DataPlane.CDC.MaxChunkSize = 4 * 1024 * 1024
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.modify(cfg)

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_SaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config", "config.toml")

	cfg := Default()
	cfg.Server.Port = 9999
	cfg.Log.Level = "debug"

	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("Config file was not created")
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.Server.Port != 9999 {
		t.Errorf("Loaded port = %d, want 9999", loaded.Server.Port)
	}

	if loaded.Log.Level != "debug" {
		t.Errorf("Loaded log level = %s, want debug", loaded.Log.Level)
	}
}

func TestLoad_NonExistentFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("Load() should not error for non-existent file: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("Port = %d, want default 8080", cfg.Server.Port)
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.toml")

	if err := os.WriteFile(configPath, []byte("this is not valid [toml"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("Load() should error for invalid TOML")
	}
}

func TestConfig_EnsureDirs(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Default()
	// Set the storage root to use the temp directory
	cfg.Storage.Root = tmpDir

	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs() error: %v", err)
	}

	// Check new directory structure
	dirs := []string{
		filepath.Join(tmpDir, "files"),
		filepath.Join(tmpDir, ".mnemonas"),
		filepath.Join(tmpDir, ".mnemonas", "objects"),
		filepath.Join(tmpDir, ".mnemonas", "trash"),
		filepath.Join(tmpDir, ".mnemonas", "thumbnails"),
		filepath.Join(tmpDir, ".mnemonas", "maintenance"),
	}

	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if os.IsNotExist(err) {
			t.Errorf("Directory %s was not created", dir)
		} else if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

func TestConfig_Address(t *testing.T) {
	cfg := Default()
	cfg.Server.Host = "192.168.1.1"
	cfg.Server.Port = 3000

	addr := cfg.Address()
	if addr != "192.168.1.1:3000" {
		t.Errorf("Address() = %s, want 192.168.1.1:3000", addr)
	}
}

func TestDataPlaneConfig_Address(t *testing.T) {
	cfg := Default()
	cfg.DataPlane.GRPCAddress = "custom:1234"

	addr := cfg.DataPlane.Address()
	if addr != "custom:1234" {
		t.Errorf("DataPlane.Address() = %s, want custom:1234", addr)
	}
}

func TestConfig_TimeoutValues(t *testing.T) {
	cfg := Default()

	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Errorf("ReadTimeout = %v, want 30s", cfg.Server.ReadTimeout)
	}

	if cfg.Server.WriteTimeout != 60*time.Second {
		t.Errorf("WriteTimeout = %v, want 60s", cfg.Server.WriteTimeout)
	}

	if cfg.Storage.Retention.GCInterval != 24*time.Hour {
		t.Errorf("GCInterval = %v, want 24h", cfg.Storage.Retention.GCInterval)
	}
}

func TestConfig_RetentionDefaults(t *testing.T) {
	cfg := Default()

	if cfg.Storage.Retention.MaxVersions != 50 {
		t.Errorf("MaxVersions = %d, want 50", cfg.Storage.Retention.MaxVersions)
	}

	if cfg.Storage.Retention.MinFreeSpace != 10*1024*1024*1024 {
		t.Errorf("MinFreeSpace = %d, want 10GB", cfg.Storage.Retention.MinFreeSpace)
	}
}

func TestConfig_CDCDefaults(t *testing.T) {
	cfg := Default()

	if cfg.DataPlane.CDC.MinChunkSize != 256*1024 {
		t.Errorf("MinChunkSize = %d, want 256KB", cfg.DataPlane.CDC.MinChunkSize)
	}

	if cfg.DataPlane.CDC.AvgChunkSize != 1024*1024 {
		t.Errorf("AvgChunkSize = %d, want 1MB", cfg.DataPlane.CDC.AvgChunkSize)
	}

	if cfg.DataPlane.CDC.MaxChunkSize != 4*1024*1024 {
		t.Errorf("MaxChunkSize = %d, want 4MB", cfg.DataPlane.CDC.MaxChunkSize)
	}
}
