// Package thumbnail provides image thumbnail generation and caching
package thumbnail

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

var errThumbnailCacheSymlink = errors.New("thumbnail cache path must not be a symlink")
var syncThumbnailCacheDir = syncThumbnailDir
var ErrThumbnailSourceTooLarge = errors.New("source image too large for thumbnail generation")

// Size represents thumbnail size preset
type Size string

const (
	SizeSmall  Size = "small"  // 150x150
	SizeMedium Size = "medium" // 300x300
	SizeLarge  Size = "large"  // 600x600
)

// SizeDimensions maps size presets to pixel dimensions
var SizeDimensions = map[Size]int{
	SizeSmall:  150,
	SizeMedium: 300,
	SizeLarge:  600,
}

const (
	maxThumbnailSourceDimension = 10000
	maxThumbnailSourcePixels    = int64(50000000)
)

// SupportedExtensions lists image extensions that can be thumbnailed
var SupportedExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".webp": true,
	".bmp":  true,
	".tiff": true,
	".tif":  true,
}

// Service provides thumbnail generation and caching
type Service struct {
	cacheDir string
	mu       sync.RWMutex
	// In-progress generation to prevent duplicate work
	inProgress map[string]*thumbnailGenerationResult
	ipMu       sync.Mutex
}

type thumbnailGenerationResult struct {
	done chan struct{}
	data []byte
	err  error
}

// NewService creates a new thumbnail service
func NewService(cacheDir string) (*Service, error) {
	// Create cache directory if it doesn't exist
	if err := ensureThumbnailDir(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache dir: %w", err)
	}

	return &Service{
		cacheDir:   cacheDir,
		inProgress: make(map[string]*thumbnailGenerationResult),
	}, nil
}

// IsSupportedImage checks if a file extension is supported for thumbnail generation
func IsSupportedImage(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return SupportedExtensions[ext]
}

// GetThumbnail returns a thumbnail for the given file, generating if needed
func (s *Service) GetThumbnail(ctx context.Context, filePath string, size Size, reader io.ReadSeeker) ([]byte, error) {
	if size == "" {
		size = SizeMedium
	}

	dim, ok := SizeDimensions[size]
	if !ok {
		dim = SizeDimensions[SizeMedium]
	}

	// Generate cache key based on file path and size
	cacheKey := s.cacheKey(filePath, size)
	cachePath := s.cachePath(cacheKey)

	// Check cache first
	if data, err := s.loadFromCache(cachePath); err == nil {
		return data, nil
	}

	// Check if generation is already in progress
	s.ipMu.Lock()
	if result, ok := s.inProgress[cacheKey]; ok {
		s.ipMu.Unlock()
		// Wait for in-progress generation
		select {
		case <-result.done:
			if result.err != nil {
				return nil, result.err
			}
			if len(result.data) > 0 {
				return append([]byte(nil), result.data...), nil
			}
			// Fallback for older or incomplete in-progress state.
			if data, err := s.loadFromCache(cachePath); err == nil {
				return data, nil
			}
			return nil, fmt.Errorf("thumbnail generation failed")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Mark generation as in-progress
	result := &thumbnailGenerationResult{done: make(chan struct{})}
	s.inProgress[cacheKey] = result
	s.ipMu.Unlock()

	defer func() {
		s.ipMu.Lock()
		delete(s.inProgress, cacheKey)
		close(result.done)
		s.ipMu.Unlock()
	}()

	// Generate thumbnail
	data, err := s.generate(ctx, reader, dim)
	if err != nil {
		result.err = err
		return nil, err
	}
	result.data = append([]byte(nil), data...)

	// Save to cache (async, don't block return)
	go func() {
		if err := s.saveToCache(cachePath, data); err != nil {
			log.Printf("thumbnail: failed to save cache for %s: %v", filePath, err)
		}
	}()

	return data, nil
}

// generate creates a thumbnail from the source image
func (s *Service) generate(ctx context.Context, reader io.ReadSeeker, size int) ([]byte, error) {
	// Check context before starting expensive operation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if err := validateThumbnailSourceBounds(reader); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Reset reader to start
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek: %w", err)
	}

	// Decode source image
	img, format, err := image.Decode(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	// Check context after decode (expensive operation)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Resize image maintaining aspect ratio
	// Fill mode: resize to cover the bounding box, then crop center
	thumb := imaging.Fill(img, size, size, imaging.Center, imaging.Lanczos)

	// Encode to JPEG for smaller size (unless original is PNG with transparency)
	var buf bytes.Buffer
	if format == "png" {
		// Check if image has alpha channel
		if hasAlpha(img) {
			if err := png.Encode(&buf, thumb); err != nil {
				return nil, fmt.Errorf("failed to encode PNG: %w", err)
			}
		} else {
			if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 85}); err != nil {
				return nil, fmt.Errorf("failed to encode JPEG: %w", err)
			}
		}
	} else {
		if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 85}); err != nil {
			return nil, fmt.Errorf("failed to encode JPEG: %w", err)
		}
	}

	return buf.Bytes(), nil
}

func validateThumbnailSourceBounds(reader io.ReadSeeker) error {
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	config, _, err := image.DecodeConfig(reader)
	if err != nil {
		return fmt.Errorf("failed to decode image config: %w", err)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	pixels := int64(config.Width) * int64(config.Height)
	if config.Width > maxThumbnailSourceDimension || config.Height > maxThumbnailSourceDimension || pixels > maxThumbnailSourcePixels {
		return fmt.Errorf(
			"%w: %dx%d exceeds %dx%d/%dpx limit",
			ErrThumbnailSourceTooLarge,
			config.Width,
			config.Height,
			maxThumbnailSourceDimension,
			maxThumbnailSourceDimension,
			maxThumbnailSourcePixels,
		)
	}

	return nil
}

// cacheKey generates a unique cache key for a file+size combination
func (s *Service) cacheKey(filePath string, size Size) string {
	h := md5.New()
	h.Write([]byte(filePath))
	h.Write([]byte(size))
	return hex.EncodeToString(h.Sum(nil))
}

// cachePath returns the full path for a cached thumbnail
func (s *Service) cachePath(cacheKey string) string {
	// Use two-level directory structure to avoid too many files in one dir
	return filepath.Join(s.cacheDir, cacheKey[:2], cacheKey[2:4], cacheKey+".jpg")
}

// loadFromCache attempts to load a cached thumbnail
func (s *Service) loadFromCache(cachePath string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := validateThumbnailCachePath(cachePath); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// saveToCache saves a thumbnail to the cache
func (s *Service) saveToCache(cachePath string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateThumbnailCachePath(cachePath); err != nil {
		return err
	}

	// Create directory if needed
	dir := filepath.Dir(cachePath)
	if err := ensureThumbnailDir(dir, 0755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, ".thumbnail-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0644); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, cachePath); err != nil {
		return err
	}
	cleanup = false
	if err := syncThumbnailCacheDir(dir); err != nil {
		return fmt.Errorf("failed to sync thumbnail cache directory: %w", err)
	}
	return nil
}

func collectMissingThumbnailDirs(dir string) ([]string, error) {
	missing := make([]string, 0)
	current := filepath.Clean(dir)
	for {
		if _, err := os.Stat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return missing, nil
}

func syncCreatedThumbnailDirs(createdDirs []string) error {
	for i := len(createdDirs) - 1; i >= 0; i-- {
		if err := syncThumbnailCacheDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync thumbnail cache directory tree: %w", err)
		}
	}
	return nil
}

func ensureThumbnailDir(dir string, perm os.FileMode) error {
	createdDirs, err := collectMissingThumbnailDirs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	return syncCreatedThumbnailDirs(createdDirs)
}

func syncThumbnailDir(dir string) error {
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func validateThumbnailCachePath(path string) error {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return fmt.Errorf("failed to resolve thumbnail cache path: %w", err)
		}
		cleaned = absPath
	}

	root := filepath.VolumeName(cleaned) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(cleaned, root)
	if trimmed == "" {
		info, err := os.Lstat(cleaned)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errThumbnailCacheSymlink
		}
		return nil
	}

	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errThumbnailCacheSymlink
		}
	}
	return nil
}

// CleanCache removes cached thumbnails older than maxAge
func (s *Service) CleanCache(ctx context.Context, maxAge time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	var count int

	err := filepath.Walk(s.cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !info.IsDir() && info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err == nil {
				count++
			}
		}
		return nil
	})

	return count, err
}

// CacheStats returns cache statistics
func (s *Service) CacheStats(ctx context.Context) (count int, size int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	err = filepath.Walk(s.cacheDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !info.IsDir() {
			count++
			size += info.Size()
		}
		return nil
	})

	return
}

// InvalidateCache removes cached thumbnails for a specific file
func (s *Service) InvalidateCache(filePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var removeErr error

	// Remove all size variants
	for size := range SizeDimensions {
		cacheKey := s.cacheKey(filePath, size)
		cachePath := s.cachePath(cacheKey)
		if err := os.Remove(cachePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErr = errors.Join(removeErr, fmt.Errorf("remove thumbnail cache %q: %w", cachePath, err))
		}
	}

	return removeErr
}

// hasAlpha checks if an image has an alpha channel
func hasAlpha(img image.Image) bool {
	switch img.(type) {
	case *image.NRGBA, *image.NRGBA64, *image.RGBA, *image.RGBA64:
		return true
	default:
		return false
	}
}
