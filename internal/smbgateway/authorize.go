// Package smbgateway contains authorization and path mapping for the SMB sidecar.
package smbgateway

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
)

var (
	// ErrShareNotFound means the requested SMB share is not configured.
	ErrShareNotFound = errors.New("SMB share not found")
	// ErrAccessDenied means the principal is not allowed to access a share or path.
	ErrAccessDenied = errors.New("SMB access denied")
	// ErrInvalidPath means the requested path is malformed or attempts traversal.
	ErrInvalidPath = errors.New("invalid SMB path")
	// ErrReadOnly means a write was requested through a read-only view.
	ErrReadOnly = errors.New("SMB share is read-only")
)

// ResolvedPath is an authorized MnemoNAS storage path for one SMB request.
type ResolvedPath struct {
	ShareName string
	ShareRoot string
	Path      string
	ReadOnly  bool
	CanWrite  bool
}

// ResolvePath maps a tree-relative SMB path to a MnemoNAS virtual path.
func ResolvePath(shares []config.SMBShareConfig, shareName, requestPath string, user *auth.User, write bool) (ResolvedPath, error) {
	share, ok := FindShare(shares, shareName)
	if !ok {
		return ResolvedPath{}, ErrShareNotFound
	}
	if !ShareAllowed(share, user) {
		return ResolvedPath{}, ErrAccessDenied
	}

	shareRoot, targetPath, err := mapSharePath(share.Path, requestPath)
	if err != nil {
		return ResolvedPath{}, err
	}
	if err := enforceHomeDir(user, targetPath); err != nil {
		return ResolvedPath{}, err
	}

	canWrite := !share.ReadOnly && user.Role != auth.RoleGuest
	if write && !canWrite {
		return ResolvedPath{}, ErrReadOnly
	}

	return ResolvedPath{
		ShareName: share.Name,
		ShareRoot: shareRoot,
		Path:      targetPath,
		ReadOnly:  share.ReadOnly,
		CanWrite:  canWrite,
	}, nil
}

// FindShare locates a configured SMB share by case-insensitive share name.
func FindShare(shares []config.SMBShareConfig, shareName string) (config.SMBShareConfig, bool) {
	for _, share := range shares {
		if strings.EqualFold(share.Name, strings.TrimSpace(shareName)) {
			return share, true
		}
	}
	return config.SMBShareConfig{}, false
}

// ShareAllowed checks share-level role/user allow lists.
func ShareAllowed(share config.SMBShareConfig, user *auth.User) bool {
	if user == nil || user.Disabled {
		return false
	}
	for _, allowedUser := range share.AllowedUsers {
		if userMatches(allowedUser, user) {
			return true
		}
	}
	for _, allowedRole := range share.AllowedRoles {
		if strings.EqualFold(strings.TrimSpace(allowedRole), string(user.Role)) {
			return true
		}
	}
	return false
}

func userMatches(allowed string, user *auth.User) bool {
	normalized := strings.ToLower(strings.TrimSpace(allowed))
	return normalized != "" &&
		(normalized == strings.ToLower(user.ID) || normalized == strings.ToLower(user.Username))
}

func mapSharePath(sharePath, requestPath string) (string, string, error) {
	shareRoot, err := cleanVirtualPath(sharePath)
	if err != nil {
		return "", "", fmt.Errorf("%w: share root", ErrInvalidPath)
	}
	relativePath, err := cleanVirtualPath(requestPath)
	if err != nil {
		return "", "", err
	}
	if relativePath == "/" {
		return shareRoot, shareRoot, nil
	}
	if shareRoot == "/" {
		return shareRoot, relativePath, nil
	}
	return shareRoot, path.Clean(shareRoot + "/" + strings.TrimPrefix(relativePath, "/")), nil
}

func enforceHomeDir(user *auth.User, targetPath string) error {
	if user.Role == auth.RoleAdmin {
		return nil
	}
	if strings.TrimSpace(user.HomeDir) == "" {
		return ErrAccessDenied
	}
	homeDir, err := cleanVirtualPath(user.HomeDir)
	if err != nil {
		return ErrAccessDenied
	}
	if !pathWithinBase(homeDir, targetPath) {
		return ErrAccessDenied
	}
	return nil
}

func cleanVirtualPath(value string) (string, error) {
	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') || hasDotSegment(normalized) {
		return "", ErrInvalidPath
	}
	cleaned := path.Clean("/" + normalized)
	if cleaned != "/" && !strings.HasPrefix(cleaned, "/") {
		return "", ErrInvalidPath
	}
	return cleaned, nil
}

func hasDotSegment(filePath string) bool {
	for _, segment := range strings.Split(filePath, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func pathWithinBase(basePath, targetPath string) bool {
	basePath = path.Clean(basePath)
	targetPath = path.Clean(targetPath)
	if basePath == "/" {
		return strings.HasPrefix(targetPath, "/")
	}
	return targetPath == basePath || strings.HasPrefix(targetPath, basePath+"/")
}
