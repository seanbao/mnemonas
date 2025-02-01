// Package config provides configuration management for MnemoNAS
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config is the main configuration structure for MnemoNAS
type Config struct {
	Server    ServerConfig    `toml:"server"`
	Storage   StorageConfig   `toml:"storage"`
	DataPlane DataPlaneConfig `toml:"dataplane"`
	WebDAV    WebDAVConfig    `toml:"webdav"`
	Auth      AuthConfig      `toml:"auth"`
	Share     ShareConfig     `toml:"share"`
	Favorites FavoritesConfig `toml:"favorites"`
	Alerts    AlertsConfig    `toml:"alerts"`
	Log       LogConfig       `toml:"log"`
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Host         string        `toml:"host"`
	Port         int           `toml:"port"`
	ReadTimeout  time.Duration `toml:"read_timeout"`
	WriteTimeout time.Duration `toml:"write_timeout"`
	IdleTimeout  time.Duration `toml:"idle_timeout"`
	// TLS configuration
	TLS TLSConfig `toml:"tls"`
}

// TLSConfig holds TLS/HTTPS configuration
type TLSConfig struct {
	Enabled      bool   `toml:"enabled"`       // Enable HTTPS
	CertFile     string `toml:"cert_file"`     // Path to certificate file
	KeyFile      string `toml:"key_file"`      // Path to private key file
	AutoGenerate bool   `toml:"auto_generate"` // Auto-generate self-signed cert if missing
	CertDir      string `toml:"cert_dir"`      // Directory for generated certificates
}

// StorageConfig holds storage configuration
type StorageConfig struct {
	DataDir        string `toml:"data_dir"`
	MetadataDir    string `toml:"metadata_dir"`
	TempDir        string `toml:"temp_dir"`
	ThumbnailDir   string `toml:"thumbnail_dir"`
	MaintenanceDir string `toml:"maintenance_dir"`
	ActivityDir    string `toml:"activity_dir"`
	// Version retention policy
	Retention RetentionConfig `toml:"retention"`
}

// RetentionConfig holds version retention policy
type RetentionConfig struct {
	MaxVersions  int           `toml:"max_versions"`   // max versions per file, 0=unlimited
	MaxAge       time.Duration `toml:"max_age"`        // max retention time, 0=forever
	MinFreeSpace uint64        `toml:"min_free_space"` // minimum free space (bytes)
	GCInterval   time.Duration `toml:"gc_interval"`    // GC run interval
}

// DataPlaneConfig holds Rust data plane configuration
type DataPlaneConfig struct {
	GRPCAddress string        `toml:"grpc_address"`
	Timeout     time.Duration `toml:"timeout"`
	MaxRetries  int           `toml:"max_retries"`
	// CDC (Content-Defined Chunking) configuration
	CDC CDCConfig `toml:"cdc"`
}

// Address returns the gRPC address for data plane connection
func (d *DataPlaneConfig) Address() string {
	return d.GRPCAddress
}

// CDCConfig holds content-defined chunking configuration
type CDCConfig struct {
	MinChunkSize uint32 `toml:"min_chunk_size"` // min chunk size (default 256KB)
	AvgChunkSize uint32 `toml:"avg_chunk_size"` // avg chunk size (default 1MB)
	MaxChunkSize uint32 `toml:"max_chunk_size"` // max chunk size (default 4MB)
}

// WebDAVConfig holds WebDAV service configuration
type WebDAVConfig struct {
	Enabled  bool   `toml:"enabled"`
	Prefix   string `toml:"prefix"` // URL prefix, e.g., /dav
	ReadOnly bool   `toml:"read_only"`
	AuthType string `toml:"auth_type"` // none, basic
	Username string `toml:"username"`  // for basic auth
	Password string `toml:"password"`  // for basic auth
}

// AuthConfig holds authentication configuration
type AuthConfig struct {
	Enabled         bool          `toml:"enabled"`
	JWTSecret       string        `toml:"jwt_secret"`        // Secret key for JWT signing
	AccessTokenTTL  time.Duration `toml:"access_token_ttl"`  // Access token expiry (default 15m)
	RefreshTokenTTL time.Duration `toml:"refresh_token_ttl"` // Refresh token expiry (default 7d)
	UsersFile       string        `toml:"users_file"`        // Path to users.json
}

// ShareConfig holds file sharing configuration
type ShareConfig struct {
	Enabled   bool   `toml:"enabled"`    // Enable file sharing
	StoreFile string `toml:"store_file"` // Path to shares.json
	BaseURL   string `toml:"base_url"`   // Base URL for share links (optional)
}

// FavoritesConfig holds favorites configuration
type FavoritesConfig struct {
	Enabled   bool   `toml:"enabled"`    // Enable favorites feature
	StoreFile string `toml:"store_file"` // Path to favorites.json
}

// AlertsConfig holds storage space alerting configuration
type AlertsConfig struct {
	Enabled        bool          `toml:"enabled"`         // Enable storage alerts
	CheckInterval  time.Duration `toml:"check_interval"`  // How often to check (default 1h)
	ThresholdPct   float64       `toml:"threshold_pct"`   // Alert when usage exceeds this % (default 90)
	CriticalPct    float64       `toml:"critical_pct"`    // Critical alert threshold (default 95)
	MinFreeBytes   uint64        `toml:"min_free_bytes"`  // Alert when free space < this (default 10GB)
	CooldownPeriod time.Duration `toml:"cooldown_period"` // Min time between alerts (default 4h)
	WebhookURL     string        `toml:"webhook_url"`     // Webhook URL for notifications
	WebhookMethod  string        `toml:"webhook_method"`  // POST or GET (default POST)
	WebhookHeaders []string      `toml:"webhook_headers"` // Additional headers (key:value format)
}

// LogConfig holds logging configuration
type LogConfig struct {
	Level      string `toml:"level"`       // debug, info, warn, error
	Format     string `toml:"format"`      // json, console
	Output     string `toml:"output"`      // stdout, stderr, file path
	TimeFormat string `toml:"time_format"` // RFC3339, Unix, etc.
}

// Default returns the default configuration
func Default() *Config {
	homeDir, _ := os.UserHomeDir()
	dataRoot := filepath.Join(homeDir, ".mnemonas")

	return &Config{
		Server: ServerConfig{
			Host:         "0.0.0.0",
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
			TLS: TLSConfig{
				Enabled:      false,
				AutoGenerate: true, // Auto-generate self-signed cert for easy setup
				CertDir:      filepath.Join(dataRoot, "certs"),
			},
		},
		Storage: StorageConfig{
			DataDir:        filepath.Join(dataRoot, "data"),
			MetadataDir:    filepath.Join(dataRoot, "metadata"),
			TempDir:        filepath.Join(dataRoot, "tmp"),
			ThumbnailDir:   filepath.Join(dataRoot, "thumbnails"),
			MaintenanceDir: filepath.Join(dataRoot, "maintenance"),
			ActivityDir:    filepath.Join(dataRoot, "activity"),
			Retention: RetentionConfig{
				MaxVersions:  100,
				MaxAge:       365 * 24 * time.Hour,    // 1 year
				MinFreeSpace: 10 * 1024 * 1024 * 1024, // 10GB
				GCInterval:   24 * time.Hour,
			},
		},
		DataPlane: DataPlaneConfig{
			GRPCAddress: "127.0.0.1:9090",
			Timeout:     30 * time.Second,
			MaxRetries:  3,
			CDC: CDCConfig{
				MinChunkSize: 256 * 1024,      // 256KB
				AvgChunkSize: 1024 * 1024,     // 1MB
				MaxChunkSize: 4 * 1024 * 1024, // 4MB
			},
		},
		WebDAV: WebDAVConfig{
			Enabled:  true,
			Prefix:   "/dav",
			ReadOnly: false,
			AuthType: "none", // default to no auth for development
		},
		Auth: AuthConfig{
			Enabled:         false, // disabled by default for easy development
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 7 * 24 * time.Hour,
			UsersFile:       filepath.Join(dataRoot, "users.json"),
		},
		Share: ShareConfig{
			Enabled:   false, // disabled by default
			StoreFile: filepath.Join(dataRoot, "shares.json"),
		},
		Favorites: FavoritesConfig{
			Enabled:   true, // enabled by default
			StoreFile: filepath.Join(dataRoot, "favorites.json"),
		},
		Alerts: AlertsConfig{
			Enabled:        false, // disabled by default
			CheckInterval:  1 * time.Hour,
			ThresholdPct:   90.0,
			CriticalPct:    95.0,
			MinFreeBytes:   10 * 1024 * 1024 * 1024, // 10GB
			CooldownPeriod: 4 * time.Hour,
			WebhookMethod:  "POST",
		},
		Log: LogConfig{
			Level:      "info",
			Format:     "console",
			Output:     "stdout",
			TimeFormat: time.RFC3339,
		},
	}
}

// Load loads configuration from file
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil // file not found, use default config
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

// Save saves configuration to file
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate validates configuration
func (c *Config) Validate() error {
	var errs []error

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("invalid port: %d", c.Server.Port))
	}

	if c.Storage.DataDir == "" {
		errs = append(errs, errors.New("data_dir cannot be empty"))
	}

	if c.DataPlane.GRPCAddress == "" {
		errs = append(errs, errors.New("dataplane.grpc_address cannot be empty"))
	}

	// CDC configuration validation
	cdc := c.DataPlane.CDC
	if cdc.MinChunkSize >= cdc.AvgChunkSize {
		errs = append(errs, errors.New("min_chunk_size must be less than avg_chunk_size"))
	}
	if cdc.AvgChunkSize >= cdc.MaxChunkSize {
		errs = append(errs, errors.New("avg_chunk_size must be less than max_chunk_size"))
	}

	return errors.Join(errs...)
}

// EnsureDirs ensures all required directories exist
func (c *Config) EnsureDirs() error {
	dirs := []string{
		c.Storage.DataDir,
		c.Storage.MetadataDir,
		c.Storage.TempDir,
		c.Storage.ThumbnailDir,
		c.Storage.MaintenanceDir,
		c.Storage.ActivityDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// Address returns the server listen address
func (c *Config) Address() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}
