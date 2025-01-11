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
		MaxVersionedSize: 100 * 1024 * 1024, // 100MB
		store:            store,
	}
}

// ShouldVersion determines if a file should have version management
// Logic:
// 1. Check user override (highest priority, ignores size limit)
// 2. Check file size limit
// 3. Check extension
// 4. Check filename
func (p *VersioningPolicy) ShouldVersion(ctx context.Context, path string, fileSize int64) bool {
	// 1. Check user override (highest priority)
	if p.store != nil {
		if enabled, exists := p.store.GetVersioningOverride(ctx, path); exists {
			return enabled // User override ignores size limit
		}
	}

	// 2. Check file size limit (skip large files)
	if p.MaxVersionedSize > 0 && fileSize > p.MaxVersionedSize {
		return false
	}

	// 3. Check extension
	ext := strings.ToLower(filepath.Ext(path))
	for _, versionedExt := range p.AutoVersionedExtensions {
		if ext == versionedExt {
			return true
		}
	}

	// 4. Check filename (for files without extension)
	filename := filepath.Base(path)
	for _, versionedName := range p.AutoVersionedFilenames {
		if filename == versionedName {
			return true
		}
	}

	return false
}

// IsVersionedExtension checks if an extension is in the versioned list
func (p *VersioningPolicy) IsVersionedExtension(ext string) bool {
	ext = strings.ToLower(ext)
	for _, versionedExt := range p.AutoVersionedExtensions {
		if ext == versionedExt {
			return true
		}
	}
	return false
}

// IsVersionedFilename checks if a filename is in the versioned list
func (p *VersioningPolicy) IsVersionedFilename(filename string) bool {
	for _, versionedName := range p.AutoVersionedFilenames {
		if filename == versionedName {
			return true
		}
	}
	return false
}

// GetVersioningStatus returns the versioning status for a file
// Returns: (enabled, reason)
func (p *VersioningPolicy) GetVersioningStatus(ctx context.Context, path string, fileSize int64) (bool, string) {
	// Check user override
	if p.store != nil {
		if enabled, exists := p.store.GetVersioningOverride(ctx, path); exists {
			if enabled {
				return true, "user_override_enabled"
			}
			return false, "user_override_disabled"
		}
	}

	// Check file size
	if p.MaxVersionedSize > 0 && fileSize > p.MaxVersionedSize {
		return false, "file_too_large"
	}

	// Check extension
	ext := strings.ToLower(filepath.Ext(path))
	for _, versionedExt := range p.AutoVersionedExtensions {
		if ext == versionedExt {
			return true, "extension_match"
		}
	}

	// Check filename
	filename := filepath.Base(path)
	for _, versionedName := range p.AutoVersionedFilenames {
		if filename == versionedName {
			return true, "filename_match"
		}
	}

	return false, "not_versioned_type"
}
