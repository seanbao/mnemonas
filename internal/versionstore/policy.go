package versionstore

import (
	"context"
	"path/filepath"
	"strings"
)

// VersioningPolicy determines which files should have version management
type VersioningPolicy struct {
	// Extensions that should have versioning enabled by default
	AutoVersionedExtensions []string

	// Filenames (without extension) that should have versioning
	AutoVersionedFilenames []string

	// Maximum file size for auto versioning (larger files skip versioning)
	MaxVersionedSize int64

	// store for checking user overrides
	store *Store
}

// MaxVersionObjectSize is the largest whole-file version object supported by
// the current unary dataplane object contract.
const MaxVersionObjectSize int64 = 100 * 1024 * 1024

func normalizeConfiguredVersionedExtension(ext string) string {
	return strings.ToLower(strings.TrimSpace(ext))
}

func normalizeConfiguredVersionedFilename(filename string) string {
	return strings.TrimSpace(filename)
}

// DefaultVersioningPolicy returns the default versioning policy
func DefaultVersioningPolicy(store *Store) *VersioningPolicy {
	return &VersioningPolicy{
		AutoVersionedExtensions: []string{
			// Documents
			".md", ".txt", ".org", ".rst", ".tex", ".rtf",
			// Code
			".go", ".rs", ".py", ".ts", ".js", ".tsx", ".jsx",
			".c", ".cpp", ".h", ".hpp", ".java", ".kt", ".swift",
			".rb", ".php", ".cs", ".fs", ".scala", ".clj",
			".lua", ".pl", ".r", ".jl", ".hs", ".elm", ".ex", ".exs",
			// Config
			".toml", ".yaml", ".yml", ".json", ".xml", ".ini", ".conf",
			".env", ".properties",
			// Scripts
			".sh", ".bash", ".zsh", ".fish", ".ps1", ".bat", ".cmd",
			// Web
			".html", ".htm", ".css", ".scss", ".sass", ".less",
			".vue", ".svelte",
			// Data/markup
			".csv", ".sql", ".graphql",
		},
		AutoVersionedFilenames: []string{
			// Common config files without extension
			"Makefile", "Dockerfile", "Vagrantfile", "Procfile",
			"Gemfile", "Rakefile", "Brewfile",
			"LICENSE", "README", "CHANGELOG", "AUTHORS", "CONTRIBUTORS",
			"INSTALL", "TODO", "COPYING", "NOTICE",
			".gitignore", ".dockerignore", ".editorconfig", ".gitattributes",
			".prettierrc", ".eslintrc", ".babelrc", ".npmrc", ".yarnrc",
			"go.mod", "go.sum", "Cargo.toml", "Cargo.lock",
			"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
			"requirements.txt", "Pipfile", "Pipfile.lock", "poetry.lock",
			"composer.json", "composer.lock",
		},
		MaxVersionedSize: MaxVersionObjectSize,
		store:            store,
	}
}

// ShouldVersion determines if a file should have version management
// Logic:
// 1. Check the storage contract size limit
// 2. Check user override
// 3. Check the automatic-versioning size threshold
// 4. Check extension
// 5. Check filename
func (p *VersioningPolicy) ShouldVersion(ctx context.Context, path string, fileSize int64) bool {
	// Whole-file objects currently use one bounded unary dataplane message.
	if fileSize < 0 || fileSize > MaxVersionObjectSize {
		return false
	}

	// User overrides select files inside the supported size boundary.
	if p.store != nil {
		if enabled, exists := p.store.GetVersioningOverride(ctx, path); exists {
			return enabled
		}
	}

	// The configured threshold applies only to automatic version selection.
	if p.MaxVersionedSize > 0 && fileSize > p.MaxVersionedSize {
		return false
	}

	// 4. Check extension
	ext := normalizeConfiguredVersionedExtension(filepath.Ext(path))
	for _, versionedExt := range p.AutoVersionedExtensions {
		if ext == normalizeConfiguredVersionedExtension(versionedExt) {
			return true
		}
	}

	// 5. Check filename (for files without extension)
	filename := normalizeConfiguredVersionedFilename(filepath.Base(path))
	for _, versionedName := range p.AutoVersionedFilenames {
		if filename == normalizeConfiguredVersionedFilename(versionedName) {
			return true
		}
	}

	return false
}

// IsVersionedExtension checks if an extension is in the versioned list
func (p *VersioningPolicy) IsVersionedExtension(ext string) bool {
	ext = normalizeConfiguredVersionedExtension(ext)
	for _, versionedExt := range p.AutoVersionedExtensions {
		if ext == normalizeConfiguredVersionedExtension(versionedExt) {
			return true
		}
	}
	return false
}

// IsVersionedFilename checks if a filename is in the versioned list
func (p *VersioningPolicy) IsVersionedFilename(filename string) bool {
	filename = normalizeConfiguredVersionedFilename(filename)
	for _, versionedName := range p.AutoVersionedFilenames {
		if filename == normalizeConfiguredVersionedFilename(versionedName) {
			return true
		}
	}
	return false
}

// GetVersioningStatus returns the versioning status for a file
// Returns: (enabled, reason)
func (p *VersioningPolicy) GetVersioningStatus(ctx context.Context, path string, fileSize int64) (bool, string) {
	if fileSize < 0 || fileSize > MaxVersionObjectSize {
		return false, "file_too_large"
	}

	// Check user override
	if p.store != nil {
		if enabled, exists := p.store.GetVersioningOverride(ctx, path); exists {
			if enabled {
				return true, "user_override_enabled"
			}
			return false, "user_override_disabled"
		}
	}

	if p.MaxVersionedSize > 0 && fileSize > p.MaxVersionedSize {
		return false, "file_too_large"
	}

	// Check extension
	ext := normalizeConfiguredVersionedExtension(filepath.Ext(path))
	for _, versionedExt := range p.AutoVersionedExtensions {
		if ext == normalizeConfiguredVersionedExtension(versionedExt) {
			return true, "extension_match"
		}
	}

	// Check filename
	filename := normalizeConfiguredVersionedFilename(filepath.Base(path))
	for _, versionedName := range p.AutoVersionedFilenames {
		if filename == normalizeConfiguredVersionedFilename(versionedName) {
			return true, "filename_match"
		}
	}

	return false, "not_versioned_type"
}
