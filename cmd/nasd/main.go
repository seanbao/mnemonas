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
	quotareservation "github.com/seanbao/mnemonas/internal/quota"
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
var errHTTPServerExitedWithoutShutdown = errors.New("HTTP server exited without a shutdown request")

const httpServerShutdownTimeout = 30 * time.Second

var startupDataplaneContext = dataplane.WithTimeout
var startupDataplaneConnect = func(client *dataplane.Client, ctx context.Context) error {
	return client.Connect(ctx)
}
var afterOpenLogOutputParent = func() {}

type gracefulHTTPServer interface {
	Shutdown(context.Context) error
	Close() error
}

type httpServerLifecycleResult struct {
	ShutdownRequested bool
	ServeErr          error
	ShutdownErr       error
	CloseErr          error
}

func (r httpServerLifecycleResult) Err() error {
	return errors.Join(r.ServeErr, r.ShutdownErr, r.CloseErr)
}

func runHTTPServerLifecycle(
	server gracefulHTTPServer,
	serve func() error,
	shutdownRequest <-chan struct{},
	shutdownTimeout time.Duration,
	onShutdownStart func(),
) httpServerLifecycleResult {
	type shutdownOutcome struct {
		shutdownErr error
		closeErr    error
	}

	shutdownServer := func() shutdownOutcome {
		if onShutdownStart != nil {
			onShutdownStart()
		}
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		shutdownErr := server.Shutdown(ctx)
		cancel()

		var closeErr error
		if shutdownErr != nil {
			closeErr = server.Close()
		}
		return shutdownOutcome{
			shutdownErr: shutdownErr,
			closeErr:    closeErr,
		}
	}

	lifecycleDone := make(chan struct{})
	shutdownDone := make(chan struct{})
	shutdownResult := make(chan shutdownOutcome, 1)
	go func() {
		defer close(shutdownDone)
		select {
		case <-shutdownRequest:
			shutdownResult <- shutdownServer()
		case <-lifecycleDone:
		}
	}()

	serveErr := serve()
	close(lifecycleDone)
	// If Shutdown closed the listener first, Serve may already have returned
	// ErrServerClosed. Wait for the shutdown worker before any caller-owned
	// resources are released.
	<-shutdownDone

	result := httpServerLifecycleResult{}
	select {
	case outcome := <-shutdownResult:
		result.ShutdownRequested = true
		result.ShutdownErr = outcome.shutdownErr
		result.CloseErr = outcome.closeErr
	default:
		// Serve may exit because its listener failed or was closed outside the
		// signal path. Drain active connections before caller-owned resources
		// are released even though no shutdown request initiated the exit.
		select {
		case <-shutdownRequest:
			result.ShutdownRequested = true
		default:
		}
		outcome := shutdownServer()
		result.ShutdownErr = outcome.shutdownErr
		result.CloseErr = outcome.closeErr
	}
	if !result.ShutdownRequested {
		if serveErr == nil || errors.Is(serveErr, http.ErrServerClosed) {
			result.ServeErr = errHTTPServerExitedWithoutShutdown
		} else {
			result.ServeErr = serveErr
		}
	} else if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		result.ServeErr = serveErr
	}
	return result
}

type switchableWebDAVHandler struct {
	mu                     sync.Mutex
	current                *switchableWebDAVGeneration
	retired                []*switchableWebDAVGeneration
	pendingClose           []http.Handler
	registeredLifecycleIDs map[webDAVHandlerLifecycleKey]struct{}
	fallbackClosers        map[http.Handler]struct{}
}

type switchableWebDAVGeneration struct {
	prefix  string
	handler http.Handler
	active  int
	retired bool
}

type webdavPathChangeHandler interface {
	OnPathRenamed(oldPath, newPath string)
	OnPathDeleted(path string) *storage.PathDeleteHookResult
}

type sharedWebDAVPathChangeHandler interface {
	webdavPathChangeHandler
	PathChangeRuntimeState() *webdav.RuntimeLockState
	InvalidateRenamedPathCache(oldPath, newPath string)
	InvalidateDeletedPathCache(path string)
}

type webDAVLifecycleHandler interface {
	WebDAVLifecycleID() uint64
}

type webDAVHandlerLifecycleKey struct {
	handlerType reflect.Type
	id          uint64
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
	if s.current != nil && handlersEqual(s.current.handler, handler) {
		s.current.prefix = prefix
		s.mu.Unlock()
		return
	}
	if handlerIsClosable(handler) && !handlerIsComparable(handler) {
		s.mu.Unlock()
		panic("closable WebDAV handlers must be comparable")
	}
	if err := s.registerHandlerLifecycleLocked(handler); err != nil {
		s.mu.Unlock()
		panic(err.Error())
	}

	next := &switchableWebDAVGeneration{
		prefix:  prefix,
		handler: handler,
	}
	previous := s.current
	s.current = next

	if previous != nil {
		previous.retired = true
		if previous.active == 0 {
			s.queueHandlerCloseLocked(previous.handler)
		} else {
			s.retired = append(s.retired, previous)
		}
	}
	closeHandlers := s.collectClosableHandlersLocked()
	s.mu.Unlock()

	s.closeWebDAVHandlersAsync(closeHandlers)
}

func closeWebDAVHandler(handler http.Handler) {
	switch closer := handler.(type) {
	case interface{ Close() }:
		closer.Close()
	case io.Closer:
		_ = closer.Close()
	}
}

func (s *switchableWebDAVHandler) closeWebDAVHandlersAsync(handlers []http.Handler) {
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		go func(retired http.Handler) {
			closeWebDAVHandler(retired)
		}(handler)
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

func handlerIsComparable(handler http.Handler) bool {
	return handler == nil || reflect.TypeOf(handler).Comparable()
}

func handlerIsClosable(handler http.Handler) bool {
	if handler == nil {
		return false
	}
	if _, ok := handler.(interface{ Close() }); ok {
		return true
	}
	_, ok := handler.(io.Closer)
	return ok
}

func (s *switchableWebDAVHandler) registerHandlerLifecycleLocked(handler http.Handler) error {
	if handler == nil || !handlerIsClosable(handler) {
		return nil
	}

	if lifecycle, ok := handler.(webDAVLifecycleHandler); ok {
		id := lifecycle.WebDAVLifecycleID()
		if id == 0 {
			return errors.New("WebDAV handler lifecycle ID must not be zero")
		}
		key := webDAVHandlerLifecycleKey{
			handlerType: reflect.TypeOf(handler),
			id:          id,
		}
		if s.registeredLifecycleIDs == nil {
			s.registeredLifecycleIDs = make(map[webDAVHandlerLifecycleKey]struct{})
		}
		if _, exists := s.registeredLifecycleIDs[key]; exists {
			return errors.New("WebDAV handler lifecycle ID has already been published")
		}
		s.registeredLifecycleIDs[key] = struct{}{}
		return nil
	}

	if s.fallbackClosers == nil {
		s.fallbackClosers = make(map[http.Handler]struct{})
	}
	if _, exists := s.fallbackClosers[handler]; exists {
		return errors.New("closed WebDAV handlers cannot be published again")
	}
	s.fallbackClosers[handler] = struct{}{}
	return nil
}

func (s *switchableWebDAVHandler) queueHandlerCloseLocked(handler http.Handler) {
	if !handlerIsClosable(handler) {
		return
	}
	for _, pending := range s.pendingClose {
		if handlersEqual(pending, handler) {
			return
		}
	}
	s.pendingClose = append(s.pendingClose, handler)
}

func (s *switchableWebDAVHandler) collectClosableHandlersLocked() []http.Handler {
	if len(s.pendingClose) == 0 {
		return nil
	}

	closable := make([]http.Handler, 0, len(s.pendingClose))
	retained := s.pendingClose[:0]
	for _, pending := range s.pendingClose {
		if s.handlerIsLiveLocked(pending) {
			retained = append(retained, pending)
			continue
		}
		closable = append(closable, pending)
	}
	s.pendingClose = retained
	return closable
}

func (s *switchableWebDAVHandler) handlerIsLiveLocked(handler http.Handler) bool {
	if s.current != nil && handlersEqual(s.current.handler, handler) {
		return true
	}
	for _, generation := range s.retired {
		if handlersEqual(generation.handler, handler) {
			return true
		}
	}
	return false
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
	s.mu.Lock()
	generation := s.current
	if generation == nil ||
		generation.handler == nil ||
		!matchesWebDAVPrefix(generation.prefix, r.URL.Path) {
		s.mu.Unlock()
		return false
	}
	generation.active++
	s.mu.Unlock()
	defer s.releaseGeneration(generation)

	generation.handler.ServeHTTP(w, r)
	return true
}

func (s *switchableWebDAVHandler) OnPathRenamed(oldPath, newPath string) {
	leases := s.acquirePathChangeGenerations()
	defer s.releasePathChangeGenerations(leases)

	seenRuntimeStates := make(map[*webdav.RuntimeLockState]struct{})
	invokedHandlers := make([]http.Handler, 0, len(leases))
	for _, lease := range leases {
		if shared, ok := lease.notifier.(sharedWebDAVPathChangeHandler); ok {
			if runtimeState := shared.PathChangeRuntimeState(); runtimeState != nil {
				if _, seen := seenRuntimeStates[runtimeState]; seen {
					shared.InvalidateRenamedPathCache(oldPath, newPath)
					continue
				}
				seenRuntimeStates[runtimeState] = struct{}{}
			}
		} else if handlerAlreadyInvoked(invokedHandlers, lease.generation.handler) {
			continue
		}
		lease.notifier.OnPathRenamed(oldPath, newPath)
		invokedHandlers = append(invokedHandlers, lease.generation.handler)
	}
}

func (s *switchableWebDAVHandler) OnPathDeleted(path string) *storage.PathDeleteHookResult {
	leases := s.acquirePathChangeGenerations()
	defer s.releasePathChangeGenerations(leases)

	seenRuntimeStates := make(map[*webdav.RuntimeLockState]struct{})
	invokedHandlers := make([]http.Handler, 0, len(leases))
	results := make([]*storage.PathDeleteHookResult, 0, len(leases))
	for _, lease := range leases {
		if shared, ok := lease.notifier.(sharedWebDAVPathChangeHandler); ok {
			if runtimeState := shared.PathChangeRuntimeState(); runtimeState != nil {
				if _, seen := seenRuntimeStates[runtimeState]; seen {
					shared.InvalidateDeletedPathCache(path)
					continue
				}
				seenRuntimeStates[runtimeState] = struct{}{}
			}
		} else if handlerAlreadyInvoked(invokedHandlers, lease.generation.handler) {
			continue
		}
		results = append(results, lease.notifier.OnPathDeleted(path))
		invokedHandlers = append(invokedHandlers, lease.generation.handler)
	}
	return combineWebDAVPathDeleteHookResults(results)
}

type webdavPathChangeGenerationLease struct {
	generation *switchableWebDAVGeneration
	notifier   webdavPathChangeHandler
}

func (s *switchableWebDAVHandler) acquirePathChangeGenerations() []webdavPathChangeGenerationLease {
	s.mu.Lock()
	defer s.mu.Unlock()

	generations := make([]*switchableWebDAVGeneration, 0, len(s.retired)+1)
	if s.current != nil {
		generations = append(generations, s.current)
	}
	generations = append(generations, s.retired...)

	leases := make([]webdavPathChangeGenerationLease, 0, len(generations))
	for _, generation := range generations {
		notifier, ok := generation.handler.(webdavPathChangeHandler)
		if !ok {
			continue
		}
		generation.active++
		leases = append(leases, webdavPathChangeGenerationLease{
			generation: generation,
			notifier:   notifier,
		})
	}
	return leases
}

func (s *switchableWebDAVHandler) releaseGeneration(generation *switchableWebDAVGeneration) {
	s.releaseGenerations([]*switchableWebDAVGeneration{generation})
}

func (s *switchableWebDAVHandler) releasePathChangeGenerations(leases []webdavPathChangeGenerationLease) {
	generations := make([]*switchableWebDAVGeneration, 0, len(leases))
	for _, lease := range leases {
		generations = append(generations, lease.generation)
	}
	s.releaseGenerations(generations)
}

func (s *switchableWebDAVHandler) releaseGenerations(generations []*switchableWebDAVGeneration) {
	if len(generations) == 0 {
		return
	}

	s.mu.Lock()
	for _, generation := range generations {
		generation.active--
		if !generation.retired || generation.active > 0 {
			continue
		}
		s.removeRetiredGenerationLocked(generation)
		s.queueHandlerCloseLocked(generation.handler)
	}
	closeHandlers := s.collectClosableHandlersLocked()
	s.mu.Unlock()

	s.closeWebDAVHandlersAsync(closeHandlers)
}

func (s *switchableWebDAVHandler) removeRetiredGenerationLocked(target *switchableWebDAVGeneration) {
	for i, generation := range s.retired {
		if generation != target {
			continue
		}
		copy(s.retired[i:], s.retired[i+1:])
		s.retired[len(s.retired)-1] = nil
		s.retired = s.retired[:len(s.retired)-1]
		return
	}
}

func handlerAlreadyInvoked(invoked []http.Handler, handler http.Handler) bool {
	for _, candidate := range invoked {
		if handlersEqual(candidate, handler) {
			return true
		}
	}
	return false
}

func combineWebDAVPathDeleteHookResults(results []*storage.PathDeleteHookResult) *storage.PathDeleteHookResult {
	nonNil := make([]*storage.PathDeleteHookResult, 0, len(results))
	rollbacks := make([]func() error, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		if len(result.RestoreData) > 0 {
			panic("WebDAV path delete hooks must not return restore metadata")
		}
		nonNil = append(nonNil, result)
		if result.Rollback != nil {
			rollbacks = append(rollbacks, result.Rollback)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	if len(nonNil) == 1 {
		return nonNil[0]
	}

	combined := &storage.PathDeleteHookResult{}
	if len(rollbacks) > 0 {
		combined.Rollback = func() error {
			var rollbackErr error
			for i := len(rollbacks) - 1; i >= 0; i-- {
				rollbackErr = errors.Join(rollbackErr, rollbacks[i]())
			}
			return rollbackErr
		}
	}
	return combined
}

func buildWebDAVHandler(
	fs *storage.FileSystem,
	cfg api.WebDAVRuntimeConfig,
	serverCfg config.ServerConfig,
	quotaCoordinator *quotareservation.Coordinator,
	runtimeLockState *webdav.RuntimeLockState,
) (string, http.Handler) {
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
		FileSystem:                    fs,
		Prefix:                        prefix,
		ReadOnly:                      cfg.ReadOnly,
		AuthType:                      authType,
		Username:                      username,
		Password:                      cfg.Password,
		UserAuthenticator:             userAuthenticator,
		DirectoryQuotas:               directoryQuotas,
		DirectoryAccess:               directoryAccess,
		ReadTimeout:                   serverCfg.ReadTimeout,
		WriteTimeout:                  serverCfg.WriteTimeout,
		QuotaCoordinator:              quotaCoordinator,
		RuntimeLockState:              runtimeLockState,
		PathChangesObservedExternally: true,
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
	runMainAndExit(runMain, os.Exit)
}

func runMainAndExit(run func() int, exit func(int)) {
	if exitCode := run(); exitCode != 0 {
		exit(exitCode)
	}
}

func runMain() int {
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
			log.Error().Msg("--recover-admin cannot be combined with --check-config or --version")
			return 1
		}
		if flag.NArg() != 0 {
			log.Error().Msg("--recover-admin does not accept positional arguments")
			return 1
		}
		initLogger()
		if err := recoverAdminOnly(*configPath, *recoverAdmin, os.Stdout); err != nil {
			log.Error().Err(err).Msg("failed to recover administrator")
			return 1
		}
		return 0
	}

	if *showVersion {
		fmt.Printf("MnemoNAS %s\n", version)
		fmt.Printf("  Commit:     %s\n", commit)
		fmt.Printf("  Build Time: %s\n", buildTime)
		return 0
	}

	if *checkConfig {
		if err := validateConfigOnly(*configPath, os.Stdout); err != nil {
			log.Error().Err(err).Msg("failed to validate config")
			return 1
		}
		return 0
	}

	// Initialize logger
	initLogger()

	// Load configuration
	cfg, path, err := loadConfig(*configPath)
	if err != nil {
		log.Error().Err(err).Msg("failed to load config")
		return 1
	}
	logCloser, err := applyLoggerConfig(cfg.Log)
	if err != nil {
		log.Error().Err(err).Msg("failed to configure logger")
		return 1
	}
	defer func() {
		if logCloser != nil {
			_ = logCloser.Close()
		}
	}()

	// Ensure directories exist
	if err := cfg.EnsureDirs(); err != nil {
		log.Error().Err(err).Msg("failed to create directories")
		return 1
	}

	authStateLock, err := acquireRuntimeAuthStateLock(cfg)
	if err != nil {
		log.Error().Err(err).Msg("failed to acquire authentication state lock")
		return 1
	}
	if authStateLock != nil {
		defer func() {
			if err := authStateLock.Close(); err != nil {
				log.Warn().Err(err).Msg("failed to release authentication state lock")
			}
		}()
	}
	if err := validateRuntimeAdminRecoveryState(cfg); err != nil {
		log.Error().Err(err).Msg("offline administrator recovery must be completed before startup")
		return 1
	}

	// Load or create secrets (for JWT, etc.)
	dataRoot := cfg.Storage.Root
	secrets, isNewSecrets, err := config.LoadOrCreateSecrets(dataRoot)
	if err != nil {
		log.Error().Err(err).Msg("failed to load secrets")
		return 1
	}

	// Use secrets for JWT if not configured
	applyStartupJWTSecret(cfg, secrets)

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
		log.Error().Err(err).Str("address", cfg.DataPlane.Address()).Msg("failed to connect to dataplane")
		return 1
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
		RetentionSweepInterval:  cfg.Storage.Retention.GCInterval,
		TrashEnabled:            &cfg.Storage.Trash.Enabled,
		TrashRetentionDays:      cfg.Storage.Trash.RetentionDays,
		MaxTrashSize:            cfg.Storage.Trash.MaxSize,
		Dataplane:               dataplaneClient,
	})
	if err != nil {
		log.Error().Err(err).Msg("failed to create filesystem")
		return 1
	}
	defer fs.Close()

	retentionMonitor := storage.NewRetentionMonitor(fs, storage.RetentionMonitorConfig{
		MaxVersions:   cfg.Storage.Retention.MaxVersions,
		MaxVersionAge: cfg.Storage.Retention.MaxAge,
		MinFreeSpace:  cfg.Storage.Retention.MinFreeSpace,
		SweepInterval: cfg.Storage.Retention.GCInterval,
	}, log.Logger)
	defer retentionMonitor.Stop()

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
	diskHealthMonitor := diskhealth.NewMonitor(diskHealthRuntimeConfig(cfg.DiskHealth), alertMonitor, log.Logger)
	defer diskHealthMonitor.Stop()

	var sharedUserStore *auth.UserStore
	if cfg.Auth.Enabled {
		userStore, _, err := auth.NewUserStore(cfg.Auth.UsersFile)
		if err != nil {
			if auth.IsPersistenceWarning(err) && userStore != nil {
				log.Warn().Err(err).Msg("user store initialized with an auth persistence warning")
			} else {
				log.Error().Err(err).Msg("failed to initialize user store")
				return 1
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
	quotaCoordinator := quotareservation.NewCoordinator()
	webdavRuntimeLockState := webdav.NewRuntimeLockState()
	defer webdavRuntimeLockState.Close()
	webdavPrefix, webdavHandler := buildWebDAVHandler(fs, activeWebDAV, cfg.Server, quotaCoordinator, webdavRuntimeLockState)
	runtimeWebDAV := newSwitchableWebDAVHandler(webdavPrefix, webdavHandler)

	apiServer, err := api.NewServer(log.Logger, &api.ServerConfig{
		DataplaneAddr:             cfg.DataPlane.Address(),
		FileSystem:                fs, // Pass the new storage filesystem
		AfterPathRenamed:          runtimeWebDAV.OnPathRenamed,
		AfterPathDeleted:          runtimeWebDAV.OnPathDeleted,
		ThumbnailRoot:             filepath.Join(cfg.InternalDir(), "thumbnails"),
		MaintenanceRoot:           filepath.Join(cfg.InternalDir(), "maintenance"),
		BackupRoot:                filepath.Join(cfg.InternalDir(), "backup"),
		ActivityRoot:              filepath.Join(cfg.InternalDir(), "activity"),
		UploadSessionRoot:         filepath.Join(cfg.InternalDir(), "upload-sessions"),
		UploadSessionMinFreeSpace: cfg.Storage.Retention.MinFreeSpace,
		StorageRoot:               cfg.Storage.Root,
		BackupJobs:                cfg.Backup.Jobs,
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
		Config:           cfg,
		ConfigPath:       path,
		AppVersion:       version,
		BuildTime:        buildTime,
		ActiveWebDAV:     &activeWebDAV,
		QuotaCoordinator: quotaCoordinator,
		UpdateWebDAV: func(runtimeCfg api.WebDAVRuntimeConfig) {
			prefix, handler := buildWebDAVHandler(fs, runtimeCfg, cfg.Server, quotaCoordinator, webdavRuntimeLockState)
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
		DeferBackgroundTasks: true,
	})
	if err != nil {
		log.Error().Err(err).Msg("failed to create API server")
		return 1
	}
	defer func() {
		if err := apiServer.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close API server")
		}
	}()

	trashRecovery, recoveryErr := fs.RecoverTrashDeletions(ctx)
	if trashRecovery.RolledBack > 0 || trashRecovery.RolledForward > 0 {
		log.Info().
			Int("rolled_back", trashRecovery.RolledBack).
			Int("rolled_forward", trashRecovery.RolledForward).
			Msg("recovered interrupted Trash deletions")
	}
	if len(trashRecovery.UntrackedPaths) > 0 {
		log.Warn().
			Strs("paths", trashRecovery.UntrackedPaths).
			Msg("untracked Trash deletion residue requires manual inspection")
	}
	if recoveryErr != nil {
		log.Error().
			Err(recoveryErr).
			Strs("operations", trashRecovery.Blocked).
			Msg("interrupted Trash deletion recovery is blocked; refusing to start writable services")
		return 1
	}

	transferRecovery, transferRecoveryErr := apiServer.RecoverTrashTransfers(ctx)
	if transferRecovery.RolledBack > 0 || transferRecovery.RolledForward > 0 || transferRecovery.Completed > 0 {
		log.Info().
			Int("rolled_back", transferRecovery.RolledBack).
			Int("rolled_forward", transferRecovery.RolledForward).
			Int("completed", transferRecovery.Completed).
			Msg("recovered interrupted Trash transfers")
	}
	if len(transferRecovery.UntrackedPaths) > 0 {
		log.Warn().
			Strs("paths", transferRecovery.UntrackedPaths).
			Msg("untracked Trash transfer residue requires manual inspection")
	}
	if transferRecoveryErr != nil {
		log.Error().
			Err(transferRecoveryErr).
			Strs("operations", transferRecovery.Blocked).
			Msg("interrupted Trash transfer recovery is blocked; refusing to start writable services")
		return 1
	}

	// Cleanup staging files only after transfer recovery has reconciled every
	// path that may still be owned by an interrupted operation.
	if cleanedFiles, cleanedBytes, cleanErr := fs.CleanupStaging(ctx); cleanErr != nil {
		if errors.Is(cleanErr, storage.ErrWriteRecoveryRequired) {
			log.Error().Err(cleanErr).Msg("interrupted file write requires recovery; refusing to start writable services")
			return 1
		}
		log.Warn().Err(cleanErr).Msg("failed to cleanup staging files")
	} else if cleanedFiles > 0 {
		log.Info().
			Int("files", cleanedFiles).
			Int64("bytes", cleanedBytes).
			Msg("cleaned up staging files from previous crash")
	}

	retentionMonitor.Start(ctx)
	alertMonitor.Start(ctx)
	if cfg.Alerts.Enabled {
		log.Info().
			Float64("threshold_pct", cfg.Alerts.ThresholdPct).
			Float64("critical_pct", cfg.Alerts.CriticalPct).
			Dur("interval", cfg.Alerts.CheckInterval).
			Msg("storage alerts enabled")
	}
	diskHealthMonitor.Start(ctx)
	if cfg.DiskHealth.Enabled {
		log.Info().
			Int("devices", len(cfg.DiskHealth.Devices)).
			Dur("interval", cfg.DiskHealth.CheckInterval).
			Msg("disk health monitoring enabled")
	}
	apiServer.StartBackgroundTasks(ctx)

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
			log.Error().Err(err).Msg("failed to configure TLS")
			return 1
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

	shutdownSignalContext, stopShutdownSignals := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stopShutdownSignals()

	var serve func() error
	if cfg.Server.TLS.Enabled {
		log.Info().Str("address", cfg.Address()).Msg("server started (HTTPS)")
		serve = func() error {
			return server.ListenAndServeTLS("", "")
		}
	} else {
		log.Info().Str("address", cfg.Address()).Msg("server started (HTTP)")
		serve = server.ListenAndServe
	}

	serverResult := runHTTPServerLifecycle(
		server,
		serve,
		shutdownSignalContext.Done(),
		httpServerShutdownTimeout,
		func() {
			log.Info().Msg("shutting down server...")
			if alertMonitor != nil {
				alertMonitor.Stop()
			}
		},
	)
	exitCode := 0
	if serverResult.ShutdownErr != nil {
		exitCode = 1
		log.Error().Err(serverResult.ShutdownErr).Msg("graceful server shutdown failed")
		if serverResult.CloseErr == nil {
			log.Warn().Msg("server connections forcibly closed after graceful shutdown failure")
		}
	}
	if serverResult.CloseErr != nil {
		exitCode = 1
		log.Error().Err(serverResult.CloseErr).Msg("failed to forcibly close server connections")
	}
	if serverResult.ServeErr != nil {
		exitCode = 1
		if serverResult.ShutdownRequested {
			log.Error().Err(serverResult.ServeErr).Msg("server exited abnormally during shutdown")
		} else {
			log.Error().Err(serverResult.ServeErr).Msg("server exited abnormally")
		}
	}

	log.Info().Msg("server stopped")
	return exitCode
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

func applyStartupJWTSecret(cfg *config.Config, secrets *config.Secrets) {
	if strings.TrimSpace(cfg.Auth.JWTSecret) == "" {
		cfg.Auth.JWTSecret = secrets.JWTSecret
	}
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
