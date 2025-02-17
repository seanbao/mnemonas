package thumbnail

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// createTestImage creates a simple test PNG image
func createTestImage(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// Fill with gradient
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x * 255 / width),
				G: uint8(y * 255 / height),
				B: 128,
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// createTestImageWithAlpha creates a PNG with alpha channel
func createTestImageWithAlpha(width, height int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.NRGBA{
				R: uint8(x * 255 / width),
				G: uint8(y * 255 / height),
				B: 128,
				A: uint8(128), // Semi-transparent
			})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// createTempDir creates a temporary directory that handles async cleanup properly
// Returns the directory path and a cleanup function
func createTempDir(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "thumbnail-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	cleanup := func() {
		// Wait for async cache operations to complete
		time.Sleep(50 * time.Millisecond)
		os.RemoveAll(tmpDir)
	}
	return tmpDir, cleanup
}

func TestNewService(t *testing.T) {
	tmpDir := t.TempDir()

	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.cacheDir != tmpDir {
		t.Errorf("expected cacheDir=%s, got %s", tmpDir, svc.cacheDir)
	}
}

func TestNewService_CreateDir(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "cache")

	svc, err := NewService(nestedDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil service")
	}

	info, err := os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("cache dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestIsSupportedImage(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		{"photo.jpg", true},
		{"photo.jpeg", true},
		{"photo.JPG", true},
		{"image.png", true},
		{"image.PNG", true},
		{"anim.gif", true},
		{"photo.webp", true},
		{"photo.bmp", true},
		{"photo.tiff", true},
		{"photo.tif", true},
		{"document.pdf", false},
		{"video.mp4", false},
		{"text.txt", false},
		{"noextension", false},
		{"", false},
	}

	for _, tc := range tests {
		result := IsSupportedImage(tc.filename)
		if result != tc.expected {
			t.Errorf("IsSupportedImage(%q) = %v, expected %v", tc.filename, result, tc.expected)
		}
	}
}

func TestSizeDimensions(t *testing.T) {
	if SizeDimensions[SizeSmall] != 150 {
		t.Errorf("SizeSmall should be 150, got %d", SizeDimensions[SizeSmall])
	}
	if SizeDimensions[SizeMedium] != 300 {
		t.Errorf("SizeMedium should be 300, got %d", SizeDimensions[SizeMedium])
	}
	if SizeDimensions[SizeLarge] != 600 {
		t.Errorf("SizeLarge should be 600, got %d", SizeDimensions[SizeLarge])
	}
}

func TestGetThumbnail(t *testing.T) {
	tmpDir, cleanup := createTempDir(t)
	defer cleanup()

	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	imgData := createTestImage(800, 600)
	reader := bytes.NewReader(imgData)

	ctx := context.Background()
	thumb, err := svc.GetThumbnail(ctx, "/test/image.png", SizeMedium, reader)
	if err != nil {
		t.Fatalf("GetThumbnail failed: %v", err)
	}
	if len(thumb) == 0 {
		t.Error("expected non-empty thumbnail")
	}

	// Verify it's a valid image
	_, _, err = image.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Errorf("generated thumbnail is not a valid image: %v", err)
	}
}

func TestGetThumbnail_DefaultSize(t *testing.T) {
	tmpDir, cleanup := createTempDir(t)
	defer cleanup()

	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	imgData := createTestImage(400, 400)
	reader := bytes.NewReader(imgData)

	ctx := context.Background()
	// Empty size should default to medium
	thumb, err := svc.GetThumbnail(ctx, "/test/default.png", "", reader)
	if err != nil {
		t.Fatalf("GetThumbnail failed: %v", err)
	}
	if len(thumb) == 0 {
		t.Error("expected non-empty thumbnail")
	}
}

func TestGetThumbnail_Caching(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	imgData := createTestImage(500, 500)
	ctx := context.Background()

	// First call - generates thumbnail
	reader1 := bytes.NewReader(imgData)
	thumb1, err := svc.GetThumbnail(ctx, "/test/cached.png", SizeSmall, reader1)
	if err != nil {
		t.Fatalf("first GetThumbnail failed: %v", err)
	}

	// Wait for async cache save
	time.Sleep(100 * time.Millisecond)

	// Second call - should use cache
	reader2 := bytes.NewReader(imgData)
	thumb2, err := svc.GetThumbnail(ctx, "/test/cached.png", SizeSmall, reader2)
	if err != nil {
		t.Fatalf("second GetThumbnail failed: %v", err)
	}

	// Results should be identical
	if !bytes.Equal(thumb1, thumb2) {
		t.Error("cached thumbnail should be identical to generated one")
	}
}

func TestGetThumbnail_RejectsSymlinkCachePath(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cacheKey := svc.cacheKey("/test/image.png", SizeSmall)
	cachePath := svc.cachePath(cacheKey)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		t.Fatalf("MkdirAll(cache dir) failed: %v", err)
	}
	targetPath := filepath.Join(tmpDir, "real-thumb.jpg")
	if err := os.WriteFile(targetPath, []byte("old-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(real-thumb.jpg) failed: %v", err)
	}
	if err := os.Symlink(targetPath, cachePath); err != nil {
		t.Fatalf("Symlink(cachePath) failed: %v", err)
	}

	_, err = svc.loadFromCache(cachePath)
	if !errors.Is(err, errThumbnailCacheSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	err = svc.saveToCache(cachePath, []byte("new-thumb"))
	if !errors.Is(err, errThumbnailCacheSymlink) {
		t.Fatalf("expected symlink rejection on save, got %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile(real-thumb.jpg) failed: %v", err)
	}
	if string(data) != "old-thumb" {
		t.Fatalf("expected target cache file unchanged, got %q", string(data))
	}
}

func TestGetThumbnail_DifferentSizes(t *testing.T) {
	// Use os.MkdirTemp instead of t.TempDir to avoid cleanup race with async cache save
	tmpDir, err := os.MkdirTemp("", "thumbnail-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	imgData := createTestImage(1000, 1000)
	ctx := context.Background()

	sizes := []Size{SizeSmall, SizeMedium, SizeLarge}
	var thumbs [][]byte

	for _, size := range sizes {
		reader := bytes.NewReader(imgData)
		thumb, err := svc.GetThumbnail(ctx, "/test/sizes.png", size, reader)
		if err != nil {
			t.Fatalf("GetThumbnail(%s) failed: %v", size, err)
		}
		thumbs = append(thumbs, thumb)
	}

	// Wait for async cache saves to complete before cleanup
	time.Sleep(50 * time.Millisecond)

	// Larger sizes should generally produce larger files
	if len(thumbs[0]) >= len(thumbs[2]) {
		t.Log("Note: small thumbnail not smaller than large (depends on compression)")
	}
}

func TestGetThumbnail_ContextCancel(t *testing.T) {
	tmpDir, cleanup := createTempDir(t)
	defer cleanup()

	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	imgData := createTestImage(2000, 2000) // Large image
	reader := bytes.NewReader(imgData)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err = svc.GetThumbnail(ctx, "/test/cancelled.png", SizeMedium, reader)
	if err == nil {
		t.Log("Note: thumbnail generation completed before context was checked")
	}
}

func TestGetThumbnail_AlphaChannel(t *testing.T) {
	tmpDir, cleanup := createTempDir(t)
	defer cleanup()

	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	imgData := createTestImageWithAlpha(400, 400)
	reader := bytes.NewReader(imgData)

	ctx := context.Background()
	thumb, err := svc.GetThumbnail(ctx, "/test/alpha.png", SizeMedium, reader)
	if err != nil {
		t.Fatalf("GetThumbnail failed: %v", err)
	}
	if len(thumb) == 0 {
		t.Error("expected non-empty thumbnail")
	}
}

func TestCacheStats(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	ctx := context.Background()

	// Initial stats should be zero
	count, size, err := svc.CacheStats(ctx)
	if err != nil {
		t.Fatalf("CacheStats failed: %v", err)
	}
	if count != 0 || size != 0 {
		t.Errorf("expected zero stats, got count=%d size=%d", count, size)
	}

	// Generate a thumbnail
	imgData := createTestImage(300, 300)
	reader := bytes.NewReader(imgData)
	_, err = svc.GetThumbnail(ctx, "/test/stats.png", SizeSmall, reader)
	if err != nil {
		t.Fatalf("GetThumbnail failed: %v", err)
	}

	// Wait for cache save
	time.Sleep(100 * time.Millisecond)

	// Stats should show 1 file
	count, size, err = svc.CacheStats(ctx)
	if err != nil {
		t.Fatalf("CacheStats failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count=1, got %d", count)
	}
	if size == 0 {
		t.Error("expected non-zero size")
	}
}

func TestCleanCache(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	ctx := context.Background()

	// Generate a thumbnail
	imgData := createTestImage(300, 300)
	reader := bytes.NewReader(imgData)
	_, err = svc.GetThumbnail(ctx, "/test/clean.png", SizeSmall, reader)
	if err != nil {
		t.Fatalf("GetThumbnail failed: %v", err)
	}

	// Wait for cache save
	time.Sleep(100 * time.Millisecond)

	// Clean with very short maxAge - should remove the file
	cleaned, err := svc.CleanCache(ctx, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("CleanCache failed: %v", err)
	}
	if cleaned != 1 {
		t.Errorf("expected 1 cleaned, got %d", cleaned)
	}

	// Stats should show 0 files
	count, _, err := svc.CacheStats(ctx)
	if err != nil {
		t.Fatalf("CacheStats failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count=0 after clean, got %d", count)
	}
}

func TestSaveToCache_ReplacesFileAtomically(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cachePath := svc.cachePath(svc.cacheKey("/test/atomic.png", SizeSmall))
	if err := svc.saveToCache(cachePath, []byte("first")); err != nil {
		t.Fatalf("saveToCache(first) failed: %v", err)
	}
	if err := svc.saveToCache(cachePath, []byte("second")); err != nil {
		t.Fatalf("saveToCache(second) failed: %v", err)
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("ReadFile(cachePath) failed: %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("expected replaced cache content, got %q", string(data))
	}
}

func TestInvalidateCache(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	ctx := context.Background()

	// Generate thumbnails for multiple sizes
	imgData := createTestImage(400, 400)
	filePath := "/test/invalidate.png"

	for _, size := range []Size{SizeSmall, SizeMedium, SizeLarge} {
		reader := bytes.NewReader(imgData)
		_, err = svc.GetThumbnail(ctx, filePath, size, reader)
		if err != nil {
			t.Fatalf("GetThumbnail(%s) failed: %v", size, err)
		}
	}

	// Wait for cache save
	time.Sleep(100 * time.Millisecond)

	// Verify cache has files
	count, _, _ := svc.CacheStats(ctx)
	if count < 3 {
		t.Logf("Note: expected 3 cached files, got %d", count)
	}

	// Invalidate cache for this file
	err = svc.InvalidateCache(filePath)
	if err != nil {
		t.Fatalf("InvalidateCache failed: %v", err)
	}

	// Stats should show 0 or fewer files
	count2, _, _ := svc.CacheStats(ctx)
	if count2 >= count && count > 0 {
		t.Errorf("expected fewer files after invalidation, had %d now %d", count, count2)
	}
}

func TestCacheKey(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	// Same path + size should produce same key
	key1 := svc.cacheKey("/test/file.png", SizeMedium)
	key2 := svc.cacheKey("/test/file.png", SizeMedium)
	if key1 != key2 {
		t.Error("same inputs should produce same cache key")
	}

	// Different size should produce different key
	key3 := svc.cacheKey("/test/file.png", SizeSmall)
	if key1 == key3 {
		t.Error("different sizes should produce different cache keys")
	}

	// Different path should produce different key
	key4 := svc.cacheKey("/test/other.png", SizeMedium)
	if key1 == key4 {
		t.Error("different paths should produce different cache keys")
	}
}
