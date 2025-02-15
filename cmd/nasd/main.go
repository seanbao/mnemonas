// MnemoNAS main program
// Starts the control plane service, including WebDAV and REST API
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/seanbao/mnemonas/internal/alerts"
	"github.com/seanbao/mnemonas/internal/api"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/storage"
	mnemonasTLS "github.com/seanbao/mnemonas/internal/tls"
	"github.com/seanbao/mnemonas/internal/webdav"
)

var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	// Command line arguments
	configPath := flag.String("config", "", "config file path")
	showVersion := flag.Bool("version", false, "show version info")
	flag.Parse()

	if *showVersion {
		fmt.Printf("MnemoNAS %s\n", version)
		fmt.Printf("  Commit:     %s\n", commit)
		fmt.Printf("  Build Time: %s\n", buildTime)
		return
	}

	// Initialize logger
	initLogger()

	// Load configuration
	cfg, path, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// Ensure directories exist
	if err := cfg.EnsureDirs(); err != nil {
		log.Fatal().Err(err).Msg("failed to create directories")
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

	// Auto-generate WebDAV password if basic auth enabled but password not set
	webdavPasswordGenerated := false
	if cfg.WebDAV.Enabled && cfg.WebDAV.AuthType == "basic" && cfg.WebDAV.Password == "" {
		cfg.WebDAV.Password = secrets.WebDAVPassword
		webdavPasswordGenerated = true
		// Also set default username if not configured
		if cfg.WebDAV.Username == "" {
			cfg.WebDAV.Username = "admin"
		}
	}

	log.Info().
		Str("version", version).
		Str("storage_root", cfg.Storage.Root).
		Str("address", cfg.Address()).
		Msg("starting MnemoNAS")

	// Security warnings
	if cfg.WebDAV.Enabled && cfg.WebDAV.AuthType == "none" {
		log.Warn().Msg("⚠️  WebDAV authentication is DISABLED - WebDAV access is unprotected!")
		log.Warn().Msg("   Set [webdav].auth_type = \"basic\" to enable WebDAV authentication")
	}
	if !cfg.Server.TLS.Enabled {
		log.Warn().Msg("⚠️  TLS/HTTPS is DISABLED - data transmitted in plain text!")
		log.Warn().Msg("   Set [server.tls].enabled = true for secure connections")
	}

	// Show auto-generated WebDAV credentials (only on first run or when password was empty)
	if webdavPasswordGenerated && isNewSecrets {
		log.Info().Msg("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Info().Msg("🔐 WebDAV credentials (auto-generated, save these!):")
		log.Info().Str("username", cfg.WebDAV.Username).Msg("   Username")
		log.Info().Str("password", cfg.WebDAV.Password).Msg("   Password")
		log.Info().Msgf("   Stored in: %s/secrets.json", dataRoot)
		log.Info().Msg("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	} else if webdavPasswordGenerated {
		log.Info().Msg("🔐 WebDAV using auto-generated password from secrets.json")
	}

	// Create background context for initialization
	ctx := context.Background()

	// Create data plane client for storage operations
	dataplaneClient := dataplane.NewClient(cfg.DataPlane.Address())
	if err := dataplaneClient.Connect(ctx); err != nil {
		log.Fatal().Err(err).Str("address", cfg.DataPlane.Address()).Msg("failed to connect to dataplane")
	}
	defer dataplaneClient.Close()
	log.Info().Str("address", cfg.DataPlane.Address()).Msg("connected to dataplane")

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
		Enabled:        cfg.Alerts.Enabled,
		CheckInterval:  cfg.Alerts.CheckInterval,
		ThresholdPct:   cfg.Alerts.ThresholdPct,
		CriticalPct:    cfg.Alerts.CriticalPct,
		MinFreeBytes:   cfg.Alerts.MinFreeBytes,
		CooldownPeriod: cfg.Alerts.CooldownPeriod,
		WebhookURL:     cfg.Alerts.WebhookURL,
		WebhookMethod:  cfg.Alerts.WebhookMethod,
		WebhookHeaders: cfg.Alerts.WebhookHeaders,
	}, cfg.Storage.Root, log.Logger)
	alertMonitor.Start(ctx)
	if cfg.Alerts.Enabled {
		log.Info().
			Float64("threshold_pct", cfg.Alerts.ThresholdPct).
			Float64("critical_pct", cfg.Alerts.CriticalPct).
			Dur("interval", cfg.Alerts.CheckInterval).
			Msg("storage alerts enabled")
	}

	// Mount API with data plane connection
	apiServer, err := api.NewServer(log.Logger, &api.ServerConfig{
		DataplaneAddr:   cfg.DataPlane.Address(),
		FileSystem:      fs, // Pass the new storage filesystem
		ThumbnailRoot:   filepath.Join(cfg.InternalDir(), "thumbnails"),
		MaintenanceRoot: filepath.Join(cfg.InternalDir(), "maintenance"),
		ActivityRoot:    filepath.Join(cfg.InternalDir(), "activity"),
		// Auth configuration
		AuthEnabled:    cfg.Auth.Enabled,
		AuthUsersFile:  cfg.Auth.UsersFile,
		AuthJWTSecret:  cfg.Auth.JWTSecret,
		AuthAccessTTL:  cfg.Auth.AccessTokenTTL,
		AuthRefreshTTL: cfg.Auth.RefreshTokenTTL,
		// Share configuration
		ShareEnabled:     cfg.Share.Enabled,
		ShareStoreFile:   cfg.Share.StoreFile,
		ShareBaseURL:     cfg.Share.BaseURL,
		AlertMonitor:     alertMonitor,
		RetentionMonitor: retentionMonitor,
		// Favorites configuration
		FavoritesEnabled:   cfg.Favorites.Enabled,
		FavoritesStoreFile: cfg.Favorites.StoreFile,
		// Config for settings API
		Config:     cfg,
		ConfigPath: path,
		ActiveWebDAV: &api.WebDAVRuntimeConfig{
			Enabled:             cfg.WebDAV.Enabled,
			Prefix:              cfg.WebDAV.Prefix,
			AuthType:            cfg.WebDAV.AuthType,
			Username:            cfg.WebDAV.Username,
			Password:            cfg.WebDAV.Password,
			PasswordIsGenerated: webdavPasswordGenerated,
		},
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create API server")
	}
	router.Mount("/", apiServer.Router())

	// Create final handler - WebDAV needs to be handled before chi router
	// because chi doesn't support WebDAV methods (PROPFIND, MKCOL, etc.)
	var handler http.Handler = router
	if cfg.WebDAV.Enabled {
		davHandler := webdav.NewHandler(webdav.Config{
			FileSystem: fs,
			Prefix:     cfg.WebDAV.Prefix,
			ReadOnly:   cfg.WebDAV.ReadOnly,
			AuthType:   cfg.WebDAV.AuthType,
			Username:   cfg.WebDAV.Username,
			Password:   cfg.WebDAV.Password,
		})
		// Wrap handler to route WebDAV requests
		prefix := cfg.WebDAV.Prefix
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Route to WebDAV handler if path matches prefix
			if matchesWebDAVPrefix(prefix, r.URL.Path) {
				davHandler.ServeHTTP(w, r)
				return
			}
			router.ServeHTTP(w, r)
		})
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

func loadConfig(path string) (*config.Config, string, error) {
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

func matchesWebDAVPrefix(prefix, requestPath string) bool {
	if requestPath == prefix {
		return true
	}

	if prefix == "/" {
		return true
	}

	return len(requestPath) > len(prefix) && requestPath[:len(prefix)] == prefix && requestPath[len(prefix)] == '/'
}
