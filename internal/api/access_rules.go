package api

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
)

type pathAccessMode string

const (
	pathAccessRead  pathAccessMode = "read"
	pathAccessWrite pathAccessMode = "write"
)

var errPathAccessDenied = errors.New("path access denied by directory access rule")

func (s *Server) authorizeUserReadPath(ctx context.Context, targetPath string) error {
	return s.authorizeUserPathFor(ctx, targetPath, pathAccessRead)
}

func (s *Server) authorizeUserWritePath(ctx context.Context, targetPath string) error {
	return s.authorizeUserPathFor(ctx, targetPath, pathAccessWrite)
}

func (s *Server) authorizeUserPath(ctx context.Context, targetPath string) error {
	return s.authorizeUserReadPath(ctx, targetPath)
}

func (s *Server) authorizeUserPathFor(ctx context.Context, targetPath string, mode pathAccessMode) error {
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

	targetPath = path.Clean(targetPath)
	bestIndex := -1
	bestLength := -1
	for i, rule := range cfg.Storage.DirectoryAccessRules {
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
	return cfg.Storage.DirectoryAccessRules[bestIndex], true
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

	users := rule.ReadUsers
	roles := rule.ReadRoles
	groupsAllowed := rule.ReadGroups
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
