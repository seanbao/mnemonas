package api

import (
	"context"
	"errors"
	"path"
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

	user := auth.GetUserFromContext(ctx)
	if user == nil {
		return nil
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

	targetPath = path.Clean(targetPath)
	bestIndex := -1
	bestLength := -1
	for i, rule := range rules {
		if strings.TrimSpace(rule.Path) == "" {
			continue
		}
		if !pathWithinBase(rule.Path, targetPath) {
			continue
		}
		if len(rule.Path) > bestLength {
			bestIndex = i
			bestLength = len(rule.Path)
		}
	}
	if bestIndex < 0 {
		return config.DirectoryAccessRuleConfig{}, false
	}
	return rules[bestIndex], true
}

func (s *Server) hasExistingReadableDirectoryAccessDescendantRule(ctx context.Context, user *auth.User, targetPath string) (bool, error) {
	if s.fs == nil {
		return false, nil
	}
	cfg := s.currentConfig()
	if cfg == nil || len(cfg.Storage.DirectoryAccessRules) == 0 {
		return false, nil
	}

	for _, rule := range readableDirectoryAccessDescendantRules(cfg.Storage.DirectoryAccessRules, user, targetPath) {
		ok, err := s.directoryExistsForAccessRule(ctx, rule.Path)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func readableDirectoryAccessDescendantRules(rules []config.DirectoryAccessRuleConfig, user *auth.User, targetPath string) []config.DirectoryAccessRuleConfig {
	if len(rules) == 0 || user == nil {
		return nil
	}
	targetPath = path.Clean(targetPath)
	if targetPath == "/" {
		return nil
	}

	matched := make([]config.DirectoryAccessRuleConfig, 0)
	for _, rule := range rules {
		if strings.TrimSpace(rule.Path) == "" {
			continue
		}
		rulePath := path.Clean(rule.Path)
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
		users = rule.WriteUsers
		roles = rule.WriteRoles
		groupsAllowed = rule.WriteGroups
	} else {
		users = append(append([]string(nil), rule.ReadUsers...), rule.WriteUsers...)
		roles = append(append([]string(nil), rule.ReadRoles...), rule.WriteRoles...)
		groupsAllowed = append(append([]string(nil), rule.ReadGroups...), rule.WriteGroups...)
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

func (s *Server) hasDirectoryAccessRules() bool {
	cfg := s.currentConfig()
	return cfg != nil && len(cfg.Storage.DirectoryAccessRules) > 0
}

func (s *Server) evaluateUserPathAccess(user *auth.User, targetPath string) pathAccessCheckResult {
	cfg := s.currentConfig()
	var rules []config.DirectoryAccessRuleConfig
	if cfg != nil {
		rules = cfg.Storage.DirectoryAccessRules
	}
	return s.evaluateUserPathAccessWithRules(user, targetPath, rules)
}

func (s *Server) evaluateUserPathAccessWithRules(user *auth.User, targetPath string, rules []config.DirectoryAccessRuleConfig) pathAccessCheckResult {
	targetPath = path.Clean(targetPath)
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
	result.Read = s.evaluateUserPathAccessModeWithRules(user, targetPath, pathAccessRead, rules)
	result.Write = s.evaluateUserPathAccessModeWithRules(user, targetPath, pathAccessWrite, rules)
	return result
}

func (s *Server) evaluateUserPathAccessModeWithRules(user *auth.User, targetPath string, mode pathAccessMode, rules []config.DirectoryAccessRuleConfig) pathAccessEvaluation {
	if !s.authEnabled {
		return pathAccessEvaluation{
			Mode:    mode,
			Allowed: true,
			Source:  "auth_disabled",
			Message: "authentication is disabled",
		}
	}
	if user == nil {
		return pathAccessEvaluation{
			Mode:    mode,
			Allowed: false,
			Source:  "user_not_found",
			Message: "user was not found",
		}
	}
	if user.Disabled {
		return pathAccessEvaluation{
			Mode:    mode,
			Allowed: false,
			Source:  "user_disabled",
			Message: "user account is disabled",
		}
	}
	if user.Role == auth.RoleAdmin {
		return pathAccessEvaluation{
			Mode:    mode,
			Allowed: true,
			Source:  "admin",
			Message: "admin role has full access",
		}
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
		}
	}
	if mode == pathAccessRead {
		if descendantRules := readableDirectoryAccessDescendantRules(rules, user, targetPath); len(descendantRules) > 0 {
			matchedRule := descendantRules[0]
			return pathAccessEvaluation{
				Mode:        mode,
				Allowed:     true,
				Source:      "directory_access_rule",
				Message:     "directory access rule grants read through a descendant",
				MatchedRule: &matchedRule,
			}
		}
	}

	homeDir, err := validatePath(user.HomeDir)
	if err != nil || strings.TrimSpace(user.HomeDir) == "" {
		return pathAccessEvaluation{
			Mode:    mode,
			Allowed: false,
			Source:  "invalid_home_dir",
			Message: "user home_dir is invalid",
		}
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
	}
}
