package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seanbao/mnemonas/internal/alerts"
	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/backup"
	"github.com/seanbao/mnemonas/internal/config"
	quotareservation "github.com/seanbao/mnemonas/internal/quota"
	"github.com/seanbao/mnemonas/internal/storage"
)

const (
	quotaExceededAlertType = "quota_exceeded"
	quotaTypeUser          = "user"
	quotaTypeDirectory     = "directory"

	directoryQuotaStatusNormal   = "normal"
	directoryQuotaStatusWarning  = "warning"
	directoryQuotaStatusExceeded = "exceeded"
	directoryQuotaStatusMissing  = "missing"

	directoryQuotaWarningUsageRatio = 0.9
)

func pathWithinBase(basePath, targetPath string) bool {
	basePath = path.Clean(basePath)
	targetPath = path.Clean(targetPath)
	if basePath == "/" {
		return strings.HasPrefix(targetPath, "/")
	}
	return targetPath == basePath || strings.HasPrefix(targetPath, basePath+"/")
}

func (s *Server) currentUserHomeDir(ctx context.Context) (string, bool, error) {
	if !s.authEnabled || auth.IsAdmin(ctx) {
		return "", false, nil
	}

	user := auth.GetUserFromContext(ctx)
	if user == nil {
		return "", true, errPathAccessDenied
	}
	if user.Disabled {
		return "", true, errPathAccessDenied
	}
	if strings.TrimSpace(user.HomeDir) == "" {
		return "", true, errPathOutsideHomeDir
	}

	homeDir, err := validatePath(user.HomeDir)
	if err != nil {
		return "", true, errPathOutsideHomeDir
	}

	return homeDir, true, nil
}

func (s *Server) currentUserQuota(ctx context.Context) (homeDir string, quotaBytes int64, scoped bool, err error) {
	homeDir, homeScoped, err := s.currentUserHomeDir(ctx)
	if err != nil {
		return "", 0, false, err
	}
	if !homeScoped {
		return "", 0, false, nil
	}

	user := auth.GetUserFromContext(ctx)
	if user == nil || user.QuotaBytes <= 0 {
		return "", 0, false, nil
	}

	return homeDir, user.QuotaBytes, true, nil
}

type userQuotaRule struct {
	HomeDir    string
	QuotaBytes int64
}

func (s *Server) userQuotaRulesForTarget(targetPath string) ([]userQuotaRule, error) {
	if s.userStore == nil {
		return nil, nil
	}

	targetPath, err := validatePath(targetPath)
	if err != nil {
		return nil, err
	}

	quotasByHomeDir := make(map[string]int64)
	for _, user := range s.userStore.List() {
		if user == nil || user.Role == auth.RoleAdmin || user.QuotaBytes <= 0 {
			continue
		}
		homeDir, err := validatePath(user.HomeDir)
		if err != nil {
			return nil, err
		}
		if !pathWithinBase(homeDir, targetPath) {
			continue
		}
		if existing, ok := quotasByHomeDir[homeDir]; !ok || user.QuotaBytes < existing {
			quotasByHomeDir[homeDir] = user.QuotaBytes
		}
	}

	homeDirs := make([]string, 0, len(quotasByHomeDir))
	for homeDir := range quotasByHomeDir {
		homeDirs = append(homeDirs, homeDir)
	}
	sort.Strings(homeDirs)

	rules := make([]userQuotaRule, 0, len(homeDirs))
	for _, homeDir := range homeDirs {
		rules = append(rules, userQuotaRule{
			HomeDir:    homeDir,
			QuotaBytes: quotasByHomeDir[homeDir],
		})
	}
	return rules, nil
}

type quotaCheck struct {
	QuotaType     string
	QuotaPath     string
	UsedBytes     int64
	QuotaBytes    int64
	RequiredBytes int64
}

type directoryQuotaUsageStat struct {
	Path           string  `json:"path"`
	QuotaBytes     int64   `json:"quota_bytes"`
	UsedBytes      int64   `json:"used_bytes"`
	AvailableBytes int64   `json:"available_bytes"`
	UsageRatio     float64 `json:"usage_ratio"`
	Exists         bool    `json:"exists"`
	Status         string  `json:"status"`
}

func (s *Server) directoryQuotaRules() []config.DirectoryQuotaConfig {
	cfg := s.currentConfig()
	if cfg == nil || len(cfg.Storage.DirectoryQuotas) == 0 {
		return nil
	}
	rules := make([]config.DirectoryQuotaConfig, 0, len(cfg.Storage.DirectoryQuotas))
	for _, rule := range cfg.Storage.DirectoryQuotas {
		rule.Path = cleanRuntimePathRulePath(rule.Path)
		if rule.Path == "" {
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

func directoryQuotaRulesForTarget(rules []config.DirectoryQuotaConfig, targetPath string) []config.DirectoryQuotaConfig {
	if len(rules) == 0 {
		return nil
	}
	targetPath = cleanRuntimePathRulePath(targetPath)
	if targetPath == "" {
		return nil
	}
	matched := make([]config.DirectoryQuotaConfig, 0, len(rules))
	for _, rule := range rules {
		rule.Path = cleanRuntimePathRulePath(rule.Path)
		if rule.Path == "" || rule.QuotaBytes <= 0 {
			continue
		}
		if pathWithinBase(rule.Path, targetPath) {
			matched = append(matched, rule)
		}
	}
	return matched
}

func hasDirectoryQuotaRules(rules []config.DirectoryQuotaConfig) bool {
	for _, rule := range rules {
		if cleanRuntimePathRulePath(rule.Path) != "" && rule.QuotaBytes > 0 {
			return true
		}
	}
	return false
}

func mappedTreePathForQuota(treeRoot, mappedRoot, quotaPath string) (string, bool) {
	treeRoot = cleanQuotaPath(treeRoot)
	mappedRoot = cleanQuotaPath(mappedRoot)
	quotaPath = cleanQuotaPath(quotaPath)
	if treeRoot == "" || mappedRoot == "" || quotaPath == "" {
		return "", false
	}

	if pathWithinBase(quotaPath, mappedRoot) {
		return treeRoot, true
	}
	if !pathWithinBase(mappedRoot, quotaPath) {
		return "", false
	}

	relativePath := ""
	if mappedRoot == "/" {
		relativePath = strings.TrimPrefix(quotaPath, "/")
	} else {
		relativePath = strings.TrimPrefix(quotaPath, mappedRoot)
		relativePath = strings.TrimPrefix(relativePath, "/")
	}
	if relativePath == "" {
		return treeRoot, true
	}
	return path.Clean(path.Join(treeRoot, relativePath)), true
}

func cleanQuotaPath(targetPath string) string {
	return cleanRuntimePathRulePath(targetPath)
}

func (s *Server) directoryQuotaUsageStats(ctx context.Context) ([]directoryQuotaUsageStat, error) {
	rules := s.directoryQuotaRules()
	stats := make([]directoryQuotaUsageStat, 0, len(rules))
	for _, rule := range rules {
		info, err := s.fs.Stat(ctx, rule.Path)
		if isStorageNotFound(err) {
			stats = append(stats, newDirectoryQuotaUsageStat(rule, 0, false))
			continue
		}
		if err != nil {
			return nil, err
		}
		usedBytes, err := s.fileInfoLogicalSize(ctx, info)
		if err != nil {
			return nil, err
		}
		stats = append(stats, newDirectoryQuotaUsageStat(rule, usedBytes, true))
	}
	return stats, nil
}

func newDirectoryQuotaUsageStat(rule config.DirectoryQuotaConfig, usedBytes int64, exists bool) directoryQuotaUsageStat {
	usedBytes = nonNegativeSize(usedBytes)
	quotaBytes := nonNegativeSize(rule.QuotaBytes)
	availableBytes := quotaBytes - usedBytes
	if availableBytes < 0 {
		availableBytes = 0
	}

	usageRatio := float64(0)
	if quotaBytes > 0 {
		usageRatio = float64(usedBytes) / float64(quotaBytes)
	}

	status := directoryQuotaStatusNormal
	if !exists {
		status = directoryQuotaStatusMissing
	} else if quotaBytes > 0 && usedBytes >= quotaBytes {
		status = directoryQuotaStatusExceeded
	} else if usageRatio >= directoryQuotaWarningUsageRatio {
		status = directoryQuotaStatusWarning
	}

	return directoryQuotaUsageStat{
		Path:           rule.Path,
		QuotaBytes:     quotaBytes,
		UsedBytes:      usedBytes,
		AvailableBytes: availableBytes,
		UsageRatio:     usageRatio,
		Exists:         exists,
		Status:         status,
	}
}

func (s *Server) resolveUserUsedBytes(ctx context.Context, user *auth.User) (int64, error) {
	if s.fs == nil || user == nil || strings.TrimSpace(user.HomeDir) == "" {
		return 0, nil
	}
	homeDir, err := validatePath(user.HomeDir)
	if err != nil {
		return user.UsedBytes, err
	}
	return s.pathLogicalSizeIfExists(ctx, homeDir)
}

func (s *Server) pathLogicalSizeIfExists(ctx context.Context, targetPath string) (int64, error) {
	size, err := s.pathLogicalSize(ctx, targetPath)
	if isStorageNotFound(err) || errors.Is(err, storage.ErrNotDir) {
		return 0, nil
	}
	return size, err
}

func (s *Server) pathLogicalSize(ctx context.Context, targetPath string) (int64, error) {
	info, err := s.fs.Stat(ctx, targetPath)
	if err != nil {
		return 0, err
	}
	return s.fileInfoLogicalSize(ctx, info)
}

func (s *Server) authorizedPathLogicalSize(ctx context.Context, targetPath string) (int64, error) {
	if err := s.authorizeUserConcreteReadPath(ctx, targetPath); err != nil {
		return 0, err
	}
	info, err := s.fs.Stat(ctx, targetPath)
	if err != nil {
		return 0, err
	}
	return s.authorizedFileInfoLogicalSize(ctx, info)
}

func (s *Server) authorizedPathLogicalSizeIfExists(ctx context.Context, targetPath string) (int64, error) {
	size, err := s.authorizedPathLogicalSize(ctx, targetPath)
	if isStorageNotFound(err) || errors.Is(err, storage.ErrNotDir) {
		return 0, nil
	}
	return size, err
}

func (s *Server) authorizedFileInfoLogicalSize(ctx context.Context, info *storage.FileInfo) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if info == nil {
		return 0, nil
	}
	if s.quotaUsageScanVisit != nil {
		s.quotaUsageScanVisit(info.Path)
	}
	if err := s.authorizeUserConcreteReadPath(ctx, info.Path); err != nil {
		return 0, err
	}
	if !info.IsDir {
		return nonNegativeSize(info.Size), nil
	}

	children, err := s.fs.ReadDir(ctx, info.Path)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		childPath, _, err := apiReadDirChildPath(info.Path, child)
		if err != nil {
			return 0, err
		}
		childInfo := *child
		childInfo.Path = childPath
		size, err := s.authorizedFileInfoLogicalSize(ctx, &childInfo)
		if err != nil {
			return 0, err
		}
		total, err = addQuotaSize(total, size)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func (s *Server) fileInfoLogicalSize(ctx context.Context, info *storage.FileInfo) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if info == nil {
		return 0, nil
	}
	if s.quotaUsageScanVisit != nil {
		s.quotaUsageScanVisit(info.Path)
	}
	if !info.IsDir {
		return nonNegativeSize(info.Size), nil
	}

	children, err := s.fs.ReadDir(ctx, info.Path)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		childPath, _, err := apiReadDirChildPath(info.Path, child)
		if err != nil {
			return 0, err
		}
		childInfo := *child
		childInfo.Path = childPath
		size, err := s.fileInfoLogicalSize(ctx, &childInfo)
		if err != nil {
			return 0, err
		}
		total, err = addQuotaSize(total, size)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func addQuotaSize(left, right int64) (int64, error) {
	if right <= 0 {
		return left, nil
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	if left > maxInt64-right {
		return 0, errors.New("logical file size overflow")
	}
	return left + right, nil
}

func nonNegativeSize(size int64) int64 {
	if size < 0 {
		return 0
	}
	return size
}

func (s *Server) quotaReservations() *quotareservation.Coordinator {
	s.quotaCoordinatorOnce.Do(func() {
		if s.quotaCoordinator == nil {
			s.quotaCoordinator = quotareservation.NewCoordinator()
		}
	})
	return s.quotaCoordinator
}

func (s *Server) acquireQuotaMutationForCommit(ctx context.Context, operation string) (*quotareservation.MutationLease, error) {
	if s.beforeQuotaMutationCommit != nil {
		s.beforeQuotaMutationCommit(operation)
	}
	return s.quotaReservations().AcquireMutation(ctx)
}

func quotaCheckScopeKey(check quotaCheck) string {
	return quotareservation.ScopeKey(check.QuotaType, check.QuotaPath)
}

func quotaClaims(checks []quotaCheck) []quotareservation.Claim {
	claims := make([]quotareservation.Claim, 0, len(checks))
	for _, check := range checks {
		claims = append(claims, quotareservation.Claim{
			Key:           quotaCheckScopeKey(check),
			UsedBytes:     check.UsedBytes,
			LimitBytes:    check.QuotaBytes,
			RequiredBytes: check.RequiredBytes,
		})
	}
	return claims
}

func (s *Server) reserveQuotaChecks(
	ctx context.Context,
	prepare func(context.Context, quotareservation.View) ([]quotaCheck, error),
) (*quotareservation.Reservation, error) {
	var checks []quotaCheck
	reservation, err := s.quotaReservations().Reserve(ctx, func(ctx context.Context, view quotareservation.View) ([]quotareservation.Claim, error) {
		var prepareErr error
		checks, prepareErr = prepare(ctx, view)
		if prepareErr != nil {
			return nil, prepareErr
		}
		return quotaClaims(checks), nil
	})
	if err == nil {
		return reservation, nil
	}
	return nil, quotaReservationError(checks, err)
}

func (s *Server) refreshQuotaChecks(
	ctx context.Context,
	mutation *quotareservation.MutationLease,
	reservation *quotareservation.Reservation,
	prepare func(context.Context, quotareservation.View) ([]quotaCheck, error),
) error {
	var checks []quotaCheck
	err := mutation.Refresh(ctx, reservation, func(ctx context.Context, view quotareservation.View) ([]quotareservation.Claim, error) {
		var prepareErr error
		checks, prepareErr = prepare(ctx, view)
		if prepareErr != nil {
			return nil, prepareErr
		}
		return quotaClaims(checks), nil
	})
	return quotaReservationError(checks, err)
}

func quotaReservationError(checks []quotaCheck, err error) error {
	if err == nil {
		return nil
	}
	var exceeded *quotareservation.ExceededError
	if !errors.As(err, &exceeded) || exceeded.ClaimIndex < 0 || exceeded.ClaimIndex >= len(checks) {
		return err
	}
	check := checks[exceeded.ClaimIndex]
	effectiveUsed := exceeded.UsedBytes
	if effectiveUsed > int64(^uint64(0)>>1)-exceeded.ReservedBytes {
		effectiveUsed = int64(^uint64(0) >> 1)
	} else {
		effectiveUsed += exceeded.ReservedBytes
	}
	return newQuotaExceededErrorFor(
		check.QuotaType,
		check.QuotaPath,
		effectiveUsed,
		exceeded.LimitBytes,
		exceeded.RequiredBytes,
		exceeded.AvailableBytes,
	)
}

const quotaUploadGrowthChunk int64 = 64 * 1024 * 1024

func (s *Server) quotaCheckedUploadReader(
	ctx context.Context,
	targetPath string,
	reader io.Reader,
	contentLength int64,
) (io.Reader, storage.WriteFileCondition, func(), error) {
	homeDir, quotaBytes, quotaScoped, err := s.currentUserQuota(ctx)
	if err != nil {
		return nil, storage.WriteFileCondition{}, nil, err
	}
	userQuotaApplies := quotaScoped && quotaBytes > 0 && pathWithinBase(homeDir, targetPath)
	directoryRules := directoryQuotaRulesForTarget(s.directoryQuotaRules(), targetPath)
	quotaApplies := userQuotaApplies || len(directoryRules) > 0

	var condition storage.WriteFileCondition
	var replacedBytes int64
	var initialChecks []quotaCheck
	requiredBytes := int64(0)
	reservation, err := s.reserveQuotaChecks(ctx, func(ctx context.Context, _ quotareservation.View) ([]quotaCheck, error) {
		var snapshotErr error
		condition, replacedBytes, snapshotErr = s.uploadTargetSnapshot(ctx, targetPath)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		if contentLength >= 0 && contentLength > replacedBytes {
			requiredBytes = contentLength - replacedBytes
		}
		initialChecks, snapshotErr = s.currentUploadQuotaChecks(
			ctx,
			homeDir,
			quotaBytes,
			userQuotaApplies,
			directoryRules,
			requiredBytes,
		)
		return initialChecks, snapshotErr
	})
	if err != nil {
		return nil, storage.WriteFileCondition{}, nil, err
	}
	reservations := []*quotareservation.Reservation{reservation}
	releaseReservations := func() {
		for index := len(reservations) - 1; index >= 0; index-- {
			reservations[index].Release()
		}
	}
	if !quotaApplies {
		return reader, condition, releaseReservations, nil
	}

	if contentLength >= 0 {
		return &quotaLimitedReader{
			reader:    reader,
			remaining: contentLength,
			err:       uploadQuotaLimitError(initialChecks),
		}, condition, releaseReservations, nil
	}

	allowedBytes := replacedBytes
	if allowedBytes > DefaultMaxUploadSize {
		allowedBytes = DefaultMaxUploadSize
	}
	grow := func() (int64, error) {
		if allowedBytes >= DefaultMaxUploadSize {
			return 0, &quotaGrowthLimitError{err: storage.ErrFileTooLarge}
		}
		requestedBytes := quotaUploadGrowthChunk
		if remaining := DefaultMaxUploadSize - allowedBytes; requestedBytes > remaining {
			requestedBytes = remaining
		}

		grantedBytes := requestedBytes
		var limitingCheck *quotaCheck
		nextReservation, reserveErr := s.reserveQuotaChecks(ctx, func(ctx context.Context, view quotareservation.View) ([]quotaCheck, error) {
			if err := s.validateUploadTargetSnapshot(ctx, targetPath, condition); err != nil {
				return nil, err
			}
			checks, err := s.currentUploadQuotaChecks(ctx, homeDir, quotaBytes, userQuotaApplies, directoryRules, 0)
			if err != nil {
				return nil, err
			}
			for index := range checks {
				availableBytes := quotaCheckAvailableWithReservations(checks[index], view)
				if availableBytes < grantedBytes {
					grantedBytes = availableBytes
					limitingCheck = &checks[index]
				}
			}
			if grantedBytes <= 0 {
				if limitingCheck == nil && len(checks) > 0 {
					limitingCheck = &checks[0]
				}
				if limitingCheck == nil {
					return nil, storage.ErrFileTooLarge
				}
				return nil, quotaCheckExceededWithReservations(*limitingCheck, view)
			}
			for index := range checks {
				checks[index].RequiredBytes = grantedBytes
			}
			return checks, nil
		})
		if reserveErr != nil {
			var quotaErr *quotaExceededError
			if errors.As(reserveErr, &quotaErr) {
				return 0, &quotaGrowthLimitError{err: reserveErr}
			}
			return 0, reserveErr
		}
		reservations = append(reservations, nextReservation)
		allowedBytes += grantedBytes
		return grantedBytes, nil
	}

	return &quotaLimitedReader{
		reader:    reader,
		remaining: allowedBytes,
		err:       uploadQuotaLimitError(initialChecks),
		grow:      grow,
	}, condition, releaseReservations, nil
}

func (s *Server) uploadTargetSnapshot(ctx context.Context, targetPath string) (storage.WriteFileCondition, int64, error) {
	info, err := s.fs.Stat(ctx, targetPath)
	if err != nil {
		if isStorageNotFound(err) {
			return storage.WriteFileCondition{ExpectedExists: false}, 0, nil
		}
		return storage.WriteFileCondition{}, 0, err
	}
	if info.IsDir {
		return storage.WriteFileCondition{}, 0, storage.ErrIsDir
	}
	return storage.WriteFileCondition{
		ExpectedExists:      true,
		DeleteIdentityToken: info.DeleteIdentityToken,
	}, nonNegativeSize(info.Size), nil
}

func (s *Server) validateUploadTargetSnapshot(ctx context.Context, targetPath string, condition storage.WriteFileCondition) error {
	info, err := s.fs.Stat(ctx, targetPath)
	if err != nil {
		if isStorageNotFound(err) && !condition.ExpectedExists {
			return nil
		}
		if isStorageNotFound(err) {
			return storage.ErrWriteConflict
		}
		return err
	}
	if !condition.ExpectedExists || info.IsDir || info.DeleteIdentityToken != condition.DeleteIdentityToken {
		return storage.ErrWriteConflict
	}
	return nil
}

func (s *Server) currentUploadQuotaChecks(
	ctx context.Context,
	homeDir string,
	quotaBytes int64,
	userQuotaApplies bool,
	directoryRules []config.DirectoryQuotaConfig,
	requiredBytes int64,
) ([]quotaCheck, error) {
	checks := make([]quotaCheck, 0, 1+len(directoryRules))
	if userQuotaApplies {
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
		if err != nil {
			return nil, err
		}
		checks = append(checks, quotaCheck{
			QuotaType:     quotaTypeUser,
			QuotaPath:     homeDir,
			UsedBytes:     usedBytes,
			QuotaBytes:    quotaBytes,
			RequiredBytes: requiredBytes,
		})
	}
	for _, rule := range directoryRules {
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, rule.Path)
		if err != nil {
			return nil, err
		}
		checks = append(checks, quotaCheck{
			QuotaType:     quotaTypeDirectory,
			QuotaPath:     rule.Path,
			UsedBytes:     usedBytes,
			QuotaBytes:    rule.QuotaBytes,
			RequiredBytes: requiredBytes,
		})
	}
	return checks, nil
}

func quotaCheckAvailableWithReservations(check quotaCheck, view quotareservation.View) int64 {
	usedBytes := nonNegativeSize(check.UsedBytes)
	quotaBytes := nonNegativeSize(check.QuotaBytes)
	reservedBytes := view.ReservedBytes(quotaCheckScopeKey(check))
	if usedBytes >= quotaBytes {
		return 0
	}
	availableBytes := quotaBytes - usedBytes
	if reservedBytes >= availableBytes {
		return 0
	}
	return availableBytes - reservedBytes
}

func quotaCheckExceededWithReservations(check quotaCheck, view quotareservation.View) error {
	reservedBytes := view.ReservedBytes(quotaCheckScopeKey(check))
	effectiveUsed := nonNegativeSize(check.UsedBytes)
	if effectiveUsed > int64(^uint64(0)>>1)-reservedBytes {
		effectiveUsed = int64(^uint64(0) >> 1)
	} else {
		effectiveUsed += reservedBytes
	}
	availableBytes := quotaCheckAvailableWithReservations(check, view)
	return newQuotaExceededErrorFor(
		check.QuotaType,
		check.QuotaPath,
		effectiveUsed,
		check.QuotaBytes,
		quotaStreamRequiredBytes(availableBytes),
		availableBytes,
	)
}

func uploadQuotaLimitError(checks []quotaCheck) error {
	if len(checks) == 0 {
		return storage.ErrFileTooLarge
	}
	check := checks[0]
	return newQuotaExceededErrorFor(
		check.QuotaType,
		check.QuotaPath,
		check.QuotaBytes,
		check.QuotaBytes,
		quotaStreamRequiredBytes(0),
		0,
	)
}

func (s *Server) copyResourceWithQuota(ctx context.Context, srcPath, dstPath string) error {
	homeDir, quotaBytes, quotaScoped, err := s.currentUserQuota(ctx)
	if err != nil {
		return err
	}
	userQuotaApplies := quotaScoped && quotaBytes > 0 && pathWithinBase(homeDir, dstPath)
	directoryRules := s.directoryQuotaRules()
	hasDirectoryQuotas := hasDirectoryQuotaRules(directoryRules)
	prepare := func(ctx context.Context, _ quotareservation.View) ([]quotaCheck, error) {
		checks := make([]quotaCheck, 0, 1+len(directoryRules))
		if userQuotaApplies {
			requiredBytes, err := s.authorizedPathLogicalSize(ctx, srcPath)
			if err != nil {
				return nil, err
			}
			usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
			if err != nil {
				return nil, err
			}
			checks = append(checks, quotaCheck{
				QuotaType:     quotaTypeUser,
				QuotaPath:     homeDir,
				UsedBytes:     usedBytes,
				QuotaBytes:    quotaBytes,
				RequiredBytes: requiredBytes,
			})
		}
		if hasDirectoryQuotas {
			directoryChecks, err := s.copyDirectoryQuotaChecks(ctx, srcPath, dstPath, directoryRules)
			if err != nil {
				return nil, err
			}
			checks = append(checks, directoryChecks...)
		}
		return checks, nil
	}

	var reservation *quotareservation.Reservation
	if userQuotaApplies || hasDirectoryQuotas {
		reservation, err = s.reserveQuotaChecks(ctx, prepare)
		if err != nil {
			return err
		}
	}

	mutation, err := s.acquireQuotaMutationForCommit(ctx, "copy")
	if err != nil {
		if reservation != nil {
			reservation.Release()
		}
		return err
	}
	defer func() {
		if reservation != nil {
			reservation.Release()
		}
		mutation.Release()
	}()
	if reservation != nil {
		if err := s.refreshQuotaChecks(ctx, mutation, reservation, prepare); err != nil {
			return err
		}
	}

	err = s.copyResource(ctx, srcPath, dstPath)
	if reservation != nil {
		reservation.Release()
	}
	mutation.Release()
	return err
}

func (s *Server) copyDirectoryQuotaChecks(ctx context.Context, srcPath, dstPath string, rules []config.DirectoryQuotaConfig) ([]quotaCheck, error) {
	checks := make([]quotaCheck, 0, len(rules))
	for _, rule := range rules {
		rule.Path = cleanRuntimePathRulePath(rule.Path)
		if rule.Path == "" || rule.QuotaBytes <= 0 {
			continue
		}
		requiredBytes, err := s.copyDirectoryQuotaRequiredBytes(ctx, srcPath, dstPath, rule.Path)
		if err != nil {
			return nil, err
		}
		if requiredBytes <= 0 {
			continue
		}
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, rule.Path)
		if err != nil {
			return nil, err
		}
		checks = append(checks, quotaCheck{
			QuotaType:     quotaTypeDirectory,
			QuotaPath:     rule.Path,
			UsedBytes:     usedBytes,
			QuotaBytes:    rule.QuotaBytes,
			RequiredBytes: requiredBytes,
		})
	}
	return checks, nil
}

func (s *Server) copyDirectoryQuotaRequiredBytes(ctx context.Context, srcPath, dstPath, quotaPath string) (int64, error) {
	if pathWithinBase(quotaPath, dstPath) {
		return s.authorizedPathLogicalSize(ctx, srcPath)
	}
	mappedSourcePath, ok := mappedTreePathForQuota(srcPath, dstPath, quotaPath)
	if !ok {
		return 0, nil
	}
	return s.authorizedPathLogicalSizeIfExists(ctx, mappedSourcePath)
}

func (s *Server) restoreFromTrashWithQuota(ctx context.Context, item *storage.TrashItem, targetPath string, restore func() error) error {
	homeDir, quotaBytes, quotaScoped, err := s.currentUserQuota(ctx)
	if err != nil {
		return err
	}
	if targetPath == "" && item != nil {
		targetPath = item.OriginalPath
	}
	userQuotaApplies := quotaScoped && quotaBytes > 0 && pathWithinBase(homeDir, targetPath)
	directoryRules := s.directoryQuotaRules()
	hasDirectoryQuotas := hasDirectoryQuotaRules(directoryRules)

	requiredBytes := int64(0)
	if item != nil {
		requiredBytes = nonNegativeSize(item.Size)
	}
	prepare := func(ctx context.Context, _ quotareservation.View) ([]quotaCheck, error) {
		checks := make([]quotaCheck, 0, 1+len(directoryRules))
		if userQuotaApplies {
			usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
			if err != nil {
				return nil, err
			}
			checks = append(checks, quotaCheck{
				QuotaType:     quotaTypeUser,
				QuotaPath:     homeDir,
				UsedBytes:     usedBytes,
				QuotaBytes:    quotaBytes,
				RequiredBytes: requiredBytes,
			})
		}
		if hasDirectoryQuotas {
			directoryChecks, err := s.trashRestoreDirectoryQuotaChecks(ctx, item, targetPath, directoryRules)
			if err != nil {
				return nil, err
			}
			checks = append(checks, directoryChecks...)
		}
		return checks, nil
	}

	var reservation *quotareservation.Reservation
	if userQuotaApplies || hasDirectoryQuotas {
		reservation, err = s.reserveQuotaChecks(ctx, prepare)
		if err != nil {
			return err
		}
	}

	mutation, err := s.acquireQuotaMutationForCommit(ctx, "trash_restore")
	if err != nil {
		if reservation != nil {
			reservation.Release()
		}
		return err
	}
	defer func() {
		if reservation != nil {
			reservation.Release()
		}
		mutation.Release()
	}()
	if reservation != nil {
		if err := s.refreshQuotaChecks(ctx, mutation, reservation, prepare); err != nil {
			return err
		}
	}

	err = restore()
	if reservation != nil {
		reservation.Release()
	}
	mutation.Release()
	return err
}

func (s *Server) trashRestoreDirectoryQuotaChecks(ctx context.Context, item *storage.TrashItem, targetPath string, rules []config.DirectoryQuotaConfig) ([]quotaCheck, error) {
	if item == nil || s.fs == nil {
		return nil, nil
	}

	requiredByRule := make(map[string]int64, len(rules))
	rulesByPath := make(map[string]config.DirectoryQuotaConfig, len(rules))
	for _, rule := range rules {
		rule.Path = cleanRuntimePathRulePath(rule.Path)
		if rule.Path == "" || rule.QuotaBytes <= 0 {
			continue
		}
		requiredByRule[rule.Path] = 0
		rulesByPath[rule.Path] = rule
	}
	if len(rulesByPath) == 0 {
		return nil, nil
	}

	sourceRoot := cleanQuotaPath(item.OriginalPath)
	targetRoot := cleanQuotaPath(targetPath)
	if targetRoot == "" {
		targetRoot = sourceRoot
	}
	if sourceRoot == "" {
		return nil, nil
	}

	if err := s.fs.WalkTrashItemRestorePaths(ctx, item.ID, func(restoredPath string, isDir bool, size int64) error {
		if isDir {
			return nil
		}
		mappedPath, ok := mapDescendantPath(sourceRoot, targetRoot, cleanQuotaPath(restoredPath))
		if !ok {
			return errPathAccessDenied
		}
		for quotaPath := range rulesByPath {
			if pathWithinBase(quotaPath, mappedPath) {
				total, err := addQuotaSize(requiredByRule[quotaPath], nonNegativeSize(size))
				if err != nil {
					return err
				}
				requiredByRule[quotaPath] = total
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	checks := make([]quotaCheck, 0, len(rulesByPath))
	for quotaPath, requiredBytes := range requiredByRule {
		if requiredBytes <= 0 {
			continue
		}
		rule := rulesByPath[quotaPath]
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, quotaPath)
		if err != nil {
			return nil, err
		}
		checks = append(checks, quotaCheck{
			QuotaType:     quotaTypeDirectory,
			QuotaPath:     quotaPath,
			UsedBytes:     usedBytes,
			QuotaBytes:    rule.QuotaBytes,
			RequiredBytes: requiredBytes,
		})
	}
	return checks, nil
}

func (s *Server) moveResourceWithQuota(ctx context.Context, srcPath, dstPath string) error {
	homeDir, quotaBytes, quotaScoped, err := s.currentUserQuota(ctx)
	if err != nil {
		return err
	}
	userQuotaApplies := quotaScoped && quotaBytes > 0 && pathWithinBase(homeDir, dstPath) && !pathWithinBase(homeDir, srcPath)
	directoryRules := s.directoryQuotaRules()
	hasDirectoryQuotas := hasDirectoryQuotaRules(directoryRules)
	prepare := func(ctx context.Context, _ quotareservation.View) ([]quotaCheck, error) {
		checks := make([]quotaCheck, 0, 1+len(directoryRules))
		if userQuotaApplies {
			requiredBytes, err := s.pathLogicalSize(ctx, srcPath)
			if err != nil {
				return nil, err
			}
			usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
			if err != nil {
				return nil, err
			}
			checks = append(checks, quotaCheck{
				QuotaType:     quotaTypeUser,
				QuotaPath:     homeDir,
				UsedBytes:     usedBytes,
				QuotaBytes:    quotaBytes,
				RequiredBytes: requiredBytes,
			})
		}
		if hasDirectoryQuotas {
			directoryChecks, err := s.moveDirectoryQuotaChecks(ctx, srcPath, dstPath, directoryRules)
			if err != nil {
				return nil, err
			}
			checks = append(checks, directoryChecks...)
		}
		return checks, nil
	}

	var reservation *quotareservation.Reservation
	if userQuotaApplies || hasDirectoryQuotas {
		reservation, err = s.reserveQuotaChecks(ctx, prepare)
		if err != nil {
			return err
		}
	}

	mutation, err := s.acquireQuotaMutationForCommit(ctx, "move")
	if err != nil {
		if reservation != nil {
			reservation.Release()
		}
		return err
	}
	defer func() {
		if reservation != nil {
			reservation.Release()
		}
		mutation.Release()
	}()
	if reservation != nil {
		if err := s.refreshQuotaChecks(ctx, mutation, reservation, prepare); err != nil {
			return err
		}
	}

	err = s.fs.Rename(ctx, srcPath, dstPath)
	if reservation != nil {
		reservation.Release()
	}
	mutation.Release()
	return err
}

func (s *Server) moveDirectoryQuotaChecks(ctx context.Context, srcPath, dstPath string, rules []config.DirectoryQuotaConfig) ([]quotaCheck, error) {
	checks := make([]quotaCheck, 0, len(rules))
	for _, rule := range rules {
		rule.Path = cleanRuntimePathRulePath(rule.Path)
		if rule.Path == "" || rule.QuotaBytes <= 0 {
			continue
		}

		destinationBytes, err := s.mappedTreeLogicalSizeIfExists(ctx, srcPath, dstPath, rule.Path)
		if err != nil {
			return nil, err
		}
		sourceBytes, err := s.mappedTreeLogicalSizeIfExists(ctx, srcPath, srcPath, rule.Path)
		if err != nil {
			return nil, err
		}
		if destinationBytes <= sourceBytes {
			continue
		}
		requiredBytes := destinationBytes - sourceBytes
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, rule.Path)
		if err != nil {
			return nil, err
		}
		checks = append(checks, quotaCheck{
			QuotaType:     quotaTypeDirectory,
			QuotaPath:     rule.Path,
			UsedBytes:     usedBytes,
			QuotaBytes:    rule.QuotaBytes,
			RequiredBytes: requiredBytes,
		})
	}
	return checks, nil
}

func (s *Server) mappedTreeLogicalSizeIfExists(ctx context.Context, treeRoot, mappedRoot, quotaPath string) (int64, error) {
	sourcePath, ok := mappedTreePathForQuota(treeRoot, mappedRoot, quotaPath)
	if !ok {
		return 0, nil
	}
	return s.pathLogicalSizeIfExists(ctx, sourcePath)
}

func (s *Server) restoreVersionWithQuota(ctx context.Context, filePath, hash string) error {
	userRules, err := s.userQuotaRulesForTarget(filePath)
	if err != nil {
		return err
	}
	directoryRules := directoryQuotaRulesForTarget(s.directoryQuotaRules(), filePath)
	prepare := func(ctx context.Context, _ quotareservation.View) ([]quotaCheck, error) {
		currentInfo, err := s.fs.Stat(ctx, filePath)
		if err != nil {
			return nil, err
		}
		if currentInfo.IsDir {
			return nil, storage.ErrIsDir
		}
		currentSize := nonNegativeSize(currentInfo.Size)

		versions, err := s.fs.ListVersions(ctx, filePath)
		if err != nil {
			return nil, err
		}
		restoredSize := int64(-1)
		for _, version := range versions {
			if strings.EqualFold(version.Hash, hash) {
				restoredSize = nonNegativeSize(version.Size)
				break
			}
		}
		if restoredSize < 0 {
			return nil, storage.ErrVersionNotFound
		}
		requiredBytes := int64(0)
		if restoredSize > currentSize {
			requiredBytes = restoredSize - currentSize
		}

		checks := make([]quotaCheck, 0, len(userRules)+len(directoryRules))
		for _, rule := range userRules {
			usedBytes, err := s.pathLogicalSizeIfExists(ctx, rule.HomeDir)
			if err != nil {
				return nil, err
			}
			checks = append(checks, quotaCheck{
				QuotaType:     quotaTypeUser,
				QuotaPath:     rule.HomeDir,
				UsedBytes:     usedBytes,
				QuotaBytes:    rule.QuotaBytes,
				RequiredBytes: requiredBytes,
			})
		}
		for _, rule := range directoryRules {
			usedBytes, err := s.pathLogicalSizeIfExists(ctx, rule.Path)
			if err != nil {
				return nil, err
			}
			checks = append(checks, quotaCheck{
				QuotaType:     quotaTypeDirectory,
				QuotaPath:     rule.Path,
				UsedBytes:     usedBytes,
				QuotaBytes:    rule.QuotaBytes,
				RequiredBytes: requiredBytes,
			})
		}
		return checks, nil
	}

	var reservation *quotareservation.Reservation
	if len(userRules) > 0 || len(directoryRules) > 0 {
		reservation, err = s.reserveQuotaChecks(ctx, prepare)
		if err != nil {
			return err
		}
	}

	mutation, err := s.acquireQuotaMutationForCommit(ctx, "version_restore")
	if err != nil {
		if reservation != nil {
			reservation.Release()
		}
		return err
	}
	defer func() {
		if reservation != nil {
			reservation.Release()
		}
		mutation.Release()
	}()
	if reservation != nil {
		if err := s.refreshQuotaChecks(ctx, mutation, reservation, prepare); err != nil {
			return err
		}
	}

	err = s.fs.RestoreVersion(ctx, filePath, hash)
	if reservation != nil {
		reservation.Release()
	}
	mutation.Release()
	return err
}

func newQuotaExceededErrorFor(quotaType, quotaPath string, usedBytes, quotaBytes, requiredBytes, availableBytes int64) *quotaExceededError {
	if usedBytes < 0 {
		usedBytes = 0
	}
	if quotaBytes < 0 {
		quotaBytes = 0
	}
	if requiredBytes < 0 {
		requiredBytes = 0
	}
	if availableBytes < 0 {
		availableBytes = 0
	}
	return &quotaExceededError{
		QuotaType:      quotaType,
		QuotaPath:      quotaPath,
		UsedBytes:      usedBytes,
		QuotaBytes:     quotaBytes,
		RequiredBytes:  requiredBytes,
		AvailableBytes: availableBytes,
	}
}

func quotaStreamRequiredBytes(availableBytes int64) int64 {
	const maxInt64 = int64(^uint64(0) >> 1)
	if availableBytes >= maxInt64 {
		return maxInt64
	}
	return availableBytes + 1
}

type quotaLimitedReader struct {
	reader    io.Reader
	remaining int64
	err       error
	grow      func() (int64, error)
}

type quotaMutationCommitReader struct {
	ctx         context.Context
	reader      io.Reader
	acquire     func(context.Context) (*quotareservation.MutationLease, error)
	acquireOnce sync.Once
	acquireErr  error
	mutation    *quotareservation.MutationLease
	releaseOnce sync.Once
}

func newQuotaMutationCommitReader(
	ctx context.Context,
	reader io.Reader,
	acquire func(context.Context) (*quotareservation.MutationLease, error),
) *quotaMutationCommitReader {
	return &quotaMutationCommitReader{
		ctx:     ctx,
		reader:  reader,
		acquire: acquire,
	}
}

func (r *quotaMutationCommitReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if !errors.Is(err, io.EOF) {
		return n, err
	}
	r.acquireOnce.Do(func() {
		// The admission-time reservation remains authoritative for this upload.
		// Cooperative commits account for it, while quota updates apply to later requests.
		if r.acquire == nil {
			r.acquireErr = errors.New("quota mutation acquire callback is unavailable")
			return
		}
		r.mutation, r.acquireErr = r.acquire(r.ctx)
	})
	if r.acquireErr != nil {
		return n, r.acquireErr
	}
	return n, err
}

func (r *quotaMutationCommitReader) Release() {
	r.releaseOnce.Do(func() {
		if r.mutation != nil {
			r.mutation.Release()
		}
	})
}

type quotaGrowthLimitError struct {
	err error
}

func (e *quotaGrowthLimitError) Error() string {
	if e == nil || e.err == nil {
		return "upload limit reached"
	}
	return e.err.Error()
}

func (e *quotaGrowthLimitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (r *quotaLimitedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining <= 0 {
		if r.grow != nil {
			grownBytes, err := r.grow()
			if err != nil {
				var limitErr *quotaGrowthLimitError
				if !errors.As(err, &limitErr) {
					return 0, err
				}
				r.err = limitErr.err
			}
			if grownBytes > 0 {
				r.remaining = grownBytes
			} else if err == nil {
				return 0, io.ErrNoProgress
			}
		}
	}
	if r.remaining <= 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, r.err
		}
		if err != nil {
			return 0, err
		}
		return 0, io.ErrNoProgress
	}

	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

type quotaExceededError struct {
	QuotaType      string `json:"quota_type,omitempty"`
	QuotaPath      string `json:"quota_path,omitempty"`
	UsedBytes      int64  `json:"used_bytes"`
	QuotaBytes     int64  `json:"quota_bytes"`
	RequiredBytes  int64  `json:"required_bytes"`
	AvailableBytes int64  `json:"available_bytes"`
}

func (e *quotaExceededError) Error() string {
	if e != nil && e.QuotaType == quotaTypeDirectory {
		return "directory quota exceeded"
	}
	return "user quota exceeded"
}

func respondQuotaExceeded(w http.ResponseWriter, err error) {
	details := any(nil)
	var quotaErr *quotaExceededError
	if errors.As(err, &quotaErr) {
		details = quotaExceededDetailsForAPI(quotaErr)
	}
	apiErr := NewAPIError(ErrCodeQuotaExceeded, quotaExceededMessage(quotaErr))
	if details != nil {
		apiErr = apiErr.WithDetails(details)
	}
	apiErr.Write(w, http.StatusInsufficientStorage)
}

func quotaExceededDetailsForAPI(err *quotaExceededError) *quotaExceededError {
	if err == nil {
		return nil
	}
	clone := *err
	clone.QuotaPath = backup.SanitizeNotificationText(clone.QuotaPath)
	return &clone
}

func quotaExceededMessage(err *quotaExceededError) string {
	if err == nil {
		return "quota exceeded"
	}
	return err.Error()
}

func (s *Server) sendQuotaExceededAlertEvent(ctx context.Context, operation string, err error) {
	var quotaErr *quotaExceededError
	if !errors.As(err, &quotaErr) {
		return
	}
	sender, ok := s.alertMonitor.(AlertEventSender)
	if !ok || sender == nil {
		return
	}

	actorScope := "unknown"
	if user := auth.GetUserFromContext(ctx); user != nil && !user.Disabled {
		actorScope = "authenticated_user"
	}
	details := map[string]any{
		"operation":       operation,
		"actor_scope":     actorScope,
		"quota_type":      quotaErr.QuotaType,
		"used_bytes":      quotaErr.UsedBytes,
		"quota_bytes":     quotaErr.QuotaBytes,
		"required_bytes":  quotaErr.RequiredBytes,
		"available_bytes": quotaErr.AvailableBytes,
	}
	event := alerts.EventPayload{
		Type:      quotaExceededAlertType,
		Level:     alerts.AlertLevelWarning,
		Message:   quotaExceededMessage(quotaErr),
		Timestamp: time.Now().UTC(),
		Details:   details,
	}
	if sendErr := sender.SendEvent(context.WithoutCancel(ctx), event); sendErr != nil {
		s.logger.Warn().Err(sendErr).Str("event_type", event.Type).Msg("failed to send quota alert event")
	}
}
