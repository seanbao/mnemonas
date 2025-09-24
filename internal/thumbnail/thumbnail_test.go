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

func newTestThumbnailService(t *testing.T) (*Service, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "thumbnail-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	svc, err := NewService(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("NewService failed: %v", err)
	}

	t.Cleanup(func() {
		svc.Wait()
		os.RemoveAll(tmpDir)
	})

	return svc, tmpDir
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

func TestServiceWaitBlocksUntilBackgroundSavesComplete(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	svc.saveWG.Add(1)
	waitDone := make(chan struct{})
	go func() {
		svc.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		t.Fatal("Wait returned before background save completed")
	case <-time.After(10 * time.Millisecond):
	}

	svc.saveWG.Done()

	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after background save completed")
	}
}

func TestWaitForInProgressThumbnailReturnsClonedData(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	result := &thumbnailGenerationResult{
		done: make(chan struct{}),
		data: []byte("thumbnail-data"),
	}
	close(result.done)

	data, err := svc.waitForInProgressThumbnail(context.Background(), filepath.Join(tmpDir, "missing.jpg"), result)
	if err != nil {
		t.Fatalf("waitForInProgressThumbnail() error: %v", err)
	}
	data[0] = 'X'
	if string(result.data) != "thumbnail-data" {
		t.Fatalf("result data mutated through returned slice: %q", string(result.data))
	}
}

func TestWaitForInProgressThumbnailFallsBackToCacheWhenDataIsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cachePath := svc.cachePath(svc.cacheKey("/test/fallback.png", SizeSmall))
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		t.Fatalf("MkdirAll(cache dir) failed: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("cached-thumbnail"), 0644); err != nil {
		t.Fatalf("WriteFile(cachePath) failed: %v", err)
	}

	result := &thumbnailGenerationResult{done: make(chan struct{})}
	close(result.done)

	data, err := svc.waitForInProgressThumbnail(context.Background(), cachePath, result)
	if err != nil {
		t.Fatalf("waitForInProgressThumbnail() error: %v", err)
	}
	if string(data) != "cached-thumbnail" {
		t.Fatalf("fallback cache data = %q, want cached-thumbnail", string(data))
	}
}

func TestWaitForInProgressThumbnailReturnsContextError(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.waitForInProgressThumbnail(ctx, filepath.Join(tmpDir, "pending.jpg"), &thumbnailGenerationResult{done: make(chan struct{})})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestScheduleInProgressExpiryClearsImmediatelyForNonPositiveTTL(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cacheKey := svc.cacheKey("/test/expiry.png", SizeSmall)
	result := &thumbnailGenerationResult{done: make(chan struct{})}
	svc.ipMu.Lock()
	svc.inProgress[cacheKey] = result
	svc.ipMu.Unlock()

	svc.scheduleInProgressExpiry(cacheKey, result, 0)

	svc.ipMu.Lock()
	_, ok := svc.inProgress[cacheKey]
	svc.ipMu.Unlock()
	if ok {
		t.Fatal("expected non-positive TTL expiry to clear in-progress entry immediately")
	}
}

func TestCleanupThumbnailTempPathJoinsRemoveError(t *testing.T) {
	tmpDir := t.TempDir()
	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("OpenRoot(tmpDir) error: %v", err)
	}
	defer root.Close()

	if err := os.Mkdir(filepath.Join(tmpDir, ".thumbnail-stuck.tmp"), 0755); err != nil {
		t.Fatalf("Mkdir(temp dir) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".thumbnail-stuck.tmp", "child"), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}

	operationErr := errors.New("write failed")
	err = cleanupThumbnailTempPath(root, ".thumbnail-stuck.tmp", operationErr)
	if !errors.Is(err, operationErr) {
		t.Fatalf("cleanupThumbnailTempPath() error = %v, want wrapped operation error", err)
	}
	if !strings.Contains(err.Error(), "cleanup temp thumbnail") {
		t.Fatalf("cleanupThumbnailTempPath() error = %v, want cleanup context", err)
	}
}

func TestNormalizeThumbnailCachePath_RelativePathIsAbsolute(t *testing.T) {
	normalized, err := normalizeThumbnailCachePath(filepath.Join("relative", "cache"))
	if err != nil {
		t.Fatalf("normalizeThumbnailCachePath() error: %v", err)
	}
	if !filepath.IsAbs(normalized) {
		t.Fatalf("normalized path = %q, want absolute path", normalized)
	}
	if filepath.Base(normalized) != "cache" {
		t.Fatalf("normalized path = %q, want to preserve final path component", normalized)
	}
}

func TestNewService_RejectsSymlinkCacheParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realCacheDir := filepath.Join(tmpDir, "real-cache")
	if err := os.MkdirAll(realCacheDir, 0755); err != nil {
		t.Fatalf("MkdirAll(real-cache) failed: %v", err)
	}
	linkedCacheDir := filepath.Join(tmpDir, "linked-cache")
	if err := os.Symlink(realCacheDir, linkedCacheDir); err != nil {
		t.Fatalf("Symlink(linked-cache) failed: %v", err)
	}

	if _, err := NewService(linkedCacheDir); !errors.Is(err, errThumbnailCacheSymlink) {
		t.Fatalf("expected NewService() to reject symlink cache dir, got %v", err)
	}
}

func TestNewService_DoesNotCreateCacheDirThroughSymlinkParent(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0755); err != nil {
		t.Fatalf("MkdirAll(real-parent) failed: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked-parent) failed: %v", err)
	}
	cacheDir := filepath.Join(linkedParent, "cache")

	if _, err := NewService(cacheDir); !errors.Is(err, errThumbnailCacheSymlink) {
		t.Fatalf("expected NewService() to reject symlink cache parent, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(realParent, "cache")); !os.IsNotExist(err) {
		t.Fatalf("expected no cache dir to be created through symlink parent, got %v", err)
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
	originalSyncThumbnailCacheRootDir := syncThumbnailCacheRootDir
	syncThumbnailCacheRootDir = func(root *os.Root, dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncThumbnailCacheRootDir = originalSyncThumbnailCacheRootDir
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
	originalSyncThumbnailCacheRootDir := syncThumbnailCacheRootDir
	syncThumbnailCacheRootDir = func(root *os.Root, dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncThumbnailCacheRootDir = originalSyncThumbnailCacheRootDir
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

func TestService_SaveToCache_CleansCreatedDirectoriesWhenTempCreateFails(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cachePath := filepath.Join(tmpDir, "ab", "cd", "thumb.jpg")
	cacheDir := filepath.Dir(cachePath)
	originalCreateThumbnailCacheTempFile := createThumbnailCacheTempFile
	tempCreateErr := errors.New("temp create failed")
	createThumbnailCacheTempFile = func(root *os.Root, dir string) (*os.File, string, error) {
		return nil, "", tempCreateErr
	}
	defer func() {
		createThumbnailCacheTempFile = originalCreateThumbnailCacheTempFile
	}()

	err = svc.saveToCache(cachePath, []byte("thumbnail"))
	if err == nil {
		t.Fatal("expected saveToCache() to fail when temp file creation fails")
	}
	if !errors.Is(err, tempCreateErr) {
		t.Fatalf("expected temp create failure, got %v", err)
	}
	if _, statErr := os.Stat(cachePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no thumbnail cache file to be created, got %v", statErr)
	}
	if _, statErr := os.Stat(cacheDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected created thumbnail cache directory to be removed, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "ab")); !os.IsNotExist(statErr) {
		t.Fatalf("expected created parent thumbnail cache directory to be removed, got %v", statErr)
	}

	createThumbnailCacheTempFile = originalCreateThumbnailCacheTempFile
	if err := svc.saveToCache(cachePath, []byte("thumbnail")); err != nil {
		t.Fatalf("expected retry after failed saveToCache() cleanup to succeed, got %v", err)
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
	svc, _ := newTestThumbnailService(t)

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
	svc, _ := newTestThumbnailService(t)

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
	svc, _ := newTestThumbnailService(t)

	imgData := createTestImage(500, 500)
	ctx := context.Background()

	// First call - generates thumbnail
	reader1 := bytes.NewReader(imgData)
	thumb1, err := svc.GetThumbnail(ctx, "/test/cached.png", SizeSmall, reader1)
	if err != nil {
		t.Fatalf("first GetThumbnail failed: %v", err)
	}

	svc.Wait()

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

func TestGetThumbnailVersioned_BypassesStaleCacheForNewVersion(t *testing.T) {
	svc, _ := newTestThumbnailService(t)

	ctx := context.Background()
	if _, err := svc.GetThumbnailVersioned(ctx, "/test/versioned.png", "v1", SizeSmall, bytes.NewReader(createTestImage(64, 64))); err != nil {
		t.Fatalf("GetThumbnailVersioned(v1) failed: %v", err)
	}

	// Ensure async cache persistence completes so a stale cache entry exists for v1.
	svc.Wait()

	if _, err := svc.GetThumbnailVersioned(ctx, "/test/versioned.png", "v2", SizeSmall, bytes.NewReader([]byte("not-an-image"))); err == nil {
		t.Fatal("expected new thumbnail version to bypass old cache entry and regenerate")
	}
}

func TestGetThumbnail_ReturnsInProgressGeneratedBytesBeforeCacheSaveCompletes(t *testing.T) {
	svc, _ := newTestThumbnailService(t)

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

func TestGetThumbnail_ReusesGeneratedBytesUntilAsyncCacheSaveCompletes(t *testing.T) {
	svc, _ := newTestThumbnailService(t)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	originalSyncThumbnailCacheRootDir := syncThumbnailCacheRootDir
	syncThumbnailCacheRootDir = func(root *os.Root, dir string) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil
	}
	t.Cleanup(func() {
		syncThumbnailCacheRootDir = originalSyncThumbnailCacheRootDir
	})

	ctx := context.Background()
	thumb1, err := svc.GetThumbnail(ctx, "/test/pending-cache.png", SizeSmall, bytes.NewReader(createTestImage(32, 32)))
	if err != nil {
		t.Fatalf("first GetThumbnail failed: %v", err)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async cache save to start")
	}

	cacheKey := svc.cacheKey("/test/pending-cache.png", SizeSmall)
	svc.ipMu.Lock()
	_, ok := svc.inProgress[cacheKey]
	svc.ipMu.Unlock()
	if !ok {
		t.Fatal("expected completed thumbnail to remain reusable until cache save finishes")
	}

	thumb2, err := svc.GetThumbnail(ctx, "/test/pending-cache.png", SizeSmall, bytes.NewReader([]byte("not-an-image")))
	if err != nil {
		t.Fatalf("expected second GetThumbnail to reuse generated bytes, got %v", err)
	}
	if !bytes.Equal(thumb1, thumb2) {
		t.Fatal("expected second GetThumbnail to return the first generated thumbnail bytes")
	}

	close(release)
	svc.Wait()
	svc.ipMu.Lock()
	_, stillInProgress := svc.inProgress[cacheKey]
	svc.ipMu.Unlock()
	if stillInProgress {
		t.Fatal("expected in-progress entry to clear after cache save completed")
	}
}

func TestGetThumbnail_ReusesGeneratedBytesBrieflyAfterCacheSaveFailure(t *testing.T) {
	svc, _ := newTestThumbnailService(t)
	svc.failedCacheReuseTTL = 50 * time.Millisecond
	expired := make(chan string, 1)
	originalOnThumbnailInProgressExpired := onThumbnailInProgressExpired
	onThumbnailInProgressExpired = func(cacheKey string) {
		select {
		case expired <- cacheKey:
		default:
		}
	}
	t.Cleanup(func() {
		onThumbnailInProgressExpired = originalOnThumbnailInProgressExpired
	})

	failedSave := make(chan struct{}, 1)
	originalSyncThumbnailCacheRootDir := syncThumbnailCacheRootDir
	syncThumbnailCacheRootDir = func(root *os.Root, dir string) error {
		select {
		case failedSave <- struct{}{}:
		default:
		}
		return errors.New("directory fsync failed")
	}
	t.Cleanup(func() {
		syncThumbnailCacheRootDir = originalSyncThumbnailCacheRootDir
	})

	ctx := context.Background()
	thumb1, err := svc.GetThumbnail(ctx, "/test/cache-failure.png", SizeSmall, bytes.NewReader(createTestImage(32, 32)))
	if err != nil {
		t.Fatalf("first GetThumbnail failed: %v", err)
	}

	select {
	case <-failedSave:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async cache save failure")
	}

	cacheKey := svc.cacheKey("/test/cache-failure.png", SizeSmall)
	svc.ipMu.Lock()
	_, ok := svc.inProgress[cacheKey]
	svc.ipMu.Unlock()
	if !ok {
		t.Fatal("expected generated thumbnail to stay reusable briefly after cache save failure")
	}

	thumb2, err := svc.GetThumbnail(ctx, "/test/cache-failure.png", SizeSmall, bytes.NewReader([]byte("not-an-image")))
	if err != nil {
		t.Fatalf("expected second GetThumbnail to reuse generated bytes after cache save failure, got %v", err)
	}
	if !bytes.Equal(thumb1, thumb2) {
		t.Fatal("expected second GetThumbnail to return the first generated thumbnail bytes")
	}

	select {
	case got := <-expired:
		if got != cacheKey {
			t.Fatalf("expired cache key = %q, want %q", got, cacheKey)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed-cache reuse entry to expire")
	}
	svc.ipMu.Lock()
	_, stillInProgress := svc.inProgress[cacheKey]
	svc.ipMu.Unlock()
	if stillInProgress {
		t.Fatal("expected failed cache reuse entry to expire after the configured TTL")
	}

	if _, err := svc.GetThumbnail(ctx, "/test/cache-failure.png", SizeSmall, bytes.NewReader([]byte("not-an-image"))); err == nil {
		t.Fatal("expected thumbnail generation to run again after failed-cache reuse TTL expires")
	}
}

func TestGetThumbnail_ReturnsInProgressGenerationError(t *testing.T) {
	svc, _ := newTestThumbnailService(t)

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

func TestGetThumbnail_RejectsCachePathSymlinkIntroducedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cacheKey := svc.cacheKey("/test/race.png", SizeSmall)
	cachePath := svc.cachePath(cacheKey)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		t.Fatalf("MkdirAll(cache dir) failed: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("cached-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(cachePath) failed: %v", err)
	}
	targetPath := filepath.Join(tmpDir, "secret-thumb.jpg")
	if err := os.WriteFile(targetPath, []byte("secret-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(secret-thumb.jpg) failed: %v", err)
	}

	originalAfterValidateThumbnailCachePath := afterValidateThumbnailCachePath
	afterValidateThumbnailCachePath = func() {
		if err := os.Remove(cachePath); err != nil {
			t.Fatalf("Remove(cachePath) failed: %v", err)
		}
		relTarget, err := filepath.Rel(filepath.Dir(cachePath), targetPath)
		if err != nil {
			t.Fatalf("Rel(cache target) failed: %v", err)
		}
		if err := os.Symlink(relTarget, cachePath); err != nil {
			t.Fatalf("Symlink(cachePath) failed: %v", err)
		}
	}
	defer func() {
		afterValidateThumbnailCachePath = originalAfterValidateThumbnailCachePath
	}()

	_, err = svc.loadFromCache(cachePath)
	if !errors.Is(err, errThumbnailCacheSymlink) {
		t.Fatalf("expected symlink rejection after validation race, got %v", err)
	}
}

func TestGetThumbnail_LoadFromCache_DoesNotFollowSymlinkParentInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cacheKey := svc.cacheKey("/test/parent-race.png", SizeSmall)
	cachePath := svc.cachePath(cacheKey)
	cacheDir := filepath.Dir(cachePath)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatalf("MkdirAll(cache dir) failed: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("cached-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(cachePath) failed: %v", err)
	}
	outsideDir := filepath.Join(tmpDir, "outside-cache")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outside-cache) failed: %v", err)
	}
	outsidePath := filepath.Join(outsideDir, filepath.Base(cachePath))
	if err := os.WriteFile(outsidePath, []byte("secret-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(outside cache) failed: %v", err)
	}
	backupDir := cacheDir + "-backup"

	originalAfterValidateThumbnailCachePath := afterValidateThumbnailCachePath
	afterValidateThumbnailCachePath = func() {
		if err := os.Rename(cacheDir, backupDir); err != nil {
			t.Fatalf("Rename(cacheDir) failed: %v", err)
		}
		if err := os.Symlink(outsideDir, cacheDir); err != nil {
			t.Fatalf("Symlink(cacheDir) failed: %v", err)
		}
	}
	defer func() {
		afterValidateThumbnailCachePath = originalAfterValidateThumbnailCachePath
	}()

	_, err = svc.loadFromCache(cachePath)
	if !errors.Is(err, errThumbnailCacheSymlink) {
		t.Fatalf("expected parent-directory symlink rejection after validation race, got %v", err)
	}

	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("ReadFile(outside cache) failed: %v", err)
	}
	if string(data) != "secret-thumb" {
		t.Fatalf("expected outside cache file unchanged, got %q", string(data))
	}

	backupPath := filepath.Join(backupDir, filepath.Base(cachePath))
	data, err = os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("ReadFile(backup cache) failed: %v", err)
	}
	if string(data) != "cached-thumb" {
		t.Fatalf("expected original cache file preserved, got %q", string(data))
	}
}

func TestService_SaveToCache_DoesNotFollowSymlinkParentInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cacheKey := svc.cacheKey("/test/save-parent-race.png", SizeSmall)
	cachePath := svc.cachePath(cacheKey)
	cacheDir := filepath.Dir(cachePath)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatalf("MkdirAll(cache dir) failed: %v", err)
	}
	outsideDir := filepath.Join(tmpDir, "outside-cache")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("MkdirAll(outside-cache) failed: %v", err)
	}
	outsidePath := filepath.Join(outsideDir, filepath.Base(cachePath))
	if err := os.WriteFile(outsidePath, []byte("secret-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(outside cache) failed: %v", err)
	}
	backupDir := cacheDir + "-backup"

	originalAfterValidateThumbnailCachePath := afterValidateThumbnailCachePath
	afterValidateThumbnailCachePath = func() {
		if err := os.Rename(cacheDir, backupDir); err != nil {
			t.Fatalf("Rename(cacheDir) failed: %v", err)
		}
		if err := os.Symlink(outsideDir, cacheDir); err != nil {
			t.Fatalf("Symlink(cacheDir) failed: %v", err)
		}
	}
	defer func() {
		afterValidateThumbnailCachePath = originalAfterValidateThumbnailCachePath
	}()

	err = svc.saveToCache(cachePath, []byte("new-thumb"))
	if !errors.Is(err, errThumbnailCacheSymlink) {
		t.Fatalf("expected parent-directory symlink rejection on save after validation race, got %v", err)
	}

	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("ReadFile(outside cache) failed: %v", err)
	}
	if string(data) != "secret-thumb" {
		t.Fatalf("expected outside cache file unchanged, got %q", string(data))
	}

	backupPath := filepath.Join(backupDir, filepath.Base(cachePath))
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("expected no thumbnail cache file to be created in original directory, got %v", err)
	}
}

func TestService_SaveToCacheRejectsParentSymlinkInsertedAfterValidationInsideRoot(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	cacheKey := svc.cacheKey("/test/save-parent-internal-race.png", SizeSmall)
	cachePath := svc.cachePath(cacheKey)
	cacheDir := filepath.Dir(cachePath)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatalf("MkdirAll(cache dir) failed: %v", err)
	}
	realDir := filepath.Join(filepath.Dir(cacheDir), "real-cache")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("MkdirAll(real cache dir) failed: %v", err)
	}
	backupDir := cacheDir + "-backup"

	originalAfterValidateThumbnailCachePath := afterValidateThumbnailCachePath
	afterValidateThumbnailCachePath = func() {
		if err := os.Rename(cacheDir, backupDir); err != nil {
			t.Fatalf("Rename(cacheDir) failed: %v", err)
		}
		if err := os.Symlink(filepath.Base(realDir), cacheDir); err != nil {
			t.Fatalf("Symlink(cacheDir) failed: %v", err)
		}
	}
	defer func() {
		afterValidateThumbnailCachePath = originalAfterValidateThumbnailCachePath
	}()

	err = svc.saveToCache(cachePath, []byte("new-thumb"))
	if !errors.Is(err, errThumbnailCacheSymlink) {
		t.Fatalf("saveToCache() error = %v, want errThumbnailCacheSymlink", err)
	}
	if entries, readErr := os.ReadDir(realDir); readErr != nil {
		t.Fatalf("ReadDir(realDir) failed: %v", readErr)
	} else if len(entries) != 0 {
		t.Fatalf("expected no thumbnail files in symlink target, got %v", entries)
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

	svc.Wait()

	// Larger sizes should generally produce larger files
	if len(thumbs[0]) >= len(thumbs[2]) {
		t.Log("Note: small thumbnail not smaller than large (depends on compression)")
	}
}

func TestGetThumbnail_ContextCancel(t *testing.T) {
	svc, _ := newTestThumbnailService(t)

	imgData := createTestImage(2000, 2000) // Large image
	reader := bytes.NewReader(imgData)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := svc.GetThumbnail(ctx, "/test/cancelled.png", SizeMedium, reader)
	if err == nil {
		t.Log("Note: thumbnail generation completed before context was checked")
	}
}

func TestGetThumbnail_RejectsOversizedSourceImage(t *testing.T) {
	svc, _ := newTestThumbnailService(t)

	reader := bytes.NewReader(createTestPNGConfigOnly(maxThumbnailSourceDimension+1, maxThumbnailSourceDimension))
	_, err := svc.GetThumbnail(context.Background(), "/test/oversized.png", SizeMedium, reader)
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
	svc, _ := newTestThumbnailService(t)

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
	svc, _ := newTestThumbnailService(t)

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
	svc, _ := newTestThumbnailService(t)

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

	svc.Wait()

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

func TestCacheStats_DoesNotBlockSaveToCache(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	originalWalkThumbnailCache := walkThumbnailCache
	walkThumbnailCache = func(root string, cacheRoot *os.Root, walkFn thumbnailWalkFunc) error {
		close(started)
		<-release
		return nil
	}
	t.Cleanup(func() {
		walkThumbnailCache = originalWalkThumbnailCache
	})

	type cacheStatsResult struct {
		count int
		size  int64
		err   error
	}
	statsDone := make(chan cacheStatsResult, 1)
	go func() {
		count, size, err := svc.CacheStats(context.Background())
		statsDone <- cacheStatsResult{count: count, size: size, err: err}
	}()

	<-started

	cachePath := svc.cachePath(svc.cacheKey("/test/concurrent.png", SizeSmall))
	saveDone := make(chan error, 1)
	go func() {
		saveDone <- svc.saveToCache(cachePath, []byte("thumbnail"))
	}()

	select {
	case err := <-saveDone:
		if err != nil {
			t.Fatalf("saveToCache() during CacheStats() error: %v", err)
		}
	case <-time.After(time.Second):
		close(release)
		<-statsDone
		t.Fatal("expected CacheStats() traversal not to block cache writes")
	}

	close(release)
	stats := <-statsDone
	if stats.err != nil {
		t.Fatalf("CacheStats() error: %v", stats.err)
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("ReadFile(cachePath) failed: %v", err)
	}
	if string(data) != "thumbnail" {
		t.Fatalf("cache content = %q, want thumbnail", string(data))
	}
}

func TestCleanCache(t *testing.T) {
	svc, _ := newTestThumbnailService(t)

	ctx := context.Background()

	// Generate a thumbnail
	imgData := createTestImage(300, 300)
	reader := bytes.NewReader(imgData)
	_, err := svc.GetThumbnail(ctx, "/test/clean.png", SizeSmall, reader)
	if err != nil {
		t.Fatalf("GetThumbnail failed: %v", err)
	}

	svc.Wait()
	cachePath := svc.cachePath(svc.cacheKey("/test/clean.png", SizeSmall))
	staleTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(cachePath, staleTime, staleTime); err != nil {
		t.Fatalf("Chtimes(cachePath) failed: %v", err)
	}

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

func TestCleanCache_DoesNotFollowCacheRootSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	originalPath := filepath.Join(tmpDir, "ab", "thumb.jpg")
	if err := os.MkdirAll(filepath.Dir(originalPath), 0755); err != nil {
		t.Fatalf("MkdirAll(original cache dir) failed: %v", err)
	}
	if err := os.WriteFile(originalPath, []byte("cached-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(original thumb) failed: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(originalPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(original thumb) failed: %v", err)
	}

	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "ab", "thumb.jpg")
	if err := os.MkdirAll(filepath.Dir(outsidePath), 0755); err != nil {
		t.Fatalf("MkdirAll(outside cache dir) failed: %v", err)
	}
	if err := os.WriteFile(outsidePath, []byte("outside-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(outside thumb) failed: %v", err)
	}
	if err := os.Chtimes(outsidePath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(outside thumb) failed: %v", err)
	}

	backupRoot := tmpDir + "-backup"
	originalAfterValidateThumbnailCachePath := afterValidateThumbnailCachePath
	afterValidateThumbnailCachePath = func() {
		if err := os.Rename(tmpDir, backupRoot); err != nil {
			t.Fatalf("Rename(cache root) failed: %v", err)
		}
		if err := os.Symlink(outsideRoot, tmpDir); err != nil {
			t.Fatalf("Symlink(cache root) failed: %v", err)
		}
	}
	defer func() {
		afterValidateThumbnailCachePath = originalAfterValidateThumbnailCachePath
		if info, err := os.Lstat(tmpDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(tmpDir); removeErr != nil {
				t.Errorf("Remove(cache root symlink) failed: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupRoot); err == nil {
			if renameErr := os.Rename(backupRoot, tmpDir); renameErr != nil {
				t.Errorf("Rename(backup root) failed: %v", renameErr)
			}
		}
	}()

	cleaned, err := svc.CleanCache(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("CleanCache failed: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("expected 1 cleaned file, got %d", cleaned)
	}

	if _, err := os.Stat(filepath.Join(backupRoot, "ab", "thumb.jpg")); !os.IsNotExist(err) {
		t.Fatalf("expected anchored cache file to be removed, got %v", err)
	}
	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("ReadFile(outside thumb) failed: %v", err)
	}
	if string(data) != "outside-thumb" {
		t.Fatalf("expected outside cache file unchanged, got %q", string(data))
	}
}

func TestCleanCache_ReturnsRemovalErrors(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	originalWalkThumbnailCache := walkThumbnailCache
	walkThumbnailCache = func(root string, cacheRoot *os.Root, walkFn thumbnailWalkFunc) error {
		return walkFn("../outside-thumb.jpg", thumbnailTestFileInfo{
			name:    "outside-thumb.jpg",
			modTime: time.Now().Add(-2 * time.Hour),
			size:    int64(len("thumb")),
		})
	}
	t.Cleanup(func() {
		walkThumbnailCache = originalWalkThumbnailCache
	})

	cleaned, err := svc.CleanCache(context.Background(), time.Hour)
	if err == nil {
		t.Fatal("expected CleanCache to report cache removal failures")
	}
	if cleaned != 0 {
		t.Fatalf("CleanCache cleaned = %d, want 0", cleaned)
	}
	if !strings.Contains(err.Error(), "outside-thumb.jpg") {
		t.Fatalf("expected removal error to mention cache entry, got %v", err)
	}
}

func TestCacheStats_DoesNotFollowCacheRootSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	svc, err := NewService(tmpDir)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	originalPath := filepath.Join(tmpDir, "ab", "thumb.jpg")
	if err := os.MkdirAll(filepath.Dir(originalPath), 0755); err != nil {
		t.Fatalf("MkdirAll(original cache dir) failed: %v", err)
	}
	if err := os.WriteFile(originalPath, []byte("cached-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(original thumb) failed: %v", err)
	}

	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "ab", "thumb.jpg")
	if err := os.MkdirAll(filepath.Dir(outsidePath), 0755); err != nil {
		t.Fatalf("MkdirAll(outside cache dir) failed: %v", err)
	}
	if err := os.WriteFile(outsidePath, []byte("outside-thumb"), 0644); err != nil {
		t.Fatalf("WriteFile(outside thumb) failed: %v", err)
	}

	backupRoot := tmpDir + "-backup"
	originalAfterValidateThumbnailCachePath := afterValidateThumbnailCachePath
	afterValidateThumbnailCachePath = func() {
		if err := os.Rename(tmpDir, backupRoot); err != nil {
			t.Fatalf("Rename(cache root) failed: %v", err)
		}
		if err := os.Symlink(outsideRoot, tmpDir); err != nil {
			t.Fatalf("Symlink(cache root) failed: %v", err)
		}
	}
	defer func() {
		afterValidateThumbnailCachePath = originalAfterValidateThumbnailCachePath
		if info, err := os.Lstat(tmpDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(tmpDir); removeErr != nil {
				t.Errorf("Remove(cache root symlink) failed: %v", removeErr)
			}
		}
		if _, err := os.Stat(backupRoot); err == nil {
			if renameErr := os.Rename(backupRoot, tmpDir); renameErr != nil {
				t.Errorf("Rename(backup root) failed: %v", renameErr)
			}
		}
	}()

	count, size, err := svc.CacheStats(context.Background())
	if err != nil {
		t.Fatalf("CacheStats failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 cached file, got %d", count)
	}
	if size != int64(len("cached-thumb")) {
		t.Fatalf("expected cache size %d, got %d", len("cached-thumb"), size)
	}

	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("ReadFile(outside thumb) failed: %v", err)
	}
	if string(data) != "outside-thumb" {
		t.Fatalf("expected outside cache file unchanged, got %q", string(data))
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
	svc, _ := newTestThumbnailService(t)

	ctx := context.Background()

	// Generate thumbnails for multiple sizes
	imgData := createTestImage(400, 400)
	filePath := "/test/invalidate.png"

	for _, size := range []Size{SizeSmall, SizeMedium, SizeLarge} {
		reader := bytes.NewReader(imgData)
		_, err := svc.GetThumbnail(ctx, filePath, size, reader)
		if err != nil {
			t.Fatalf("GetThumbnail(%s) failed: %v", size, err)
		}
	}

	svc.Wait()

	// Verify cache has files
	count, _, _ := svc.CacheStats(ctx)
	if count < 3 {
		t.Logf("Note: expected 3 cached files, got %d", count)
	}

	// Invalidate cache for this file
	err := svc.InvalidateCache(filePath)
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

type thumbnailTestFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (i thumbnailTestFileInfo) Name() string {
	return i.name
}

func (i thumbnailTestFileInfo) Size() int64 {
	return i.size
}

func (i thumbnailTestFileInfo) Mode() os.FileMode {
	return i.mode
}

func (i thumbnailTestFileInfo) ModTime() time.Time {
	return i.modTime
}

func (i thumbnailTestFileInfo) IsDir() bool {
	return i.isDir
}

func (i thumbnailTestFileInfo) Sys() any {
	return nil
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

func TestHasAlpha(t *testing.T) {
	if !hasAlpha(image.NewRGBA(image.Rect(0, 0, 1, 1))) {
		t.Fatal("expected RGBA image to report alpha support")
	}
	if hasAlpha(image.NewGray(image.Rect(0, 0, 1, 1))) {
		t.Fatal("expected Gray image to report no alpha support")
	}
}
