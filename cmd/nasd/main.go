// MnemoNAS main program
// Starts the control plane service, including WebDAV and REST API
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/seanbao/mnemonas/internal/alerts"
	"github.com/seanbao/mnemonas/internal/api"
	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/diskhealth"
	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/storage"
	mnemonasTLS "github.com/seanbao/mnemonas/internal/tls"
	"github.com/seanbao/mnemonas/internal/webdav"
)

var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

var errLogOutputSymlink = errors.New("log output path must not be a symlink")

var startupDataplaneContext = dataplane.WithTimeout
var startupDataplaneConnect = func(client *dataplane.Client, ctx context.Context) error {
	return client.Connect(ctx)
}
var afterOpenLogOutputParent = func() {}

type switchableWebDAVHandler struct {
	mu      sync.RWMutex
	prefix  string
	handler http.Handler
}

type webdavPathChangeHandler interface {
	OnPathRenamed(oldPath, newPath string)
	OnPathDeleted(path string) *storage.PathDeleteHookResult
}

func newSwitchableWebDAVHandler(prefix string, handler http.Handler) *switchableWebDAVHandler {
	s := &switchableWebDAVHandler{}
	s.Update(prefix, handler)
	return s
}

func diskHealthRuntimeConfig(cfg config.DiskHealthConfig) diskhealth.Config {
	devices := make([]diskhealth.DeviceConfig, 0, len(cfg.Devices))
	for _, device := range cfg.Devices {
		devices = append(devices, diskhealth.DeviceConfig{
			Name:                 device.Name,
			Path:                 device.Path,
			Type:                 device.Type,
			Serial:               device.Serial,
			TemperatureWarningC:  device.TemperatureWarningC,
			TemperatureCriticalC: device.TemperatureCriticalC,
		})
	}
	return diskhealth.Config{
		Enabled:              cfg.Enabled,
		CheckInterval:        cfg.CheckInterval,
		ProbeTimeout:         cfg.ProbeTimeout,
		CooldownPeriod:       cfg.CooldownPeriod,
		Command:              cfg.Command,
		TemperatureWarningC:  cfg.TemperatureWarningC,
		TemperatureCriticalC: cfg.TemperatureCriticalC,
		MediaWearWarningPct:  cfg.MediaWearWarningPct,
		MediaWearCriticalPct: cfg.MediaWearCriticalPct,
		Devices:              devices,
	}
}

func (s *switchableWebDAVHandler) Update(prefix string, handler http.Handler) {
	s.mu.Lock()
	previous := s.handler
	s.prefix = prefix
	s.handler = handler
	s.mu.Unlock()

	if handlersEqual(previous, handler) {
		return
	}
	if closer, ok := previous.(io.Closer); ok {
		_ = closer.Close()
	}
}

func handlersEqual(a, b http.Handler) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return false
	}
	if !reflect.TypeOf(a).Comparable() {
		return false
	}
	return a == b
}

type frontendHandler struct {
	root string
}

const appContentSecurityPolicy = "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self'; frame-src 'self' blob:; worker-src 'self' blob:"

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setHeaderIfAbsent(w.Header(), "X-Content-Type-Options", "nosniff")
		setHeaderIfAbsent(w.Header(), "Referrer-Policy", "no-referrer")
		setHeaderIfAbsent(w.Header(), "X-Frame-Options", "DENY")
		setHeaderIfAbsent(w.Header(), "Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		setHeaderIfAbsent(w.Header(), "Content-Security-Policy", appContentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

func setHeaderIfAbsent(headers http.Header, key, value string) {
	if headers.Get(key) == "" {
		headers.Set(key, value)
	}
}

func newFrontendHandler(root string) http.Handler {
	return &frontendHandler{root: root}
}

func (h *frontendHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}

	cleanPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	rawFirstSegment := firstRequestPathSegment(r.URL.Path)
	rawPathHasDotSegment := requestPathHasDotSegment(r.URL.Path)
	if rawFirstSegment == "assets" && rawPathHasDotSegment {
		http.NotFound(w, r)
		return
	}
	if cleanPath != "/" {
		localPath, err := filepath.Localize(strings.TrimPrefix(cleanPath, "/"))
		if err == nil {
			if h.serveLocalFileNoFollow(w, r, localPath) {
				return
			}
		}
		if isFrontendBuildAssetPath(cleanPath) {
			http.NotFound(w, r)
			return
		}
	}

	if cleanPath != "/" && !requestAcceptsHTML(r) {
		http.NotFound(w, r)
		return
	}

	if !h.serveLocalFileNoFollow(w, r, "index.html") {
		http.NotFound(w, r)
	}
}

func (h *frontendHandler) serveLocalFileNoFollow(w http.ResponseWriter, r *http.Request, localPath string) bool {
	root, err := os.OpenRoot(h.root)
	if err != nil {
		return false
	}
	defer root.Close()

	file, err := rootio.OpenFileNoFollow(root, localPath, os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return false
	}

	if localPath == "index.html" {
		setHeaderIfAbsent(w.Header(), "Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, path.Base(localPath), info.ModTime(), file)
	return true
}

func isFrontendBuildAssetPath(cleanPath string) bool {
	return cleanPath == "/assets" || strings.HasPrefix(cleanPath, "/assets/")
}

func discoverFrontendAssets() string {
	candidates := []string{}
	if envDir := strings.TrimSpace(os.Getenv("MNEMONAS_WEB_DIR")); envDir != "" {
		candidates = append(candidates, envDir)
	}
	candidates = append(candidates,
		filepath.Join("web", "dist"),
		"web",
	)
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "web"),
			filepath.Join(exeDir, "web", "dist"),
			filepath.Join(exeDir, "..", "web", "dist"),
		)
	}

	for _, candidate := range candidates {
		if hasFrontendIndex(candidate) {
			return candidate
		}
	}
	return ""
}

func hasFrontendIndex(dir string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer root.Close()

	file, err := rootio.OpenFileNoFollow(root, "index.html", os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return false
	}
	return !bytes.Contains(data, []byte("src/main.tsx"))
}

func shouldServeFrontend(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}

	rawFirstSegment := firstRequestPathSegment(r.URL.Path)
	if rawFirstSegment == "api" || rawFirstSegment == "health" {
		return false
	}
	if rawFirstSegment == "s" && requestPathHasDotSegment(r.URL.Path) {
		return false
	}

	cleanPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	switch {
	case cleanPath == "/health":
		return false
	case cleanPath == "/api" || strings.HasPrefix(cleanPath, "/api/"):
		return false
	case cleanPath == "/s":
		return false
	case strings.HasPrefix(cleanPath, "/s/"):
		return isShareFrontendRoute(cleanPath, r)
	default:
		return true
	}
}

func isShareFrontendRoute(cleanPath string, r *http.Request) bool {
	if !requestExplicitlyAcceptsHTML(r) {
		return false
	}
	parts := strings.Split(strings.Trim(cleanPath, "/"), "/")
	return len(parts) == 2 && parts[0] == "s" && parts[1] != ""
}

func firstRequestPathSegment(requestPath string) string {
	trimmed := strings.TrimLeft(requestPath, "/")
	segment, _, _ := strings.Cut(trimmed, "/")
	return segment
}

func requestPathHasDotSegment(requestPath string) bool {
	for _, segment := range strings.Split(requestPath, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func requestAcceptsHTML(r *http.Request) bool {
	return requestAcceptsHTMLWithWildcards(r, true)
}

func requestExplicitlyAcceptsHTML(r *http.Request) bool {
	return requestAcceptsHTMLWithWildcards(r, false)
}

func requestAcceptsHTMLWithWildcards(r *http.Request, allowWildcards bool) bool {
	accept := strings.TrimSpace(r.Header.Get("Accept"))
	if accept == "" {
		return allowWildcards
	}

	htmlQ, htmlSeen := 0.0, false
	textWildcardQ, textWildcardSeen := 0.0, false
	broadWildcardQ, broadWildcardSeen := 0.0, false
	for _, rawMediaRange := range strings.Split(accept, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(rawMediaRange))
		if err != nil {
			continue
		}
		qValue, ok := parseAcceptQuality(params["q"])
		if !ok {
			continue
		}
		mediaType = strings.ToLower(mediaType)
		switch mediaType {
		case "text/html":
			if !htmlSeen || qValue > htmlQ {
				htmlQ = qValue
			}
			htmlSeen = true
		case "text/*":
			if allowWildcards && (!textWildcardSeen || qValue > textWildcardQ) {
				textWildcardQ = qValue
				textWildcardSeen = true
			}
		case "*/*":
			if allowWildcards && (!broadWildcardSeen || qValue > broadWildcardQ) {
				broadWildcardQ = qValue
				broadWildcardSeen = true
			}
		}
	}
	switch {
	case htmlSeen:
		return htmlQ > 0
	case textWildcardSeen:
		return textWildcardQ > 0
	case broadWildcardSeen:
		return broadWildcardQ > 0
	default:
		return false
	}
}

func parseAcceptQuality(raw string) (float64, bool) {
	q := strings.TrimSpace(raw)
	if q == "" {
		return 1, true
	}
	value, err := strconv.ParseFloat(q, 64)
	if err != nil || value < 0 || value > 1 {
		return 0, false
	}
	return value, true
}

func (s *switchableWebDAVHandler) ServeIfMatches(w http.ResponseWriter, r *http.Request) bool {
	s.mu.RLock()
	prefix := s.prefix
	handler := s.handler
	s.mu.RUnlock()

	if handler == nil || !matchesWebDAVPrefix(prefix, r.URL.Path) {
		return false
	}

	handler.ServeHTTP(w, r)
	return true
}

func (s *switchableWebDAVHandler) OnPathRenamed(oldPath, newPath string) {
	notifier, ok := s.currentPathChangeHandler()
	if !ok {
		return
	}
	notifier.OnPathRenamed(oldPath, newPath)
}

func (s *switchableWebDAVHandler) OnPathDeleted(path string) *storage.PathDeleteHookResult {
	notifier, ok := s.currentPathChangeHandler()
	if !ok {
		return nil
	}
	return notifier.OnPathDeleted(path)
}

func (s *switchableWebDAVHandler) currentPathChangeHandler() (webdavPathChangeHandler, bool) {
	s.mu.RLock()
	handler := s.handler
	s.mu.RUnlock()
	notifier, ok := handler.(webdavPathChangeHandler)
	return notifier, ok
}

func buildWebDAVHandler(fs *storage.FileSystem, cfg api.WebDAVRuntimeConfig) (string, http.Handler) {
	if !cfg.Enabled {
		return "", nil
	}

	authType := config.NormalizeWebDAVAuthType(cfg.AuthType)
	username := cfg.Username
	if authType == "basic" && strings.TrimSpace(username) == "" {
		username = "admin"
	}

	var userAuthenticator webdav.UserAuthenticator
	if authType == "users" && cfg.UserStore != nil {
		userAuthenticator = func(ctx context.Context, username, password string) (*webdav.UserIdentity, error) {
			user, err := cfg.UserStore.VerifyCredentials(username, password)
			if err != nil {
				return nil, err
			}
			return &webdav.UserIdentity{
				Username:   user.Username,
				Role:       string(user.Role),
				Groups:     append([]string(nil), user.Groups...),
				HomeDir:    user.HomeDir,
				QuotaBytes: user.QuotaBytes,
			}, nil
		}
	}

	directoryQuotas := make([]webdav.DirectoryQuota, 0, len(cfg.DirectoryQuotas))
	for _, quota := range cfg.DirectoryQuotas {
		directoryQuotas = append(directoryQuotas, webdav.DirectoryQuota{
			Path:       quota.Path,
			QuotaBytes: quota.QuotaBytes,
		})
	}
	directoryAccess := make([]webdav.DirectoryAccessRule, 0, len(cfg.DirectoryAccessRules))
	for _, rule := range cfg.DirectoryAccessRules {
		directoryAccess = append(directoryAccess, webdav.DirectoryAccessRule{
			Path:        rule.Path,
			ReadUsers:   append([]string(nil), rule.ReadUsers...),
			WriteUsers:  append([]string(nil), rule.WriteUsers...),
			ReadGroups:  append([]string(nil), rule.ReadGroups...),
			WriteGroups: append([]string(nil), rule.WriteGroups...),
			ReadRoles:   append([]string(nil), rule.ReadRoles...),
			WriteRoles:  append([]string(nil), rule.WriteRoles...),
		})
	}

	prefix := config.NormalizeWebDAVPrefix(cfg.Prefix)
	return prefix, webdav.NewHandler(webdav.Config{
		FileSystem:        fs,
		Prefix:            prefix,
		ReadOnly:          cfg.ReadOnly,
		AuthType:          authType,
		Username:          username,
		Password:          cfg.Password,
		UserAuthenticator: userAuthenticator,
		DirectoryQuotas:   directoryQuotas,
		DirectoryAccess:   directoryAccess,
	})
}

func cloneConfigDirectoryAccessRules(rules []config.DirectoryAccessRuleConfig) []config.DirectoryAccessRuleConfig {
	if len(rules) == 0 {
		return nil
	}
	cloned := make([]config.DirectoryAccessRuleConfig, len(rules))
	for i, rule := range rules {
		cloned[i] = rule
		cloned[i].ReadUsers = append([]string(nil), rule.ReadUsers...)
		cloned[i].WriteUsers = append([]string(nil), rule.WriteUsers...)
		cloned[i].ReadGroups = append([]string(nil), rule.ReadGroups...)
		cloned[i].WriteGroups = append([]string(nil), rule.WriteGroups...)
		cloned[i].ReadRoles = append([]string(nil), rule.ReadRoles...)
		cloned[i].WriteRoles = append([]string(nil), rule.WriteRoles...)
	}
	return cloned
}

func buildApplicationHandler(runtimeWebDAV *switchableWebDAVHandler, frontend http.Handler, router http.Handler) http.Handler {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if runtimeWebDAV != nil && runtimeWebDAV.ServeIfMatches(w, r) {
			return
		}
		if frontend != nil && shouldServeFrontend(r) {
			frontend.ServeHTTP(w, r)
			return
		}
		router.ServeHTTP(w, r)
	})
	return withSecurityHeaders(api.RejectCrossOriginUnsafeRequests(handler))
}

func connectStartupDataplane(addr string, timeout time.Duration) (*dataplane.Client, error) {
	ctx, cancel := startupDataplaneContext(timeout)
	defer cancel()

	client := dataplane.NewClient(addr)
	if err := startupDataplaneConnect(client, ctx); err != nil {
		_ = client.Close()
		return nil, err
	}

	return client, nil
}

func acquireRuntimeAuthStateLock(cfg *config.Config) (*auth.StateLock, error) {
	if cfg == nil || !cfg.Auth.Enabled {
		return nil, nil
	}
	return auth.AcquireStateLock(cfg.Auth.UsersFile)
}

func validateRuntimeAdminRecoveryState(cfg *config.Config) error {
	if cfg == nil || !cfg.Auth.Enabled {
		return nil
	}
	return auth.ValidateAdminRecoveryStartupState(cfg.Auth.UsersFile)
}

func main() {
	// Command line arguments
	configPath := flag.String("config", "", "config file path")
	checkConfig := flag.Bool("check-config", false, "validate config and exit")
	showVersion := flag.Bool("version", false, "show version info")
	recoverAdmin := flag.String("recover-admin", "", "recover an existing administrator while the service is stopped")
	flag.Parse()
	recoverAdminRequested := false
	flag.Visit(func(parsed *flag.Flag) {
		if parsed.Name == "recover-admin" {
			recoverAdminRequested = true
		}
	})

	if recoverAdminRequested {
		if *checkConfig || *showVersion {
			log.Fatal().Msg("--recover-admin cannot be combined with --check-config or --version")
		}
		if flag.NArg() != 0 {
			log.Fatal().Msg("--recover-admin does not accept positional arguments")
		}
		initLogger()
		if err := recoverAdminOnly(*configPath, *recoverAdmin, os.Stdout); err != nil {
			log.Fatal().Err(err).Msg("failed to recover administrator")
		}
		return
	}

	if *showVersion {
		fmt.Printf("MnemoNAS %s\n", version)
		fmt.Printf("  Commit:     %s\n", commit)
		fmt.Printf("  Build Time: %s\n", buildTime)
		return
	}

	if *checkConfig {
		if err := validateConfigOnly(*configPath, os.Stdout); err != nil {
			log.Fatal().Err(err).Msg("failed to validate config")
		}
		return
	}

	// Initialize logger
	initLogger()

	// Load configuration
	cfg, path, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}
	logCloser, err := applyLoggerConfig(cfg.Log)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to configure logger")
	}
	defer func() {
		if logCloser != nil {
			_ = logCloser.Close()
		}
	}()

	// Ensure directories exist
	if err := cfg.EnsureDirs(); err != nil {
		log.Fatal().Err(err).Msg("failed to create directories")
	}

	authStateLock, err := acquireRuntimeAuthStateLock(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to acquire authentication state lock")
	}
	if authStateLock != nil {
		defer func() {
			if err := authStateLock.Close(); err != nil {
				log.Warn().Err(err).Msg("failed to release authentication state lock")
			}
		}()
	}
	if err := validateRuntimeAdminRecoveryState(cfg); err != nil {
		log.Fatal().Err(err).Msg("offline administrator recovery must be completed before startup")
	}

	// Load or create secrets (for JWT, etc.)
	dataRoot := cfg.Storage.Root
	secrets, isNewSecrets, err := config.LoadOrCreateSecrets(dataRoot)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load secrets")
	}

	// Use secrets for JWT if not configured
	if cfg.Auth.JWTSecret == "" {
		cfg.Auth.JWTSecret = secrets.JWTSecret
	}

	webdavPasswordGenerated := applyStartupWebDAVCredentials(cfg, secrets)

	log.Info().
		Str("version", version).
		Str("storage_root", cfg.Storage.Root).
		Str("address", cfg.Address()).
		Msg("starting MnemoNAS")

	// Security warnings
	for _, warning := range configWarnings(cfg) {
		log.Warn().Msg(warning)
	}
	if cfg.WebDAV.Enabled && cfg.WebDAV.AuthType == "none" {
		log.Warn().Msg("⚠️  WebDAV authentication is DISABLED - WebDAV access is unprotected!")
		log.Warn().Msg("   Set [webdav].auth_type = \"users\" or \"basic\" to enable WebDAV authentication")
	}
	if !cfg.Server.TLS.Enabled {
		log.Warn().Msg("⚠️  TLS/HTTPS is DISABLED - data transmitted in plain text!")
		log.Warn().Msg("   Set [server.tls].enabled = true for secure connections")
	}

	logWebDAVCredentialStatus(dataRoot, cfg.WebDAV.Username, webdavPasswordGenerated, isNewSecrets)

	// Create data plane client for storage operations
	dataplaneClient, err := connectStartupDataplane(cfg.DataPlane.Address(), cfg.DataPlane.Timeout)
	if err != nil {
		log.Fatal().Err(err).Str("address", cfg.DataPlane.Address()).Msg("failed to connect to dataplane")
	}
	defer dataplaneClient.Close()
	log.Info().Str("address", cfg.DataPlane.Address()).Msg("connected to dataplane")

	// Create background context for initialization
	ctx := context.Background()

	// Create filesystem with new storage architecture
	fs, err := storage.New(&storage.Config{
		FilesRoot:               cfg.FilesDir(),
		InternalRoot:            cfg.InternalDir(),
		TrashRoot:               cfg.TrashDir(),
		AutoVersionedExtensions: cfg.Storage.Versioning.AutoVersionedExtensions,
		AutoVersionedFilenames:  cfg.Storage.Versioning.AutoVersionedFilenames,
		MaxVersionedSize:        cfg.Storage.Versioning.MaxVersionedSize,
		MaxVersions:             cfg.Storage.Retention.MaxVersions,
		MaxVersionAge:           cfg.Storage.Retention.MaxAge,
		MinFreeSpace:            cfg.Storage.Retention.MinFreeSpace,
		TrashEnabled:            &cfg.Storage.Trash.Enabled,
		TrashRetentionDays:      cfg.Storage.Trash.RetentionDays,
		MaxTrashSize:            cfg.Storage.Trash.MaxSize,
		Dataplane:               dataplaneClient,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create filesystem")
	}
	defer fs.Close()

	retentionMonitor := storage.NewRetentionMonitor(fs, storage.RetentionMonitorConfig{
		MaxVersions:   cfg.Storage.Retention.MaxVersions,
		MaxVersionAge: cfg.Storage.Retention.MaxAge,
		MinFreeSpace:  cfg.Storage.Retention.MinFreeSpace,
		SweepInterval: cfg.Storage.Retention.GCInterval,
	}, log.Logger)
	retentionMonitor.Start(ctx)
	defer retentionMonitor.Stop()

	// Cleanup staging files from previous crashes
	if cleanedFiles, cleanedBytes, cleanErr := fs.CleanupStaging(ctx); cleanErr != nil {
		log.Warn().Err(cleanErr).Msg("failed to cleanup staging files")
	} else if cleanedFiles > 0 {
		log.Info().
			Int("files", cleanedFiles).
			Int64("bytes", cleanedBytes).
			Msg("cleaned up staging files from previous crash")
	}

	// Create router
	router := chi.NewRouter()

	// Initialize storage alerts monitor
	alertMonitor := alerts.NewMonitor(alerts.Config{
		Enabled:            cfg.Alerts.Enabled,
		CheckInterval:      cfg.Alerts.CheckInterval,
		ThresholdPct:       cfg.Alerts.ThresholdPct,
		CriticalPct:        cfg.Alerts.CriticalPct,
		MinFreeBytes:       cfg.Alerts.MinFreeBytes,
		CooldownPeriod:     cfg.Alerts.CooldownPeriod,
		WebhookURL:         cfg.Alerts.WebhookURL,
		WebhookMethod:      cfg.Alerts.WebhookMethod,
		WebhookHeaders:     cfg.Alerts.WebhookHeaders,
		TelegramEnabled:    cfg.Alerts.TelegramEnabled,
		TelegramBotToken:   cfg.Alerts.TelegramBotToken,
		TelegramChatID:     cfg.Alerts.TelegramChatID,
		WeComEnabled:       cfg.Alerts.WeComEnabled,
		WeComWebhookURL:    cfg.Alerts.WeComWebhookURL,
		DingTalkEnabled:    cfg.Alerts.DingTalkEnabled,
		DingTalkWebhookURL: cfg.Alerts.DingTalkWebhookURL,
		EmailEnabled:       cfg.Alerts.EmailEnabled,
		SMTPHost:           cfg.Alerts.SMTPHost,
		SMTPPort:           cfg.Alerts.SMTPPort,
		SMTPUsername:       cfg.Alerts.SMTPUsername,
		SMTPPassword:       cfg.Alerts.SMTPPassword,
		SMTPFrom:           cfg.Alerts.SMTPFrom,
		SMTPTo:             cfg.Alerts.SMTPTo,
	}, cfg.Storage.Root, log.Logger)
	alertMonitor.Start(ctx)
	if cfg.Alerts.Enabled {
		log.Info().
			Float64("threshold_pct", cfg.Alerts.ThresholdPct).
			Float64("critical_pct", cfg.Alerts.CriticalPct).
			Dur("interval", cfg.Alerts.CheckInterval).
			Msg("storage alerts enabled")
	}

	diskHealthMonitor := diskhealth.NewMonitor(diskHealthRuntimeConfig(cfg.DiskHealth), alertMonitor, log.Logger)
	diskHealthMonitor.Start(ctx)
	defer diskHealthMonitor.Stop()
	if cfg.DiskHealth.Enabled {
		log.Info().
			Int("devices", len(cfg.DiskHealth.Devices)).
			Dur("interval", cfg.DiskHealth.CheckInterval).
			Msg("disk health monitoring enabled")
	}

	var sharedUserStore *auth.UserStore
	if cfg.Auth.Enabled {
		userStore, _, err := auth.NewUserStore(cfg.Auth.UsersFile)
		if err != nil {
			if auth.IsPersistenceWarning(err) && userStore != nil {
				log.Warn().Err(err).Msg("user store initialized with an auth persistence warning")
			} else {
				log.Fatal().Err(err).Msg("failed to initialize user store")
			}
		}
		sharedUserStore = userStore
	}

	// Mount API with data plane connection
	activeWebDAV := api.WebDAVRuntimeConfig{
		Enabled:              cfg.WebDAV.Enabled,
		Prefix:               cfg.WebDAV.Prefix,
		ReadOnly:             cfg.WebDAV.ReadOnly,
		AuthType:             cfg.WebDAV.AuthType,
		Username:             cfg.WebDAV.Username,
		Password:             cfg.WebDAV.Password,
		PasswordIsGenerated:  webdavPasswordGenerated,
		UserStore:            sharedUserStore,
		DirectoryQuotas:      append([]config.DirectoryQuotaConfig(nil), cfg.Storage.DirectoryQuotas...),
		DirectoryAccessRules: cloneConfigDirectoryAccessRules(cfg.Storage.DirectoryAccessRules),
	}
	webdavPrefix, webdavHandler := buildWebDAVHandler(fs, activeWebDAV)
	runtimeWebDAV := newSwitchableWebDAVHandler(webdavPrefix, webdavHandler)

	apiServer, err := api.NewServer(log.Logger, &api.ServerConfig{
		DataplaneAddr:    cfg.DataPlane.Address(),
		FileSystem:       fs, // Pass the new storage filesystem
		AfterPathRenamed: runtimeWebDAV.OnPathRenamed,
		AfterPathDeleted: runtimeWebDAV.OnPathDeleted,
		ThumbnailRoot:    filepath.Join(cfg.InternalDir(), "thumbnails"),
		MaintenanceRoot:  filepath.Join(cfg.InternalDir(), "maintenance"),
		BackupRoot:       filepath.Join(cfg.InternalDir(), "backup"),
		ActivityRoot:     filepath.Join(cfg.InternalDir(), "activity"),
		StorageRoot:      cfg.Storage.Root,
		BackupJobs:       cfg.Backup.Jobs,
		// Auth configuration
		AuthEnabled:    cfg.Auth.Enabled,
		AuthUsersFile:  cfg.Auth.UsersFile,
		AuthUserStore:  sharedUserStore,
		AuthJWTSecret:  cfg.Auth.JWTSecret,
		AuthAccessTTL:  cfg.Auth.AccessTokenTTL,
		AuthRefreshTTL: cfg.Auth.RefreshTokenTTL,
		// Share configuration
		ShareEnabled:     cfg.Share.Enabled,
		ShareStoreFile:   cfg.Share.StoreFile,
		ShareBaseURL:     cfg.Share.BaseURL,
		AlertMonitor:     alertMonitor,
		RetentionMonitor: retentionMonitor,
		DiskHealth:       diskHealthMonitor,
		// Favorites configuration
		FavoritesEnabled:   cfg.Favorites.Enabled,
		FavoritesStoreFile: cfg.Favorites.StoreFile,
		// Config for settings API
		Config:       cfg,
		ConfigPath:   path,
		AppVersion:   version,
		BuildTime:    buildTime,
		ActiveWebDAV: &activeWebDAV,
		UpdateWebDAV: func(runtimeCfg api.WebDAVRuntimeConfig) {
			prefix, handler := buildWebDAVHandler(fs, runtimeCfg)
			runtimeWebDAV.Update(prefix, handler)
			if runtimeCfg.Enabled {
				log.Info().
					Str("prefix", runtimeCfg.Prefix).
					Str("auth", runtimeCfg.AuthType).
					Bool("read_only", runtimeCfg.ReadOnly).
					Msg("WebDAV runtime configuration updated")
				return
			}
			log.Info().Msg("WebDAV disabled at runtime")
		},
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create API server")
	}
	router.Mount("/", apiServer.Router())

	frontendDir := discoverFrontendAssets()
	var frontend http.Handler
	if frontendDir != "" {
		frontend = newFrontendHandler(frontendDir)
		log.Info().Str("dir", frontendDir).Msg("frontend assets enabled")
	} else {
		log.Info().Msg("frontend assets not found; serving API and WebDAV only")
	}

	// WebDAV is matched before chi because chi does not route methods such as
	// PROPFIND and MKCOL. Cross-origin unsafe request checks are applied around
	// the combined handler so WebDAV and API mutations share the same guard.
	handler := buildApplicationHandler(runtimeWebDAV, frontend, router)
	if activeWebDAV.Enabled {
		log.Info().Str("prefix", cfg.WebDAV.Prefix).Str("auth", cfg.WebDAV.AuthType).Msg("WebDAV enabled")
	}

	// Create HTTP server
	server := &http.Server{
		Addr:         cfg.Address(),
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	// Configure TLS if enabled
	tlsManager := mnemonasTLS.NewManager(mnemonasTLS.Config{
		Enabled:      cfg.Server.TLS.Enabled,
		CertFile:     cfg.Server.TLS.CertFile,
		KeyFile:      cfg.Server.TLS.KeyFile,
		AutoGenerate: cfg.Server.TLS.AutoGenerate,
		CertDir:      cfg.Server.TLS.CertDir,
	})

	if cfg.Server.TLS.Enabled {
		tlsConfig, err := tlsManager.GetTLSConfig()
		if err != nil {
			log.Fatal().Err(err).Msg("failed to configure TLS")
		}
		server.TLSConfig = tlsConfig

		// Log certificate info
		if certInfo, err := tlsManager.GetCertificateInfo(); err == nil {
			log.Info().
				Bool("self_signed", certInfo.SelfSigned).
				Time("expires", certInfo.NotAfter).
				Strs("dns_names", certInfo.DNSNames).
				Strs("ip_addresses", certInfo.IPAddresses).
				Msg("TLS certificate loaded")
		}
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Info().Msg("shutting down server...")

		// Stop storage alerts monitor
		if alertMonitor != nil {
			alertMonitor.Stop()
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("failed to shutdown server")
		}
	}()

	// Start server
	if cfg.Server.TLS.Enabled {
		log.Info().Str("address", cfg.Address()).Msg("server started (HTTPS)")
		if err := server.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server exited abnormally")
		}
	} else {
		log.Info().Str("address", cfg.Address()).Msg("server started (HTTP)")
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server exited abnormally")
		}
	}

	log.Info().Msg("server stopped")
}

func logWebDAVCredentialStatus(dataRoot, username string, passwordGenerated, newSecrets bool) {
	if !passwordGenerated {
		return
	}

	secretsPath := filepath.Join(dataRoot, config.SecretsFile)
	if newSecrets {
		log.Info().Msg("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Info().Msg("🔐 WebDAV credentials were auto-generated")
		log.Info().Str("username", username).Msg("   Username")
		log.Info().Str("secrets_file", secretsPath).Msg("   Password stored in")
		log.Info().Msg("   View the password from the server-side secrets file or Web UI settings")
		log.Info().Msg("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		return
	}

	log.Info().Str("secrets_file", secretsPath).Msg("🔐 WebDAV using auto-generated password from secrets file")
}

func applyStartupWebDAVCredentials(cfg *config.Config, secrets *config.Secrets) bool {
	webdavAuthType := config.NormalizeWebDAVAuthType(cfg.WebDAV.AuthType)
	if cfg.WebDAV.Enabled && webdavAuthType == "basic" && strings.TrimSpace(cfg.WebDAV.Username) == "" {
		cfg.WebDAV.Username = "admin"
	}

	if cfg.WebDAV.Enabled && webdavAuthType == "basic" && strings.TrimSpace(cfg.WebDAV.Password) == "" {
		cfg.WebDAV.Password = secrets.WebDAVPassword
		return true
	}
	return false
}

func initLogger() {
	// Use colored console output only when writing to a terminal
	noColor := !isatty.IsTerminal(os.Stderr.Fd()) && !isatty.IsCygwinTerminal(os.Stderr.Fd())
	log.Logger = zerolog.New(
		zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: "15:04:05",
			NoColor:    noColor,
		},
	).With().Timestamp().Caller().Logger()

	zerolog.SetGlobalLevel(zerolog.InfoLevel)
}

func applyLoggerConfig(cfg config.LogConfig) (io.Closer, error) {
	writer, closer, noColor, err := resolveLogOutput(cfg.Output)
	if err != nil {
		return nil, err
	}

	level, err := resolveLogLevel(cfg.Level)
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		return nil, err
	}

	format := strings.ToLower(strings.TrimSpace(cfg.Format))
	if format == "" {
		format = "console"
	}

	if format == "json" {
		zerolog.TimeFieldFormat = resolveJSONLogTimeFormat(cfg.TimeFormat)
		log.Logger = zerolog.New(writer).With().Timestamp().Caller().Logger()
	} else {
		zerolog.TimeFieldFormat = resolveConsoleLogTimeFieldFormat(cfg.TimeFormat)
		consoleWriter := zerolog.ConsoleWriter{
			Out:        writer,
			TimeFormat: resolveConsoleLogTimeFormat(cfg.TimeFormat),
			NoColor:    noColor,
		}
		if formatter := resolveConsoleLogTimestampFormatter(cfg.TimeFormat); formatter != nil {
			consoleWriter.FormatTimestamp = formatter
		}
		log.Logger = zerolog.New(consoleWriter).With().Timestamp().Caller().Logger()
	}

	zerolog.SetGlobalLevel(level)
	return closer, nil
}

func normalizeLogOutputPath(output string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(output))
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve log output path: %w", err)
	}
	return absPath, nil
}

func validateLogOutputPath(path string) error {
	root := filepath.VolumeName(path) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(path, root)
	if trimmed == "" {
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errLogOutputSymlink
		}
		return nil
	}

	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errLogOutputSymlink
		}
	}
	return nil
}

func resolveLogOutput(output string) (io.Writer, io.Closer, bool, error) {
	switch strings.ToLower(strings.TrimSpace(output)) {
	case "", "stdout":
		noColor := !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd())
		return os.Stdout, nil, noColor, nil
	case "stderr":
		noColor := !isatty.IsTerminal(os.Stderr.Fd()) && !isatty.IsCygwinTerminal(os.Stderr.Fd())
		return os.Stderr, nil, noColor, nil
	default:
		cleanPath, err := normalizeLogOutputPath(output)
		if err != nil {
			return nil, nil, true, err
		}
		if err := validateLogOutputPath(cleanPath); err != nil {
			return nil, nil, true, err
		}
		file, err := openLogOutputFile(cleanPath)
		if err != nil {
			return nil, nil, true, err
		}
		return file, file, true, nil
	}
}

func resolveLogLevel(level string) (zerolog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return zerolog.InfoLevel, nil
	case "debug":
		return zerolog.DebugLevel, nil
	case "warn":
		return zerolog.WarnLevel, nil
	case "error":
		return zerolog.ErrorLevel, nil
	default:
		return zerolog.InfoLevel, fmt.Errorf("invalid log level %q", level)
	}
}

func resolveConsoleLogTimeFormat(timeFormat string) string {
	if format, ok := resolveNamedRFCLogTimeFormat(timeFormat); ok {
		return format
	}
	if _, ok := resolveNamedUnixLogTimeFormat(timeFormat); ok {
		return time.RFC3339
	}
	return strings.TrimSpace(timeFormat)
}

func resolveConsoleLogTimeFieldFormat(timeFormat string) string {
	if format, ok := resolveNamedLogTimeFormat(timeFormat); ok {
		return format
	}
	return time.RFC3339
}

func resolveConsoleLogTimestampFormatter(timeFormat string) zerolog.Formatter {
	if _, ok := resolveNamedUnixLogTimeFormat(timeFormat); ok {
		return formatRawLogTimestamp
	}
	return nil
}

func formatRawLogTimestamp(value interface{}) string {
	if value == nil {
		return "<nil>"
	}
	return fmt.Sprint(value)
}

func resolveJSONLogTimeFormat(timeFormat string) string {
	if format, ok := resolveNamedLogTimeFormat(timeFormat); ok {
		return format
	}
	return strings.TrimSpace(timeFormat)
}

func resolveNamedLogTimeFormat(timeFormat string) (string, bool) {
	if format, ok := resolveNamedRFCLogTimeFormat(timeFormat); ok {
		return format, true
	}
	return resolveNamedUnixLogTimeFormat(timeFormat)
}

func resolveNamedRFCLogTimeFormat(timeFormat string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(timeFormat)) {
	case "", "RFC3339":
		return time.RFC3339, true
	case "RFC3339NANO":
		return time.RFC3339Nano, true
	default:
		return "", false
	}
}

func resolveNamedUnixLogTimeFormat(timeFormat string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(timeFormat)) {
	case "UNIX":
		return zerolog.TimeFormatUnix, true
	case "UNIXMS":
		return zerolog.TimeFormatUnixMs, true
	case "UNIXMICRO":
		return zerolog.TimeFormatUnixMicro, true
	case "UNIXNANO":
		return zerolog.TimeFormatUnixNano, true
	default:
		return "", false
	}
}

func loadConfig(path string) (*config.Config, string, error) {
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return nil, path, fmt.Errorf("config file does not exist: %s", path)
			}
			return nil, path, fmt.Errorf("failed to stat config file %s: %w", path, err)
		}
	}

	if path == "" {
		// Try default paths
		home, _ := os.UserHomeDir()
		candidates := []string{
			home + "/.mnemonas/config.toml",
		}

		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
	}

	if path != "" {
		log.Info().Str("path", path).Msg("loading config file")
		cfg, err := config.Load(path)
		return cfg, path, err
	}

	log.Info().Msg("using default config")
	return config.Default(), "", nil
}

func validateConfigOnly(path string, output io.Writer) error {
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("config file does not exist: %s", path)
			}
			return fmt.Errorf("failed to stat config file %s: %w", path, err)
		}
	}

	resolvedPath := path
	if resolvedPath == "" {
		home, _ := os.UserHomeDir()
		candidate := filepath.Join(home, ".mnemonas", "config.toml")
		if _, err := os.Stat(candidate); err == nil {
			resolvedPath = candidate
		}
	}

	if resolvedPath == "" {
		cfg := config.Default()
		if err := cfg.Validate(); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(output, "configuration is valid (using built-in defaults)")
		writeConfigWarnings(output, cfg)
		return nil
	}

	cfg, err := config.Load(resolvedPath)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "configuration is valid: %s\n", resolvedPath)
	writeConfigWarnings(output, cfg)
	return nil
}

func writeConfigWarnings(output io.Writer, cfg *config.Config) {
	for _, warning := range configWarnings(cfg) {
		_, _ = fmt.Fprintf(output, "warning: %s\n", warning)
	}
}

func configWarnings(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}

	warnings := make([]string, 0, 3)
	serverBeyondLoopback := listensBeyondLoopback(cfg.Server.Host)
	if serverBeyondLoopback && !cfg.Auth.Enabled {
		warnings = append(warnings, "auth.enabled=false while server.host listens beyond loopback; Web UI/API will be reachable without login")
	}
	if serverBeyondLoopback && cfg.WebDAV.Enabled && strings.EqualFold(strings.TrimSpace(cfg.WebDAV.AuthType), "none") {
		warnings = append(warnings, "webdav.auth_type=none while server.host listens beyond loopback; WebDAV will be reachable without authentication")
	}
	if listensBeyondLoopback(hostFromTCPAddress(cfg.DataPlane.GRPCAddress)) {
		warnings = append(warnings, "dataplane.grpc_address listens beyond loopback; dataplane has no external authentication")
	}
	if cfg.SMB.Enabled {
		warnings = append(warnings, "smb.enabled=true configures a preview gateway contract only; this build does not start an SMB/Samba listener")
	}
	return warnings
}

func hostFromTCPAddress(address string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(trimmed)
	if err == nil {
		return strings.Trim(host, "[]")
	}

	if strings.HasPrefix(trimmed, "[") {
		if end := strings.Index(trimmed, "]"); end > 0 {
			return strings.Trim(trimmed[1:end], "[]")
		}
	}

	if strings.Count(trimmed, ":") == 1 {
		host, _, _ := strings.Cut(trimmed, ":")
		return strings.Trim(host, "[]")
	}
	return strings.Trim(trimmed, "[]")
}

func listensBeyondLoopback(host string) bool {
	normalized := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
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

func matchesWebDAVPrefix(prefix, requestPath string) bool {
	if requestPath == prefix {
		return true
	}

	if prefix == "/" {
		return true
	}

	return len(requestPath) > len(prefix) && requestPath[:len(prefix)] == prefix && requestPath[len(prefix)] == '/'
}
