// Package config provides configuration management for MnemoNAS
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	neturl "net/url"
	"os"
	urlpath "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/pelletier/go-toml/v2"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
)

var errConfigFileSymlink = errors.New("config file path must not be a symlink")
var errManagedDirectorySymlink = errors.New("managed directory path must not be a symlink")

var syncManagedDir = syncManagedDirectory
var syncManagedRootDir = syncManagedRootDirectory
var afterValidateManagedFilePath = func() {}
var managedTempName = newManagedTempName

var managedDirRootsMu sync.RWMutex
var managedDirRoots = map[string]*os.Root{}

const (
	configFileMode              os.FileMode = 0600
	managedRootEscapeError                  = "path escapes from parent"
	maxManagedTempAttempts                  = 32
	maxBackupCredentialFileSize             = 4 * 1024 * 1024
	minimumAuthAccessTokenTTL               = 30 * time.Second
)

var durationFieldPaths = [][]string{
	{"server", "read_timeout"},
	{"server", "write_timeout"},
	{"server", "idle_timeout"},
	{"storage", "retention", "max_age"},
	{"storage", "retention", "gc_interval"},
	{"dataplane", "timeout"},
	{"auth", "access_token_ttl"},
	{"auth", "refresh_token_ttl"},
	{"share", "default_expires_in"},
	{"alerts", "check_interval"},
	{"alerts", "cooldown_period"},
	{"disk_health", "check_interval"},
	{"disk_health", "probe_timeout"},
	{"disk_health", "cooldown_period"},
	{"maintenance", "scrub", "schedule_interval"},
	{"maintenance", "scrub", "retry_interval"},
}

var backupJobDurationFields = []string{
	"schedule_interval",
	"restore_drill_stale_after",
	"max_age",
	"stale_after",
}

// Config is the main configuration structure for MnemoNAS
type Config struct {
	Server      ServerConfig      `toml:"server"`
	Storage     StorageConfig     `toml:"storage"`
	DataPlane   DataPlaneConfig   `toml:"dataplane"`
	WebDAV      WebDAVConfig      `toml:"webdav"`
	SMB         SMBConfig         `toml:"smb"`
	Backup      BackupConfig      `toml:"backup"`
	Auth        AuthConfig        `toml:"auth"`
	Share       ShareConfig       `toml:"share"`
	Favorites   FavoritesConfig   `toml:"favorites"`
	Alerts      AlertsConfig      `toml:"alerts"`
	DiskHealth  DiskHealthConfig  `toml:"disk_health"`
	Maintenance MaintenanceConfig `toml:"maintenance"`
	Security    SecurityConfig    `toml:"security"`
	Log         LogConfig         `toml:"log"`
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Host              string        `toml:"host"`
	Port              int           `toml:"port"`
	ReadTimeout       time.Duration `toml:"read_timeout"`
	WriteTimeout      time.Duration `toml:"write_timeout"`
	IdleTimeout       time.Duration `toml:"idle_timeout"`
	TrustedProxyHops  int           `toml:"trusted_proxy_hops"`
	TrustedProxyCIDRs []string      `toml:"trusted_proxy_cidrs"`
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

	// DirectoryQuotas limits logical file bytes under selected MnemoNAS paths.
	DirectoryQuotas []DirectoryQuotaConfig `toml:"directory_quotas"`

	// DirectoryAccessRules grants or denies non-admin access outside the user's home_dir.
	DirectoryAccessRules []DirectoryAccessRuleConfig `toml:"directory_access_rules"`

	// Version retention policy
	Retention RetentionConfig `toml:"retention"`

	// Versioning policy
	Versioning VersioningConfig `toml:"versioning"`

	// Trash configuration
	Trash TrashConfig `toml:"trash"`
}

// DirectoryQuotaConfig limits the logical current-file bytes under a virtual path.
type DirectoryQuotaConfig struct {
	Path       string `toml:"path" json:"path"`
	QuotaBytes int64  `toml:"quota_bytes" json:"quota_bytes"`
}

// DirectoryAccessRuleConfig controls read/write access for a logical directory.
type DirectoryAccessRuleConfig struct {
	Path        string   `toml:"path" json:"path"`
	ReadUsers   []string `toml:"read_users" json:"read_users,omitempty"`
	WriteUsers  []string `toml:"write_users" json:"write_users,omitempty"`
	ReadGroups  []string `toml:"read_groups" json:"read_groups,omitempty"`
	WriteGroups []string `toml:"write_groups" json:"write_groups,omitempty"`
	ReadRoles   []string `toml:"read_roles" json:"read_roles,omitempty"`
	WriteRoles  []string `toml:"write_roles" json:"write_roles,omitempty"`
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

const (
	MinCDCChunkSize uint32 = 64 * 1024
	MaxCDCChunkSize uint32 = 64 * 1024 * 1024
)

// WebDAVConfig holds WebDAV service configuration
type WebDAVConfig struct {
	Enabled  bool   `toml:"enabled"`
	Prefix   string `toml:"prefix"` // URL prefix, e.g., /dav
	ReadOnly bool   `toml:"read_only"`
	AuthType string `toml:"auth_type"` // none, basic, users
	Username string `toml:"username"`  // for basic auth
	Password string `toml:"password"`  // for basic auth
}

// SMBConfig holds SMB sidecar configuration.
type SMBConfig struct {
	Enabled            bool             `toml:"enabled"`
	Listen             string           `toml:"listen"`
	ServerName         string           `toml:"server_name"`
	GatewaySocket      string           `toml:"gateway_socket"`
	CredentialFile     string           `toml:"credential_file"`
	SigningRequired    bool             `toml:"signing_required"`
	EncryptionRequired bool             `toml:"encryption_required"`
	Shares             []SMBShareConfig `toml:"shares"`
}

// SMBShareConfig maps an SMB share to a MnemoNAS virtual path.
type SMBShareConfig struct {
	Name         string   `toml:"name"`
	Path         string   `toml:"path"`
	ReadOnly     bool     `toml:"read_only"`
	AllowedRoles []string `toml:"allowed_roles"`
	AllowedUsers []string `toml:"allowed_users"`
}

// BackupConfig holds configured backup jobs.
type BackupConfig struct {
	Jobs []BackupJobConfig `toml:"jobs"`
}

// MaintenanceConfig controls background maintenance jobs.
type MaintenanceConfig struct {
	Scrub ScrubMaintenanceConfig `toml:"scrub"`
}

// ScrubMaintenanceConfig controls scheduled data-integrity scrub runs.
type ScrubMaintenanceConfig struct {
	Enabled          bool          `toml:"enabled"`
	ScheduleInterval time.Duration `toml:"schedule_interval"`
	RetryInterval    time.Duration `toml:"retry_interval"`
	MaxRetries       int           `toml:"max_retries"`
}

// BackupJobConfig describes a local backup job.
type BackupJobConfig struct {
	ID                     string        `toml:"id"`
	Name                   string        `toml:"name"`
	Type                   string        `toml:"type"`
	Source                 string        `toml:"source"`
	Destination            string        `toml:"destination"`
	Repository             string        `toml:"repository"`
	Remote                 string        `toml:"remote"`
	Command                string        `toml:"command"`
	PasswordFile           string        `toml:"password_file"`
	ConfigFile             string        `toml:"config_file"`
	ExtraArgs              []string      `toml:"extra_args"`
	Disabled               bool          `toml:"disabled"`
	ScheduleInterval       time.Duration `toml:"schedule_interval"`
	ScheduleWindowStart    string        `toml:"schedule_window_start"`
	ScheduleWindowEnd      string        `toml:"schedule_window_end"`
	StaleAfter             time.Duration `toml:"stale_after"`
	RestoreDrillStaleAfter time.Duration `toml:"restore_drill_stale_after"`
	RetentionPolicy        string        `toml:"retention_policy"`
	MaxSnapshots           int           `toml:"max_snapshots"`
	MaxAge                 time.Duration `toml:"max_age"`
	IncludeConfig          bool          `toml:"include_config"`
	VerifyAfterBackup      bool          `toml:"verify_after_backup"`
	Exclude                []string      `toml:"exclude"`
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
	Enabled          bool                    `toml:"enabled"`            // Enable file sharing
	StoreFile        string                  `toml:"store_file"`         // Path to shares.json
	BaseURL          string                  `toml:"base_url"`           // Base URL for share links (optional)
	DefaultExpiresIn time.Duration           `toml:"default_expires_in"` // Default expiry for newly-created shares
	DefaultMaxAccess int64                   `toml:"default_max_access"` // Default logical download limit for newly-created shares
	PolicyRules      []SharePolicyRuleConfig `toml:"policy_rules"`       // Path-scoped sharing constraints
}

// SharePolicyRuleConfig applies stricter share defaults under a MnemoNAS path.
type SharePolicyRuleConfig struct {
	Path            string        `toml:"path" json:"path"`
	RequirePassword bool          `toml:"require_password" json:"require_password,omitempty"`
	MaxExpiresIn    time.Duration `toml:"max_expires_in" json:"max_expires_in,omitempty"`
	MaxAccess       int64         `toml:"max_access" json:"max_access,omitempty"`
	AllowedUsers    []string      `toml:"allowed_users" json:"allowed_users,omitempty"`
	AllowedGroups   []string      `toml:"allowed_groups" json:"allowed_groups,omitempty"`
	AllowedRoles    []string      `toml:"allowed_roles" json:"allowed_roles,omitempty"`
}

// FavoritesConfig holds favorites configuration
type FavoritesConfig struct {
	Enabled   bool   `toml:"enabled"`    // Enable favorites feature
	StoreFile string `toml:"store_file"` // Path to favorites.json
}

// AlertsConfig holds storage space alerting configuration
type AlertsConfig struct {
	Enabled            bool          `toml:"enabled"`              // Enable storage alerts
	CheckInterval      time.Duration `toml:"check_interval"`       // How often to check (default 1h)
	ThresholdPct       float64       `toml:"threshold_pct"`        // Alert when usage exceeds this % (default 90)
	CriticalPct        float64       `toml:"critical_pct"`         // Critical alert threshold (default 95)
	MinFreeBytes       uint64        `toml:"min_free_bytes"`       // Alert when free space < this (default 10GB)
	CooldownPeriod     time.Duration `toml:"cooldown_period"`      // Min time between alerts (default 4h)
	WebhookURL         string        `toml:"webhook_url"`          // Webhook URL for notifications
	WebhookMethod      string        `toml:"webhook_method"`       // POST or GET (default POST)
	WebhookHeaders     []string      `toml:"webhook_headers"`      // Additional headers (key:value format)
	TelegramEnabled    bool          `toml:"telegram_enabled"`     // Enable Telegram notifications
	TelegramBotToken   string        `toml:"telegram_bot_token"`   // Telegram bot token
	TelegramChatID     string        `toml:"telegram_chat_id"`     // Telegram chat ID or @channel
	WeComEnabled       bool          `toml:"wecom_enabled"`        // Enable WeCom group robot notifications
	WeComWebhookURL    string        `toml:"wecom_webhook_url"`    // WeCom group robot webhook URL
	DingTalkEnabled    bool          `toml:"dingtalk_enabled"`     // Enable DingTalk group robot notifications
	DingTalkWebhookURL string        `toml:"dingtalk_webhook_url"` // DingTalk group robot webhook URL
	EmailEnabled       bool          `toml:"email_enabled"`        // Enable SMTP email notifications
	SMTPHost           string        `toml:"smtp_host"`            // SMTP host without port
	SMTPPort           int           `toml:"smtp_port"`            // SMTP port
	SMTPUsername       string        `toml:"smtp_username"`        // SMTP username
	SMTPPassword       string        `toml:"smtp_password"`        // SMTP password
	SMTPFrom           string        `toml:"smtp_from"`            // Sender email address
	SMTPTo             []string      `toml:"smtp_to"`              // Recipient email addresses
}

// DiskHealthConfig holds SMART and temperature monitoring configuration.
type DiskHealthConfig struct {
	Enabled              bool                     `toml:"enabled"`
	CheckInterval        time.Duration            `toml:"check_interval"`
	ProbeTimeout         time.Duration            `toml:"probe_timeout"`
	CooldownPeriod       time.Duration            `toml:"cooldown_period"`
	Command              string                   `toml:"command"`
	TemperatureWarningC  int                      `toml:"temperature_warning_c"`
	TemperatureCriticalC int                      `toml:"temperature_critical_c"`
	MediaWearWarningPct  int                      `toml:"media_wear_warning_percent"`
	MediaWearCriticalPct int                      `toml:"media_wear_critical_percent"`
	Devices              []DiskHealthDeviceConfig `toml:"devices"`
}

// DiskHealthDeviceConfig describes one disk device for SMART checks.
type DiskHealthDeviceConfig struct {
	Name                 string `toml:"name" json:"name,omitempty"`
	Path                 string `toml:"path" json:"path"`
	Type                 string `toml:"type" json:"type,omitempty"`
	Serial               string `toml:"serial" json:"serial,omitempty"`
	TemperatureWarningC  int    `toml:"temperature_warning_c" json:"temperature_warning_c,omitempty"`
	TemperatureCriticalC int    `toml:"temperature_critical_c" json:"temperature_critical_c,omitempty"`
}

// SecurityConfig holds explicit safety overrides for risky deployment modes.
type SecurityConfig struct {
	AllowUnsafeNoAuth bool `toml:"allow_unsafe_no_auth"` // Permit unauthenticated services on non-loopback binds
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
			Host:              "0.0.0.0",
			Port:              8080,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			TrustedProxyHops:  0,
			TrustedProxyCIDRs: []string{},
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
				MaxVersionedSize: versionstore.MaxVersionObjectSize,
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
		SMB: SMBConfig{
			Enabled:         false,
			Listen:          "127.0.0.1:1445",
			ServerName:      "mnemonas",
			GatewaySocket:   filepath.Join(storageRoot, ".mnemonas", "run", "smb-gateway.sock"),
			CredentialFile:  filepath.Join(storageRoot, ".mnemonas", "smb-credentials.json"),
			SigningRequired: true,
			Shares:          []SMBShareConfig{},
		},
		Backup: BackupConfig{
			Jobs: []BackupJobConfig{},
		},
		Auth: AuthConfig{
			Enabled:         true, // enabled by default for security
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 7 * 24 * time.Hour,
			UsersFile:       filepath.Join(storageRoot, ".mnemonas", "users.json"),
		},
		Share: ShareConfig{
			Enabled:          false, // disabled by default
			StoreFile:        filepath.Join(storageRoot, ".mnemonas", "shares.json"),
			DefaultExpiresIn: 7 * 24 * time.Hour,
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
			SMTPPort:       587,
			SMTPTo:         []string{},
		},
		DiskHealth: DiskHealthConfig{
			Enabled:              false,
			CheckInterval:        1 * time.Hour,
			ProbeTimeout:         15 * time.Second,
			CooldownPeriod:       4 * time.Hour,
			Command:              "smartctl",
			TemperatureWarningC:  50,
			TemperatureCriticalC: 60,
			MediaWearWarningPct:  80,
			MediaWearCriticalPct: 100,
			Devices:              []DiskHealthDeviceConfig{},
		},
		Maintenance: MaintenanceConfig{
			Scrub: ScrubMaintenanceConfig{
				Enabled:          false,
				ScheduleInterval: 7 * 24 * time.Hour,
				RetryInterval:    1 * time.Hour,
				MaxRetries:       1,
			},
		},
		Security: SecurityConfig{
			AllowUnsafeNoAuth: false,
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
	cfg.Alerts.TelegramBotToken = strings.TrimSpace(cfg.Alerts.TelegramBotToken)
	cfg.Alerts.TelegramChatID = strings.TrimSpace(cfg.Alerts.TelegramChatID)
	cfg.Alerts.WeComWebhookURL = strings.TrimSpace(cfg.Alerts.WeComWebhookURL)
	cfg.Alerts.DingTalkWebhookURL = strings.TrimSpace(cfg.Alerts.DingTalkWebhookURL)
	cfg.Alerts.SMTPHost = strings.TrimSpace(cfg.Alerts.SMTPHost)
	cfg.Alerts.SMTPUsername = strings.TrimSpace(cfg.Alerts.SMTPUsername)
	cfg.Alerts.SMTPFrom = strings.TrimSpace(cfg.Alerts.SMTPFrom)
	cfg.Alerts.SMTPTo = normalizeTrimmedStringSlice(cfg.Alerts.SMTPTo)
	cfg.DiskHealth.Command = expandUserPath(strings.TrimSpace(cfg.DiskHealth.Command))
	cfg.DiskHealth.Devices = NormalizeDiskHealthDevices(cfg.DiskHealth.Devices)
	cfg.Server.TLS.CertDir = expandUserPath(cfg.Server.TLS.CertDir)
	cfg.Server.TLS.CertFile = expandUserPath(cfg.Server.TLS.CertFile)
	cfg.Server.TLS.KeyFile = expandUserPath(cfg.Server.TLS.KeyFile)
	cfg.Auth.UsersFile = expandUserPath(cfg.Auth.UsersFile)
	cfg.Share.StoreFile = expandUserPath(cfg.Share.StoreFile)
	cfg.Favorites.StoreFile = expandUserPath(cfg.Favorites.StoreFile)
	cfg.SMB.GatewaySocket = expandUserPath(cfg.SMB.GatewaySocket)
	cfg.SMB.CredentialFile = expandUserPath(cfg.SMB.CredentialFile)
	cfg.SMB.Shares = normalizeSMBShares(cfg.SMB.Shares)
	cfg.Backup.Jobs = normalizeBackupJobs(cfg.Backup.Jobs)
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

	if cfg.SMB.Listen == "" {
		cfg.SMB.Listen = "127.0.0.1:1445"
	}
	if cfg.SMB.ServerName == "" {
		cfg.SMB.ServerName = "mnemonas"
	}

	defaultSMBGatewaySocket := filepath.Join(defaultInternal, "run", "smb-gateway.sock")
	if cfg.SMB.GatewaySocket == "" || cfg.SMB.GatewaySocket == defaultSMBGatewaySocket {
		cfg.SMB.GatewaySocket = filepath.Join(internal, "run", "smb-gateway.sock")
	}

	defaultSMBCredentialFile := filepath.Join(defaultInternal, "smb-credentials.json")
	if cfg.SMB.CredentialFile == "" || cfg.SMB.CredentialFile == defaultSMBCredentialFile {
		cfg.SMB.CredentialFile = filepath.Join(internal, "smb-credentials.json")
	}
	if cfg.DiskHealth.Command == "" {
		cfg.DiskHealth.Command = "smartctl"
	}
	if cfg.DiskHealth.CheckInterval == 0 {
		cfg.DiskHealth.CheckInterval = time.Hour
	}
	if cfg.DiskHealth.ProbeTimeout == 0 {
		cfg.DiskHealth.ProbeTimeout = 15 * time.Second
	}
	if cfg.DiskHealth.CooldownPeriod == 0 {
		cfg.DiskHealth.CooldownPeriod = 4 * time.Hour
	}
	if cfg.DiskHealth.TemperatureWarningC == 0 {
		cfg.DiskHealth.TemperatureWarningC = 50
	}
	if cfg.DiskHealth.TemperatureCriticalC == 0 {
		cfg.DiskHealth.TemperatureCriticalC = 60
	}
	if cfg.DiskHealth.MediaWearWarningPct == 0 {
		cfg.DiskHealth.MediaWearWarningPct = 80
	}
	if cfg.DiskHealth.MediaWearCriticalPct == 0 {
		cfg.DiskHealth.MediaWearCriticalPct = 100
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

func isProtectedStorageRoot(path string) bool {
	if isFilesystemRoot(path) {
		return true
	}

	cleaned := filepath.ToSlash(filepath.Clean(path))
	switch cleaned {
	case "/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib64", "/media", "/mnt",
		"/opt", "/proc", "/root", "/run", "/sbin", "/srv", "/sys", "/tmp", "/usr",
		"/usr/local", "/usr/local/bin", "/usr/local/share", "/var":
		return true
	default:
		return false
	}
}

func normalizeStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func normalizeTrimmedStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
}

func NormalizeTrustedProxyCIDRs(values []string) []string {
	return normalizeTrimmedStringSlice(values)
}

func normalizeSMBShares(shares []SMBShareConfig) []SMBShareConfig {
	if shares == nil {
		return []SMBShareConfig{}
	}
	for i := range shares {
		shares[i].AllowedRoles = normalizeStringSlice(shares[i].AllowedRoles)
		shares[i].AllowedUsers = normalizeStringSlice(shares[i].AllowedUsers)
	}
	return shares
}

// NormalizeDiskHealthDevices returns a copy with trimmed labels and clean host paths.
func NormalizeDiskHealthDevices(devices []DiskHealthDeviceConfig) []DiskHealthDeviceConfig {
	if devices == nil {
		return []DiskHealthDeviceConfig{}
	}
	normalized := make([]DiskHealthDeviceConfig, 0, len(devices))
	for _, device := range devices {
		copied := device
		copied.Name = strings.TrimSpace(copied.Name)
		copied.Path = expandUserPath(strings.TrimSpace(copied.Path))
		if copied.Path != "" {
			copied.Path = filepath.Clean(copied.Path)
		}
		copied.Type = strings.TrimSpace(copied.Type)
		copied.Serial = strings.TrimSpace(copied.Serial)
		normalized = append(normalized, copied)
	}
	return normalized
}

func normalizeBackupJobs(jobs []BackupJobConfig) []BackupJobConfig {
	if jobs == nil {
		return []BackupJobConfig{}
	}
	for i := range jobs {
		jobs[i].ID = strings.TrimSpace(jobs[i].ID)
		jobs[i].Name = strings.TrimSpace(jobs[i].Name)
		jobs[i].Type = strings.ToLower(strings.TrimSpace(jobs[i].Type))
		if jobs[i].Type == "" {
			jobs[i].Type = "local"
		}
		jobs[i].Source = expandUserPath(strings.TrimSpace(jobs[i].Source))
		jobs[i].Destination = expandUserPath(strings.TrimSpace(jobs[i].Destination))
		jobs[i].Repository = strings.TrimSpace(jobs[i].Repository)
		jobs[i].Remote = strings.TrimSpace(jobs[i].Remote)
		jobs[i].Command = expandUserPath(strings.TrimSpace(jobs[i].Command))
		jobs[i].PasswordFile = expandUserPath(strings.TrimSpace(jobs[i].PasswordFile))
		jobs[i].ConfigFile = expandUserPath(strings.TrimSpace(jobs[i].ConfigFile))
		jobs[i].ScheduleWindowStart = strings.TrimSpace(jobs[i].ScheduleWindowStart)
		jobs[i].ScheduleWindowEnd = strings.TrimSpace(jobs[i].ScheduleWindowEnd)
		jobs[i].RetentionPolicy = strings.TrimSpace(jobs[i].RetentionPolicy)
		jobs[i].ExtraArgs = normalizeStringSlice(jobs[i].ExtraArgs)
		for j := range jobs[i].ExtraArgs {
			jobs[i].ExtraArgs[j] = strings.TrimSpace(jobs[i].ExtraArgs[j])
		}
		jobs[i].Exclude = normalizeStringSlice(jobs[i].Exclude)
		for j := range jobs[i].Exclude {
			jobs[i].Exclude[j] = strings.TrimSpace(filepath.ToSlash(jobs[i].Exclude[j]))
		}
	}
	return jobs
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
	if err := ensureConfigFilePermissions(normalizedPath); err != nil {
		return nil, err
	}

	normalizedData, err := normalizeDurationFields(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := toml.Unmarshal(normalizedData, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	applyStorageRootDefaults(cfg, getDefaultStorageRoot())
	if strings.TrimSpace(cfg.Auth.JWTSecret) == "" {
		cfg.Auth.JWTSecret = ""
	}
	cfg.Server.TrustedProxyCIDRs = NormalizeTrustedProxyCIDRs(cfg.Server.TrustedProxyCIDRs)
	cfg.Storage.DirectoryQuotas = NormalizeDirectoryQuotas(cfg.Storage.DirectoryQuotas)
	cfg.Storage.DirectoryAccessRules = NormalizeDirectoryAccessRules(cfg.Storage.DirectoryAccessRules)
	cfg.WebDAV.Prefix = NormalizeWebDAVPrefix(cfg.WebDAV.Prefix)
	cfg.WebDAV.AuthType = NormalizeWebDAVAuthType(cfg.WebDAV.AuthType)
	cfg.Share.BaseURL = strings.TrimSpace(cfg.Share.BaseURL)
	cfg.Share.PolicyRules = NormalizeSharePolicyRules(cfg.Share.PolicyRules)
	cfg.Alerts.WebhookURL = strings.TrimSpace(cfg.Alerts.WebhookURL)
	cfg.Alerts.WebhookMethod = strings.ToUpper(strings.TrimSpace(cfg.Alerts.WebhookMethod))
	cfg.Alerts.TelegramBotToken = strings.TrimSpace(cfg.Alerts.TelegramBotToken)
	cfg.Alerts.TelegramChatID = strings.TrimSpace(cfg.Alerts.TelegramChatID)
	cfg.Alerts.WeComWebhookURL = strings.TrimSpace(cfg.Alerts.WeComWebhookURL)
	cfg.Alerts.DingTalkWebhookURL = strings.TrimSpace(cfg.Alerts.DingTalkWebhookURL)
	cfg.Alerts.SMTPHost = strings.TrimSpace(cfg.Alerts.SMTPHost)
	cfg.Alerts.SMTPUsername = strings.TrimSpace(cfg.Alerts.SMTPUsername)
	cfg.Alerts.SMTPFrom = strings.TrimSpace(cfg.Alerts.SMTPFrom)
	cfg.Alerts.SMTPTo = normalizeTrimmedStringSlice(cfg.Alerts.SMTPTo)
	if cfg.Alerts.SMTPPort == 0 {
		cfg.Alerts.SMTPPort = 587
	}
	cfg.SMB.ServerName = strings.TrimSpace(cfg.SMB.ServerName)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

// NormalizeDirectoryQuotas returns a copy with trimmed, clean MnemoNAS paths.
func NormalizeDirectoryQuotas(quotas []DirectoryQuotaConfig) []DirectoryQuotaConfig {
	if len(quotas) == 0 {
		return nil
	}

	normalized := make([]DirectoryQuotaConfig, 0, len(quotas))
	for _, quota := range quotas {
		copied := quota
		copied.Path = normalizeScopedConfigPath(copied.Path)
		normalized = append(normalized, copied)
	}
	return normalized
}

// NormalizeDirectoryAccessRules returns a copy with clean paths and normalized principals.
func NormalizeDirectoryAccessRules(rules []DirectoryAccessRuleConfig) []DirectoryAccessRuleConfig {
	if len(rules) == 0 {
		return nil
	}

	normalized := make([]DirectoryAccessRuleConfig, 0, len(rules))
	for _, rule := range rules {
		copied := rule
		copied.Path = normalizeScopedConfigPath(copied.Path)
		copied.ReadUsers = normalizeAccessRulePrincipalList(copied.ReadUsers)
		copied.WriteUsers = normalizeAccessRulePrincipalList(copied.WriteUsers)
		copied.ReadGroups = normalizeAccessRulePrincipalList(copied.ReadGroups)
		copied.WriteGroups = normalizeAccessRulePrincipalList(copied.WriteGroups)
		copied.ReadRoles = normalizeAccessRuleRoleList(copied.ReadRoles)
		copied.WriteRoles = normalizeAccessRuleRoleList(copied.WriteRoles)
		normalized = append(normalized, copied)
	}
	return normalized
}

// NormalizeSharePolicyRules returns a copy with trimmed, clean MnemoNAS paths.
func NormalizeSharePolicyRules(rules []SharePolicyRuleConfig) []SharePolicyRuleConfig {
	if len(rules) == 0 {
		return nil
	}

	normalized := make([]SharePolicyRuleConfig, 0, len(rules))
	for _, rule := range rules {
		copied := rule
		copied.Path = normalizeScopedConfigPath(copied.Path)
		copied.AllowedUsers = normalizeAccessRulePrincipalList(copied.AllowedUsers)
		copied.AllowedGroups = normalizeAccessRulePrincipalList(copied.AllowedGroups)
		copied.AllowedRoles = normalizeAccessRuleRoleList(copied.AllowedRoles)
		normalized = append(normalized, copied)
	}
	return normalized
}

func normalizeScopedConfigPath(rawPath string) string {
	cleaned := strings.TrimSpace(rawPath)
	if cleaned != "" && strings.HasPrefix(cleaned, "/") && !hasScopedConfigDotSegment(cleaned) {
		cleaned = urlpath.Clean(cleaned)
	}
	return cleaned
}

func hasScopedConfigDotSegment(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func normalizeAccessRulePrincipalList(values []string) []string {
	return normalizeUniqueLowercaseStrings(values)
}

func normalizeAccessRuleRoleList(values []string) []string {
	return normalizeUniqueLowercaseStrings(values)
}

// ValidateDirectoryAccessRules validates normalized directory access rules.
func ValidateDirectoryAccessRules(rules []DirectoryAccessRuleConfig) error {
	return validateDirectoryAccessRules(rules)
}

func normalizeUniqueLowercaseStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		cleaned := strings.ToLower(strings.TrimSpace(value))
		if cleaned == "" {
			normalized = append(normalized, cleaned)
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	sort.Strings(normalized)
	return normalized
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
	if err := normalizeBackupJobDurationFields(raw); err != nil {
		return nil, err
	}
	if err := normalizeSharePolicyRuleDurationFields(raw); err != nil {
		return nil, err
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

	if durationText == "" && durationFieldAllowsEmptyString(fieldPath) {
		current[leafKey] = int64(0)
		return nil
	}

	parsedDuration, err := time.ParseDuration(durationText)
	if err != nil {
		return fmt.Errorf("invalid %s duration %q: %w", strings.Join(fieldPath, "."), durationText, err)
	}

	current[leafKey] = int64(parsedDuration)
	return nil
}

func durationFieldAllowsEmptyString(fieldPath []string) bool {
	return len(fieldPath) == 2 && fieldPath[0] == "share" && fieldPath[1] == "default_expires_in"
}

func normalizeBackupJobDurationFields(raw map[string]any) error {
	backupValue, ok := raw["backup"]
	if !ok {
		return nil
	}
	backupMap, ok := backupValue.(map[string]any)
	if !ok {
		return errors.New("invalid config structure at backup")
	}
	jobsValue, ok := backupMap["jobs"]
	if !ok {
		return nil
	}
	jobs, ok := jobsValue.([]any)
	if !ok {
		return errors.New("invalid config structure at backup.jobs")
	}
	for i, jobValue := range jobs {
		jobMap, ok := jobValue.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid config structure at backup.jobs[%d]", i)
		}
		for _, field := range backupJobDurationFields {
			if err := normalizeBackupJobDurationField(jobMap, i, field); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeBackupJobDurationField(job map[string]any, index int, field string) error {
	value, ok := job[field]
	if !ok {
		return nil
	}
	durationText, ok := value.(string)
	if !ok {
		return nil
	}
	if durationText == "" && backupJobDurationFieldAllowsEmptyString(field) {
		job[field] = int64(0)
		return nil
	}
	parsedDuration, err := time.ParseDuration(durationText)
	if err != nil {
		return fmt.Errorf("invalid backup.jobs[%d].%s duration %q: %w", index, field, durationText, err)
	}
	job[field] = int64(parsedDuration)
	return nil
}

func backupJobDurationFieldAllowsEmptyString(field string) bool {
	return field == "schedule_interval" || field == "restore_drill_stale_after"
}

func normalizeSharePolicyRuleDurationFields(raw map[string]any) error {
	shareValue, ok := raw["share"]
	if !ok {
		return nil
	}
	shareMap, ok := shareValue.(map[string]any)
	if !ok {
		return errors.New("invalid config structure at share")
	}
	rulesValue, ok := shareMap["policy_rules"]
	if !ok {
		return nil
	}
	rules, ok := rulesValue.([]any)
	if !ok {
		return errors.New("invalid config structure at share.policy_rules")
	}
	for i, ruleValue := range rules {
		ruleMap, ok := ruleValue.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid config structure at share.policy_rules[%d]", i)
		}
		value, ok := ruleMap["max_expires_in"]
		if !ok {
			continue
		}
		durationText, ok := value.(string)
		if !ok {
			continue
		}
		parsedDuration, err := time.ParseDuration(durationText)
		if err != nil {
			return fmt.Errorf("invalid share.policy_rules[%d].max_expires_in duration %q: %w", i, durationText, err)
		}
		ruleMap["max_expires_in"] = int64(parsedDuration)
	}
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
	normalizedPath, _, _, err := ensureManagedDirRootWithState(path, symlinkErr, label, create)
	return normalizedPath, err
}

func ensureManagedDirRootWithState(path string, symlinkErr error, label string, create bool) (string, *os.Root, []string, error) {
	normalizedPath, err := normalizeManagedFilePath(path, label)
	if err != nil {
		return "", nil, nil, err
	}
	if err := validateManagedFilePath(normalizedPath, symlinkErr, label); err != nil {
		return "", nil, nil, err
	}

	dir := filepath.Dir(normalizedPath)
	managedDirRootsMu.RLock()
	root := managedDirRoots[dir]
	managedDirRootsMu.RUnlock()
	if root != nil {
		return normalizedPath, nil, nil, nil
	}

	createdDirs := []string(nil)
	if create {
		var err error
		createdDirs, err = ensureManagedDir(dir, 0755, symlinkErr)
		if err != nil {
			return "", nil, createdDirs, fmt.Errorf("failed to create %s directory: %w", label, err)
		}
	} else if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizedPath, nil, nil, nil
		}
		return "", nil, nil, fmt.Errorf("failed to stat %s directory: %w", label, err)
	}

	root, err = os.OpenRoot(dir)
	if err != nil {
		return "", nil, createdDirs, fmt.Errorf("failed to open %s directory root: %w", label, err)
	}

	managedDirRootsMu.Lock()
	if existing := managedDirRoots[dir]; existing != nil {
		managedDirRootsMu.Unlock()
		_ = root.Close()
		return normalizedPath, nil, createdDirs, nil
	}
	managedDirRoots[dir] = root
	managedDirRootsMu.Unlock()

	return normalizedPath, root, createdDirs, nil
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
		normalizedPath, _, _, err = ensureManagedDirRootWithState(normalizedPath, symlinkErr, label, false)
		if err != nil {
			return nil, err
		}
		root, normalizedPath, ok, err = registeredManagedDirRoot(normalizedPath, label)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &os.PathError{Op: "open", Path: normalizedPath, Err: os.ErrNotExist}
		}
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
	registeredRoot := (*os.Root)(nil)
	releaseRootOnError := false
	createdDirs := []string(nil)
	normalizedPath, registeredRoot, createdDirs, err = ensureManagedDirRootWithState(normalizedPath, symlinkErr, label, true)
	if err != nil {
		releaseRegisteredManagedDirRoot(filepath.Dir(normalizedPath), registeredRoot)
		return cleanupCreatedManagedDirs(createdDirs, label, err)
	}
	if registeredRoot != nil {
		releaseRootOnError = true
	} else {
		root, normalizedPath, ok, err = registeredManagedDirRoot(normalizedPath, label)
		if err != nil {
			return err
		}
		if !ok {
			return &os.PathError{Op: "open", Path: filepath.Dir(normalizedPath), Err: os.ErrNotExist}
		}
		registeredRoot = root
	}
	if err := writeManagedFileAtomicallyWithRoot(registeredRoot, normalizedPath, data, symlinkErr, pattern, label, perm); err != nil {
		if releaseRootOnError {
			releaseRegisteredManagedDirRoot(filepath.Dir(normalizedPath), registeredRoot)
			return cleanupCreatedManagedDirs(createdDirs, label, err)
		}
		return err
	}
	return nil
}

func chmodRegisteredManagedFile(path string, mode os.FileMode, symlinkErr error, label string) error {
	root, normalizedPath, ok, err := registeredManagedDirRoot(path, label)
	if err != nil {
		return err
	}
	if !ok {
		normalizedPath, _, _, err = ensureManagedDirRootWithState(normalizedPath, symlinkErr, label, false)
		if err != nil {
			return err
		}
		root, normalizedPath, ok, err = registeredManagedDirRoot(normalizedPath, label)
		if err != nil {
			return err
		}
		if !ok {
			return &os.PathError{Op: "chmod", Path: normalizedPath, Err: os.ErrNotExist}
		}
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
	parentDir, err := rootio.OpenDirPathNoFollow(dir)
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

func syncCreatedManagedDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncManagedDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync managed directory tree: %w", err)
		}
	}
	return nil
}

func ensureManagedDir(dir string, perm os.FileMode, symlinkErr error) ([]string, error) {
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return createdDirs, symlinkErr
		}
		return createdDirs, err
	}
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return createdDirs, symlinkErr
		}
		return createdDirs, err
	}
	defer dirHandle.Close()
	if err := dirHandle.Chmod(perm); err != nil {
		return createdDirs, err
	}
	return createdDirs, syncCreatedManagedDirs(createdDirs)
}

func readManagedFileWithRoot(root *os.Root, path string, symlinkErr error, label string) ([]byte, error) {
	if err := validateManagedFilePath(path, symlinkErr, label); err != nil {
		return nil, err
	}
	afterValidateManagedFilePath()

	file, err := rootio.OpenFileNoFollow(root, filepath.Base(path), os.O_RDONLY, 0)
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
	file, err := rootio.OpenFileNoFollow(root, filepath.Base(path), os.O_RDONLY, 0)
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
	for range maxManagedTempAttempts {
		tmpName, err := managedTempName(pattern)
		if err != nil {
			return nil, "", err
		}
		tmpFile, err := rootio.OpenFileNoFollow(root, tmpName, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err == nil {
			return tmpFile, tmpName, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapManagedRootPathError(err, symlinkErr)
	}

	return nil, "", errors.New("failed to allocate unique managed temp file")
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
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ELOOP) || rootio.IsSymlinkError(err) || isManagedRootEscapeError(err) {
		return symlinkErr
	}
	return err
}

func isManagedRootEscapeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), managedRootEscapeError)
}

func writeConfigFile(path string, data []byte) error {
	return writeRegisteredManagedFileAtomically(path, data, errConfigFileSymlink, ".config-*.tmp", "config", configFileMode)
}

func ensureConfigFilePermissions(configPath string) error {
	if err := chmodRegisteredManagedFile(configPath, configFileMode, errConfigFileSymlink, "config file"); err != nil {
		return fmt.Errorf("failed to secure config file permissions: %w", err)
	}
	return nil
}

// Validate validates configuration
func (c *Config) Validate() error {
	var errs []error

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("invalid port: %d", c.Server.Port))
	}
	if err := validateListenHost(c.Server.Host); err != nil {
		errs = append(errs, err)
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
	if err := validateTrustedProxyCIDRs(c.Server.TrustedProxyCIDRs); err != nil {
		errs = append(errs, err)
	}
	if err := validateServerTLSConfig(c.Server.TLS); err != nil {
		errs = append(errs, err)
	}

	if c.Storage.Root == "" {
		errs = append(errs, errors.New("storage.root cannot be empty"))
	} else if isFilesystemRoot(c.Storage.Root) {
		errs = append(errs, errors.New("storage.root cannot be filesystem root"))
	} else if isProtectedStorageRoot(c.Storage.Root) {
		errs = append(errs, errors.New("storage.root cannot be a protected system directory"))
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
	} else if c.Storage.Versioning.MaxVersionedSize > versionstore.MaxVersionObjectSize {
		errs = append(errs, fmt.Errorf("storage.versioning.max_versioned_size must not exceed %d", versionstore.MaxVersionObjectSize))
	}
	if err := validateDirectoryQuotas(c.Storage.DirectoryQuotas); err != nil {
		errs = append(errs, err)
	}
	if err := validateDirectoryAccessRules(c.Storage.DirectoryAccessRules); err != nil {
		errs = append(errs, err)
	}
	webdavAuthType := NormalizeWebDAVAuthType(c.WebDAV.AuthType)
	if webdavAuthType != "" && webdavAuthType != "none" && webdavAuthType != "basic" && webdavAuthType != "users" {
		errs = append(errs, fmt.Errorf("invalid webdav.auth_type: %q", c.WebDAV.AuthType))
	}
	if c.WebDAV.Enabled && webdavAuthType == "users" && !c.Auth.Enabled {
		errs = append(errs, errors.New("webdav.auth_type=users requires auth.enabled=true"))
	}
	if err := validateWebDAVPrefix(c.WebDAV.Prefix); err != nil {
		errs = append(errs, err)
	}
	if c.WebDAV.Enabled && webDAVPrefixOverlapsReservedRoute(c.WebDAV.Prefix) {
		errs = append(errs, errors.New("webdav.prefix overlaps a reserved HTTP route namespace"))
	}
	if c.Auth.AccessTokenTTL < minimumAuthAccessTokenTTL {
		errs = append(errs, fmt.Errorf("auth.access_token_ttl must be at least %s", minimumAuthAccessTokenTTL))
	}
	if c.Auth.RefreshTokenTTL <= 0 {
		errs = append(errs, errors.New("auth.refresh_token_ttl must be positive"))
	}
	trimmedJWTSecret := strings.TrimSpace(c.Auth.JWTSecret)
	if trimmedJWTSecret != "" && len([]byte(trimmedJWTSecret)) < 32 {
		errs = append(errs, errors.New("auth.jwt_secret must be empty for generated secrets or at least 32 bytes"))
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
	serverBeyondLoopback := listensBeyondLoopback(c.Server.Host)
	if !c.Security.AllowUnsafeNoAuth && serverBeyondLoopback && !c.Auth.Enabled {
		errs = append(errs, errors.New("auth.enabled=false requires server.host to be loopback or security.allow_unsafe_no_auth=true"))
	}
	if !c.Security.AllowUnsafeNoAuth && serverBeyondLoopback && c.WebDAV.Enabled && webdavAuthType == "none" {
		errs = append(errs, errors.New("webdav.auth_type=none requires server.host to be loopback or security.allow_unsafe_no_auth=true"))
	}
	if err := validateSMBConfig(c.SMB); err != nil {
		errs = append(errs, err)
	}
	if err := validateBackupConfig(c.Backup, c.Storage.Root); err != nil {
		errs = append(errs, err)
	}
	if err := validateDiskHealthConfig(c.DiskHealth); err != nil {
		errs = append(errs, err)
	}
	if err := validateMaintenanceConfig(c.Maintenance); err != nil {
		errs = append(errs, err)
	}
	if err := validateShareBaseURL(c.Share.BaseURL); err != nil {
		errs = append(errs, err)
	}
	if c.Share.DefaultExpiresIn < 0 {
		errs = append(errs, errors.New("share.default_expires_in cannot be negative"))
	}
	if c.Share.DefaultMaxAccess < 0 {
		errs = append(errs, errors.New("share.default_max_access cannot be negative"))
	}
	if err := validateSharePolicyRules(c.Share.PolicyRules); err != nil {
		errs = append(errs, err)
	}

	if err := validateTCPAddress(c.DataPlane.GRPCAddress, "dataplane.grpc_address"); err != nil {
		errs = append(errs, err)
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
	if cdc.MinChunkSize > 0 && cdc.MinChunkSize < MinCDCChunkSize {
		errs = append(errs, fmt.Errorf("min_chunk_size must be greater than or equal to %d", MinCDCChunkSize))
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
	if cdc.MaxChunkSize > MaxCDCChunkSize {
		errs = append(errs, fmt.Errorf("max_chunk_size must be less than or equal to %d", MaxCDCChunkSize))
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
	if err := validateOptionalHTTPURL(c.Alerts.WebhookURL, "alerts.webhook_url"); err != nil {
		errs = append(errs, err)
	}
	seenWebhookHeaderNames := make(map[string]string)
	for _, header := range c.Alerts.WebhookHeaders {
		key, err := validateWebhookHeader(header)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if key == "" {
			continue
		}
		normalizedKey := strings.ToLower(key)
		if previous, ok := seenWebhookHeaderNames[normalizedKey]; ok {
			errs = append(errs, fmt.Errorf("duplicate alerts.webhook_headers header name: %q conflicts with %q", key, previous))
			continue
		}
		seenWebhookHeaderNames[normalizedKey] = key
	}
	if err := validateEmailAlertsConfig(c.Alerts); err != nil {
		errs = append(errs, err)
	}
	if err := validateTelegramAlertsConfig(c.Alerts); err != nil {
		errs = append(errs, err)
	}
	if err := validateWeComAlertsConfig(c.Alerts); err != nil {
		errs = append(errs, err)
	}
	if err := validateDingTalkAlertsConfig(c.Alerts); err != nil {
		errs = append(errs, err)
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

func validateTrustedProxyCIDRs(values []string) error {
	for index, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if strings.Contains(value, "/") {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return fmt.Errorf("server.trusted_proxy_cidrs[%d] must be an IP address or CIDR", index)
			}
			continue
		}
		if net.ParseIP(value) == nil {
			return fmt.Errorf("server.trusted_proxy_cidrs[%d] must be an IP address or CIDR", index)
		}
	}
	return nil
}

func validateServerTLSConfig(cfg TLSConfig) error {
	if !cfg.Enabled {
		return nil
	}

	var errs []error
	certFile := strings.TrimSpace(cfg.CertFile)
	keyFile := strings.TrimSpace(cfg.KeyFile)
	certDir := strings.TrimSpace(cfg.CertDir)
	certConfigured := certFile != ""
	keyConfigured := keyFile != ""

	if certConfigured != keyConfigured {
		errs = append(errs, errors.New("server.tls.cert_file and server.tls.key_file must be set together"))
	}
	if !cfg.AutoGenerate && !certConfigured && !keyConfigured && certDir == "" {
		errs = append(errs, errors.New("server.tls.cert_dir or a certificate file pair is required when server.tls.auto_generate=false"))
	}
	if certConfigured && keyConfigured {
		samePath, err := sameConfigFilePath(certFile, keyFile)
		if err != nil {
			errs = append(errs, err)
		} else if samePath {
			errs = append(errs, errors.New("server.tls.cert_file and server.tls.key_file must be different files"))
		}
	}

	return errors.Join(errs...)
}

func sameConfigFilePath(left, right string) (bool, error) {
	normalizedLeft, err := normalizeComparableConfigPath(left, "server.tls.cert_file")
	if err != nil {
		return false, err
	}
	normalizedRight, err := normalizeComparableConfigPath(right, "server.tls.key_file")
	if err != nil {
		return false, err
	}
	return normalizedLeft == normalizedRight, nil
}

func normalizeComparableConfigPath(path, label string) (string, error) {
	cleaned := filepath.Clean(expandUserPath(path))
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s path: %w", label, err)
	}
	return filepath.Clean(absPath), nil
}

func validateWebhookHeader(header string) (string, error) {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return "", nil
	}

	key, value, ok := strings.Cut(trimmed, ":")
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if !ok || key == "" || value == "" {
		return "", fmt.Errorf("invalid alerts.webhook_headers entry: %q", header)
	}
	if !isValidHTTPHeaderToken(key) {
		return "", fmt.Errorf("invalid alerts.webhook_headers header name: %q", key)
	}
	if hasInvalidHTTPHeaderValueControl(value) {
		return "", fmt.Errorf("invalid alerts.webhook_headers header value for %q", key)
	}
	return key, nil
}

func validateEmailAlertsConfig(alerts AlertsConfig) error {
	var errs []error
	if alerts.SMTPPort < 0 || alerts.SMTPPort > 65535 {
		errs = append(errs, errors.New("alerts.smtp_port must be between 1 and 65535"))
	}
	if alerts.SMTPHost != "" && hasControlChar(alerts.SMTPHost) {
		errs = append(errs, errors.New("alerts.smtp_host contains invalid control characters"))
	}
	if alerts.SMTPUsername != "" && hasControlChar(alerts.SMTPUsername) {
		errs = append(errs, errors.New("alerts.smtp_username contains invalid control characters"))
	}
	if alerts.SMTPPassword != "" && hasControlChar(alerts.SMTPPassword) {
		errs = append(errs, errors.New("alerts.smtp_password contains invalid control characters"))
	}
	if strings.TrimSpace(alerts.SMTPFrom) != "" {
		if _, err := mail.ParseAddress(alerts.SMTPFrom); err != nil {
			errs = append(errs, fmt.Errorf("alerts.smtp_from must be a valid email address: %w", err))
		}
	}
	for _, recipient := range alerts.SMTPTo {
		if _, err := mail.ParseAddress(recipient); err != nil {
			errs = append(errs, fmt.Errorf("alerts.smtp_to contains invalid email address %q: %w", recipient, err))
		}
	}
	if alerts.EmailEnabled {
		if strings.TrimSpace(alerts.SMTPHost) == "" {
			errs = append(errs, errors.New("alerts.smtp_host is required when email alerts are enabled"))
		}
		if alerts.SMTPPort <= 0 || alerts.SMTPPort > 65535 {
			errs = append(errs, errors.New("alerts.smtp_port must be between 1 and 65535 when email alerts are enabled"))
		}
		if strings.TrimSpace(alerts.SMTPFrom) == "" {
			errs = append(errs, errors.New("alerts.smtp_from is required when email alerts are enabled"))
		}
		if len(alerts.SMTPTo) == 0 {
			errs = append(errs, errors.New("alerts.smtp_to must include at least one recipient when email alerts are enabled"))
		}
	}
	return errors.Join(errs...)
}

func validateTelegramAlertsConfig(alerts AlertsConfig) error {
	var errs []error
	if alerts.TelegramBotToken != "" {
		if strings.ContainsAny(alerts.TelegramBotToken, "/?#") || hasWhitespaceOrControl(alerts.TelegramBotToken) {
			errs = append(errs, errors.New("alerts.telegram_bot_token contains invalid characters"))
		}
	}
	if alerts.TelegramChatID != "" && hasWhitespaceOrControl(alerts.TelegramChatID) {
		errs = append(errs, errors.New("alerts.telegram_chat_id contains invalid characters"))
	}
	if alerts.TelegramEnabled {
		if strings.TrimSpace(alerts.TelegramBotToken) == "" {
			errs = append(errs, errors.New("alerts.telegram_bot_token is required when telegram alerts are enabled"))
		}
		if strings.TrimSpace(alerts.TelegramChatID) == "" {
			errs = append(errs, errors.New("alerts.telegram_chat_id is required when telegram alerts are enabled"))
		}
	}
	return errors.Join(errs...)
}

func validateWeComAlertsConfig(alerts AlertsConfig) error {
	var errs []error
	if err := validateOptionalHTTPURL(alerts.WeComWebhookURL, "alerts.wecom_webhook_url"); err != nil {
		errs = append(errs, err)
	}
	if alerts.WeComEnabled && strings.TrimSpace(alerts.WeComWebhookURL) == "" {
		errs = append(errs, errors.New("alerts.wecom_webhook_url is required when wecom alerts are enabled"))
	}
	return errors.Join(errs...)
}

func validateDingTalkAlertsConfig(alerts AlertsConfig) error {
	var errs []error
	if err := validateOptionalHTTPURL(alerts.DingTalkWebhookURL, "alerts.dingtalk_webhook_url"); err != nil {
		errs = append(errs, err)
	}
	if alerts.DingTalkEnabled && strings.TrimSpace(alerts.DingTalkWebhookURL) == "" {
		errs = append(errs, errors.New("alerts.dingtalk_webhook_url is required when dingtalk alerts are enabled"))
	}
	return errors.Join(errs...)
}

func validateSMBConfig(smb SMBConfig) error {
	var errs []error

	if err := validateTCPAddress(smb.Listen, "smb.listen"); err != nil {
		errs = append(errs, err)
	}
	if err := validateSMBServerName(smb.ServerName); err != nil {
		errs = append(errs, err)
	}
	if err := validateAbsoluteFilePath(smb.GatewaySocket, "smb.gateway_socket"); err != nil {
		errs = append(errs, err)
	}
	if err := validateAbsoluteFilePath(smb.CredentialFile, "smb.credential_file"); err != nil {
		errs = append(errs, err)
	}
	if smb.Enabled && len(smb.Shares) == 0 {
		errs = append(errs, errors.New("smb.shares must contain at least one share when smb.enabled=true"))
	}

	seen := map[string]struct{}{}
	for _, share := range smb.Shares {
		if err := validateSMBShare(share, seen); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func validateSMBServerName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return errors.New("smb.server_name cannot be empty")
	}
	if trimmed != name || len(trimmed) > 63 || strings.ContainsAny(trimmed, "\\/:*?\"<>|") {
		return fmt.Errorf("invalid smb.server_name: %q", name)
	}
	if hasWhitespaceOrControl(trimmed) {
		return errors.New("smb.server_name must not contain whitespace or control characters")
	}
	return nil
}

func validateAbsoluteFilePath(path, field string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return fmt.Errorf("%s cannot be empty", field)
	}
	if trimmed != path {
		return fmt.Errorf("%s must not contain leading or trailing whitespace", field)
	}
	if hasControlChar(trimmed) {
		return fmt.Errorf("%s must not contain control characters", field)
	}
	if !filepath.IsAbs(trimmed) {
		return fmt.Errorf("%s must be an absolute path", field)
	}
	return nil
}

func validateSMBShare(share SMBShareConfig, seen map[string]struct{}) error {
	var errs []error

	name := strings.TrimSpace(share.Name)
	switch {
	case name == "":
		errs = append(errs, errors.New("smb.shares.name cannot be empty"))
	case name != share.Name:
		errs = append(errs, fmt.Errorf("smb share name %q must not contain leading or trailing whitespace", share.Name))
	case strings.EqualFold(name, "IPC$"):
		errs = append(errs, errors.New("smb share name IPC$ is reserved"))
	case strings.ContainsAny(name, "\\/:*?\"<>|"):
		errs = append(errs, fmt.Errorf("invalid smb share name: %q", share.Name))
	case hasWhitespaceOrControl(name):
		errs = append(errs, fmt.Errorf("smb share name %q must not contain whitespace or control characters", share.Name))
	default:
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			errs = append(errs, fmt.Errorf("duplicate smb share name: %q", share.Name))
		}
		seen[key] = struct{}{}
	}

	if err := validateSMBSharePath(share.Path); err != nil {
		errs = append(errs, err)
	}
	if len(share.AllowedRoles) == 0 && len(share.AllowedUsers) == 0 {
		errs = append(errs, fmt.Errorf("smb share %q must allow at least one role or user", share.Name))
	}
	for _, role := range share.AllowedRoles {
		switch strings.ToLower(strings.TrimSpace(role)) {
		case "admin", "user", "guest":
		default:
			errs = append(errs, fmt.Errorf("invalid smb share role %q for share %q", role, share.Name))
		}
	}
	for _, user := range share.AllowedUsers {
		if strings.TrimSpace(user) == "" || hasWhitespaceOrControl(user) {
			errs = append(errs, fmt.Errorf("invalid smb share user %q for share %q", user, share.Name))
		}
	}

	return errors.Join(errs...)
}

func validateSMBSharePath(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return errors.New("smb.shares.path cannot be empty")
	}
	if trimmed != path {
		return fmt.Errorf("smb share path %q must not contain leading or trailing whitespace", path)
	}
	if !strings.HasPrefix(trimmed, "/") {
		return fmt.Errorf("smb share path %q must be absolute", path)
	}
	if strings.ContainsAny(trimmed, "\\?#") {
		return fmt.Errorf("smb share path %q must be a clean MnemoNAS path", path)
	}
	if hasControlChar(trimmed) {
		return errors.New("smb share path must not contain control characters")
	}
	if urlpath.Clean(trimmed) != trimmed {
		return fmt.Errorf("smb share path %q must be clean", path)
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("smb share path %q must not contain dot segments", path)
		}
	}
	return nil
}

func validateDirectoryQuotas(quotas []DirectoryQuotaConfig) error {
	var errs []error
	seen := map[string]struct{}{}
	for i, quota := range quotas {
		fieldPrefix := fmt.Sprintf("storage.directory_quotas[%d]", i)
		if err := validateDirectoryQuotaPath(quota.Path, fieldPrefix+".path"); err != nil {
			errs = append(errs, err)
		}
		if quota.QuotaBytes <= 0 {
			errs = append(errs, fmt.Errorf("%s.quota_bytes must be positive", fieldPrefix))
		}
		if quota.Path != "" {
			key := quota.Path
			if _, ok := seen[key]; ok {
				errs = append(errs, fmt.Errorf("duplicate directory quota path: %q", quota.Path))
			}
			seen[key] = struct{}{}
		}
	}
	return errors.Join(errs...)
}

func validateDirectoryAccessRules(rules []DirectoryAccessRuleConfig) error {
	var errs []error
	seen := map[string]struct{}{}
	for i, rule := range rules {
		fieldPrefix := fmt.Sprintf("storage.directory_access_rules[%d]", i)
		if err := validateDirectoryQuotaPath(rule.Path, fieldPrefix+".path"); err != nil {
			errs = append(errs, err)
		}
		if rule.Path != "" {
			if _, ok := seen[rule.Path]; ok {
				errs = append(errs, fmt.Errorf("duplicate directory access rule path: %q", rule.Path))
			}
			seen[rule.Path] = struct{}{}
		}
		if !directoryAccessRuleHasPrincipals(rule) {
			errs = append(errs, fmt.Errorf("%s must grant at least one read or write principal", fieldPrefix))
		}
		errs = append(errs, validateAccessRulePrincipals(rule.ReadUsers, fieldPrefix+".read_users")...)
		errs = append(errs, validateAccessRulePrincipals(rule.WriteUsers, fieldPrefix+".write_users")...)
		errs = append(errs, validateAccessRulePrincipals(rule.ReadGroups, fieldPrefix+".read_groups")...)
		errs = append(errs, validateAccessRulePrincipals(rule.WriteGroups, fieldPrefix+".write_groups")...)
		errs = append(errs, validateAccessRuleRoles(rule.ReadRoles, fieldPrefix+".read_roles")...)
		errs = append(errs, validateAccessRuleRoles(rule.WriteRoles, fieldPrefix+".write_roles")...)
	}
	return errors.Join(errs...)
}

func validateSharePolicyRules(rules []SharePolicyRuleConfig) error {
	var errs []error
	seen := map[string]struct{}{}
	for i, rule := range rules {
		fieldPrefix := fmt.Sprintf("share.policy_rules[%d]", i)
		if err := validateDirectoryQuotaPath(rule.Path, fieldPrefix+".path"); err != nil {
			errs = append(errs, err)
		}
		if rule.Path != "" {
			if _, ok := seen[rule.Path]; ok {
				errs = append(errs, fmt.Errorf("duplicate share policy rule path: %q", rule.Path))
			}
			seen[rule.Path] = struct{}{}
		}
		if rule.MaxExpiresIn < 0 {
			errs = append(errs, fmt.Errorf("%s.max_expires_in cannot be negative", fieldPrefix))
		}
		if rule.MaxAccess < 0 {
			errs = append(errs, fmt.Errorf("%s.max_access cannot be negative", fieldPrefix))
		}
		errs = append(errs, validateAccessRulePrincipals(rule.AllowedUsers, fieldPrefix+".allowed_users")...)
		errs = append(errs, validateAccessRulePrincipals(rule.AllowedGroups, fieldPrefix+".allowed_groups")...)
		errs = append(errs, validateAccessRuleRoles(rule.AllowedRoles, fieldPrefix+".allowed_roles")...)
		if !rule.RequirePassword && rule.MaxExpiresIn == 0 && rule.MaxAccess == 0 &&
			len(rule.AllowedUsers) == 0 && len(rule.AllowedGroups) == 0 && len(rule.AllowedRoles) == 0 {
			errs = append(errs, fmt.Errorf("%s must set at least one constraint", fieldPrefix))
		}
	}
	return errors.Join(errs...)
}

func directoryAccessRuleHasPrincipals(rule DirectoryAccessRuleConfig) bool {
	return len(rule.ReadUsers) > 0 ||
		len(rule.WriteUsers) > 0 ||
		len(rule.ReadGroups) > 0 ||
		len(rule.WriteGroups) > 0 ||
		len(rule.ReadRoles) > 0 ||
		len(rule.WriteRoles) > 0
}

func validateAccessRulePrincipals(values []string, field string) []error {
	var errs []error
	seen := map[string]struct{}{}
	for i, value := range values {
		itemField := fmt.Sprintf("%s[%d]", field, i)
		if err := validateAccessRulePrincipal(value, itemField); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, ok := seen[value]; ok {
			errs = append(errs, fmt.Errorf("%s contains duplicate principal %q", field, value))
		}
		seen[value] = struct{}{}
	}
	return errs
}

func validateAccessRulePrincipal(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s cannot be empty", field)
	}
	if strings.TrimSpace(value) != value || strings.ToLower(value) != value {
		return fmt.Errorf("%s must be normalized lowercase without surrounding whitespace", field)
	}
	for _, r := range value {
		if r > 0x7f || !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return fmt.Errorf("%s contains invalid characters", field)
		}
	}
	return nil
}

func validateAccessRuleRoles(values []string, field string) []error {
	var errs []error
	seen := map[string]struct{}{}
	for i, value := range values {
		itemField := fmt.Sprintf("%s[%d]", field, i)
		if value != "admin" && value != "user" && value != "guest" {
			errs = append(errs, fmt.Errorf("%s must be one of admin, user, or guest", itemField))
			continue
		}
		if _, ok := seen[value]; ok {
			errs = append(errs, fmt.Errorf("%s contains duplicate role %q", field, value))
		}
		seen[value] = struct{}{}
	}
	return errs
}

func validateDirectoryQuotaPath(value, field string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s cannot be empty", field)
	}
	if trimmed != value {
		return fmt.Errorf("%s must not contain leading or trailing whitespace", field)
	}
	if !strings.HasPrefix(trimmed, "/") {
		return fmt.Errorf("%s must be absolute", field)
	}
	if strings.ContainsAny(trimmed, "\\?#") {
		return fmt.Errorf("%s must be a clean MnemoNAS path", field)
	}
	if strings.IndexFunc(trimmed, unicode.IsControl) >= 0 {
		return fmt.Errorf("%s must not contain control characters", field)
	}
	if urlpath.Clean(trimmed) != trimmed {
		return fmt.Errorf("%s must be clean", field)
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("%s must not contain dot segments", field)
		}
	}
	return nil
}

func validateBackupConfig(backup BackupConfig, storageRoot string) error {
	var errs []error

	seen := map[string]struct{}{}
	for _, job := range backup.Jobs {
		if err := validateBackupJob(job, storageRoot, seen); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func validateDiskHealthConfig(cfg DiskHealthConfig) error {
	var errs []error

	if cfg.CheckInterval <= 0 {
		errs = append(errs, errors.New("disk_health.check_interval must be positive"))
	}
	if cfg.ProbeTimeout <= 0 {
		errs = append(errs, errors.New("disk_health.probe_timeout must be positive"))
	}
	if cfg.CooldownPeriod <= 0 {
		errs = append(errs, errors.New("disk_health.cooldown_period must be positive"))
	}
	if err := validateBackupCommand(cfg.Command, "disk_health.command"); err != nil {
		errs = append(errs, err)
	}
	if err := validateTemperatureThresholds(cfg.TemperatureWarningC, cfg.TemperatureCriticalC, "disk_health"); err != nil {
		errs = append(errs, err)
	}
	if err := validatePercentThresholds(cfg.MediaWearWarningPct, cfg.MediaWearCriticalPct, "disk_health.media_wear"); err != nil {
		errs = append(errs, err)
	}

	for i, device := range cfg.Devices {
		label := fmt.Sprintf("disk_health.devices[%d]", i)
		if hasControlChar(device.Name) {
			errs = append(errs, fmt.Errorf("%s.name contains invalid control characters", label))
		}
		if hasControlChar(device.Type) {
			errs = append(errs, fmt.Errorf("%s.type contains invalid control characters", label))
		}
		if hasControlChar(device.Serial) {
			errs = append(errs, fmt.Errorf("%s.serial contains invalid control characters", label))
		}
		if strings.TrimSpace(device.Path) == "" {
			errs = append(errs, fmt.Errorf("%s.path cannot be empty", label))
		} else if !filepath.IsAbs(device.Path) {
			errs = append(errs, fmt.Errorf("%s.path must be absolute", label))
		} else if hasControlChar(device.Path) {
			errs = append(errs, fmt.Errorf("%s.path contains invalid control characters", label))
		}
		warnC, criticalC := cfg.TemperatureWarningC, cfg.TemperatureCriticalC
		if device.TemperatureWarningC != 0 {
			warnC = device.TemperatureWarningC
		}
		if device.TemperatureCriticalC != 0 {
			criticalC = device.TemperatureCriticalC
		}
		if err := validateTemperatureThresholds(warnC, criticalC, label); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func validateMaintenanceConfig(cfg MaintenanceConfig) error {
	var errs []error

	if cfg.Scrub.ScheduleInterval <= 0 {
		errs = append(errs, errors.New("maintenance.scrub.schedule_interval must be positive"))
	}
	if cfg.Scrub.RetryInterval <= 0 {
		errs = append(errs, errors.New("maintenance.scrub.retry_interval must be positive"))
	}
	if cfg.Scrub.MaxRetries < 0 {
		errs = append(errs, errors.New("maintenance.scrub.max_retries cannot be negative"))
	}

	return errors.Join(errs...)
}

func validateTemperatureThresholds(warnC, criticalC int, label string) error {
	if warnC < 0 || criticalC < 0 {
		return fmt.Errorf("%s temperature thresholds cannot be negative", label)
	}
	if warnC > 0 && criticalC > 0 && criticalC < warnC {
		return fmt.Errorf("%s temperature critical threshold must be greater than or equal to warning threshold", label)
	}
	return nil
}

func validatePercentThresholds(warnPct, criticalPct int, label string) error {
	if warnPct < 0 || criticalPct < 0 {
		return fmt.Errorf("%s thresholds cannot be negative", label)
	}
	if warnPct > 0 && criticalPct > 0 && criticalPct < warnPct {
		return fmt.Errorf("%s critical threshold must be greater than or equal to warning threshold", label)
	}
	return nil
}

func validateBackupJob(job BackupJobConfig, storageRoot string, seen map[string]struct{}) error {
	var errs []error

	if !isValidBackupJobID(job.ID) {
		errs = append(errs, fmt.Errorf("invalid backup job id: %q", job.ID))
	} else {
		key := strings.ToLower(job.ID)
		if _, ok := seen[key]; ok {
			errs = append(errs, fmt.Errorf("duplicate backup job id: %q", job.ID))
		}
		seen[key] = struct{}{}
	}

	if job.Name == "" {
		errs = append(errs, fmt.Errorf("backup job %q name cannot be empty", job.ID))
	}
	if job.Type != "local" && job.Type != "restic" && job.Type != "rclone" {
		errs = append(errs, fmt.Errorf("backup job %q has unsupported type: %q", job.ID, job.Type))
	}
	if err := validateBackupCommand(job.Command, fmt.Sprintf("backup job %q command", job.ID)); err != nil {
		errs = append(errs, err)
	}
	for _, arg := range job.ExtraArgs {
		if err := validateBackupCommandArg(arg, fmt.Sprintf("backup job %q extra_args", job.ID)); err != nil {
			errs = append(errs, err)
		}
		if err := validateBackupIdentityOverrideArg(job.Type, arg, fmt.Sprintf("backup job %q extra_args", job.ID)); err != nil {
			errs = append(errs, err)
		}
	}
	if job.ScheduleInterval < 0 {
		errs = append(errs, fmt.Errorf("backup job %q schedule_interval cannot be negative", job.ID))
	}
	if err := validateBackupScheduleWindow(job); err != nil {
		errs = append(errs, err)
	}
	if job.StaleAfter < 0 {
		errs = append(errs, fmt.Errorf("backup job %q stale_after cannot be negative", job.ID))
	}
	if job.RestoreDrillStaleAfter < 0 {
		errs = append(errs, fmt.Errorf("backup job %q restore_drill_stale_after cannot be negative", job.ID))
	}
	if hasControlChar(job.RetentionPolicy) {
		errs = append(errs, fmt.Errorf("backup job %q retention_policy cannot contain control characters", job.ID))
	}
	if job.MaxSnapshots < 0 {
		errs = append(errs, fmt.Errorf("backup job %q max_snapshots cannot be negative", job.ID))
	}
	if job.MaxAge < 0 {
		errs = append(errs, fmt.Errorf("backup job %q max_age cannot be negative", job.ID))
	}

	source := job.Source
	if source == "" {
		source = storageRoot
		if !filepath.IsAbs(source) {
			if absSource, err := filepath.Abs(source); err == nil {
				source = absSource
			}
		}
	}
	if err := validateBackupAbsoluteDirectory(source, fmt.Sprintf("backup job %q source", job.ID)); err != nil {
		errs = append(errs, err)
	}
	switch job.Type {
	case "local":
		if err := validateBackupAbsoluteDirectory(job.Destination, fmt.Sprintf("backup job %q destination", job.ID)); err != nil {
			errs = append(errs, err)
		}
		if source != "" && job.Destination != "" && pathContainsOrEquals(source, job.Destination) {
			errs = append(errs, fmt.Errorf("backup job %q destination must not be inside source", job.ID))
		}
	case "restic":
		if job.Repository == "" {
			errs = append(errs, fmt.Errorf("backup job %q repository cannot be empty", job.ID))
		}
		if hasControlChar(job.Repository) {
			errs = append(errs, fmt.Errorf("backup job %q repository contains invalid control characters", job.ID))
		}
		if job.PasswordFile == "" {
			errs = append(errs, fmt.Errorf("backup job %q password_file cannot be empty for restic", job.ID))
		}
	case "rclone":
		if job.Remote == "" {
			errs = append(errs, fmt.Errorf("backup job %q remote cannot be empty", job.ID))
		}
		if hasControlChar(job.Remote) {
			errs = append(errs, fmt.Errorf("backup job %q remote contains invalid control characters", job.ID))
		}
		if job.ConfigFile == "" {
			errs = append(errs, fmt.Errorf("backup job %q config_file cannot be empty for rclone", job.ID))
		}
	}
	storageRootForValidation := storageRoot
	if storageRootForValidation != "" && !filepath.IsAbs(storageRootForValidation) {
		if absStorageRoot, err := filepath.Abs(storageRootForValidation); err == nil {
			storageRootForValidation = absStorageRoot
		}
	}
	if job.Type == "local" && filepath.IsAbs(storageRootForValidation) && job.Destination != "" && pathContainsOrEquals(storageRootForValidation, job.Destination) {
		errs = append(errs, fmt.Errorf("backup job %q destination must not be inside storage.root", job.ID))
	}
	if job.Type == "restic" {
		if err := validateResticRepositoryBoundary(job.Repository, job.ID, source, storageRootForValidation); err != nil {
			errs = append(errs, err)
		}
	}
	for field, filePath := range map[string]string{
		"password_file": job.PasswordFile,
		"config_file":   job.ConfigFile,
	} {
		if err := validateBackupCredentialFile(filePath, field, job.ID, source, storageRootForValidation); err != nil {
			errs = append(errs, err)
		}
	}
	if job.Type == "rclone" && job.ConfigFile != "" {
		if err := validateRcloneConfigEvidenceFile(job.ConfigFile, job.ID, job.Remote); err != nil {
			errs = append(errs, err)
		}
	}
	for _, pattern := range job.Exclude {
		if pattern == "" {
			errs = append(errs, fmt.Errorf("backup job %q exclude patterns cannot contain empty entries", job.ID))
			continue
		}
		if hasControlChar(pattern) {
			errs = append(errs, fmt.Errorf("backup job %q exclude pattern must not contain control characters", job.ID))
		}
	}

	return errors.Join(errs...)
}

func validateBackupCommand(command string, label string) error {
	if command == "" {
		return nil
	}
	if hasControlChar(command) || strings.ContainsAny(command, " \t\r\n") {
		return fmt.Errorf("%s must be a single executable path without whitespace or control characters", label)
	}
	if filepath.IsAbs(command) {
		return nil
	}
	if strings.ContainsRune(command, filepath.Separator) || (filepath.Separator != '/' && strings.ContainsRune(command, '/')) {
		return fmt.Errorf("%s must be an absolute path or a bare executable name", label)
	}
	if command == "." || command == ".." {
		return fmt.Errorf("%s must be an executable name", label)
	}
	return nil
}

func validateBackupScheduleWindow(job BackupJobConfig) error {
	if job.ScheduleWindowStart == "" && job.ScheduleWindowEnd == "" {
		return nil
	}
	if job.ScheduleWindowStart == "" || job.ScheduleWindowEnd == "" {
		return fmt.Errorf("backup job %q schedule window requires both schedule_window_start and schedule_window_end", job.ID)
	}
	start, err := parseBackupWindowClock(job.ScheduleWindowStart, fmt.Sprintf("backup job %q schedule_window_start", job.ID))
	if err != nil {
		return err
	}
	end, err := parseBackupWindowClock(job.ScheduleWindowEnd, fmt.Sprintf("backup job %q schedule_window_end", job.ID))
	if err != nil {
		return err
	}
	if start == end {
		return fmt.Errorf("backup job %q schedule window start and end cannot be equal", job.ID)
	}
	return nil
}

func parseBackupWindowClock(value string, label string) (int, error) {
	if len(value) != len("15:04") || value[2] != ':' {
		return 0, fmt.Errorf("%s must use HH:MM format", label)
	}
	hour, err := strconv.Atoi(value[:2])
	if err != nil {
		return 0, fmt.Errorf("%s must use HH:MM format", label)
	}
	minute, err := strconv.Atoi(value[3:])
	if err != nil {
		return 0, fmt.Errorf("%s must use HH:MM format", label)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("%s must be a valid 24-hour time", label)
	}
	return hour*60 + minute, nil
}

func hasControlChar(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func hasWhitespaceOrControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0
}

func validateBackupCommandArg(arg string, label string) error {
	if arg == "" {
		return fmt.Errorf("%s cannot contain empty entries", label)
	}
	if hasControlChar(arg) {
		return fmt.Errorf("%s contains invalid control characters", label)
	}
	return nil
}

func validateBackupIdentityOverrideArg(jobType, arg, label string) error {
	arg = strings.ToLower(strings.TrimSpace(arg))
	var forbidden []string
	switch jobType {
	case "restic":
		forbidden = []string{"-r", "--repo", "--repository-file", "--password-file", "--password-command"}
	case "rclone":
		if arg != "--fast-list" {
			return fmt.Errorf("%s currently allows only --fast-list for rclone", label)
		}
		return nil
	default:
		return nil
	}
	for _, flag := range forbidden {
		if arg == flag || strings.HasPrefix(arg, flag+"=") || flag == "-r" && strings.HasPrefix(arg, "-r") {
			return fmt.Errorf("%s cannot override backup identity option %q", label, flag)
		}
	}
	return nil
}

func validateBackupCredentialFile(filePath string, field string, jobID string, source string, storageRoot string) error {
	if filePath == "" {
		return nil
	}
	if !filepath.IsAbs(filePath) {
		return fmt.Errorf("backup job %q %s must be an absolute path", jobID, field)
	}
	if hasControlChar(filePath) {
		return fmt.Errorf("backup job %q %s contains invalid control characters", jobID, field)
	}
	if err := validateBackupCredentialPathNoSymlink(filePath, field, jobID); err != nil {
		return err
	}
	if source != "" && filepath.IsAbs(source) && pathContainsOrEquals(source, filePath) {
		return fmt.Errorf("backup job %q %s must not be inside backup source", jobID, field)
	}
	if storageRoot != "" && filepath.IsAbs(storageRoot) && pathContainsOrEquals(storageRoot, filePath) {
		return fmt.Errorf("backup job %q %s must not be inside storage.root", jobID, field)
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backup job %q %s does not exist", jobID, field)
		}
		return fmt.Errorf("backup job %q %s cannot be checked: %w", jobID, field, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup job %q %s must not be a symlink", jobID, field)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup job %q %s must be a regular file", jobID, field)
	}
	if info.Size() > maxBackupCredentialFileSize {
		return fmt.Errorf("backup job %q %s exceeds the %d-byte limit", jobID, field, maxBackupCredentialFileSize)
	}
	return nil
}

func validateBackupCredentialPathNoSymlink(filePath string, field string, jobID string) error {
	if err := validateManagedFilePath(filePath, errManagedDirectorySymlink, field); err != nil {
		if errors.Is(err, errManagedDirectorySymlink) {
			return fmt.Errorf("backup job %q %s path must not contain symlink", jobID, field)
		}
		return fmt.Errorf("backup job %q %s cannot be checked: %w", jobID, field, err)
	}
	return nil
}

func validateRcloneConfigEvidenceFile(filePath, jobID, remote string) error {
	file, err := rootio.OpenRegularFilePathNoFollow(filePath)
	if err != nil {
		return fmt.Errorf("backup job %q config_file cannot be read safely: %w", jobID, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxBackupCredentialFileSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return fmt.Errorf("backup job %q config_file cannot be read: %w", jobID, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("backup job %q config_file cannot be closed: %w", jobID, closeErr)
	}
	if len(data) > maxBackupCredentialFileSize {
		return fmt.Errorf("backup job %q config_file exceeds the %d-byte limit", jobID, maxBackupCredentialFileSize)
	}
	remoteName, err := backupRcloneRemoteName(remote)
	if err != nil {
		return fmt.Errorf("backup job %q %w", jobID, err)
	}
	content := string(data)
	if strings.Contains(content, "${") {
		return fmt.Errorf("backup job %q config_file cannot expand environment-dependent paths", jobID)
	}
	sections := make(map[string]map[string]string)
	currentSection := ""
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 4096), maxBackupCredentialFileSize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			if currentSection == "" {
				return fmt.Errorf("backup job %q config_file contains an empty remote section", jobID)
			}
			if _, exists := sections[currentSection]; !exists {
				sections[currentSection] = make(map[string]string)
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || currentSection == "" || strings.TrimSpace(value) == "" {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		sections[currentSection][key] = value
		if key == "env_auth" && strings.EqualFold(value, "true") {
			return fmt.Errorf("backup job %q config_file cannot enable env_auth", jobID)
		}
		if strings.Contains(key, "_file") || strings.Contains(key, "_path") ||
			strings.Contains(key, "command") || strings.Contains(key, "agent") || key == "ssh" {
			return fmt.Errorf("backup job %q config_file option %q cannot depend on an external runtime input", jobID, key)
		}
		if strings.Contains(key, "token") {
			return fmt.Errorf("backup job %q token-refreshing config_file requires a managed writable credential store", jobID)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("backup job %q config_file cannot be parsed: %w", jobID, err)
	}
	remoteSection, exists := sections[remoteName]
	if !exists {
		return fmt.Errorf("backup job %q remote %q is not defined in config_file", jobID, remoteName)
	}
	if strings.TrimSpace(remoteSection["type"]) == "" {
		return fmt.Errorf("backup job %q remote %q has no type", jobID, remoteName)
	}
	return nil
}

func backupRcloneRemoteName(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	separator := strings.IndexByte(remote, ':')
	if separator <= 0 || strings.HasPrefix(remote, ":") {
		return "", errors.New("rclone remote must reference a named config_file section")
	}
	name := strings.TrimSpace(remote[:separator])
	if name == "" || strings.ContainsAny(name, `/\\`) || hasControlChar(name) {
		return "", errors.New("rclone remote name is invalid")
	}
	return name, nil
}

func validateResticRepositoryBoundary(repository, jobID, source, storageRoot string) error {
	repository = strings.TrimSpace(repository)
	if repository == "" {
		return nil
	}
	if !filepath.IsAbs(repository) {
		lower := strings.ToLower(repository)
		if !strings.HasPrefix(lower, "rest:") {
			return fmt.Errorf("backup job %q repository must be an absolute local path or an explicit REST server URL", jobID)
		}
		endpoint, err := neturl.Parse(repository[len("rest:"):])
		if err != nil || endpoint.Host == "" || endpoint.Fragment != "" ||
			!strings.EqualFold(endpoint.Scheme, "http") && !strings.EqualFold(endpoint.Scheme, "https") {
			return fmt.Errorf("backup job %q REST repository URL is invalid", jobID)
		}
		return nil
	}
	repository = filepath.Clean(repository)
	for label, protected := range map[string]string{
		"backup source": source,
		"storage.root":  storageRoot,
	} {
		if protected != "" && filepath.IsAbs(protected) && pathContainsOrEquals(protected, repository) {
			return fmt.Errorf("backup job %q repository must not be inside %s", jobID, label)
		}
	}
	return nil
}

func isValidBackupJobID(id string) bool {
	if id == "" || len(id) > 64 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func validateBackupAbsoluteDirectory(value, field string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s cannot be empty", field)
	}
	if trimmed != value {
		return fmt.Errorf("%s must not contain leading or trailing whitespace", field)
	}
	if hasControlChar(trimmed) {
		return fmt.Errorf("%s must not contain control characters", field)
	}
	if !filepath.IsAbs(trimmed) {
		return fmt.Errorf("%s must be an absolute path", field)
	}
	if isFilesystemRoot(trimmed) || isProtectedStorageRoot(trimmed) {
		return fmt.Errorf("%s must not be a protected system directory", field)
	}
	return nil
}

func pathContainsOrEquals(parent, child string) bool {
	parentClean := filepath.Clean(parent)
	childClean := filepath.Clean(child)
	if parentClean == childClean {
		return true
	}
	rel, err := filepath.Rel(parentClean, childClean)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isValidHTTPHeaderToken(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if !isHTTPTokenChar(value[i]) {
			return false
		}
	}
	return true
}

func isHTTPTokenChar(b byte) bool {
	if b >= 'a' && b <= 'z' {
		return true
	}
	if b >= 'A' && b <= 'Z' {
		return true
	}
	if b >= '0' && b <= '9' {
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

func hasInvalidHTTPHeaderValueControl(value string) bool {
	for _, r := range value {
		if r != '\t' && unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func validateOptionalHTTPURL(rawURL, field string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil
	}
	if trimmed != rawURL || hasWhitespaceOrControl(trimmed) {
		return fmt.Errorf("%s must not contain whitespace or control characters", field)
	}

	parsed, err := neturl.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute http or https URL", field)
	}
	if !isValidTCPHost(parsed.Hostname()) {
		return fmt.Errorf("%s host is invalid", field)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("%s must use http or https", field)
	}
}

func validateShareBaseURL(rawURL string) error {
	const field = "share.base_url"

	if err := validateOptionalHTTPURL(rawURL, field); err != nil {
		return err
	}

	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil
	}
	parsed, err := neturl.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%s must be an absolute http or https URL", field)
	}
	if parsed.User != nil {
		return fmt.Errorf("%s must not contain userinfo", field)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(trimmed, "#") {
		return fmt.Errorf("%s must not contain query or fragment", field)
	}
	if !isValidTCPHost(parsed.Hostname()) {
		return fmt.Errorf("%s host is invalid", field)
	}
	if shareBaseURLPathHasBackslashes(parsed.Path) {
		return fmt.Errorf("%s path must not contain backslashes", field)
	}
	if shareBaseURLPathHasQueryOrFragmentMarkers(parsed.Path) {
		return fmt.Errorf("%s path must not contain encoded query or fragment markers", field)
	}
	if shareBaseURLPathHasDuplicateSlashes(parsed.Path) {
		return fmt.Errorf("%s path must not contain duplicate slashes", field)
	}
	if shareBaseURLPathHasDotSegments(parsed.Path) {
		return fmt.Errorf("%s path must not contain . or .. segments", field)
	}
	return nil
}

func shareBaseURLPathHasBackslashes(path string) bool {
	return strings.Contains(path, "\\")
}

func shareBaseURLPathHasQueryOrFragmentMarkers(path string) bool {
	return strings.ContainsAny(path, "?#")
}

func shareBaseURLPathHasDuplicateSlashes(path string) bool {
	return strings.Contains(path, "//")
}

func shareBaseURLPathHasDotSegments(urlPath string) bool {
	for _, segment := range strings.Split(urlPath, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func validateTCPAddress(address, field string) error {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return fmt.Errorf("%s cannot be empty", field)
	}
	if trimmed != address || strings.ContainsAny(trimmed, "\r\n\t ") {
		return fmt.Errorf("%s must not contain whitespace", field)
	}
	if hasControlChar(trimmed) {
		return fmt.Errorf("%s must not contain control characters", field)
	}

	host, port, err := net.SplitHostPort(trimmed)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return fmt.Errorf("%s must be a host:port address", field)
	}
	if !isValidTCPHost(host) {
		return fmt.Errorf("%s host is invalid", field)
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return fmt.Errorf("%s port must be between 1 and 65535", field)
	}
	return nil
}

func isValidTCPHost(host string) bool {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" || strings.ContainsAny(host, "[]") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}

	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			b := label[i]
			if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func normalizeListenHost(host string) string {
	trimmed := strings.TrimSpace(host)
	if trimmed == "*" {
		return ""
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") &&
		strings.Count(trimmed, "[") == 1 && strings.Count(trimmed, "]") == 1 {
		return strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
	}
	return trimmed
}

func validateListenHost(host string) error {
	if strings.TrimSpace(host) != host || strings.IndexFunc(host, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return errors.New("server.host must not contain whitespace or control characters")
	}

	normalized := normalizeListenHost(host)
	if normalized == "" {
		return nil
	}
	if !isValidTCPHost(normalized) {
		return fmt.Errorf("server.host is invalid: %q", host)
	}
	return nil
}

func listensBeyondLoopback(host string) bool {
	normalized := strings.ToLower(normalizeListenHost(host))
	switch normalized {
	case "", "*":
		return true
	case "localhost", "ip6-localhost":
		return false
	}

	ip := net.ParseIP(normalized)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback()
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

func NormalizeWebDAVAuthType(authType string) string {
	normalized := strings.ToLower(strings.TrimSpace(authType))
	if normalized == "" {
		return "basic"
	}
	return normalized
}

func webDAVPrefixOverlapsReservedRoute(prefix string) bool {
	normalized := NormalizeWebDAVPrefix(prefix)
	if normalized == "/" {
		return true
	}

	for _, reserved := range []string{"/api", "/s", "/health"} {
		if normalized == reserved || strings.HasPrefix(normalized, reserved+"/") {
			return true
		}
	}
	return false
}

func validateWebDAVPrefix(prefix string) error {
	normalized := NormalizeWebDAVPrefix(prefix)
	for _, r := range normalized {
		if unicode.IsControl(r) {
			return errors.New("webdav.prefix cannot contain control characters")
		}
	}
	if strings.ContainsAny(normalized, "\\?#") {
		return errors.New("webdav.prefix must be a URL path prefix without backslash, query, or fragment characters")
	}
	return nil
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
	if isProtectedStorageRoot(root) {
		return errors.New("storage.root cannot be a protected system directory")
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
		{filepath.Join(root, ".mnemonas", "run"), 0700},
	}

	for _, dir := range dirs {
		if err := validateManagedFilePath(dir.path, errManagedDirectorySymlink, "managed directory"); err != nil {
			return fmt.Errorf("failed to validate directory %s: %w", dir.path, err)
		}
	}

	for _, dir := range dirs {
		if _, err := ensureManagedDir(dir.path, dir.perm, errManagedDirectorySymlink); err != nil {
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

// SMBGatewaySocketPath returns the SMB gateway Unix socket path.
func (c *Config) SMBGatewaySocketPath() string {
	return c.SMB.GatewaySocket
}

// SMBCredentialFilePath returns the SMB credential store path.
func (c *Config) SMBCredentialFilePath() string {
	return c.SMB.CredentialFile
}

// Address returns the server listen address
func (c *Config) Address() string {
	return net.JoinHostPort(normalizeListenHost(c.Server.Host), strconv.Itoa(c.Server.Port))
}
