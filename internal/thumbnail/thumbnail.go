// Package thumbnail provides image thumbnail generation and caching
package thumbnail

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"image"
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
)

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
	inProgress map[string]chan struct{}
	ipMu       sync.Mutex
}

// NewService creates a new thumbnail service
func NewService(cacheDir string) (*Service, error) {
	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache dir: %w", err)
	}

	return &Service{
		cacheDir:   cacheDir,
		inProgress: make(map[string]chan struct{}),
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
	if ch, ok := s.inProgress[cacheKey]; ok {
		s.ipMu.Unlock()
		// Wait for in-progress generation
		select {
		case <-ch:
			// Generation complete, try cache again
			if data, err := s.loadFromCache(cachePath); err == nil {
				return data, nil
			}
			return nil, fmt.Errorf("thumbnail generation failed")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Mark generation as in-progress
	ch := make(chan struct{})
	s.inProgress[cacheKey] = ch
	s.ipMu.Unlock()

	defer func() {
		s.ipMu.Lock()
		delete(s.inProgress, cacheKey)
		close(ch)
		s.ipMu.Unlock()
	}()

	// Generate thumbnail
	data, err := s.generate(ctx, reader, dim)
	if err != nil {
		return nil, err
	}

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

	// Create directory if needed
	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Write atomically using temp file
	tmpPath := cachePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, cachePath)
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

	// Remove all size variants
	for size := range SizeDimensions {
		cacheKey := s.cacheKey(filePath, size)
		cachePath := s.cachePath(cacheKey)
		os.Remove(cachePath) // Ignore errors
	}

	return nil
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
