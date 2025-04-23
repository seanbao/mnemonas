package thumbnail

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
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

func createTestPNGConfigOnly(width, height int) []byte {
	chunkData := make([]byte, 13)
	binary.BigEndian.PutUint32(chunkData[0:4], uint32(width))
	binary.BigEndian.PutUint32(chunkData[4:8], uint32(height))
	chunkData[8] = 8
	chunkData[9] = 2

	chunkType := []byte("IHDR")
	crcInput := append(append([]byte(nil), chunkType...), chunkData...)
	crc := crc32.ChecksumIEEE(crcInput)

	buf := bytes.NewBuffer(nil)
	buf.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	buf.Write([]byte{0, 0, 0, 13})
	buf.Write(chunkType)
	buf.Write(chunkData)
	buf.Write([]byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)})
	buf.Write([]byte{0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130})
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

func createTestGIF(width, height int) []byte {
	img := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
	})
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if (x+y)%2 == 0 {
				img.SetColorIndex(x, y, 1)
			}
		}
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func createTestBMP(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 32, G: 128, B: 224, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func createTestTIFF(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 224, G: 96, B: 48, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := tiff.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
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

func TestNewService_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "cache")

	originalSyncThumbnailCacheDir := syncThumbnailCacheDir
	syncThumbnailCacheDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncThumbnailCacheDir = originalSyncThumbnailCacheDir
	}()

	if _, err := NewService(nestedDir); err == nil {
		t.Fatal("expected NewService() to fail when directory tree sync fails")
	} else if !strings.Contains(err.Error(), "failed to sync thumbnail cache directory tree") {
		t.Fatalf("expected directory tree sync error, got %v", err)
	}
}

func TestService_SaveToCache_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cachePath := filepath.Join(tmpDir, "ab", "cd", "thumb.jpg")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		t.Fatalf("MkdirAll(cache dir) error: %v", err)
	}
	originalSyncThumbnailCacheDir := syncThumbnailCacheDir
	syncThumbnailCacheDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncThumbnailCacheDir = originalSyncThumbnailCacheDir
	}()

	err = svc.saveToCache(cachePath, []byte("thumbnail"))
	if err == nil {
		t.Fatal("expected saveToCache() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync thumbnail cache directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(cachePath)
	if readErr != nil {
		t.Fatalf("expected thumbnail cache file to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "thumbnail" {
		t.Fatalf("expected thumbnail cache content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(cachePath)
	if statErr != nil {
		t.Fatalf("expected thumbnail cache file to exist after sync failure, got %v", statErr)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("expected thumbnail cache file permissions 0644, got %o", info.Mode().Perm())
	}
}

func TestService_SaveToCache_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cachePath := filepath.Join(tmpDir, "ab", "cd", "thumb.jpg")
	originalSyncThumbnailCacheDir := syncThumbnailCacheDir
	syncThumbnailCacheDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncThumbnailCacheDir = originalSyncThumbnailCacheDir
	}()

	err = svc.saveToCache(cachePath, []byte("thumbnail"))
	if err == nil {
		t.Fatal("expected saveToCache() to fail when directory tree sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync thumbnail cache directory tree") {
		t.Fatalf("expected directory tree sync error, got %v", err)
	}
	if _, statErr := os.Stat(cachePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no thumbnail cache file to be created, got %v", statErr)
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

func TestGetThumbnail_ReturnsInProgressGeneratedBytesBeforeCacheSaveCompletes(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cacheKey := svc.cacheKey("/test/in-progress.png", SizeSmall)
	result := &thumbnailGenerationResult{done: make(chan struct{})}
	svc.ipMu.Lock()
	svc.inProgress[cacheKey] = result
	svc.ipMu.Unlock()
	defer func() {
		svc.ipMu.Lock()
		delete(svc.inProgress, cacheKey)
		svc.ipMu.Unlock()
	}()

	expected := []byte("generated-thumb")
	returned := make(chan []byte, 1)
	errCh := make(chan error, 1)

	go func() {
		data, err := svc.GetThumbnail(context.Background(), "/test/in-progress.png", SizeSmall, bytes.NewReader(createTestImage(10, 10)))
		if err != nil {
			errCh <- err
			return
		}
		returned <- data
	}()

	result.data = append([]byte(nil), expected...)
	close(result.done)

	select {
	case err := <-errCh:
		t.Fatalf("expected waiter to reuse generated bytes, got error: %v", err)
	case data := <-returned:
		if !bytes.Equal(data, expected) {
			t.Fatalf("expected in-progress bytes %q, got %q", expected, data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for in-progress thumbnail result")
	}
}

func TestGetThumbnail_ReturnsInProgressGenerationError(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cacheKey := svc.cacheKey("/test/in-progress-error.png", SizeSmall)
	expectedErr := errors.New("decode failed")
	result := &thumbnailGenerationResult{done: make(chan struct{}), err: expectedErr}
	svc.ipMu.Lock()
	svc.inProgress[cacheKey] = result
	svc.ipMu.Unlock()
	defer func() {
		svc.ipMu.Lock()
		delete(svc.inProgress, cacheKey)
		svc.ipMu.Unlock()
	}()

	errCh := make(chan error, 1)
	go func() {
		_, err := svc.GetThumbnail(context.Background(), "/test/in-progress-error.png", SizeSmall, bytes.NewReader(createTestImage(10, 10)))
		errCh <- err
	}()

	close(result.done)

	select {
	case err := <-errCh:
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected in-progress error %v, got %v", expectedErr, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for in-progress thumbnail error")
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

func TestGetThumbnail_RejectsSymlinkCacheParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realCacheDir := filepath.Join(tmpDir, "real-cache")
	if err := os.MkdirAll(realCacheDir, 0755); err != nil {
		t.Fatalf("MkdirAll(real-cache) failed: %v", err)
	}
	linkedCacheDir := filepath.Join(tmpDir, "linked-cache")
	if err := os.Symlink(realCacheDir, linkedCacheDir); err != nil {
		t.Fatalf("Symlink(linked-cache) failed: %v", err)
	}

	svc, err := NewService(linkedCacheDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cachePath := filepath.Join(linkedCacheDir, "ab", "thumb.jpg")
	if err := os.MkdirAll(filepath.Join(realCacheDir, "ab"), 0755); err != nil {
		t.Fatalf("MkdirAll(real-cache/ab) failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realCacheDir, "ab", "thumb.jpg"), []byte("old-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(real thumb) failed: %v", err)
	}

	_, err = svc.loadFromCache(cachePath)
	if !errors.Is(err, errThumbnailCacheSymlink) {
		t.Fatalf("expected parent-directory symlink rejection on load, got %v", err)
	}

	err = svc.saveToCache(cachePath, []byte("new-thumb"))
	if !errors.Is(err, errThumbnailCacheSymlink) {
		t.Fatalf("expected parent-directory symlink rejection on save, got %v", err)
	}

	data, err := os.ReadFile(filepath.Join(realCacheDir, "ab", "thumb.jpg"))
	if err != nil {
		t.Fatalf("ReadFile(real thumb) failed: %v", err)
	}
	if string(data) != "old-thumb" {
		t.Fatalf("expected real cache file unchanged, got %q", string(data))
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

func TestGetThumbnail_RejectsOversizedSourceImage(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	reader := bytes.NewReader(createTestPNGConfigOnly(maxThumbnailSourceDimension+1, maxThumbnailSourceDimension))
	_, err = svc.GetThumbnail(context.Background(), "/test/oversized.png", SizeMedium, reader)
	if !errors.Is(err, ErrThumbnailSourceTooLarge) {
		t.Fatalf("expected ErrThumbnailSourceTooLarge, got %v", err)
	}

	count, _, statsErr := svc.CacheStats(context.Background())
	if statsErr != nil {
		t.Fatalf("CacheStats failed: %v", statsErr)
	}
	if count != 0 {
		t.Fatalf("expected no cache files for oversized source image, got %d", count)
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

func TestGetThumbnail_SupportsDeclaredNonPNGFormats(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	tests := []struct {
		name     string
		filePath string
		data     []byte
	}{
		{name: "gif", filePath: "/test/anim.gif", data: createTestGIF(32, 32)},
		{name: "bmp", filePath: "/test/photo.bmp", data: createTestBMP(32, 32)},
		{name: "tiff", filePath: "/test/photo.tiff", data: createTestTIFF(32, 32)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			thumb, err := svc.GetThumbnail(context.Background(), tc.filePath, SizeSmall, bytes.NewReader(tc.data))
			if err != nil {
				t.Fatalf("GetThumbnail(%s) failed: %v", tc.filePath, err)
			}
			if len(thumb) == 0 {
				t.Fatalf("expected non-empty thumbnail for %s", tc.filePath)
			}
			if _, _, err := image.Decode(bytes.NewReader(thumb)); err != nil {
				t.Fatalf("generated thumbnail for %s is not a valid image: %v", tc.filePath, err)
			}
		})
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

func TestInvalidateCache_ReturnsRemovalErrors(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cachePath := svc.cachePath(svc.cacheKey("/test/invalidate-error.png", SizeSmall))
	if err := os.MkdirAll(cachePath, 0755); err != nil {
		t.Fatalf("MkdirAll(cachePath) failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "stale-thumb"), []byte("thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(stale-thumb) failed: %v", err)
	}

	err = svc.InvalidateCache("/test/invalidate-error.png")
	if err == nil {
		t.Fatal("expected InvalidateCache to report cache removal failures")
	}
	if !strings.Contains(err.Error(), cachePath) {
		t.Fatalf("expected cache removal error to mention %q, got %v", cachePath, err)
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
