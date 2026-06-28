package api

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/storage"
)

type pathAccessMode string

const (
	pathAccessRead  pathAccessMode = "read"
	pathAccessWrite pathAccessMode = "write"
)

var errPathAccessDenied = errors.New("path access denied by directory access rule")

type pathAccessEvaluation struct {
	Mode        pathAccessMode                    `json:"mode"`
	Allowed     bool                              `json:"allowed"`
	Source      string                            `json:"source"`
	Message     string                            `json:"message,omitempty"`
	MatchedRule *config.DirectoryAccessRuleConfig `json:"matched_rule,omitempty"`
}

type pathAccessCheckResult struct {
	Username string               `json:"username"`
	UserID   string               `json:"user_id"`
	Role     auth.Role            `json:"role"`
	Groups   []string             `json:"groups,omitempty"`
	HomeDir  string               `json:"home_dir"`
	Path     string               `json:"path"`
	Read     pathAccessEvaluation `json:"read"`
	Write    pathAccessEvaluation `json:"write"`
}

func (s *Server) authorizeUserReadPath(ctx context.Context, targetPath string) error {
	return s.authorizeUserPathFor(ctx, targetPath, pathAccessRead)
}

func (s *Server) authorizeUserConcreteReadPath(ctx context.Context, targetPath string) error {
	return s.authorizeUserPathForOptions(ctx, targetPath, pathAccessRead, false)
}

func (s *Server) authorizeUserWritePath(ctx context.Context, targetPath string) error {
	return s.authorizeUserPathFor(ctx, targetPath, pathAccessWrite)
}

func (s *Server) deletePathAuthorizerSnapshot(ctx context.Context) (storage.DeletePathAuthorizer, error) {
	if !s.authEnabled || auth.IsAdmin(ctx) {
		return nil, nil
	}

	user := auth.GetUserFromContext(ctx)
	if user == nil || user.Disabled {
		return nil, errPathAccessDenied
	}
	userSnapshot := *user
	userSnapshot.Groups = append([]string(nil), user.Groups...)

	homeDir := ""
	homeDirErr := error(nil)
	if strings.TrimSpace(userSnapshot.HomeDir) == "" {
		homeDirErr = errPathOutsideHomeDir
	} else {
		var err error
		homeDir, err = validatePath(userSnapshot.HomeDir)
		if err != nil {
			homeDirErr = errPathOutsideHomeDir
		}
	}
	cfg := s.currentConfig()
	var rules []config.DirectoryAccessRuleConfig
	if cfg != nil {
		rules = cfg.Storage.DirectoryAccessRules
	}

	return func(targetPath string) error {
		cleanTargetPath, err := validatePath(targetPath)
		if err != nil {
			return errPathOutsideHomeDir
		}
		if rule, ok := matchDirectoryAccessRuleIn(rules, cleanTargetPath); ok {
			if directoryAccessRuleAllowsUser(rule, &userSnapshot, pathAccessWrite) {
				return nil
			}
			return errPathAccessDenied
		}
		if homeDirErr != nil {
			return homeDirErr
		}
		if !pathWithinBase(homeDir, cleanTargetPath) {
			return errPathOutsideHomeDir
		}
		return nil
	}, nil
}

func (s *Server) authorizeUserPath(ctx context.Context, targetPath string) error {
	return s.authorizeUserReadPath(ctx, targetPath)
}

func (s *Server) authorizeUserPathFor(ctx context.Context, targetPath string, mode pathAccessMode) error {
	return s.authorizeUserPathForOptions(ctx, targetPath, mode, true)
}

func (s *Server) authorizeUserPathForOptions(ctx context.Context, targetPath string, mode pathAccessMode, allowReadableDescendant bool) error {
	if !s.authEnabled || auth.IsAdmin(ctx) {
		return nil
	}

	cleanTargetPath, err := validatePath(targetPath)
	if err != nil {
		return errPathOutsideHomeDir
	}
	targetPath = cleanTargetPath

	user := auth.GetUserFromContext(ctx)
	if user == nil {
		return errPathAccessDenied
	}
	if user.Disabled {
		return errPathAccessDenied
	}

	if rule, ok := s.matchDirectoryAccessRule(targetPath); ok {
		if directoryAccessRuleAllowsUser(rule, user, mode) {
			return nil
		}
		return errPathAccessDenied
	}
	if allowReadableDescendant && mode == pathAccessRead {
		ok, err := s.hasExistingReadableDirectoryAccessDescendantRule(ctx, user, targetPath)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}

	homeDir, scoped, err := s.currentUserHomeDir(ctx)
	if err != nil {
		return err
	}
	if !scoped {
		return nil
	}
	if !pathWithinBase(homeDir, targetPath) {
		return errPathOutsideHomeDir
	}
	return nil
}

func (s *Server) matchDirectoryAccessRule(targetPath string) (config.DirectoryAccessRuleConfig, bool) {
	cfg := s.currentConfig()
	if cfg == nil || len(cfg.Storage.DirectoryAccessRules) == 0 {
		return config.DirectoryAccessRuleConfig{}, false
	}

	return matchDirectoryAccessRuleIn(cfg.Storage.DirectoryAccessRules, targetPath)
}

func matchDirectoryAccessRuleIn(rules []config.DirectoryAccessRuleConfig, targetPath string) (config.DirectoryAccessRuleConfig, bool) {
	if len(rules) == 0 {
		return config.DirectoryAccessRuleConfig{}, false
	}

	targetPath = cleanRuntimePathRulePath(targetPath)
	if targetPath == "" {
		return config.DirectoryAccessRuleConfig{}, false
	}
	var bestRule config.DirectoryAccessRuleConfig
	bestLength := -1
	for _, rule := range rules {
		rulePath := cleanRuntimePathRulePath(rule.Path)
		if rulePath == "" || !pathWithinBase(rulePath, targetPath) {
			continue
		}
		if len(rulePath) > bestLength {
			rule.Path = rulePath
			bestRule = rule
			bestLength = len(rulePath)
		}
	}
	if bestLength < 0 {
		return config.DirectoryAccessRuleConfig{}, false
	}
	return bestRule, true
}

func (s *Server) hasExistingReadableDirectoryAccessDescendantRule(ctx context.Context, user *auth.User, targetPath string) (bool, error) {
	cfg := s.currentConfig()
	if cfg == nil || len(cfg.Storage.DirectoryAccessRules) == 0 {
		return false, nil
	}

	_, ok, err := s.existingReadableDirectoryAccessDescendantRuleWithRules(ctx, user, targetPath, cfg.Storage.DirectoryAccessRules)
	return ok, err
}

func (s *Server) existingReadableDirectoryAccessDescendantRuleWithRules(ctx context.Context, user *auth.User, targetPath string, rules []config.DirectoryAccessRuleConfig) (config.DirectoryAccessRuleConfig, bool, error) {
	if s.fs == nil || len(rules) == 0 {
		return config.DirectoryAccessRuleConfig{}, false, nil
	}

	for _, rule := range readableDirectoryAccessDescendantRules(rules, user, targetPath) {
		ok, err := s.directoryExistsForAccessRule(ctx, rule.Path)
		if err != nil {
			return config.DirectoryAccessRuleConfig{}, false, err
		}
		if ok {
			return rule, true, nil
		}
	}
	return config.DirectoryAccessRuleConfig{}, false, nil
}

func readableDirectoryAccessDescendantRules(rules []config.DirectoryAccessRuleConfig, user *auth.User, targetPath string) []config.DirectoryAccessRuleConfig {
	if len(rules) == 0 || user == nil {
		return nil
	}
	targetPath = cleanRuntimePathRulePath(targetPath)
	if targetPath == "" {
		return nil
	}
	if targetPath == "/" {
		return nil
	}

	matched := make([]config.DirectoryAccessRuleConfig, 0)
	for _, rule := range rules {
		rulePath := cleanRuntimePathRulePath(rule.Path)
		if rulePath == "" {
			continue
		}
		if rulePath == "/" || rulePath == targetPath {
			continue
		}
		if !pathWithinBase(targetPath, rulePath) {
			continue
		}
		if !directoryAccessRuleAllowsUser(rule, user, pathAccessRead) {
			continue
		}
		rule.Path = rulePath
		matched = append(matched, rule)
	}
	return matched
}

func (s *Server) directoryExistsForAccessRule(ctx context.Context, targetPath string) (bool, error) {
	info, err := s.fs.Stat(ctx, targetPath)
	if err != nil {
		if isStorageNotFound(err) || errors.Is(err, storage.ErrNotDir) {
			return false, nil
		}
		return false, err
	}
	return info != nil && info.IsDir, nil
}

func directoryAccessRuleAllowsUser(rule config.DirectoryAccessRuleConfig, user *auth.User, mode pathAccessMode) bool {
	if user == nil {
		return false
	}
	username := strings.ToLower(strings.TrimSpace(user.Username))
	role := strings.ToLower(strings.TrimSpace(string(user.Role)))
	groups := make([]string, 0, len(user.Groups))
	for _, group := range user.Groups {
		groups = append(groups, strings.ToLower(strings.TrimSpace(group)))
	}

	var users []string
	var roles []string
	var groupsAllowed []string
	if mode == pathAccessWrite {
		users = normalizeAccessRuleRuntimeValues(rule.WriteUsers)
		roles = normalizeAccessRuleRuntimeValues(rule.WriteRoles)
		groupsAllowed = normalizeAccessRuleRuntimeValues(rule.WriteGroups)
	} else {
		users = normalizeAccessRuleRuntimeValues(append(append([]string(nil), rule.ReadUsers...), rule.WriteUsers...))
		roles = normalizeAccessRuleRuntimeValues(append(append([]string(nil), rule.ReadRoles...), rule.WriteRoles...))
		groupsAllowed = normalizeAccessRuleRuntimeValues(append(append([]string(nil), rule.ReadGroups...), rule.WriteGroups...))
	}

	if slices.Contains(users, username) || slices.Contains(roles, role) {
		return true
	}
	for _, group := range groups {
		if slices.Contains(groupsAllowed, group) {
			return true
		}
	}
	return false
}

func normalizeAccessRuleRuntimeValues(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}

func (s *Server) hasDirectoryAccessRules() bool {
	cfg := s.currentConfig()
	return cfg != nil && len(cfg.Storage.DirectoryAccessRules) > 0
}

func (s *Server) evaluateUserPathAccess(ctx context.Context, user *auth.User, targetPath string) (pathAccessCheckResult, error) {
	cfg := s.currentConfig()
	var rules []config.DirectoryAccessRuleConfig
	if cfg != nil {
		rules = cfg.Storage.DirectoryAccessRules
	}
	return s.evaluateUserPathAccessWithRules(ctx, user, targetPath, rules)
}

func (s *Server) evaluateUserPathAccessWithRules(ctx context.Context, user *auth.User, targetPath string, rules []config.DirectoryAccessRuleConfig) (pathAccessCheckResult, error) {
	result := pathAccessCheckResult{
		Path: targetPath,
	}
	if user != nil {
		result.Username = user.Username
		result.UserID = user.ID
		result.Role = user.Role
		result.Groups = append([]string(nil), user.Groups...)
		result.HomeDir = user.HomeDir
	}
	cleanTargetPath, err := validatePath(targetPath)
	if err != nil {
		result.Read = invalidPathAccessEvaluation(pathAccessRead)
		result.Write = invalidPathAccessEvaluation(pathAccessWrite)
		return result, nil
	}
	result.Path = cleanTargetPath
	targetPath = cleanTargetPath
	result.Read, err = s.evaluateUserPathAccessModeWithRules(ctx, user, targetPath, pathAccessRead, rules)
	if err != nil {
		return result, err
	}
	result.Write, err = s.evaluateUserPathAccessModeWithRules(ctx, user, targetPath, pathAccessWrite, rules)
	if err != nil {
		return result, err
	}
	return result, nil
}

func invalidPathAccessEvaluation(mode pathAccessMode) pathAccessEvaluation {
	return pathAccessEvaluation{
		Mode:    mode,
		Allowed: false,
		Source:  "invalid_path",
		Message: "path is invalid",
	}
}

func (s *Server) evaluateUserPathAccessModeWithRules(ctx context.Context, user *auth.User, targetPath string, mode pathAccessMode, rules []config.DirectoryAccessRuleConfig) (pathAccessEvaluation, error) {
	if !s.authEnabled {
		return pathAccessEvaluation{
			Mode:    mode,
			Allowed: true,
			Source:  "auth_disabled",
			Message: "authentication is disabled",
		}, nil
	}
	if user == nil {
		return pathAccessEvaluation{
			Mode:    mode,
			Allowed: false,
			Source:  "user_not_found",
			Message: "user was not found",
		}, nil
	}
	if user.Disabled {
		return pathAccessEvaluation{
			Mode:    mode,
			Allowed: false,
			Source:  "user_disabled",
			Message: "user account is disabled",
		}, nil
	}
	if user.Role == auth.RoleAdmin {
		return pathAccessEvaluation{
			Mode:    mode,
			Allowed: true,
			Source:  "admin",
			Message: "admin role has full access",
		}, nil
	}

	if rule, ok := matchDirectoryAccessRuleIn(rules, targetPath); ok {
		matchedRule := rule
		allowed := directoryAccessRuleAllowsUser(rule, user, mode)
		message := "directory access rule does not grant " + string(mode)
		if allowed {
			message = "directory access rule grants " + string(mode)
		}
		return pathAccessEvaluation{
			Mode:        mode,
			Allowed:     allowed,
			Source:      "directory_access_rule",
			Message:     message,
			MatchedRule: &matchedRule,
		}, nil
	}
	if mode == pathAccessRead {
		matchedRule, ok, err := s.existingReadableDirectoryAccessDescendantRuleWithRules(ctx, user, targetPath, rules)
		if err != nil {
			return pathAccessEvaluation{}, err
		}
		if ok {
			return pathAccessEvaluation{
				Mode:        mode,
				Allowed:     true,
				Source:      "directory_access_rule",
				Message:     "directory access rule grants read through an existing descendant",
				MatchedRule: &matchedRule,
			}, nil
		}
	}

	homeDir, err := validatePath(user.HomeDir)
	if err != nil || strings.TrimSpace(user.HomeDir) == "" {
		return pathAccessEvaluation{
			Mode:    mode,
			Allowed: false,
			Source:  "invalid_home_dir",
			Message: "user home_dir is invalid",
		}, nil
	}
	allowed := pathWithinBase(homeDir, targetPath)
	message := "path is outside the user's home_dir"
	if allowed {
		message = "path is inside the user's home_dir"
	}
	return pathAccessEvaluation{
		Mode:    mode,
		Allowed: allowed,
		Source:  "home_dir",
		Message: message,
	}, nil
}
