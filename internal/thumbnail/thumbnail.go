// Package thumbnail provides image thumbnail generation and caching
package thumbnail

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
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
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/seanbao/mnemonas/internal/rootio"
)

var errThumbnailCacheSymlink = errors.New("thumbnail cache path must not be a symlink")
var syncThumbnailCacheDir = syncThumbnailDir
var syncThumbnailCacheRootDir = syncThumbnailRootDir
var createThumbnailCacheTempFile = createThumbnailTempFile
var onThumbnailInProgressExpired = func(string) {}
var walkThumbnailCache = func(cacheDir string, cacheRoot *os.Root, walkFn thumbnailWalkFunc) error {
	if cacheRoot != nil {
		return walkThumbnailCacheWithRoot(cacheRoot, ".", walkFn)
	}

	return filepath.Walk(cacheDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relPath, err := filepath.Rel(cacheDir, path)
		if err != nil {
			return err
		}
		return walkFn(filepath.Clean(relPath), info)
	})
}
var afterValidateThumbnailCachePath = func() {}
var ErrThumbnailSourceTooLarge = errors.New("source image too large for thumbnail generation")

const thumbnailRootEscapeError = "path escapes from parent"

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
	failedCacheReuseTTL         = 30 * time.Second
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
	cacheDir  string
	cacheRoot *os.Root
	mu        sync.RWMutex
	// In-progress generation to prevent duplicate work
	inProgress          map[string]*thumbnailGenerationResult
	ipMu                sync.Mutex
	saveWG              sync.WaitGroup
	failedCacheReuseTTL time.Duration
}

type thumbnailGenerationResult struct {
	done chan struct{}
	data []byte
	err  error
}

type thumbnailWalkFunc func(relPath string, info os.FileInfo) error

func (s *Service) waitForInProgressThumbnail(ctx context.Context, cachePath string, result *thumbnailGenerationResult) ([]byte, error) {
	select {
	case <-result.done:
		if result.err != nil {
			return nil, result.err
		}
		if len(result.data) > 0 {
			return append([]byte(nil), result.data...), nil
		}
		if data, err := s.loadFromCache(cachePath); err == nil {
			return data, nil
		}
		return nil, fmt.Errorf("thumbnail generation failed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) clearInProgress(cacheKey string, result *thumbnailGenerationResult) {
	s.ipMu.Lock()
	defer s.ipMu.Unlock()
	if current, ok := s.inProgress[cacheKey]; ok && current == result {
		delete(s.inProgress, cacheKey)
	}
}

func (s *Service) scheduleInProgressExpiry(cacheKey string, result *thumbnailGenerationResult, ttl time.Duration) {
	if ttl <= 0 {
		s.clearInProgress(cacheKey, result)
		onThumbnailInProgressExpired(cacheKey)
		return
	}
	time.AfterFunc(ttl, func() {
		s.clearInProgress(cacheKey, result)
		onThumbnailInProgressExpired(cacheKey)
	})
}

// Wait blocks until background cache persistence work has finished.
func (s *Service) Wait() {
	s.saveWG.Wait()
}

// NewService creates a new thumbnail service
func NewService(cacheDir string) (*Service, error) {
	normalizedCacheDir, cacheRoot, err := ensureThumbnailCacheRoot(cacheDir)
	if err != nil {
		return nil, err
	}

	return &Service{
		cacheDir:            normalizedCacheDir,
		cacheRoot:           cacheRoot,
		inProgress:          make(map[string]*thumbnailGenerationResult),
		failedCacheReuseTTL: failedCacheReuseTTL,
	}, nil
}

// IsSupportedImage checks if a file extension is supported for thumbnail generation
func IsSupportedImage(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return SupportedExtensions[ext]
}

// GetThumbnail returns a thumbnail for the given file, generating if needed
func (s *Service) GetThumbnail(ctx context.Context, filePath string, size Size, reader io.ReadSeeker) ([]byte, error) {
	return s.getThumbnail(ctx, filePath, "", size, reader)
}

// GetThumbnailVersioned returns a thumbnail keyed by both file path and a
// caller-provided content signature so stale cache entries are bypassed when
// the underlying file content changes.
func (s *Service) GetThumbnailVersioned(ctx context.Context, filePath, cacheVersion string, size Size, reader io.ReadSeeker) ([]byte, error) {
	return s.getThumbnail(ctx, filePath, cacheVersion, size, reader)
}

func (s *Service) getThumbnail(ctx context.Context, filePath, cacheVersion string, size Size, reader io.ReadSeeker) ([]byte, error) {
	if size == "" {
		size = SizeMedium
	}

	dim, ok := SizeDimensions[size]
	if !ok {
		dim = SizeDimensions[SizeMedium]
	}

	// Generate cache key based on file path and size
	cacheKey := s.cacheKeyForVersion(filePath, cacheVersion, size)
	cachePath := s.cachePath(cacheKey)

	// Check if generation is already in progress
	s.ipMu.Lock()
	if result, ok := s.inProgress[cacheKey]; ok {
		s.ipMu.Unlock()
		return s.waitForInProgressThumbnail(ctx, cachePath, result)
	}
	s.ipMu.Unlock()

	// Check cache before scheduling new generation.
	if data, err := s.loadFromCache(cachePath); err == nil {
		return data, nil
	}

	// Another request may have started generating while the cache lookup was in progress.
	s.ipMu.Lock()
	if result, ok := s.inProgress[cacheKey]; ok {
		s.ipMu.Unlock()
		return s.waitForInProgressThumbnail(ctx, cachePath, result)
	}

	// Mark generation as in-progress
	result := &thumbnailGenerationResult{done: make(chan struct{})}
	s.inProgress[cacheKey] = result
	s.ipMu.Unlock()

	cleanupInProgress := true
	defer func() {
		if !cleanupInProgress {
			return
		}
		s.clearInProgress(cacheKey, result)
		close(result.done)
	}()

	// Generate thumbnail
	data, err := s.generate(ctx, reader, dim)
	if err != nil {
		result.err = err
		return nil, err
	}
	result.data = append([]byte(nil), data...)

	s.ipMu.Lock()
	close(result.done)
	s.ipMu.Unlock()
	cleanupInProgress = false

	// Keep the completed result available to concurrent callers until cache persistence finishes.
	s.saveWG.Add(1)
	go func() {
		defer s.saveWG.Done()
		if err := s.saveToCache(cachePath, data); err != nil {
			log.Printf("thumbnail: failed to save cache for %s: %v", filePath, err)
			s.scheduleInProgressExpiry(cacheKey, result, s.failedCacheReuseTTL)
			return
		}
		s.clearInProgress(cacheKey, result)
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
	return s.cacheKeyForVersion(filePath, "", size)
}

func (s *Service) cacheKeyForVersion(filePath, cacheVersion string, size Size) string {
	h := md5.New()
	h.Write([]byte(filePath))
	h.Write([]byte{0})
	h.Write([]byte(cacheVersion))
	h.Write([]byte{0})
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
	afterValidateThumbnailCachePath()
	if s.cacheRoot != nil {
		relPath, err := s.cacheRelativePath(cachePath)
		if err != nil {
			return nil, err
		}

		cacheFile, err := rootio.OpenFileNoFollow(s.cacheRoot, relPath, os.O_RDONLY, 0)
		if err != nil {
			return nil, mapThumbnailRootPathError(err)
		}
		defer cacheFile.Close()

		data, err := io.ReadAll(cacheFile)
		if err != nil {
			return nil, err
		}
		return data, nil
	}

	cacheFile, err := rootio.OpenFilePathNoFollow(cachePath, os.O_RDONLY, 0)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return nil, errThumbnailCacheSymlink
		}
		return nil, err
	}
	defer cacheFile.Close()

	data, err := io.ReadAll(cacheFile)
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
	afterValidateThumbnailCachePath()

	if s.cacheRoot != nil {
		relPath, err := s.cacheRelativePath(cachePath)
		if err != nil {
			return err
		}
		relDir := filepath.Dir(relPath)
		createdDirs, err := ensureThumbnailDirWithRoot(s.cacheRoot, relDir, 0755)
		if err != nil {
			return err
		}

		tmpFile, tmpPath, err := createThumbnailCacheTempFile(s.cacheRoot, relDir)
		if err != nil {
			cleanupCreatedThumbnailDirsWithRoot(s.cacheRoot, createdDirs)
			return err
		}
		cleanup := true
		defer func() {
			if cleanup {
				_ = s.cacheRoot.Remove(tmpPath)
			}
		}()

		if err := tmpFile.Chmod(0644); err != nil {
			_ = tmpFile.Close()
			cleanupCreatedThumbnailDirsWithRoot(s.cacheRoot, createdDirs)
			return cleanupThumbnailTempPath(s.cacheRoot, tmpPath, err)
		}
		if _, err := tmpFile.Write(data); err != nil {
			_ = tmpFile.Close()
			cleanupCreatedThumbnailDirsWithRoot(s.cacheRoot, createdDirs)
			return cleanupThumbnailTempPath(s.cacheRoot, tmpPath, err)
		}
		if err := tmpFile.Sync(); err != nil {
			_ = tmpFile.Close()
			cleanupCreatedThumbnailDirsWithRoot(s.cacheRoot, createdDirs)
			return cleanupThumbnailTempPath(s.cacheRoot, tmpPath, err)
		}
		if err := tmpFile.Close(); err != nil {
			cleanupCreatedThumbnailDirsWithRoot(s.cacheRoot, createdDirs)
			return cleanupThumbnailTempPath(s.cacheRoot, tmpPath, err)
		}
		if err := s.cacheRoot.Rename(tmpPath, relPath); err != nil {
			cleanupCreatedThumbnailDirsWithRoot(s.cacheRoot, createdDirs)
			return cleanupThumbnailTempPath(s.cacheRoot, tmpPath, mapThumbnailRootPathError(err))
		}
		cleanup = false
		if err := syncThumbnailCacheRootDir(s.cacheRoot, relDir); err != nil {
			return fmt.Errorf("failed to sync thumbnail cache directory: %w", err)
		}
		return nil
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

func (s *Service) cacheRelativePath(cachePath string) (string, error) {
	cleanedPath, err := normalizeThumbnailCachePath(cachePath)
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(s.cacheDir, cleanedPath)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", errThumbnailCacheSymlink
	}
	return filepath.Clean(relPath), nil
}

func syncCreatedThumbnailDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncThumbnailCacheDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync thumbnail cache directory tree: %w", err)
		}
	}
	return nil
}

func ensureThumbnailDir(dir string, perm os.FileMode) error {
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return errThumbnailCacheSymlink
		}
		return err
	}
	return syncCreatedThumbnailDirs(createdDirs)
}

func syncCreatedThumbnailDirsWithRoot(root *os.Root, createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncThumbnailCacheRootDir(root, filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync thumbnail cache directory tree: %w", err)
		}
	}
	return nil
}

func ensureThumbnailDirWithRoot(root *os.Root, dir string, perm os.FileMode) ([]string, error) {
	if dir == "." || dir == "" {
		return nil, nil
	}

	createdDirs, err := rootio.MkdirAllNoFollowTracked(root, dir, perm)
	if err != nil {
		return createdDirs, mapThumbnailRootPathError(err)
	}
	return createdDirs, syncCreatedThumbnailDirsWithRoot(root, createdDirs)
}

func syncThumbnailDir(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func syncThumbnailRootDir(root *os.Root, dir string) error {
	if dir == "" {
		dir = "."
	}
	dirHandle, err := rootio.OpenDirNoFollow(root, dir)
	if err != nil {
		return mapThumbnailRootPathError(err)
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func createThumbnailTempFile(root *os.Root, dir string) (*os.File, string, error) {
	for range 32 {
		tmpName, err := newThumbnailTempName()
		if err != nil {
			return nil, "", err
		}
		tmpPath := filepath.Join(dir, tmpName)
		tmpFile, err := rootio.OpenFileNoFollow(root, tmpPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err == nil {
			return tmpFile, tmpPath, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapThumbnailRootPathError(err)
	}
	return nil, "", errors.New("failed to allocate unique thumbnail temp file")
}

func newThumbnailTempName() (string, error) {
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return ".thumbnail-" + hex.EncodeToString(randomBytes) + ".tmp", nil
}

func cleanupThumbnailTempPath(root *os.Root, path string, err error) error {
	if removeErr := root.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(err, fmt.Errorf("cleanup temp thumbnail %s: %w", path, removeErr))
	}
	return err
}

func cleanupCreatedThumbnailDirsWithRoot(root *os.Root, createdDirs []string) {
	for _, dir := range createdDirs {
		if dir == "." {
			continue
		}
		if err := root.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			break
		}
	}
}

func thumbnailChildRelativePath(parent, child string) string {
	if parent == "." {
		return child
	}
	return filepath.Join(parent, child)
}

func safeThumbnailCacheEntryName(name string) (string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.Contains(normalized, "/") || strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return "", errThumbnailCacheSymlink
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return "", errThumbnailCacheSymlink
		}
	}
	return name, nil
}

func walkThumbnailCacheWithRoot(root *os.Root, relPath string, walkFn thumbnailWalkFunc) error {
	info, err := root.Lstat(relPath)
	if err != nil {
		return mapThumbnailRootPathError(err)
	}
	return walkThumbnailCacheEntryWithRoot(root, relPath, info, walkFn)
}

func walkThumbnailCacheEntryWithRoot(root *os.Root, relPath string, info os.FileInfo, walkFn thumbnailWalkFunc) error {
	if err := walkFn(relPath, info); err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}

	dirHandle, err := rootio.OpenDirNoFollow(root, relPath)
	if err != nil {
		return mapThumbnailRootPathError(err)
	}
	defer dirHandle.Close()

	entries, err := dirHandle.ReadDir(-1)
	if err != nil {
		return mapThumbnailRootPathError(err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		entryName, err := safeThumbnailCacheEntryName(entry.Name())
		if err != nil {
			return err
		}
		childRelPath := thumbnailChildRelativePath(relPath, entryName)
		childInfo, err := root.Lstat(childRelPath)
		if err != nil {
			return mapThumbnailRootPathError(err)
		}
		if err := walkThumbnailCacheEntryWithRoot(root, childRelPath, childInfo, walkFn); err != nil {
			return err
		}
	}

	return nil
}

func mapThumbnailRootPathError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ELOOP) || rootio.IsSymlinkError(err) || isThumbnailRootEscapeError(err) {
		return errThumbnailCacheSymlink
	}
	return err
}

func isThumbnailRootEscapeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), thumbnailRootEscapeError)
}

func normalizeThumbnailCachePath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve thumbnail cache path: %w", err)
	}
	return absPath, nil
}

func ensureThumbnailCacheRoot(cacheDir string) (string, *os.Root, error) {
	normalizedCacheDir, err := normalizeThumbnailCachePath(cacheDir)
	if err != nil {
		return "", nil, err
	}
	if err := validateThumbnailCachePath(normalizedCacheDir); err != nil {
		return "", nil, err
	}
	if err := ensureThumbnailDir(normalizedCacheDir, 0755); err != nil {
		return "", nil, fmt.Errorf("failed to create cache dir: %w", err)
	}
	cacheRoot, err := os.OpenRoot(normalizedCacheDir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open thumbnail cache root: %w", mapThumbnailRootPathError(err))
	}
	return normalizedCacheDir, cacheRoot, nil
}

func validateThumbnailCachePath(path string) error {
	cleaned, err := normalizeThumbnailCachePath(path)
	if err != nil {
		return err
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

	if s.cacheRoot == nil {
		if err := validateThumbnailCachePath(s.cacheDir); err != nil {
			return 0, err
		}
	}
	afterValidateThumbnailCachePath()
	if s.cacheRoot == nil {
		if err := validateThumbnailCachePath(s.cacheDir); err != nil {
			return 0, err
		}
	}

	cutoff := time.Now().Add(-maxAge)
	var count int

	err := walkThumbnailCache(s.cacheDir, s.cacheRoot, func(relPath string, info os.FileInfo) error {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !info.IsDir() && info.ModTime().Before(cutoff) {
			if s.cacheRoot != nil {
				if err := s.cacheRoot.Remove(relPath); err != nil {
					if !errors.Is(err, os.ErrNotExist) {
						return fmt.Errorf("remove thumbnail cache %q: %w", relPath, mapThumbnailRootPathError(err))
					}
					return nil
				}
				count++
				return nil
			}

			absPath := s.cacheDir
			if relPath != "." {
				absPath = filepath.Join(s.cacheDir, relPath)
			}
			if err := os.Remove(absPath); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("remove thumbnail cache %q: %w", absPath, err)
				}
				return nil
			}
			count++
		}
		return nil
	})

	return count, err
}

// CacheStats returns cache statistics
func (s *Service) CacheStats(ctx context.Context) (count int, size int64, err error) {
	s.mu.RLock()
	cacheDir := s.cacheDir
	cacheRoot := s.cacheRoot
	s.mu.RUnlock()

	if cacheRoot == nil {
		if err := validateThumbnailCachePath(cacheDir); err != nil {
			return 0, 0, err
		}
	}
	afterValidateThumbnailCachePath()
	if cacheRoot == nil {
		if err := validateThumbnailCachePath(cacheDir); err != nil {
			return 0, 0, err
		}
	}

	err = walkThumbnailCache(cacheDir, cacheRoot, func(relPath string, info os.FileInfo) error {
		_ = relPath

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
		if s.cacheRoot != nil {
			relPath, err := s.cacheRelativePath(cachePath)
			if err != nil {
				removeErr = errors.Join(removeErr, fmt.Errorf("remove thumbnail cache %q: %w", cachePath, err))
				continue
			}
			if err := s.cacheRoot.Remove(relPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				removeErr = errors.Join(removeErr, fmt.Errorf("remove thumbnail cache %q: %w", cachePath, mapThumbnailRootPathError(err)))
			}
			continue
		}
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
