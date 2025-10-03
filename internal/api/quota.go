package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/seanbao/mnemonas/internal/alerts"
	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
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
		return "", false, nil
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

func (c quotaCheck) availableBytes() int64 {
	availableBytes := c.QuotaBytes - c.UsedBytes
	if availableBytes < 0 {
		return 0
	}
	return availableBytes
}

func (c quotaCheck) exceededError() *quotaExceededError {
	return newQuotaExceededErrorFor(c.QuotaType, c.QuotaPath, c.UsedBytes, c.QuotaBytes, c.RequiredBytes, c.availableBytes())
}

func (s *Server) directoryQuotaRules() []config.DirectoryQuotaConfig {
	cfg := s.currentConfig()
	if cfg == nil || len(cfg.Storage.DirectoryQuotas) == 0 {
		return nil
	}
	return append([]config.DirectoryQuotaConfig(nil), cfg.Storage.DirectoryQuotas...)
}

func directoryQuotaRulesForTarget(rules []config.DirectoryQuotaConfig, targetPath string) []config.DirectoryQuotaConfig {
	if len(rules) == 0 {
		return nil
	}
	matched := make([]config.DirectoryQuotaConfig, 0, len(rules))
	for _, rule := range rules {
		if rule.QuotaBytes <= 0 {
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
		if strings.TrimSpace(rule.Path) != "" && rule.QuotaBytes > 0 {
			return true
		}
	}
	return false
}

func mappedTreePathForQuota(treeRoot, mappedRoot, quotaPath string) (string, bool) {
	treeRoot = cleanQuotaPath(treeRoot)
	mappedRoot = cleanQuotaPath(mappedRoot)
	quotaPath = cleanQuotaPath(quotaPath)

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
	targetPath = strings.TrimSpace(strings.ReplaceAll(targetPath, "\\", "/"))
	if targetPath == "" {
		return ""
	}
	cleanPath := path.Clean(targetPath)
	if !path.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	return cleanPath
}

func ensureQuotaCheckAvailable(check quotaCheck) error {
	if check.RequiredBytes <= 0 {
		return nil
	}
	if check.RequiredBytes > check.availableBytes() {
		return check.exceededError()
	}
	return nil
}

func (s *Server) directoryQuotaChecksForRequiredBytes(ctx context.Context, targetPath string, requiredBytes int64) ([]quotaCheck, error) {
	rules := directoryQuotaRulesForTarget(s.directoryQuotaRules(), targetPath)
	if len(rules) == 0 || requiredBytes <= 0 {
		return nil, nil
	}

	checks := make([]quotaCheck, 0, len(rules))
	for _, rule := range rules {
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
	if info == nil {
		return 0, nil
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
	if info == nil {
		return 0, nil
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

func (s *Server) quotaCheckedUploadReader(ctx context.Context, targetPath string, reader io.Reader, contentLength int64) (io.Reader, func(), error) {
	homeDir, quotaBytes, quotaScoped, err := s.currentUserQuota(ctx)
	if err != nil {
		return nil, nil, err
	}
	userQuotaApplies := quotaScoped && quotaBytes > 0 && pathWithinBase(homeDir, targetPath)
	directoryRules := directoryQuotaRulesForTarget(s.directoryQuotaRules(), targetPath)
	if !userQuotaApplies && len(directoryRules) == 0 {
		return reader, func() {}, nil
	}

	s.quotaMu.Lock()
	unlock := func() {
		s.quotaMu.Unlock()
	}

	replacedBytes, err := s.existingUploadTargetSize(ctx, targetPath)
	if err != nil {
		unlock()
		return nil, nil, err
	}

	checks := make([]quotaCheck, 0, 1+len(directoryRules))
	if userQuotaApplies {
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
		if err != nil {
			unlock()
			return nil, nil, err
		}
		checks = append(checks, quotaCheck{
			QuotaType:     quotaTypeUser,
			QuotaPath:     homeDir,
			UsedBytes:     usedBytes - replacedBytes,
			QuotaBytes:    quotaBytes,
			RequiredBytes: contentLength,
		})
	}
	for _, rule := range directoryRules {
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, rule.Path)
		if err != nil {
			unlock()
			return nil, nil, err
		}
		checks = append(checks, quotaCheck{
			QuotaType:     quotaTypeDirectory,
			QuotaPath:     rule.Path,
			UsedBytes:     usedBytes - replacedBytes,
			QuotaBytes:    rule.QuotaBytes,
			RequiredBytes: contentLength,
		})
	}

	var streamLimit *quotaCheck
	for i := range checks {
		if checks[i].UsedBytes < 0 {
			checks[i].UsedBytes = 0
		}
		if contentLength >= 0 {
			if err := ensureQuotaCheckAvailable(checks[i]); err != nil {
				unlock()
				return nil, nil, err
			}
		}
		if streamLimit == nil || checks[i].availableBytes() < streamLimit.availableBytes() {
			streamLimit = &checks[i]
		}
	}
	if streamLimit == nil {
		unlock()
		return reader, func() {}, nil
	}
	streamErr := newQuotaExceededErrorFor(streamLimit.QuotaType, streamLimit.QuotaPath, streamLimit.UsedBytes, streamLimit.QuotaBytes, quotaStreamRequiredBytes(streamLimit.availableBytes()), streamLimit.availableBytes())

	return &quotaLimitedReader{
		reader:    reader,
		remaining: streamLimit.availableBytes(),
		err:       streamErr,
	}, unlock, nil
}

func (s *Server) existingUploadTargetSize(ctx context.Context, targetPath string) (int64, error) {
	info, err := s.fs.Stat(ctx, targetPath)
	if err != nil {
		if isStorageNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	if info.IsDir {
		return 0, storage.ErrIsDir
	}
	return nonNegativeSize(info.Size), nil
}

func (s *Server) copyResourceWithQuota(ctx context.Context, srcPath, dstPath string) error {
	homeDir, quotaBytes, quotaScoped, err := s.currentUserQuota(ctx)
	if err != nil {
		return err
	}
	userQuotaApplies := quotaScoped && quotaBytes > 0 && pathWithinBase(homeDir, dstPath)
	directoryRules := s.directoryQuotaRules()
	hasDirectoryQuotas := hasDirectoryQuotaRules(directoryRules)
	if !userQuotaApplies && !hasDirectoryQuotas {
		return s.copyResource(ctx, srcPath, dstPath)
	}

	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()

	if userQuotaApplies {
		requiredBytes, err := s.authorizedPathLogicalSize(ctx, srcPath)
		if err != nil {
			return err
		}
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
		if err != nil {
			return err
		}
		if err := ensureQuotaCheckAvailable(quotaCheck{
			QuotaType:     quotaTypeUser,
			QuotaPath:     homeDir,
			UsedBytes:     usedBytes,
			QuotaBytes:    quotaBytes,
			RequiredBytes: requiredBytes,
		}); err != nil {
			return err
		}
	}
	if hasDirectoryQuotas {
		checks, err := s.copyDirectoryQuotaChecks(ctx, srcPath, dstPath, directoryRules)
		if err != nil {
			return err
		}
		for _, check := range checks {
			if err := ensureQuotaCheckAvailable(check); err != nil {
				return err
			}
		}
	}

	return s.copyResource(ctx, srcPath, dstPath)
}

func (s *Server) copyDirectoryQuotaChecks(ctx context.Context, srcPath, dstPath string, rules []config.DirectoryQuotaConfig) ([]quotaCheck, error) {
	checks := make([]quotaCheck, 0, len(rules))
	for _, rule := range rules {
		rule.Path = cleanQuotaPath(rule.Path)
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
	if !userQuotaApplies && !hasDirectoryQuotas {
		return restore()
	}

	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()

	requiredBytes := int64(0)
	if item != nil {
		requiredBytes = nonNegativeSize(item.Size)
	}
	if userQuotaApplies {
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
		if err != nil {
			return err
		}
		if err := ensureQuotaCheckAvailable(quotaCheck{
			QuotaType:     quotaTypeUser,
			QuotaPath:     homeDir,
			UsedBytes:     usedBytes,
			QuotaBytes:    quotaBytes,
			RequiredBytes: requiredBytes,
		}); err != nil {
			return err
		}
	}
	if hasDirectoryQuotas {
		checks, err := s.trashRestoreDirectoryQuotaChecks(ctx, item, targetPath, directoryRules)
		if err != nil {
			return err
		}
		for _, check := range checks {
			if err := ensureQuotaCheckAvailable(check); err != nil {
				return err
			}
		}
	}

	return restore()
}

func (s *Server) trashRestoreDirectoryQuotaChecks(ctx context.Context, item *storage.TrashItem, targetPath string, rules []config.DirectoryQuotaConfig) ([]quotaCheck, error) {
	if item == nil || s.fs == nil {
		return nil, nil
	}

	requiredByRule := make(map[string]int64, len(rules))
	rulesByPath := make(map[string]config.DirectoryQuotaConfig, len(rules))
	for _, rule := range rules {
		rule.Path = cleanQuotaPath(rule.Path)
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
	if !userQuotaApplies && !hasDirectoryQuotas {
		return s.fs.Rename(ctx, srcPath, dstPath)
	}

	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()

	if userQuotaApplies {
		requiredBytes, err := s.pathLogicalSize(ctx, srcPath)
		if err != nil {
			return err
		}
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
		if err != nil {
			return err
		}
		if err := ensureQuotaCheckAvailable(quotaCheck{
			QuotaType:     quotaTypeUser,
			QuotaPath:     homeDir,
			UsedBytes:     usedBytes,
			QuotaBytes:    quotaBytes,
			RequiredBytes: requiredBytes,
		}); err != nil {
			return err
		}
	}
	if hasDirectoryQuotas {
		checks, err := s.moveDirectoryQuotaChecks(ctx, srcPath, dstPath, directoryRules)
		if err != nil {
			return err
		}
		for _, check := range checks {
			if err := ensureQuotaCheckAvailable(check); err != nil {
				return err
			}
		}
	}

	return s.fs.Rename(ctx, srcPath, dstPath)
}

func (s *Server) moveDirectoryQuotaChecks(ctx context.Context, srcPath, dstPath string, rules []config.DirectoryQuotaConfig) ([]quotaCheck, error) {
	checks := make([]quotaCheck, 0, len(rules))
	for _, rule := range rules {
		rule.Path = cleanQuotaPath(rule.Path)
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
		requiredBytes := destinationBytes - sourceBytes
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

func (s *Server) mappedTreeLogicalSizeIfExists(ctx context.Context, treeRoot, mappedRoot, quotaPath string) (int64, error) {
	sourcePath, ok := mappedTreePathForQuota(treeRoot, mappedRoot, quotaPath)
	if !ok {
		return 0, nil
	}
	return s.pathLogicalSizeIfExists(ctx, sourcePath)
}

func (s *Server) restoreVersionWithQuota(ctx context.Context, filePath, hash string) error {
	directoryRules := directoryQuotaRulesForTarget(s.directoryQuotaRules(), filePath)
	if len(directoryRules) == 0 {
		return s.fs.RestoreVersion(ctx, filePath, hash)
	}

	versions, err := s.fs.ListVersions(ctx, filePath)
	if err != nil {
		return err
	}
	currentSize := int64(0)
	if len(versions) > 0 {
		currentSize = nonNegativeSize(versions[0].Size)
	}
	restoredSize := int64(-1)
	for _, version := range versions {
		if strings.EqualFold(version.Hash, hash) {
			restoredSize = nonNegativeSize(version.Size)
			break
		}
	}
	if restoredSize < 0 {
		return storage.ErrVersionNotFound
	}
	requiredBytes := restoredSize - currentSize
	if requiredBytes < 0 {
		requiredBytes = 0
	}

	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()

	for _, rule := range directoryRules {
		usedBytes, err := s.pathLogicalSizeIfExists(ctx, rule.Path)
		if err != nil {
			return err
		}
		if err := ensureQuotaCheckAvailable(quotaCheck{
			QuotaType:     quotaTypeDirectory,
			QuotaPath:     rule.Path,
			UsedBytes:     usedBytes,
			QuotaBytes:    rule.QuotaBytes,
			RequiredBytes: requiredBytes,
		}); err != nil {
			return err
		}
	}

	return s.fs.RestoreVersion(ctx, filePath, hash)
}

func ensureQuotaAvailable(usedBytes, quotaBytes, requiredBytes int64) error {
	requiredBytes = nonNegativeSize(requiredBytes)
	availableBytes := quotaBytes - usedBytes
	if availableBytes < 0 {
		availableBytes = 0
	}
	if requiredBytes > availableBytes {
		return newQuotaExceededError(usedBytes, quotaBytes, requiredBytes, availableBytes)
	}
	return nil
}

func newQuotaExceededError(usedBytes, quotaBytes, requiredBytes, availableBytes int64) *quotaExceededError {
	return newQuotaExceededErrorFor(quotaTypeUser, "", usedBytes, quotaBytes, requiredBytes, availableBytes)
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
}

func (r *quotaLimitedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, r.err
		}
		if err != nil {
			return 0, err
		}
		return 0, nil
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
		details = quotaErr
	}
	apiErr := NewAPIError(ErrCodeQuotaExceeded, quotaExceededMessage(quotaErr))
	if details != nil {
		apiErr = apiErr.WithDetails(details)
	}
	apiErr.Write(w, http.StatusInsufficientStorage)
}

func quotaExceededMessage(err *quotaExceededError) string {
	if err == nil {
		return "quota exceeded"
	}
	return err.Error()
}

func (s *Server) sendQuotaExceededAlertEvent(ctx context.Context, operation, targetPath string, err error) {
	var quotaErr *quotaExceededError
	if !errors.As(err, &quotaErr) {
		return
	}
	sender, ok := s.alertMonitor.(AlertEventSender)
	if !ok || sender == nil {
		return
	}

	username := ""
	homeDir := ""
	if user := auth.GetUserFromContext(ctx); user != nil {
		username = user.Username
		homeDir = user.HomeDir
	}
	event := alerts.EventPayload{
		Type:      quotaExceededAlertType,
		Level:     alerts.AlertLevelWarning,
		Message:   quotaExceededMessage(quotaErr),
		Timestamp: time.Now().UTC(),
		Details: map[string]any{
			"operation":       operation,
			"target_path":     targetPath,
			"username":        username,
			"home_dir":        homeDir,
			"quota_type":      quotaErr.QuotaType,
			"quota_path":      quotaErr.QuotaPath,
			"used_bytes":      quotaErr.UsedBytes,
			"quota_bytes":     quotaErr.QuotaBytes,
			"required_bytes":  quotaErr.RequiredBytes,
			"available_bytes": quotaErr.AvailableBytes,
		},
	}
	if sendErr := sender.SendEvent(context.WithoutCancel(ctx), event); sendErr != nil {
		s.logger.Warn().Err(sendErr).Str("event_type", event.Type).Msg("failed to send quota alert event")
	}
}
