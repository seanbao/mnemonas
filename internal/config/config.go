// Package config provides configuration management for MnemoNAS
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Root is the base directory for all storage (default: ~/.mnemonas)
	// User files will be stored in Root/files/
	// Internal data will be stored in Root/.mnemonas/
	Root string `toml:"root"`

	// Version retention policy
	Retention RetentionConfig `toml:"retention"`

	// Versioning policy
	Versioning VersioningConfig `toml:"versioning"`

	// Trash configuration
	Trash TrashConfig `toml:"trash"`
}

// VersioningConfig holds versioning policy configuration
type VersioningConfig struct {
	// AutoVersionedExtensions lists extensions that should have versioning by default
	AutoVersionedExtensions []string `toml:"auto_versioned_extensions"`
	// AutoVersionedFilenames lists filenames (without extension) that should have versioning
	AutoVersionedFilenames []string `toml:"auto_versioned_filenames"`
	// MaxVersionedSize is the max file size for auto versioning (default: 100MB)
	MaxVersionedSize int64 `toml:"max_versioned_size"`
}

// TrashConfig holds trash/recycle bin configuration
type TrashConfig struct {
	Enabled       bool  `toml:"enabled"`
	RetentionDays int   `toml:"retention_days"`
	MaxSize       int64 `toml:"max_size"`
}

// RetentionConfig holds version retention policy
type RetentionConfig struct {
	MaxVersions  int           `toml:"max_versions"`   // max versions per file, 0=unlimited
	MaxAge       time.Duration `toml:"max_age"`        // max retention time, 0=forever
	MinFreeSpace uint64        `toml:"min_free_space"` // minimum free space (bytes)
	GCInterval   time.Duration `toml:"gc_interval"`    // retention sweep interval, 0=disabled
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

// getDefaultStorageRoot returns the default storage root directory.
// Uses ~/.mnemonas for easy setup without root privileges.
// Falls back to ./data if user home directory is not available.
func getDefaultStorageRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to current directory if home is not available
		return filepath.Join(".", "data")
	}
	return filepath.Join(home, ".mnemonas")
}

// Default returns the default configuration
func Default() *Config {
	// Default storage root: ~/.mnemonas (user home directory)
	storageRoot := getDefaultStorageRoot()

	cfg := &Config{
		Server: ServerConfig{
			Host:         "0.0.0.0",
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
			TLS: TLSConfig{
				Enabled:      false,
				AutoGenerate: true, // Auto-generate self-signed cert for easy setup
				CertDir:      filepath.Join(storageRoot, ".mnemonas", "certs"),
			},
		},
		Storage: StorageConfig{
			Root: storageRoot,
			Retention: RetentionConfig{
				MaxVersions:  50,
				MaxAge:       90 * 24 * time.Hour,     // 90 days
				MinFreeSpace: 10 * 1024 * 1024 * 1024, // 10GB
				GCInterval:   24 * time.Hour,
			},
			Versioning: VersioningConfig{
				AutoVersionedExtensions: []string{
					".md", ".txt", ".org", ".rst", ".tex",
					".go", ".rs", ".py", ".ts", ".js", ".tsx", ".jsx",
					".c", ".cpp", ".h", ".java", ".kt", ".swift",
					".toml", ".yaml", ".yml", ".json", ".xml",
					".sh", ".bash", ".zsh", ".fish",
				},
				AutoVersionedFilenames: []string{
					"Makefile", "Dockerfile", "Vagrantfile",
					"LICENSE", "README", "CHANGELOG",
					".gitignore", ".dockerignore", ".editorconfig",
				},
				MaxVersionedSize: 100 * 1024 * 1024, // 100MB
			},
			Trash: TrashConfig{
				Enabled:       true,
				RetentionDays: 30,
				MaxSize:       10 * 1024 * 1024 * 1024, // 10GB
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
			AuthType: "basic", // default to basic auth with auto-generated password
		},
		Auth: AuthConfig{
			Enabled:         true, // enabled by default for security
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 7 * 24 * time.Hour,
			UsersFile:       filepath.Join(storageRoot, ".mnemonas", "users.json"),
		},
		Share: ShareConfig{
			Enabled:   false, // disabled by default
			StoreFile: filepath.Join(storageRoot, ".mnemonas", "shares.json"),
		},
		Favorites: FavoritesConfig{
			Enabled:   true, // enabled by default
			StoreFile: filepath.Join(storageRoot, ".mnemonas", "favorites.json"),
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

	applyStorageRootDefaults(cfg, storageRoot)
	return cfg
}

func applyStorageRootDefaults(cfg *Config, defaultRoot string) {
	if cfg.Storage.Root == "" {
		cfg.Storage.Root = defaultRoot
	}

	defaultInternal := filepath.Join(defaultRoot, ".mnemonas")
	internal := filepath.Join(cfg.Storage.Root, ".mnemonas")

	defaultCertDir := filepath.Join(defaultInternal, "certs")
	if cfg.Server.TLS.CertDir == "" || cfg.Server.TLS.CertDir == defaultCertDir {
		cfg.Server.TLS.CertDir = filepath.Join(internal, "certs")
	}

	defaultUsersFile := filepath.Join(defaultInternal, "users.json")
	if cfg.Auth.UsersFile == "" || cfg.Auth.UsersFile == defaultUsersFile {
		cfg.Auth.UsersFile = filepath.Join(internal, "users.json")
	}

	defaultShareFile := filepath.Join(defaultInternal, "shares.json")
	if cfg.Share.StoreFile == "" || cfg.Share.StoreFile == defaultShareFile {
		cfg.Share.StoreFile = filepath.Join(internal, "shares.json")
	}

	defaultFavoritesFile := filepath.Join(defaultInternal, "favorites.json")
	if cfg.Favorites.StoreFile == "" || cfg.Favorites.StoreFile == defaultFavoritesFile {
		cfg.Favorites.StoreFile = filepath.Join(internal, "favorites.json")
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

	applyStorageRootDefaults(cfg, getDefaultStorageRoot())
	cfg.WebDAV.Prefix = NormalizeWebDAVPrefix(cfg.WebDAV.Prefix)

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
	if c.Server.ReadTimeout <= 0 {
		errs = append(errs, errors.New("server.read_timeout must be positive"))
	}
	if c.Server.WriteTimeout <= 0 {
		errs = append(errs, errors.New("server.write_timeout must be positive"))
	}
	if c.Server.IdleTimeout <= 0 {
		errs = append(errs, errors.New("server.idle_timeout must be positive"))
	}

	if c.Storage.Root == "" {
		errs = append(errs, errors.New("storage.root cannot be empty"))
	}
	if c.Storage.Trash.RetentionDays < 0 {
		errs = append(errs, errors.New("storage.trash.retention_days cannot be negative"))
	}
	if c.Storage.Trash.MaxSize <= 0 {
		errs = append(errs, errors.New("storage.trash.max_size must be positive"))
	}
	if c.Storage.Versioning.MaxVersionedSize <= 0 {
		errs = append(errs, errors.New("storage.versioning.max_versioned_size must be positive"))
	}
	for _, ext := range c.Storage.Versioning.AutoVersionedExtensions {
		trimmed := strings.TrimSpace(ext)
		if trimmed == "" || !strings.HasPrefix(trimmed, ".") {
			errs = append(errs, fmt.Errorf("invalid storage.versioning.auto_versioned_extensions entry: %q", ext))
		}
	}
	for _, name := range c.Storage.Versioning.AutoVersionedFilenames {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, errors.New("storage.versioning.auto_versioned_filenames cannot contain empty entries"))
		}
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

	if c.Alerts.CheckInterval <= 0 {
		errs = append(errs, errors.New("alerts.check_interval must be positive"))
	}
	if c.Alerts.CooldownPeriod <= 0 {
		errs = append(errs, errors.New("alerts.cooldown_period must be positive"))
	}
	if c.Alerts.ThresholdPct < 0 || c.Alerts.ThresholdPct > 100 {
		errs = append(errs, errors.New("alerts.threshold_pct must be between 0 and 100"))
	}
	if c.Alerts.CriticalPct < 0 || c.Alerts.CriticalPct > 100 {
		errs = append(errs, errors.New("alerts.critical_pct must be between 0 and 100"))
	}
	if c.Alerts.CriticalPct < c.Alerts.ThresholdPct {
		errs = append(errs, errors.New("alerts.critical_pct must be greater than or equal to alerts.threshold_pct"))
	}
	if c.Alerts.WebhookMethod != "" && c.Alerts.WebhookMethod != "GET" && c.Alerts.WebhookMethod != "POST" {
		errs = append(errs, errors.New("alerts.webhook_method must be GET or POST"))
	}
	for _, header := range c.Alerts.WebhookHeaders {
		trimmed := strings.TrimSpace(header)
		if trimmed == "" {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("invalid alerts.webhook_headers entry: %q", header))
		}
	}

	return errors.Join(errs...)
}

// NormalizeWebDAVPrefix ensures the WebDAV prefix has a leading slash and no trailing slash.
func NormalizeWebDAVPrefix(prefix string) string {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" || trimmed == "/" {
		return "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	return strings.TrimRight(trimmed, "/")
}

// EnsureDirs ensures all required directories exist
func (c *Config) EnsureDirs() error {
	// New directory structure
	root := c.Storage.Root
	dirs := []string{
		filepath.Join(root, "files"),                    // User files (755)
		filepath.Join(root, ".mnemonas"),                // Internal data (700)
		filepath.Join(root, ".mnemonas", "objects"),     // Version objects
		filepath.Join(root, ".mnemonas", "trash"),       // Trash
		filepath.Join(root, ".mnemonas", "thumbnails"),  // Thumbnails
		filepath.Join(root, ".mnemonas", "maintenance"), // Maintenance
		filepath.Join(root, ".mnemonas", "activity"),    // Activity logs
		filepath.Join(root, ".mnemonas", "tmp"),         // Temp files
	}

	for i, dir := range dirs {
		var perm os.FileMode = 0700
		if i == 0 { // files/ directory should be accessible
			perm = 0755
		}
		if err := os.MkdirAll(dir, perm); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// FilesDir returns the path to user files directory
func (c *Config) FilesDir() string {
	return filepath.Join(c.Storage.Root, "files")
}

// InternalDir returns the path to internal data directory
func (c *Config) InternalDir() string {
	return filepath.Join(c.Storage.Root, ".mnemonas")
}

// IndexDBPath returns the path to SQLite index database
func (c *Config) IndexDBPath() string {
	return filepath.Join(c.Storage.Root, ".mnemonas", "index.db")
}

// ObjectsDir returns the path to version objects directory
func (c *Config) ObjectsDir() string {
	return filepath.Join(c.Storage.Root, ".mnemonas", "objects")
}

// TrashDir returns the path to trash directory
func (c *Config) TrashDir() string {
	return filepath.Join(c.Storage.Root, ".mnemonas", "trash")
}

// Address returns the server listen address
func (c *Config) Address() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}
