// Package config provides configuration management for MnemoNAS
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	urlpath "path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"
)

var errConfigFileSymlink = errors.New("config file path must not be a symlink")
var errManagedDirectorySymlink = errors.New("managed directory path must not be a symlink")

var syncManagedDir = syncManagedDirectory
var syncManagedRootDir = syncManagedRootDirectory
var afterValidateManagedFilePath = func() {}

var managedDirRootsMu sync.RWMutex
var managedDirRoots = map[string]*os.Root{}

const managedRootEscapeError = "path escapes from parent"

var durationFieldPaths = [][]string{
	{"server", "read_timeout"},
	{"server", "write_timeout"},
	{"server", "idle_timeout"},
	{"storage", "retention", "max_age"},
	{"storage", "retention", "gc_interval"},
	{"dataplane", "timeout"},
	{"auth", "access_token_ttl"},
	{"auth", "refresh_token_ttl"},
	{"alerts", "check_interval"},
	{"alerts", "cooldown_period"},
}

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
	Host             string        `toml:"host"`
	Port             int           `toml:"port"`
	ReadTimeout      time.Duration `toml:"read_timeout"`
	WriteTimeout     time.Duration `toml:"write_timeout"`
	IdleTimeout      time.Duration `toml:"idle_timeout"`
	TrustedProxyHops int           `toml:"trusted_proxy_hops"`
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
			Host:             "0.0.0.0",
			Port:             8080,
			ReadTimeout:      30 * time.Second,
			WriteTimeout:     60 * time.Second,
			IdleTimeout:      120 * time.Second,
			TrustedProxyHops: 0,
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
	} else {
		cfg.Storage.Root = expandUserPath(cfg.Storage.Root)
	}

	cfg.Storage.Versioning.AutoVersionedExtensions = normalizeStringSlice(cfg.Storage.Versioning.AutoVersionedExtensions)
	cfg.Storage.Versioning.AutoVersionedFilenames = normalizeStringSlice(cfg.Storage.Versioning.AutoVersionedFilenames)
	cfg.Alerts.WebhookHeaders = normalizeStringSlice(cfg.Alerts.WebhookHeaders)
	cfg.Server.TLS.CertDir = expandUserPath(cfg.Server.TLS.CertDir)
	cfg.Server.TLS.CertFile = expandUserPath(cfg.Server.TLS.CertFile)
	cfg.Server.TLS.KeyFile = expandUserPath(cfg.Server.TLS.KeyFile)
	cfg.Auth.UsersFile = expandUserPath(cfg.Auth.UsersFile)
	cfg.Share.StoreFile = expandUserPath(cfg.Share.StoreFile)
	cfg.Favorites.StoreFile = expandUserPath(cfg.Favorites.StoreFile)
	cfg.Log.Output = expandUserPath(cfg.Log.Output)

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

func expandUserPath(path string) string {
	if path == "" {
		return ""
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}

	switch path {
	case "~":
		return home
	default:
		if strings.HasPrefix(path, "~/") {
			return filepath.Join(home, path[2:])
		}
	}

	return path
}

func isFilesystemRoot(path string) bool {
	cleaned := filepath.Clean(path)
	volume := filepath.VolumeName(cleaned)
	if volume != "" {
		return cleaned == volume+string(os.PathSeparator)
	}
	return cleaned == string(os.PathSeparator)
}

func normalizeStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// Load loads configuration from file
func Load(path string) (*Config, error) {
	cfg := Default()
	normalizedPath, err := ensureManagedDirRoot(path, errConfigFileSymlink, "config file", false)
	if err != nil {
		return nil, err
	}

	data, err := readRegisteredManagedFile(normalizedPath, errConfigFileSymlink, "config file")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil // file not found, use default config
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	normalizedData, err := normalizeDurationFields(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := toml.Unmarshal(normalizedData, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	applyStorageRootDefaults(cfg, getDefaultStorageRoot())
	cfg.WebDAV.Prefix = NormalizeWebDAVPrefix(cfg.WebDAV.Prefix)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

func normalizeDurationFields(data []byte) ([]byte, error) {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	for _, fieldPath := range durationFieldPaths {
		if err := normalizeDurationFieldValue(raw, fieldPath); err != nil {
			return nil, err
		}
	}

	normalizedData, err := toml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize duration fields: %w", err)
	}

	return normalizedData, nil
}

func normalizeDurationFieldValue(raw map[string]any, fieldPath []string) error {
	if len(fieldPath) < 2 {
		return nil
	}

	current := raw
	for depth, key := range fieldPath[:len(fieldPath)-1] {
		value, ok := current[key]
		if !ok {
			return nil
		}

		next, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid config structure at %s", strings.Join(fieldPath[:depth+1], "."))
		}
		current = next
	}

	leafKey := fieldPath[len(fieldPath)-1]
	value, ok := current[leafKey]
	if !ok {
		return nil
	}

	durationText, ok := value.(string)
	if !ok {
		return nil
	}

	parsedDuration, err := time.ParseDuration(durationText)
	if err != nil {
		return fmt.Errorf("invalid %s duration %q: %w", strings.Join(fieldPath, "."), durationText, err)
	}

	current[leafKey] = int64(parsedDuration)
	return nil
}

// Save saves configuration to file
func (c *Config) Save(path string) error {
	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	if err := writeConfigFile(path, data); err != nil {
		return err
	}

	return nil
}

func normalizeManagedFilePath(path, label string) (string, error) {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s path: %w", label, err)
	}
	return absPath, nil
}

func validateManagedFilePath(path string, symlinkErr error, label string) error {
	cleaned, err := normalizeManagedFilePath(path, label)
	if err != nil {
		return err
	}

	root := filepath.VolumeName(cleaned) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(cleaned, root)
	if trimmed == "" {
		info, err := os.Lstat(cleaned)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
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
			if errors.Is(err, os.ErrNotExist) {
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

func ensureManagedDirRoot(path string, symlinkErr error, label string, create bool) (string, error) {
	normalizedPath, _, err := ensureManagedDirRootWithState(path, symlinkErr, label, create)
	return normalizedPath, err
}

func ensureManagedDirRootWithState(path string, symlinkErr error, label string, create bool) (string, *os.Root, error) {
	normalizedPath, err := normalizeManagedFilePath(path, label)
	if err != nil {
		return "", nil, err
	}
	if err := validateManagedFilePath(normalizedPath, symlinkErr, label); err != nil {
		return "", nil, err
	}

	dir := filepath.Dir(normalizedPath)
	managedDirRootsMu.RLock()
	root := managedDirRoots[dir]
	managedDirRootsMu.RUnlock()
	if root != nil {
		return normalizedPath, nil, nil
	}

	if create {
		if err := ensureManagedDir(dir, 0755); err != nil {
			return "", nil, fmt.Errorf("failed to create %s directory: %w", label, err)
		}
	} else if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizedPath, nil, nil
		}
		return "", nil, fmt.Errorf("failed to stat %s directory: %w", label, err)
	}

	root, err = os.OpenRoot(dir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open %s directory root: %w", label, err)
	}

	managedDirRootsMu.Lock()
	if existing := managedDirRoots[dir]; existing != nil {
		managedDirRootsMu.Unlock()
		_ = root.Close()
		return normalizedPath, nil, nil
	}
	managedDirRoots[dir] = root
	managedDirRootsMu.Unlock()

	return normalizedPath, root, nil
}

func releaseRegisteredManagedDirRoot(dir string, root *os.Root) {
	if root == nil {
		return
	}
	managedDirRootsMu.Lock()
	if managedDirRoots[dir] == root {
		delete(managedDirRoots, dir)
	}
	managedDirRootsMu.Unlock()
	_ = root.Close()
}

func registeredManagedDirRoot(path, label string) (*os.Root, string, bool, error) {
	normalizedPath, err := normalizeManagedFilePath(path, label)
	if err != nil {
		return nil, "", false, err
	}
	dir := filepath.Dir(normalizedPath)
	managedDirRootsMu.RLock()
	root := managedDirRoots[dir]
	managedDirRootsMu.RUnlock()
	return root, normalizedPath, root != nil, nil
}

func readRegisteredManagedFile(path string, symlinkErr error, label string) ([]byte, error) {
	root, normalizedPath, ok, err := registeredManagedDirRoot(path, label)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := validateManagedFilePath(normalizedPath, symlinkErr, label); err != nil {
			return nil, err
		}
		return os.ReadFile(normalizedPath)
	}
	return readManagedFileWithRoot(root, normalizedPath, symlinkErr, label)
}

func writeRegisteredManagedFileAtomically(path string, data []byte, symlinkErr error, pattern, label string, perm os.FileMode) error {
	root, normalizedPath, ok, err := registeredManagedDirRoot(path, label)
	if err != nil {
		return err
	}
	if ok {
		return writeManagedFileAtomicallyWithRoot(root, normalizedPath, data, symlinkErr, pattern, label, perm)
	}
	if err := validateManagedFilePath(normalizedPath, symlinkErr, label); err != nil {
		return err
	}
	createdDirs, err := collectMissingManagedDirs(filepath.Dir(normalizedPath))
	if err != nil {
		return err
	}
	registeredRoot := (*os.Root)(nil)
	normalizedPath, registeredRoot, err = ensureManagedDirRootWithState(normalizedPath, symlinkErr, label, true)
	if err != nil {
		releaseRegisteredManagedDirRoot(filepath.Dir(normalizedPath), registeredRoot)
		return cleanupCreatedManagedDirs(createdDirs, label, err)
	}
	if err := writeManagedFileAtomicallyWithRoot(registeredRoot, normalizedPath, data, symlinkErr, pattern, label, perm); err != nil {
		releaseRegisteredManagedDirRoot(filepath.Dir(normalizedPath), registeredRoot)
		return cleanupCreatedManagedDirs(createdDirs, label, err)
	}
	return nil
}

func chmodRegisteredManagedFile(path string, mode os.FileMode, symlinkErr error, label string) error {
	root, normalizedPath, ok, err := registeredManagedDirRoot(path, label)
	if err != nil {
		return err
	}
	if !ok {
		if err := validateManagedFilePath(normalizedPath, symlinkErr, label); err != nil {
			return err
		}
		afterValidateManagedFilePath()
		return os.Chmod(normalizedPath, mode)
	}
	return chmodManagedFileWithRoot(root, normalizedPath, mode, symlinkErr)
}

func syncRegisteredManagedDir(path, label string) error {
	root, normalizedPath, ok, err := registeredManagedDirRoot(path, label)
	if err != nil {
		return err
	}
	if ok {
		return syncManagedRootDir(root)
	}
	return syncManagedDir(filepath.Dir(normalizedPath))
}

func syncManagedDirectory(dir string) error {
	parentDir, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open directory %s: %w", dir, err)
	}
	defer func() {
		_ = parentDir.Close()
	}()
	if err := parentDir.Sync(); err != nil {
		return fmt.Errorf("sync directory %s: %w", dir, err)
	}
	if err := parentDir.Close(); err != nil {
		return fmt.Errorf("close directory %s: %w", dir, err)
	}
	return nil
}

func syncManagedRootDirectory(root *os.Root) error {
	parentDir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() {
		_ = parentDir.Close()
	}()
	if err := parentDir.Sync(); err != nil {
		return err
	}
	if err := parentDir.Close(); err != nil {
		return err
	}
	return nil
}

func collectMissingManagedDirs(dir string) ([]string, error) {
	missing := make([]string, 0)
	current := filepath.Clean(dir)
	for {
		if _, err := os.Stat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return missing, nil
}

func syncCreatedManagedDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncManagedDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync managed directory tree: %w", err)
		}
	}
	return nil
}

func ensureManagedDir(dir string, perm os.FileMode) error {
	createdDirs, err := collectMissingManagedDirs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	if err := os.Chmod(dir, perm); err != nil {
		return err
	}
	return syncCreatedManagedDirs(createdDirs)
}

func readManagedFileWithRoot(root *os.Root, path string, symlinkErr error, label string) ([]byte, error) {
	if err := validateManagedFilePath(path, symlinkErr, label); err != nil {
		return nil, err
	}
	afterValidateManagedFilePath()

	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, mapManagedRootPathError(err, symlinkErr)
	}
	defer file.Close()

	return io.ReadAll(file)
}

func writeManagedFileAtomicallyWithRoot(root *os.Root, path string, data []byte, symlinkErr error, pattern, label string, perm os.FileMode) error {
	if err := validateManagedFilePath(path, symlinkErr, label); err != nil {
		return err
	}
	afterValidateManagedFilePath()

	tmpFile, tmpName, err := createManagedTempFile(root, pattern, symlinkErr)
	if err != nil {
		return fmt.Errorf("failed to create temp %s file: %w", label, err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmpName)
		}
	}()

	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		return cleanupManagedTempPath(root, tmpName, fmt.Errorf("failed to set temp %s permissions: %w", label, err))
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return cleanupManagedTempPath(root, tmpName, fmt.Errorf("failed to write %s file: %w", label, err))
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return cleanupManagedTempPath(root, tmpName, fmt.Errorf("failed to sync %s file: %w", label, err))
	}
	if err := tmpFile.Close(); err != nil {
		return cleanupManagedTempPath(root, tmpName, fmt.Errorf("failed to close temp %s file: %w", label, err))
	}
	if err := root.Rename(tmpName, filepath.Base(path)); err != nil {
		return cleanupManagedTempPath(root, tmpName, fmt.Errorf("failed to replace %s: %w", label, mapManagedRootPathError(err, symlinkErr)))
	}
	cleanup = false
	if err := syncRegisteredManagedDir(path, label); err != nil {
		return fmt.Errorf("failed to sync %s directory: %w", label, err)
	}
	return nil
}

func chmodManagedFileWithRoot(root *os.Root, path string, mode os.FileMode, symlinkErr error) error {
	file, err := root.OpenFile(filepath.Base(path), os.O_RDWR, 0)
	if err != nil {
		return mapManagedRootPathError(err, symlinkErr)
	}
	defer file.Close()

	if err := file.Chmod(mode); err != nil {
		return err
	}
	return nil
}

func createManagedTempFile(root *os.Root, pattern string, symlinkErr error) (*os.File, string, error) {
	tmpName, err := newManagedTempName(pattern)
	if err != nil {
		return nil, "", err
	}
	tmpFile, err := root.OpenFile(tmpName, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, "", mapManagedRootPathError(err, symlinkErr)
	}
	return tmpFile, tmpName, nil
}

func newManagedTempName(pattern string) (string, error) {
	random, err := generateSecureKey(8)
	if err != nil {
		return "", err
	}
	name := strings.Replace(pattern, "*", random, 1)
	if !strings.Contains(name, random) {
		name = pattern + random
	}
	return name, nil
}

func cleanupManagedTempPath(root *os.Root, path string, err error) error {
	_ = root.Remove(path)
	return err
}

func cleanupCreatedManagedDirs(createdDirs []string, label string, operationErr error) error {
	rollbackErr := operationErr
	for _, dir := range createdDirs {
		if removeErr := os.Remove(dir); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("cleanup created %s directory %s: %w", label, dir, removeErr))
			break
		}
	}
	return rollbackErr
}

func mapManagedRootPathError(err error, symlinkErr error) error {
	if errors.Is(err, os.ErrPermission) || isManagedRootEscapeError(err) {
		return symlinkErr
	}
	return err
}

func isManagedRootEscapeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), managedRootEscapeError)
}

func writeConfigFile(path string, data []byte) error {
	return writeRegisteredManagedFileAtomically(path, data, errConfigFileSymlink, ".config-*.tmp", "config", 0644)
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
	if c.Server.TrustedProxyHops < 0 {
		errs = append(errs, errors.New("server.trusted_proxy_hops cannot be negative"))
	}

	if c.Storage.Root == "" {
		errs = append(errs, errors.New("storage.root cannot be empty"))
	} else if isFilesystemRoot(c.Storage.Root) {
		errs = append(errs, errors.New("storage.root cannot be filesystem root"))
	}
	if c.Storage.Trash.RetentionDays < 0 {
		errs = append(errs, errors.New("storage.trash.retention_days cannot be negative"))
	}
	if c.Storage.Retention.MaxVersions < 0 {
		errs = append(errs, errors.New("storage.retention.max_versions cannot be negative"))
	}
	if c.Storage.Retention.MaxAge < 0 {
		errs = append(errs, errors.New("storage.retention.max_age cannot be negative"))
	}
	if c.Storage.Retention.GCInterval < 0 {
		errs = append(errs, errors.New("storage.retention.gc_interval cannot be negative"))
	}
	if c.Storage.Trash.MaxSize <= 0 {
		errs = append(errs, errors.New("storage.trash.max_size must be positive"))
	}
	if c.Storage.Versioning.MaxVersionedSize <= 0 {
		errs = append(errs, errors.New("storage.versioning.max_versioned_size must be positive"))
	}
	webdavAuthType := strings.ToLower(strings.TrimSpace(c.WebDAV.AuthType))
	if webdavAuthType != "" && webdavAuthType != "none" && webdavAuthType != "basic" {
		errs = append(errs, fmt.Errorf("invalid webdav.auth_type: %q", c.WebDAV.AuthType))
	}
	if c.Auth.AccessTokenTTL <= 0 {
		errs = append(errs, errors.New("auth.access_token_ttl must be positive"))
	}
	if c.Auth.RefreshTokenTTL <= 0 {
		errs = append(errs, errors.New("auth.refresh_token_ttl must be positive"))
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
	if c.DataPlane.Timeout <= 0 {
		errs = append(errs, errors.New("dataplane.timeout must be positive"))
	}
	if c.DataPlane.MaxRetries < 0 {
		errs = append(errs, errors.New("dataplane.max_retries cannot be negative"))
	}

	// CDC configuration validation
	cdc := c.DataPlane.CDC
	if cdc.MinChunkSize == 0 {
		errs = append(errs, errors.New("min_chunk_size must be positive"))
	}
	if cdc.AvgChunkSize == 0 {
		errs = append(errs, errors.New("avg_chunk_size must be positive"))
	}
	if cdc.MaxChunkSize == 0 {
		errs = append(errs, errors.New("max_chunk_size must be positive"))
	}
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
	logLevel := strings.ToLower(strings.TrimSpace(c.Log.Level))
	if logLevel != "debug" && logLevel != "info" && logLevel != "warn" && logLevel != "error" {
		errs = append(errs, fmt.Errorf("invalid log.level: %q", c.Log.Level))
	}
	logFormat := strings.ToLower(strings.TrimSpace(c.Log.Format))
	if logFormat != "json" && logFormat != "console" {
		errs = append(errs, fmt.Errorf("invalid log.format: %q", c.Log.Format))
	}
	if strings.TrimSpace(c.Log.Output) == "" {
		errs = append(errs, errors.New("log.output cannot be empty"))
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

	cleaned := trimmed
	for {
		next := urlpath.Clean(strings.TrimSpace(cleaned))
		if next == "" {
			return "/"
		}
		if next == cleaned {
			return next
		}
		cleaned = next
	}
}

// EnsureDirs ensures all required directories exist
func (c *Config) EnsureDirs() error {
	// New directory structure
	root := c.Storage.Root
	if root == "" {
		return errors.New("storage.root cannot be empty")
	}
	if isFilesystemRoot(root) {
		return errors.New("storage.root cannot be filesystem root")
	}

	dirs := []struct {
		path string
		perm os.FileMode
	}{
		{root, 0750},
		{filepath.Join(root, "files"), 0750},
		{filepath.Join(root, ".mnemonas"), 0700},
		{filepath.Join(root, ".mnemonas", "objects"), 0700},
		{filepath.Join(root, ".mnemonas", "trash"), 0700},
		{filepath.Join(root, ".mnemonas", "thumbnails"), 0700},
		{filepath.Join(root, ".mnemonas", "maintenance"), 0700},
		{filepath.Join(root, ".mnemonas", "activity"), 0700},
		{filepath.Join(root, ".mnemonas", "tmp"), 0700},
	}

	for _, dir := range dirs {
		if err := validateManagedFilePath(dir.path, errManagedDirectorySymlink, "managed directory"); err != nil {
			return fmt.Errorf("failed to validate directory %s: %w", dir.path, err)
		}
	}

	for _, dir := range dirs {
		if err := ensureManagedDir(dir.path, dir.perm); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir.path, err)
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
