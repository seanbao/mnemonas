package share

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/requestip"
	"github.com/seanbao/mnemonas/internal/storage"
)

type fakeShareFS struct {
	statInfo        *storage.FileInfo
	statInfoByPath  map[string]*storage.FileInfo
	beforeStat      func(string) error
	statErrByPath   map[string]error
	dirItems        []*storage.FileInfo
	dirItemsByPath  map[string][]*storage.FileInfo
	beforeOpenFile  func(string) error
	openByPath      map[string]FileReader
	openErrByPath   map[string]error
	beforeReadDir   func(string) error
	readDirErr      error
	readDirLimits   []int
	readDirReturned int
}

type fakeShareSnapshotFS struct {
	*fakeShareFS
	snapshotByPath map[string]fakeShareSnapshot
}

type fakeShareSnapshot struct {
	hostPath string
	info     *storage.FileInfo
	err      error
}

type failingReadCloser struct {
	err error
}

type partialFailingReadCloser struct {
	reader io.Reader
}

type closeFailingReadCloser struct {
	reader io.Reader
	err    error
}

type dataAndErrorReader struct {
	data []byte
	err  error
	done bool
}

type readSeekCloser struct {
	*bytes.Reader
}

type failFirstWriteResponseWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	failed   bool
	writeErr error
}

type failPartialWriteResponseWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	failed   bool
	writeErr error
	limit    int
}

func TestNormalizeSharePathsRejectNUL(t *testing.T) {
	tests := []struct {
		name      string
		normalize func(string) (string, error)
		input     string
	}{
		{
			name:      "absolute path",
			normalize: normalizeShareAbsolutePath,
			input:     "/docs\x00secret.txt",
		},
		{
			name:      "relative path",
			normalize: normalizeShareRelativePath,
			input:     "docs\x00secret.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := tt.normalize(tt.input); err == nil {
				t.Fatalf("normalize(%q) = %q, want error", tt.input, got)
			}
		})
	}
}

func TestNormalizeSharePathsRejectControlCharacters(t *testing.T) {
	tests := []struct {
		name      string
		normalize func(string) (string, error)
		input     string
	}{
		{
			name:      "absolute control character",
			normalize: normalizeShareAbsolutePath,
			input:     "/docs\a/report.pdf",
		},
		{
			name:      "absolute delete control character",
			normalize: normalizeShareAbsolutePath,
			input:     "/docs\x7f/report.pdf",
		},
		{
			name:      "relative control character",
			normalize: normalizeShareRelativePath,
			input:     "docs\a/report.pdf",
		},
		{
			name:      "relative delete control character",
			normalize: normalizeShareRelativePath,
			input:     "docs\x7f/report.pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := tt.normalize(tt.input); err == nil {
				t.Fatalf("normalize(%q) = %q, want error", tt.input, got)
			}
		})
	}
}

func TestNormalizeSharePathsRejectDotSegments(t *testing.T) {
	tests := []struct {
		name      string
		normalize func(string) (string, error)
		input     string
	}{
		{
			name:      "absolute dot segment",
			normalize: normalizeShareAbsolutePath,
			input:     "/docs/./report.pdf",
		},
		{
			name:      "absolute parent segment",
			normalize: normalizeShareAbsolutePath,
			input:     "/docs/../report.pdf",
		},
		{
			name:      "relative dot segment",
			normalize: normalizeShareRelativePath,
			input:     "docs/./report.pdf",
		},
		{
			name:      "relative parent segment",
			normalize: normalizeShareRelativePath,
			input:     "docs/../report.pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := tt.normalize(tt.input); err == nil {
				t.Fatalf("normalize(%q) = %q, want error", tt.input, got)
			}
		})
	}
}

func TestShareReadDirChildPathRejectsUnsafeChildren(t *testing.T) {
	tests := []struct {
		name       string
		parentPath string
		child      *storage.FileInfo
		wantPath   string
		wantName   string
		wantErr    bool
	}{
		{
			name:       "direct child path",
			parentPath: "/docs",
			child:      &storage.FileInfo{Path: "/docs/report.txt", Name: "report.txt"},
			wantPath:   "/docs/report.txt",
			wantName:   "report.txt",
		},
		{
			name:       "fallback direct child name",
			parentPath: "/docs",
			child:      &storage.FileInfo{Name: "report.txt"},
			wantPath:   "/docs/report.txt",
			wantName:   "report.txt",
		},
		{
			name:       "root parent direct child",
			parentPath: "/",
			child:      &storage.FileInfo{Path: "/readme.txt", Name: "readme.txt"},
			wantPath:   "/readme.txt",
			wantName:   "readme.txt",
		},
		{
			name:       "similar prefix sibling",
			parentPath: "/docs",
			child:      &storage.FileInfo{Path: "/docs-archive/secret.txt", Name: "secret.txt"},
			wantErr:    true,
		},
		{
			name:       "nested descendant",
			parentPath: "/docs",
			child:      &storage.FileInfo{Path: "/docs/nested/secret.txt", Name: "secret.txt"},
			wantErr:    true,
		},
		{
			name:       "same directory path",
			parentPath: "/docs",
			child:      &storage.FileInfo{Path: "/docs", Name: "docs"},
			wantErr:    true,
		},
		{
			name:       "dot segment child path",
			parentPath: "/docs",
			child:      &storage.FileInfo{Path: "/docs/./report.txt", Name: "report.txt"},
			wantErr:    true,
		},
		{
			name:       "parent segment child path",
			parentPath: "/docs",
			child:      &storage.FileInfo{Path: "/docs/../report.txt", Name: "report.txt"},
			wantErr:    true,
		},
		{
			name:       "dot segment fallback name",
			parentPath: "/docs",
			child:      &storage.FileInfo{Name: "./report.txt"},
			wantErr:    true,
		},
		{
			name:       "leading slash fallback name",
			parentPath: "/docs",
			child:      &storage.FileInfo{Name: "/report.txt"},
			wantErr:    true,
		},
		{
			name:       "trailing slash fallback name",
			parentPath: "/docs",
			child:      &storage.FileInfo{Name: "report.txt/"},
			wantErr:    true,
		},
		{
			name:       "backslash fallback name",
			parentPath: "/docs",
			child:      &storage.FileInfo{Name: "nested\\report.txt"},
			wantErr:    true,
		},
		{
			name:       "backslash child path",
			parentPath: "/docs",
			child:      &storage.FileInfo{Path: "/docs\\report.txt", Name: "report.txt"},
			wantErr:    true,
		},
		{
			name:       "backslash dot segment child path",
			parentPath: "/docs",
			child:      &storage.FileInfo{Path: "/docs\\.\\report.txt", Name: "report.txt"},
			wantErr:    true,
		},
		{
			name:       "nul child path",
			parentPath: "/docs",
			child:      &storage.FileInfo{Path: "/docs/report\x00.txt", Name: "report.txt"},
			wantErr:    true,
		},
		{
			name:       "control character child path",
			parentPath: "/docs",
			child:      &storage.FileInfo{Path: "/docs/report\n2026.txt", Name: "report\n2026.txt"},
			wantErr:    true,
		},
		{
			name:       "control character fallback name",
			parentPath: "/docs",
			child:      &storage.FileInfo{Name: "report\x7f.txt"},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotName, err := shareReadDirChildPath(tt.parentPath, tt.child)
			if (err != nil) != tt.wantErr {
				t.Fatalf("shareReadDirChildPath() error = %v, wantErr %v", err, tt.wantErr)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("shareReadDirChildPath() path = %q, want %q", gotPath, tt.wantPath)
			}
			if gotName != tt.wantName {
				t.Fatalf("shareReadDirChildPath() name = %q, want %q", gotName, tt.wantName)
			}
		})
	}
}

func TestWriteShareArchiveRejectsUnsafeHeaderNames(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewShareStore(filepath.Join(tempDir, "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/safe.txt": io.NopCloser(strings.NewReader("safe")),
		},
	})

	now := time.Unix(1700000000, 0)
	tests := []struct {
		name  string
		entry shareArchiveEntry
	}{
		{
			name: "directory traversal",
			entry: shareArchiveEntry{
				sourcePath: "/safe-dir",
				zipName:    "../evil/",
				info: &storage.FileInfo{
					Path:    "/safe-dir",
					Name:    "safe-dir",
					IsDir:   true,
					ModTime: now,
				},
			},
		},
		{
			name: "file absolute path",
			entry: shareArchiveEntry{
				sourcePath: "/safe.txt",
				zipName:    "/evil.txt",
				info: &storage.FileInfo{
					Path:    "/safe.txt",
					Name:    "safe.txt",
					Size:    4,
					ModTime: now,
				},
			},
		},
		{
			name: "file parent segment",
			entry: shareArchiveEntry{
				sourcePath: "/safe.txt",
				zipName:    "docs/../evil.txt",
				info: &storage.FileInfo{
					Path:    "/safe.txt",
					Name:    "safe.txt",
					Size:    4,
					ModTime: now,
				},
			},
		},
		{
			name: "file backslash separator",
			entry: shareArchiveEntry{
				sourcePath: "/safe.txt",
				zipName:    `docs\evil.txt`,
				info: &storage.FileInfo{
					Path:    "/safe.txt",
					Name:    "safe.txt",
					Size:    4,
					ModTime: now,
				},
			},
		},
		{
			name: "file Windows drive path",
			entry: shareArchiveEntry{
				sourcePath: "/safe.txt",
				zipName:    "C:/windows/system32/config",
				info: &storage.FileInfo{
					Path:    "/safe.txt",
					Name:    "safe.txt",
					Size:    4,
					ModTime: now,
				},
			},
		},
		{
			name: "file colon stream path",
			entry: shareArchiveEntry{
				sourcePath: "/safe.txt",
				zipName:    "docs/report.txt:stream",
				info: &storage.FileInfo{
					Path:    "/safe.txt",
					Name:    "safe.txt",
					Size:    4,
					ModTime: now,
				},
			},
		},
		{
			name: "file trailing slash",
			entry: shareArchiveEntry{
				sourcePath: "/safe.txt",
				zipName:    "safe.txt/",
				info: &storage.FileInfo{
					Path:    "/safe.txt",
					Name:    "safe.txt",
					Size:    4,
					ModTime: now,
				},
			},
		},
		{
			name: "file control character",
			entry: shareArchiveEntry{
				sourcePath: "/safe.txt",
				zipName:    "safe\n2026.txt",
				info: &storage.FileInfo{
					Path:    "/safe.txt",
					Name:    "safe.txt",
					Size:    4,
					ModTime: now,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var archive bytes.Buffer
			zipWriter := zip.NewWriter(&archive)

			err := handler.writeShareArchive(context.Background(), zipWriter, []shareArchiveEntry{tt.entry})
			if !errors.Is(err, errInvalidShareArchivePath) {
				t.Fatalf("writeShareArchive() error = %v, want errInvalidShareArchivePath", err)
			}
			if closeErr := zipWriter.Close(); closeErr != nil {
				t.Fatalf("zipWriter.Close() error: %v", closeErr)
			}
		})
	}
}

func TestWriteShareArchiveRejectsDuplicateHeaderNames(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewShareStore(filepath.Join(tempDir, "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/first.txt":  io.NopCloser(strings.NewReader("first")),
			"/second.txt": io.NopCloser(strings.NewReader("second")),
		},
	})

	now := time.Unix(1700000000, 0)
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	err = handler.writeShareArchive(context.Background(), zipWriter, []shareArchiveEntry{
		{
			sourcePath: "/first.txt",
			zipName:    "duplicate.txt",
			info: &storage.FileInfo{
				Path:    "/first.txt",
				Name:    "first.txt",
				Size:    5,
				ModTime: now,
			},
		},
		{
			sourcePath: "/second.txt",
			zipName:    "duplicate.txt",
			info: &storage.FileInfo{
				Path:    "/second.txt",
				Name:    "second.txt",
				Size:    6,
				ModTime: now,
			},
		},
	})
	if !errors.Is(err, errShareArchiveDuplicateEntry) {
		t.Fatalf("writeShareArchive() error = %v, want errShareArchiveDuplicateEntry", err)
	}
	if closeErr := zipWriter.Close(); closeErr != nil {
		t.Fatalf("zipWriter.Close() error: %v", closeErr)
	}
}

func TestWriteShareArchiveRejectsShortSnapshot(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/short.txt": io.NopCloser(strings.NewReader("abc")),
		},
	})
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	err = handler.writeShareArchive(context.Background(), zipWriter, []shareArchiveEntry{{
		sourcePath: "/short.txt",
		zipName:    "short.txt",
		info: &storage.FileInfo{
			Path: "/short.txt",
			Name: "short.txt",
			Size: 4,
		},
	}})
	if !errors.Is(err, errShareArchiveSnapshotChanged) {
		t.Fatalf("writeShareArchive() error = %v, want errShareArchiveSnapshotChanged", err)
	}
}

func TestServeAuthorizedShareArchive_DoesNotFinalizeZipAfterStartedSnapshotFailure(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	share, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "owner-1"})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	data := make([]byte, 64*1024)
	state := uint32(0x12345678)
	for index := range data {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		data[index] = byte(state)
	}
	handler := NewHandler(store, &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			"/docs": {Path: "/docs", Name: "docs", IsDir: true},
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {{Path: "/docs/short.bin", Name: "short.bin", Size: int64(len(data) + 1)}},
		},
		openByPath: map[string]FileReader{
			"/docs/short.bin": io.NopCloser(bytes.NewReader(data)),
		},
	})
	recorder := httptest.NewRecorder()
	request := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip", share.ID, nil)

	handler.serveAuthorizedShareArchive(recorder, request, share, share.Path)

	if recorder.Body.Len() == 0 {
		t.Fatal("expected the ZIP response to have started")
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte{'P', 'K', 0x05, 0x06}) {
		t.Fatal("truncated ZIP contains an end-of-central-directory record")
	}
	if _, err := zip.NewReader(bytes.NewReader(recorder.Body.Bytes()), int64(recorder.Body.Len())); err == nil {
		t.Fatal("truncated ZIP unexpectedly parsed as a complete archive")
	}
}

func TestServeAuthorizedShareArchive_ClearsAttachmentHeadersBeforeJSONError(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	share, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "owner-1"})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	handler := NewHandler(store, &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			"/docs": {Path: "/docs", Name: "docs", IsDir: true},
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {{Path: "/docs/short.txt", Name: "short.txt", Size: 4}},
		},
		openByPath: map[string]FileReader{
			"/docs/short.txt": io.NopCloser(strings.NewReader("abc")),
		},
	})
	recorder := httptest.NewRecorder()
	request := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip", share.ID, nil)

	handler.serveAuthorizedShareArchive(recorder, request, share, share.Path)

	if recorder.Code != http.StatusConflict || responseErrorCode(t, recorder) != "ARCHIVE_ENTRY_CHANGED" {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	for _, headerName := range []string{"Content-Disposition", "Content-Length", "Content-Range"} {
		if value := recorder.Header().Get(headerName); value != "" {
			t.Fatalf("%s = %q, want empty", headerName, value)
		}
	}
	if got := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox;") || !strings.Contains(got, "frame-ancestors 'self'") || strings.Contains(got, "allow-downloads") {
		t.Fatalf("Content-Security-Policy = %q, want same-origin sandbox without downloads", got)
	}
	if got := recorder.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q, want SAMEORIGIN", got)
	}
}

func TestWritePublicShareArchiveError_NotRegularIsExplicit(t *testing.T) {
	recorder := httptest.NewRecorder()
	writePublicShareArchiveError(recorder, storage.ErrNotRegular)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
	if code := responseErrorCode(t, recorder); code != "ARCHIVE_ENTRY_NOT_REGULAR" {
		t.Fatalf("code = %q, want ARCHIVE_ENTRY_NOT_REGULAR", code)
	}
}

func TestWriteShareArchiveInternalErrorTextDoesNotExposePaths(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewShareStore(filepath.Join(tempDir, "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	closeSentinel := errors.New("close failed")
	pathfulCloseErr := fmt.Errorf("close /docs/secret-token.pdf: %w", closeSentinel)
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/secret-token.pdf": &closeFailingReadCloser{
				reader: strings.NewReader("secret"),
				err:    pathfulCloseErr,
			},
		},
	})

	now := time.Unix(1700000000, 0)
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	err = handler.writeShareArchive(context.Background(), zipWriter, []shareArchiveEntry{{
		sourcePath: "/docs/secret-token.pdf",
		zipName:    "secret-token.pdf",
		info: &storage.FileInfo{
			Path:    "/docs/secret-token.pdf",
			Name:    "secret-token.pdf",
			Size:    6,
			ModTime: now,
		},
	}})
	if err == nil {
		t.Fatal("writeShareArchive() error = nil, want close failure")
	}
	if !errors.Is(err, closeSentinel) {
		t.Fatalf("writeShareArchive() error = %v, want wrapped close sentinel", err)
	}
	for _, leaked := range []string{"/docs", "secret-token", "secret-token.pdf"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("writeShareArchive() error leaked %q: %q", leaked, err.Error())
		}
	}
	if closeErr := zipWriter.Close(); closeErr != nil {
		t.Fatalf("zipWriter.Close() error: %v", closeErr)
	}
}

func TestValidateShareArchiveEntriesMissingMetadataDoesNotExposeSourcePath(t *testing.T) {
	err := validateShareArchiveEntries([]shareArchiveEntry{{
		sourcePath: "/docs/secret-token.pdf",
		zipName:    "secret-token.pdf",
	}})
	if !errors.Is(err, errShareArchiveMissingMetadata) {
		t.Fatalf("validateShareArchiveEntries() error = %v, want missing metadata", err)
	}
	for _, leaked := range []string{"/docs", "secret-token", "secret-token.pdf"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("validateShareArchiveEntries() error leaked %q: %q", leaked, err.Error())
		}
	}
}

func TestShareRelativePathPreservesShareBoundary(t *testing.T) {
	tests := []struct {
		name      string
		basePath  string
		entryPath string
		want      string
		wantErr   bool
	}{
		{name: "root base same path", basePath: "/", entryPath: "/", want: "."},
		{name: "root base child", basePath: "/", entryPath: "/readme.txt", want: "readme.txt"},
		{name: "root base nested child", basePath: "/", entryPath: "/docs/readme.txt", want: "docs/readme.txt"},
		{name: "folder base same path", basePath: "/docs", entryPath: "/docs", want: "."},
		{name: "folder base direct child", basePath: "/docs", entryPath: "/docs/readme.txt", want: "readme.txt"},
		{name: "folder base nested child", basePath: "/docs", entryPath: "/docs/nested/readme.txt", want: "nested/readme.txt"},
		{name: "similar prefix sibling", basePath: "/docs", entryPath: "/docs-archive/readme.txt", wantErr: true},
		{name: "parent after clean", basePath: "/docs", entryPath: "/docs/../secret.txt", wantErr: true},
		{name: "relative entry path", basePath: "/docs", entryPath: "docs/readme.txt", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := shareRelativePath(tt.basePath, tt.entryPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("shareRelativePath() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("shareRelativePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsWithinSharePathPreservesShareBoundary(t *testing.T) {
	tests := []struct {
		name       string
		basePath   string
		targetPath string
		want       bool
	}{
		{name: "root contains root", basePath: "/", targetPath: "/", want: true},
		{name: "root contains child", basePath: "/", targetPath: "/readme.txt", want: true},
		{name: "root rejects relative", basePath: "/", targetPath: "readme.txt", want: false},
		{name: "folder contains itself", basePath: "/docs", targetPath: "/docs", want: true},
		{name: "folder contains child", basePath: "/docs", targetPath: "/docs/readme.txt", want: true},
		{name: "folder contains nested child", basePath: "/docs", targetPath: "/docs/nested/readme.txt", want: true},
		{name: "folder rejects similar prefix", basePath: "/docs", targetPath: "/docs-archive/readme.txt", want: false},
		{name: "folder rejects parent after clean", basePath: "/docs", targetPath: "/docs/../secret.txt", want: false},
		{name: "folder rejects relative", basePath: "/docs", targetPath: "docs/readme.txt", want: false},
		{name: "folder rejects dot segment", basePath: "/docs", targetPath: "/docs/./readme.txt", want: false},
		{name: "folder rejects backslash", basePath: "/docs", targetPath: `/docs/private\secret.txt`, want: false},
		{name: "folder rejects control character", basePath: "/docs", targetPath: "/docs/private\x00secret.txt", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWithinSharePath(tt.basePath, tt.targetPath); got != tt.want {
				t.Fatalf("isWithinSharePath(%q, %q) = %v, want %v", tt.basePath, tt.targetPath, got, tt.want)
			}
		})
	}
}

func TestHandlerAuthorizeSharePathRejectsDirtyTargetBeforeAuthorizer(t *testing.T) {
	handler := NewHandler(nil, nil)
	share := &Share{Path: "/docs"}
	var authorizerCalls int
	handler.SetPathAccessAuthorizer(func(ctx context.Context, share *Share, targetPath string) error {
		authorizerCalls++
		return nil
	})

	for _, targetPath := range []string{
		"/docs/./readme.txt",
		`/docs/private\secret.txt`,
		"/docs/private\x00secret.txt",
	} {
		t.Run(targetPath, func(t *testing.T) {
			authorizerCalls = 0
			err := handler.authorizeSharePath(context.Background(), share, targetPath)
			if !errors.Is(err, ErrShareNotFound) {
				t.Fatalf("authorizeSharePath(%q) error = %v, want %v", targetPath, err, ErrShareNotFound)
			}
			if authorizerCalls != 0 {
				t.Fatalf("authorizeSharePath(%q) called path access authorizer %d times, want 0", targetPath, authorizerCalls)
			}
		})
	}
}

func (f *failingReadCloser) Read(p []byte) (int, error) {
	return 0, f.err
}

func (f *failingReadCloser) Close() error {
	return nil
}

func (p *partialFailingReadCloser) Read(buf []byte) (int, error) {
	return p.reader.Read(buf)
}

func (p *partialFailingReadCloser) Close() error {
	return nil
}

func (c *closeFailingReadCloser) Read(buf []byte) (int, error) {
	return c.reader.Read(buf)
}

func (c *closeFailingReadCloser) Close() error {
	return c.err
}

func (r *dataAndErrorReader) Read(buf []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(buf, r.data), r.err
}

func (r *readSeekCloser) Close() error {
	return nil
}

func TestPrefetchDownloadChunkPreservesEOFWithData(t *testing.T) {
	chunk, exhausted, err := prefetchDownloadChunk(&dataAndErrorReader{
		data: []byte("complete"),
		err:  io.EOF,
	})
	if err != nil {
		t.Fatalf("prefetchDownloadChunk() error: %v", err)
	}
	if !exhausted {
		t.Fatal("prefetchDownloadChunk() exhausted = false, want true")
	}
	if string(chunk) != "complete" {
		t.Fatalf("prefetchDownloadChunk() chunk = %q, want complete", string(chunk))
	}
}

func TestPrefetchDownloadChunkReturnsPartialReadErrorBeforeStreaming(t *testing.T) {
	readErr := errors.New("partial read failed")
	chunk, exhausted, err := prefetchDownloadChunk(&dataAndErrorReader{
		data: []byte("partial"),
		err:  readErr,
	})
	if !errors.Is(err, readErr) {
		t.Fatalf("prefetchDownloadChunk() error = %v, want %v", err, readErr)
	}
	if chunk != nil {
		t.Fatalf("prefetchDownloadChunk() chunk = %q, want nil", string(chunk))
	}
	if exhausted {
		t.Fatal("prefetchDownloadChunk() exhausted = true, want false")
	}
}

func (w *failFirstWriteResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failFirstWriteResponseWriter) WriteHeader(statusCode int) {
	if w.status == 0 {
		w.status = statusCode
	}
}

func (w *failFirstWriteResponseWriter) Write(p []byte) (int, error) {
	if !w.failed {
		w.failed = true
		if w.status == 0 {
			w.status = http.StatusOK
		}
		if w.writeErr == nil {
			w.writeErr = errors.New("client write failed")
		}
		return 0, w.writeErr
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

func assertFirstWriteFailureDidNotAttemptFallback(t *testing.T, writer *failFirstWriteResponseWriter) {
	t.Helper()
	if !writer.failed {
		t.Fatal("expected the initial response write to be attempted")
	}
	if writer.status != http.StatusOK {
		t.Fatalf("committed status = %d, want 200", writer.status)
	}
	if writer.body.Len() != 0 {
		t.Fatalf("fallback body = %q, want empty", writer.body.String())
	}
}

func (w *failPartialWriteResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failPartialWriteResponseWriter) WriteHeader(statusCode int) {
	if w.status == 0 {
		w.status = statusCode
	}
}

func (w *failPartialWriteResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.failed {
		w.failed = true
		limit := w.limit
		if limit <= 0 || limit > len(p) {
			limit = len(p) / 2
			if limit == 0 {
				limit = 1
			}
		}
		_, _ = w.body.Write(p[:limit])
		if w.writeErr == nil {
			w.writeErr = errors.New("client write failed")
		}
		return limit, w.writeErr
	}
	return w.body.Write(p)
}

func (f *fakeShareFS) OpenFile(ctx context.Context, filePath string) (FileReader, error) {
	if f.beforeOpenFile != nil {
		if err := f.beforeOpenFile(filePath); err != nil {
			return nil, err
		}
	}
	if f.openErrByPath != nil {
		if err, ok := f.openErrByPath[filePath]; ok {
			return nil, err
		}
	}
	if f.openByPath != nil {
		if reader, ok := f.openByPath[filePath]; ok {
			return reader, nil
		}
	}
	return nil, nil
}

type fakeShareMutationLease struct {
	fs *fakeShareFS
}

func (lease *fakeShareMutationLease) Stat(ctx context.Context, filePath string) (*storage.FileInfo, error) {
	return lease.fs.Stat(ctx, filePath)
}

func (*fakeShareMutationLease) Release() {}

func (f *fakeShareFS) AcquireMutationLease(context.Context) (MutationLease, error) {
	return &fakeShareMutationLease{fs: f}, nil
}

func (f *fakeShareSnapshotFS) OpenFileSnapshot(ctx context.Context, filePath string) (*os.File, *storage.FileInfo, error) {
	if f.snapshotByPath == nil {
		return nil, nil, storage.ErrNotFound
	}
	snapshot, ok := f.snapshotByPath[filePath]
	if !ok {
		return nil, nil, storage.ErrNotFound
	}
	if snapshot.err != nil {
		return nil, nil, snapshot.err
	}
	file, err := os.Open(snapshot.hostPath)
	if err != nil {
		return nil, nil, err
	}
	if snapshot.info == nil {
		_ = file.Close()
		return nil, nil, storage.ErrNotFound
	}
	return file, snapshot.info, nil
}

func (f *fakeShareFS) Stat(ctx context.Context, filePath string) (*storage.FileInfo, error) {
	if f.beforeStat != nil {
		if err := f.beforeStat(filePath); err != nil {
			return nil, err
		}
	}
	if f.statErrByPath != nil {
		if err, ok := f.statErrByPath[filePath]; ok {
			return nil, err
		}
	}
	if f.statInfoByPath != nil {
		if info, ok := f.statInfoByPath[filePath]; ok {
			return info, nil
		}
	}
	if f.statInfo == nil {
		return nil, storage.ErrNotFound
	}
	return f.statInfo, nil
}

func (f *fakeShareFS) ReadDir(ctx context.Context, filePath string) ([]*storage.FileInfo, error) {
	if f.beforeReadDir != nil {
		if err := f.beforeReadDir(filePath); err != nil {
			return nil, err
		}
	}
	if f.readDirErr != nil {
		return nil, f.readDirErr
	}
	if f.dirItemsByPath != nil {
		if items, ok := f.dirItemsByPath[filePath]; ok {
			return items, nil
		}
	}
	return f.dirItems, nil
}

func (f *fakeShareFS) ReadDirLimit(ctx context.Context, filePath string, limit int) ([]*storage.FileInfo, error) {
	f.readDirLimits = append(f.readDirLimits, limit)
	items, err := f.ReadDir(ctx, filePath)
	if err != nil || len(items) <= limit {
		f.readDirReturned += len(items)
		return items, err
	}
	f.readDirReturned += limit
	return items[:limit], nil
}

func newRouteRequest(method, target, id string, body []byte) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.5:1234"
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func serveDownloadShareWithTicket(t *testing.T, handler *Handler, w http.ResponseWriter, req *http.Request) {
	t.Helper()
	if !attachDownloadTicketForTest(t, handler, w, req, false) {
		return
	}
	handler.DownloadShare(w, req)
}

func serveDownloadShareFileWithTicket(t *testing.T, handler *Handler, w http.ResponseWriter, req *http.Request) {
	t.Helper()
	if !attachDownloadTicketForTest(t, handler, w, req, true) {
		return
	}
	handler.DownloadShareFile(w, req)
}

func attachDownloadTicketForTest(t *testing.T, handler *Handler, w http.ResponseWriter, req *http.Request, nested bool) bool {
	t.Helper()
	if req.Method != http.MethodGet {
		return true
	}
	if values := req.URL.Query()["ticket"]; len(values) == 1 && values[0] != "" {
		return true
	}
	id := chi.URLParam(req, "id")
	relPath := ""
	if nested {
		var err error
		relPath, err = shareDownloadPathFromRequest(req, id)
		if err != nil {
			return true
		}
		relPath, err = normalizeShareRelativePath(relPath)
		if err != nil {
			return true
		}
	}
	archive, err := shareArchiveFormatFromRequest(req)
	if err != nil {
		return true
	}
	ensureDownloadTicketTestMetadata(t, handler, id, relPath, archive)

	body := make(map[string]string, 3)
	body["client_nonce"] = fixedDownloadTicketClientNonce
	if relPath != "" {
		body["path"] = relPath
	}
	if archive != "" {
		body["archive"] = archive
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal test download ticket request: %v", err)
	}
	ticketPath := "/s/" + id + "/download-ticket"
	if strings.HasPrefix(req.URL.Path, "/api/v1/public/shares/") {
		ticketPath = "/api/v1/public/shares/" + id + "/download-ticket"
	}
	ticketReq := newRouteRequest(http.MethodPost, ticketPath, id, bodyBytes)
	ticketReq.RemoteAddr = req.RemoteAddr
	for _, cookie := range req.Cookies() {
		ticketReq.AddCookie(cookie)
	}
	ticketRecorder := httptest.NewRecorder()
	handler.CreateDownloadTicket(ticketRecorder, ticketReq)
	if ticketRecorder.Code != http.StatusOK {
		copyTestHTTPResponse(w, ticketRecorder)
		return false
	}

	var response DownloadTicketResponse
	if err := json.Unmarshal(ticketRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode test download ticket response: %v", err)
	}
	query := req.URL.Query()
	query.Set("ticket", response.Ticket)
	req.URL.RawQuery = query.Encode()
	for _, cookie := range ticketRecorder.Result().Cookies() {
		if strings.HasPrefix(cookie.Name, downloadTicketCookiePrefix) {
			req.AddCookie(cookie)
			break
		}
	}
	return true
}

func ensureDownloadTicketTestMetadata(t *testing.T, handler *Handler, id, relPath, archive string) {
	t.Helper()
	if archive != "" || handler == nil || handler.store == nil {
		return
	}
	share, err := handler.store.Get(id)
	if err != nil {
		return
	}
	targetPath := share.Path
	if relPath != "" {
		targetPath = path.Join(share.Path, relPath)
	}
	var fake *fakeShareFS
	switch fs := handler.fs.(type) {
	case *fakeShareFS:
		fake = fs
	case *fakeShareSnapshotFS:
		fake = fs.fakeShareFS
	}
	if fake == nil || fake.statInfo != nil {
		return
	}
	if _, ok := fake.statErrByPath[targetPath]; ok {
		return
	}
	if fake.statInfoByPath == nil {
		fake.statInfoByPath = make(map[string]*storage.FileInfo)
	}
	if _, ok := fake.statInfoByPath[targetPath]; !ok {
		fake.statInfoByPath[targetPath] = &storage.FileInfo{
			Path: targetPath,
			Name: path.Base(targetPath),
		}
	}
}

func copyTestHTTPResponse(w http.ResponseWriter, source *httptest.ResponseRecorder) {
	for key, values := range source.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(source.Code)
	_, _ = w.Write(source.Body.Bytes())
}

func decodeResponseBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return payload
}

func assertShareAccessCookiePaths(t *testing.T, cookies []*http.Cookie, id string) {
	t.Helper()
	expectedPaths := map[string]bool{
		"/s/" + id:                    false,
		"/api/v1/public/shares/" + id: false,
	}
	if len(cookies) != len(expectedPaths) {
		t.Fatalf("expected %d share access cookies, got %d", len(expectedPaths), len(cookies))
	}
	for _, cookie := range cookies {
		if cookie.Name != shareAccessCookieName(id) {
			t.Fatalf("unexpected cookie name %q", cookie.Name)
		}
		if _, ok := expectedPaths[cookie.Path]; !ok {
			t.Fatalf("unexpected share access cookie path %q", cookie.Path)
		}
		if cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("share access cookie SameSite = %v, want Strict", cookie.SameSite)
		}
		expectedPaths[cookie.Path] = true
	}
	for cookiePath, found := range expectedPaths {
		if !found {
			t.Fatalf("missing share access cookie path %q", cookiePath)
		}
	}
}

func assertUntrustedDownloadHeaders(t *testing.T, header http.Header) {
	t.Helper()
	if cacheControl := header.Get("Cache-Control"); cacheControl != "private, no-cache" {
		t.Fatalf("download Cache-Control = %q, want private, no-cache", cacheControl)
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("download X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("download X-Frame-Options = %q, want SAMEORIGIN", got)
	}
	if got := header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("download Referrer-Policy = %q, want no-referrer", got)
	}
	if got := header.Get("Content-Security-Policy"); !strings.Contains(got, "sandbox allow-downloads") || !strings.Contains(got, "default-src 'none'") || !strings.Contains(got, "frame-ancestors 'self'") || strings.Contains(got, "frame-ancestors 'none'") {
		t.Fatalf("download Content-Security-Policy = %q, want sandboxed attachment policy with same-origin framing only", got)
	}
}

func readShareZipEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader() error: %v", err)
	}

	entries := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			entries[file.Name] = nil
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry %q: %v", file.Name, err)
		}
		content, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %q: %v", file.Name, err)
		}
		if closeErr != nil {
			t.Fatalf("close zip entry %q: %v", file.Name, closeErr)
		}
		entries[file.Name] = content
	}
	return entries
}

func assertPublicShareJSONHeaders(t *testing.T, header http.Header) {
	t.Helper()
	if cacheControl := header.Get("Cache-Control"); cacheControl != "private, no-cache" {
		t.Fatalf("public share JSON Cache-Control = %q, want private, no-cache", cacheControl)
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("public share JSON X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("public share JSON Referrer-Policy = %q, want no-referrer", got)
	}
	foundVaryCookie := false
	for _, value := range header.Values("Vary") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "Cookie") {
				foundVaryCookie = true
			}
		}
	}
	if !foundVaryCookie {
		t.Fatalf("public share JSON Vary = %q, want Cookie", header.Values("Vary"))
	}
}

func decodeEnvelopeData(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode envelope: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success envelope, got %s", recorder.Body.String())
	}
	return payload.Data
}

func TestWriteShareJSON_MarshalFailureReturnsStructuredJSONError(t *testing.T) {
	originalMarshal := marshalShareJSON
	marshalShareJSON = func(any) ([]byte, error) {
		return nil, errors.New("marshal failed")
	}
	t.Cleanup(func() {
		marshalShareJSON = originalMarshal
	})

	recorder := httptest.NewRecorder()
	if writeShareJSON(recorder, http.StatusOK, map[string]any{"ok": true}) {
		t.Fatal("writeShareJSON returned success on marshal failure")
	}

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("marshal failure status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("marshal failure Content-Type = %q, want application/json", contentType)
	}

	var payload struct {
		Success bool `json:"success"`
		Error   *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("marshal failure response is not JSON: %v; body=%s", err, recorder.Body.String())
	}
	if payload.Success || payload.Error == nil || payload.Error.Code != "INTERNAL_ERROR" || payload.Error.Message != "internal server error" {
		t.Fatalf("unexpected marshal failure payload: %s", recorder.Body.String())
	}
}

func TestRoutes_DispatchesAuthenticatedShareEndpoints(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	handler.SetBaseURL("https://files.example.test/public/")
	router := chi.NewRouter()
	handler.Routes(router)

	getReq := httptest.NewRequest(http.MethodGet, "/"+share.ID, nil)
	getReq = getReq.WithContext(auth.WithClaimsContext(getReq.Context(), &auth.TokenClaims{UserID: "user1"}))
	getRecorder := httptest.NewRecorder()
	router.ServeHTTP(getRecorder, getReq)

	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET route status = %d, want %d: %s", getRecorder.Code, http.StatusOK, getRecorder.Body.String())
	}
	getData := decodeEnvelopeData(t, getRecorder)
	if getData["id"] != share.ID {
		t.Fatalf("expected share id %q, got %+v", share.ID, getData)
	}
	if getData["url"] != "https://files.example.test/public/s/"+share.ID {
		t.Fatalf("unexpected share url %q", getData["url"])
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/"+share.ID, nil)
	deleteReq = deleteReq.WithContext(auth.WithClaimsContext(deleteReq.Context(), &auth.TokenClaims{UserID: "user1"}))
	deleteRecorder := httptest.NewRecorder()
	router.ServeHTTP(deleteRecorder, deleteReq)

	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("DELETE route status = %d, want %d: %s", deleteRecorder.Code, http.StatusOK, deleteRecorder.Body.String())
	}
	deletePayload := decodeResponseBody(t, deleteRecorder)
	if deletePayload["success"] != true || deletePayload["message"] != "share deleted successfully" {
		t.Fatalf("unexpected delete response: %+v", deletePayload)
	}
	if _, err := store.Get(share.ID); !errors.Is(err, ErrShareNotFound) {
		t.Fatalf("expected share to be deleted, got err=%v", err)
	}
}

func TestPublicRoutes_DispatchesPublicShareAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:        "/docs/report.pdf",
		Type:        ShareTypeFile,
		CreatedBy:   "user1",
		Description: "Quarterly report",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{
			Path:    "/docs/report.pdf",
			Name:    "report.pdf",
			IsDir:   false,
			Size:    42,
			ModTime: time.Unix(100, 0),
		},
	})
	router := chi.NewRouter()
	handler.PublicRoutes(router)

	req := httptest.NewRequest(http.MethodGet, "/"+share.ID, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("public GET route status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var info PublicShareInfo
	if err := json.Unmarshal(recorder.Body.Bytes(), &info); err != nil {
		t.Fatalf("failed to decode public share info: %v", err)
	}
	if info.ID != share.ID || info.FileName != "report.pdf" || info.FileSize == nil || *info.FileSize != 42 || info.Description != "Quarterly report" {
		t.Fatalf("unexpected public share info: %+v", info)
	}
}

func TestStreamResponseErrorWrapsAndTracksStartedState(t *testing.T) {
	baseErr := errors.New("client disconnected")
	streamErr := &streamResponseError{err: baseErr, responseStarted: true}

	if streamErr.Error() != baseErr.Error() {
		t.Fatalf("Error() = %q, want %q", streamErr.Error(), baseErr.Error())
	}
	if !errors.Is(streamErr, baseErr) {
		t.Fatalf("expected stream error to unwrap %v", baseErr)
	}
	if !streamResponseStarted(streamErr) {
		t.Fatal("expected started stream response to be detected")
	}
	if streamResponseStarted(&streamResponseError{err: baseErr}) {
		t.Fatal("unexpected started response for stream error before write")
	}
	if streamResponseStarted(baseErr) {
		t.Fatal("unexpected started response for unrelated error")
	}

	headerRecorder := httptest.NewRecorder()
	headerTracker := &responseStartTrackingWriter{ResponseWriter: headerRecorder}
	headerTracker.WriteHeader(http.StatusAccepted)
	if !headerTracker.started {
		t.Fatal("expected WriteHeader to mark response started")
	}

	failedWriter := &failFirstWriteResponseWriter{writeErr: baseErr}
	failedTracker := &responseStartTrackingWriter{ResponseWriter: failedWriter}
	if _, err := failedTracker.Write([]byte("hello")); !errors.Is(err, baseErr) {
		t.Fatalf("Write() error = %v, want %v", err, baseErr)
	}
	if !failedTracker.started {
		t.Fatal("zero-byte failed write must conservatively mark response started")
	}

	partialWriter := &failPartialWriteResponseWriter{limit: 2, writeErr: baseErr}
	partialTracker := &responseStartTrackingWriter{ResponseWriter: partialWriter}
	if n, err := partialTracker.Write([]byte("hello")); n != 2 || !errors.Is(err, baseErr) {
		t.Fatalf("partial Write() = (%d, %v), want (2, %v)", n, err, baseErr)
	}
	if !partialTracker.started {
		t.Fatal("partial write should mark response started")
	}
}

func TestHashPassword_GeneratesVerifiableHash(t *testing.T) {
	hash, err := hashPassword("secret")
	if err != nil {
		t.Fatalf("hashPassword() error: %v", err)
	}
	if hash == "" || hash == "secret" {
		t.Fatalf("unexpected password hash %q", hash)
	}

	share := &Share{PasswordHash: hash}
	if !share.CheckPassword("secret") {
		t.Fatal("expected generated hash to verify original password")
	}
	if share.CheckPassword("wrong") {
		t.Fatal("expected generated hash to reject wrong password")
	}
}

func TestHashPassword_RejectsPasswordAboveBcryptLimit(t *testing.T) {
	if _, err := hashPassword(strings.Repeat("a", maxSharePasswordBytes+1)); !errors.Is(err, errSharePasswordLong) {
		t.Fatalf("hashPassword() error = %v, want %v", err, errSharePasswordLong)
	}
}

func TestShareContextIdentityUsesUserContextWhenClaimsAreMissing(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.ContextKeyUser, &auth.User{
		ID:       "user-ctx",
		Username: "alice",
	})

	if got := getUserIDFromContext(ctx); got != "user-ctx" {
		t.Fatalf("getUserIDFromContext() = %q, want user-ctx", got)
	}
	if got := getShareOwnerIdentifiersFromContext(ctx); len(got) != 2 || got[0] != "user-ctx" || got[1] != "alice" {
		t.Fatalf("owner identifiers = %#v, want [user-ctx alice]", got)
	}
	if !shareOwnedByRequester(ctx, &Share{CreatedBy: "user-ctx"}) {
		t.Fatal("expected user-context requester to own share by user ID")
	}
	if !shareOwnedByRequester(ctx, &Share{CreatedBy: "alice"}) {
		t.Fatal("expected user-context requester to own legacy username share")
	}
}

func TestShareContextIdentityIgnoresDisabledUserContextWhenClaimsAreMissing(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.ContextKeyUser, &auth.User{
		ID:       "disabled-user",
		Username: "alice",
		Disabled: true,
	})

	if got := getUserIDFromContext(ctx); got != "" {
		t.Fatalf("getUserIDFromContext() = %q, want empty", got)
	}
	if got := getShareOwnerIdentifiersFromContext(ctx); len(got) != 0 {
		t.Fatalf("owner identifiers = %#v, want empty", got)
	}
	if shareOwnedByRequester(ctx, &Share{CreatedBy: "disabled-user"}) {
		t.Fatal("disabled user context should not own shares")
	}
}

func TestShareContextIdentityIgnoresDisabledUserContextEvenWithClaims(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.ContextKeyUser, &auth.User{
		ID:       "disabled-user",
		Username: "alice",
		Disabled: true,
	})
	ctx = auth.WithClaimsContext(ctx, &auth.TokenClaims{
		UserID:   "disabled-user",
		Username: "alice",
	})

	if got := getUserIDFromContext(ctx); got != "" {
		t.Fatalf("getUserIDFromContext() = %q, want empty", got)
	}
	if got := getShareOwnerIdentifiersFromContext(ctx); len(got) != 0 {
		t.Fatalf("owner identifiers = %#v, want empty", got)
	}
	if shareOwnedByRequester(ctx, &Share{CreatedBy: "disabled-user"}) {
		t.Fatal("disabled user context should not own shares")
	}
}

func TestCreateShare_UsesBaseURL(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{statInfo: &storage.FileInfo{Path: "/docs/report.pdf", Name: "report.pdf", IsDir: false}})
	handler.SetBaseURL("https://nas.example.com/")

	body, err := json.Marshal(CreateShareRequest{
		Path: "/docs/report.pdf",
		Type: "file",
	})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()
	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", recorder.Code)
	}

	payload := decodeEnvelopeData(t, recorder)
	urlValue, ok := payload["url"].(string)
	if !ok {
		t.Fatalf("expected url in response")
	}
	if !strings.HasPrefix(urlValue, "https://nas.example.com/s/") {
		t.Fatalf("expected base URL applied, got %q", urlValue)
	}
	if strings.Contains(urlValue, "https://nas.example.com//s/") {
		t.Fatalf("expected trimmed base URL, got %q", urlValue)
	}
}

func TestShareMutationPersistenceWarningsReturnSuccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	handler := NewHandler(store, &fakeShareFS{statInfo: &storage.FileInfo{Path: "/docs/report.pdf", Name: "report.pdf", IsDir: false}})

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	assertWarningSuccess := func(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantMessage string) {
		t.Helper()
		if recorder.Code != wantStatus {
			t.Fatalf("expected status %d, got %d: %s", wantStatus, recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("Warning") != sharePersistenceWarningHeader {
			t.Fatalf("expected Warning header %q, got %q", sharePersistenceWarningHeader, recorder.Header().Get("Warning"))
		}
		var payload struct {
			Success bool   `json:"success"`
			Warning bool   `json:"warning"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !payload.Success || !payload.Warning {
			t.Fatalf("expected warning success response, got %s", recorder.Body.String())
		}
		if payload.Message != wantMessage {
			t.Fatalf("expected message %q, got %q", wantMessage, payload.Message)
		}
	}

	createBody, err := json.Marshal(CreateShareRequest{Path: "/docs/report.pdf", Type: "file"})
	if err != nil {
		t.Fatalf("failed to marshal create request: %v", err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(createBody))
	createReq = createReq.WithContext(auth.WithClaimsContext(createReq.Context(), &auth.TokenClaims{UserID: "user1"}))
	createRecorder := httptest.NewRecorder()
	handler.CreateShare(createRecorder, createReq)
	assertWarningSuccess(t, createRecorder, http.StatusCreated, "share created with persistence warning")

	shares := store.ListByUser("user1")
	if len(shares) != 1 {
		t.Fatalf("expected created share to persist after warning, got %d shares", len(shares))
	}
	shareID := shares[0].ID

	updateReq := newRouteRequest(http.MethodPut, "/api/v1/shares/"+shareID, shareID, []byte(`{"description":"after"}`))
	updateReq = updateReq.WithContext(auth.WithClaimsContext(updateReq.Context(), &auth.TokenClaims{UserID: "user1"}))
	updateRecorder := httptest.NewRecorder()
	handler.UpdateShare(updateRecorder, updateReq)
	assertWarningSuccess(t, updateRecorder, http.StatusOK, "share updated with persistence warning")
	updatedShare, err := store.Get(shareID)
	if err != nil {
		t.Fatalf("Get(updated share) error: %v", err)
	}
	if updatedShare.Description != "after" {
		t.Fatalf("expected warned update to persist description, got %q", updatedShare.Description)
	}

	deleteReq := newRouteRequest(http.MethodDelete, "/api/v1/shares/"+shareID, shareID, nil)
	deleteReq = deleteReq.WithContext(auth.WithClaimsContext(deleteReq.Context(), &auth.TokenClaims{UserID: "user1"}))
	deleteRecorder := httptest.NewRecorder()
	handler.DeleteShare(deleteRecorder, deleteReq)
	assertWarningSuccess(t, deleteRecorder, http.StatusOK, "share deleted with persistence warning")
	if _, err := store.Get(shareID); !errors.Is(err, ErrShareNotFound) {
		t.Fatalf("expected warned delete to persist removal, got %v", err)
	}
}

func TestCreateShare_InvalidNegativeExpiresInReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/secret.pdf", Name: "secret.pdf", Size: 256},
	})
	body := []byte(`{"path":"/docs/report.pdf","type":"file","expires_in":"-1h"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_EXPIRES_IN" {
		t.Fatalf("expected INVALID_EXPIRES_IN code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_RejectsPasswordAboveBcryptLimit(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/report.pdf", Name: "report.pdf", Size: 256},
	})
	body, err := json.Marshal(CreateShareRequest{
		Path:     "/docs/report.pdf",
		Type:     "file",
		Password: strings.Repeat("a", maxSharePasswordBytes+1),
	})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "PASSWORD_TOO_LONG" {
		t.Fatalf("expected PASSWORD_TOO_LONG code, got %v", errorPayload["code"])
	}
	if len(store.ListAll()) != 0 {
		t.Fatal("expected overlong password create to leave no persisted share")
	}
}

func TestCreateShare_RejectsUnknownFields(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/report.pdf", Name: "report.pdf", Size: 256},
	})
	body := []byte(`{"path":"/docs/report.pdf","type":"file","unexpected":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_REQUEST" {
		t.Fatalf("expected INVALID_REQUEST code, got %v", errorPayload["code"])
	}
	if len(store.ListAll()) != 0 {
		t.Fatal("expected share creation to be rejected before persistence")
	}
}

func TestCreateShare_RejectsOversizedRequestBody(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, nil)
	body := bytes.Repeat([]byte{'x'}, defaultJSONRequestBodyLimit+1)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("expected PAYLOAD_TOO_LARGE code, got %v", errorPayload["code"])
	}
	if len(store.ListAll()) != 0 {
		t.Fatal("expected oversized share creation to be rejected before persistence")
	}
}

func TestCreateShare_FailsClosedWhenFilesystemUnavailable(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, nil)
	body := []byte(`{"path":"/docs/report.pdf","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "FILESYSTEM_UNAVAILABLE" {
		t.Fatalf("expected FILESYSTEM_UNAVAILABLE code, got %v", errorPayload["code"])
	}
	if len(store.ListAll()) != 0 {
		t.Fatal("expected share creation to fail before persistence when filesystem is unavailable")
	}
}

func TestCreateShare_NegativeMaxAccessReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/secret.pdf", Name: "secret.pdf", Size: 256},
	})
	body := []byte(`{"path":"/docs/report.pdf","type":"file","max_access":-1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_MAX_ACCESS" {
		t.Fatalf("expected INVALID_MAX_ACCESS code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_RejectsTraversalPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/report.pdf", Name: "report.pdf", Size: 256},
	})

	for _, rawPath := range []string{
		"../docs/report.pdf",
		`..\\docs\\report.pdf`,
		"/docs/./report.pdf",
		"./docs/report.pdf",
	} {
		body := []byte(`{"path":"` + rawPath + `","type":"file"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
		req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
		recorder := httptest.NewRecorder()

		handler.CreateShare(recorder, req)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("path %q expected status 400, got %d", rawPath, recorder.Code)
		}
		payload := decodeResponseBody(t, recorder)
		errorPayload, ok := payload["error"].(map[string]any)
		if !ok {
			t.Fatalf("path %q expected error payload, got %v", rawPath, payload)
		}
		if errorPayload["code"] != "INVALID_PATH" {
			t.Fatalf("path %q expected INVALID_PATH code, got %v", rawPath, errorPayload["code"])
		}
	}

	if len(store.ListAll()) != 0 {
		t.Fatal("expected traversal-like share creation to be rejected before persistence")
	}
}

func TestCreateShare_WhitespaceOnlyPathReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/secret.pdf", Name: "secret.pdf", Size: 256},
	})
	body := []byte(`{"path":"   ","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "MISSING_PATH" {
		t.Fatalf("expected MISSING_PATH code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_PreservesWhitespaceInPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	targetPath := "/docs/report.pdf "
	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: targetPath, Name: "report.pdf ", IsDir: false},
		beforeStat: func(filePath string) error {
			if filePath != targetPath {
				return storage.ErrNotFound
			}
			return nil
		},
	})
	body := []byte(`{"path":"/docs/report.pdf ","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeEnvelopeData(t, recorder)
	pathValue, ok := payload["path"].(string)
	if !ok {
		t.Fatalf("expected path in response, got %v", payload)
	}
	if pathValue != targetPath {
		t.Fatalf("expected whitespace-preserving share path, got %q", pathValue)
	}

	shares := store.GetByPath(targetPath)
	if len(shares) != 1 {
		t.Fatalf("expected stored share indexed by whitespace-preserving path, got %+v", shares)
	}
	if shares[0].Path != targetPath {
		t.Fatalf("expected persisted share path %q, got %q", targetPath, shares[0].Path)
	}
}

func TestCreateShare_NormalizesRelativePath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{statInfo: &storage.FileInfo{Path: "/docs/report.pdf", Name: "report.pdf", IsDir: false}})
	body := []byte(`{"path":"docs/report.pdf","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeEnvelopeData(t, recorder)
	pathValue, ok := payload["path"].(string)
	if !ok {
		t.Fatalf("expected path in response, got %v", payload)
	}
	if pathValue != "/docs/report.pdf" {
		t.Fatalf("expected normalized absolute path, got %q", pathValue)
	}
}

func TestCreateShare_NormalizesBackslashes(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{statInfo: &storage.FileInfo{Path: "/docs/report.pdf", Name: "report.pdf", IsDir: false}})
	body := []byte(`{"path":"docs\\report.pdf","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeEnvelopeData(t, recorder)
	pathValue, ok := payload["path"].(string)
	if !ok {
		t.Fatalf("expected path in response, got %v", payload)
	}
	if pathValue != "/docs/report.pdf" {
		t.Fatalf("expected normalized share path, got %q", pathValue)
	}
}

func TestCreateShare_MissingTargetReturnsNotFound(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"path":"/docs/missing.pdf","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "FILE_NOT_FOUND" {
		t.Fatalf("expected FILE_NOT_FOUND code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_ReturnsBadRequestWhenParentPathIsFile(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statErrByPath: map[string]error{
			"/docs/report.pdf/child.txt": storage.ErrNotDir,
		},
	})
	body := []byte(`{"path":"/docs/report.pdf/child.txt","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_PATH" {
		t.Fatalf("expected INVALID_PATH code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_FileTypeMustMatchTarget(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{statInfo: &storage.FileInfo{Path: "/docs", Name: "docs", IsDir: true}})
	body := []byte(`{"path":"/docs","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_SHARE_TYPE" {
		t.Fatalf("expected INVALID_SHARE_TYPE code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_FolderTypeMustMatchTarget(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{statInfo: &storage.FileInfo{Path: "/docs/report.pdf", Name: "report.pdf", IsDir: false}})
	body := []byte(`{"path":"/docs/report.pdf","type":"folder"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_SHARE_TYPE" {
		t.Fatalf("expected INVALID_SHARE_TYPE code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_InvalidTypeReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"path":"/docs/report.pdf","type":"symlink"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_SHARE_TYPE" {
		t.Fatalf("expected INVALID_SHARE_TYPE code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_InvalidPermissionReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"path":"/docs/report.pdf","type":"file","permission":"admin"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_PERMISSION" {
		t.Fatalf("expected INVALID_PERMISSION code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_ReadWritePermissionReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"path":"/docs/report.pdf","type":"file","permission":"read_write"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_PERMISSION" {
		t.Fatalf("expected INVALID_PERMISSION code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_ContextCanceledAfterStatDoesNotCommit(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	fs := &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/report.pdf", Name: "report.pdf"},
		beforeStat: func(string) error {
			cancel()
			return nil
		},
	}
	handler := NewHandler(store, fs)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/shares",
		strings.NewReader(`{"path":"/docs/report.pdf","type":"file"}`),
	).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code == http.StatusCreated {
		t.Fatalf("CreateShare() status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if shares := store.ListAll(); len(shares) != 0 {
		t.Fatalf("shares committed after cancellation: %+v", shares)
	}
}

func TestListShares_WrapsResponseData(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	_, err = store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/shares", nil)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.ListShares(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var payload struct {
		Success bool             `json:"success"`
		Data    []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success response, got %s", recorder.Body.String())
	}
	if len(payload.Data) != 1 {
		t.Fatalf("expected 1 share, got %d", len(payload.Data))
	}
}

func TestListShares_RejectsAmbiguousAllParameter(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/shares?all=true&all=false", nil)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.ListShares(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous all status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_REQUEST" {
		t.Fatalf("expected INVALID_REQUEST code, got %v", errorPayload["code"])
	}
}

func TestUpdateShare_InvalidExpiresInReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"expires_in":"not-a-duration"}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}

	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil {
		t.Fatalf("expected error payload, got %s", recorder.Body.String())
	}
	if payload.Error.Code != "INVALID_EXPIRES_IN" {
		t.Fatalf("expected INVALID_EXPIRES_IN, got %q", payload.Error.Code)
	}
	if payload.Error.Message != "invalid expires_in format" {
		t.Fatalf("unexpected error message: %q", payload.Error.Message)
	}
}

func TestUpdateShare_RejectsPasswordAboveBcryptLimit(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}
	originalHash := share.PasswordHash

	handler := NewHandler(store, &fakeShareFS{})
	overlongPassword := strings.Repeat("a", maxSharePasswordBytes+1)
	body, err := json.Marshal(UpdateShareRequest{Password: &overlongPassword})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "PASSWORD_TOO_LONG" {
		t.Fatalf("expected PASSWORD_TOO_LONG code, got %v", errorPayload["code"])
	}

	fresh, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if fresh.PasswordHash != originalHash {
		t.Fatal("expected rejected password update to preserve the existing hash")
	}
}

func TestUpdateShare_ReturnsUpdatedShare(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:        "/docs/report.pdf",
		Type:        ShareTypeFile,
		CreatedBy:   "user1",
		Description: "before",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"enabled":false,"description":"after"}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	var payload struct {
		Success bool      `json:"success"`
		Data    ShareInfo `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success response, got %s", recorder.Body.String())
	}
	if payload.Data.ID != share.ID {
		t.Fatalf("expected updated share id %q, got %q", share.ID, payload.Data.ID)
	}
	if payload.Data.Description != "after" {
		t.Fatalf("expected updated description, got %q", payload.Data.Description)
	}
	if payload.Data.Enabled {
		t.Fatal("expected updated share to be disabled")
	}
	if !strings.Contains(payload.Data.URL, share.ID) {
		t.Fatalf("expected share url to include id %q, got %q", share.ID, payload.Data.URL)
	}

	reloaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if reloaded.Description != "after" {
		t.Fatalf("expected stored description to be updated, got %q", reloaded.Description)
	}
	if reloaded.Enabled {
		t.Fatal("expected stored share to be disabled")
	}
}

func TestUpdateShare_ReturnsForbiddenForDifferentNonAdminUser(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:        "/docs/report.pdf",
		Type:        ShareTypeFile,
		CreatedBy:   "owner",
		Description: "before",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"enabled":false,"description":"after"}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "other", Role: auth.RoleUser}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", recorder.Code)
	}

	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil || payload.Error.Code != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %+v", payload.Error)
	}

	reloaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if !reloaded.Enabled {
		t.Fatal("expected share to remain enabled after forbidden update")
	}
	if reloaded.Description != "before" {
		t.Fatalf("expected description to remain unchanged, got %q", reloaded.Description)
	}
}

func TestUpdateShare_ReturnsNotFoundWhenShareDeletedAfterAuthorization(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	hookCalled := false
	handler.beforeMutateShare = func(id string) error {
		if hookCalled {
			return nil
		}
		hookCalled = true
		return store.Delete(id)
	}

	body := []byte(`{"enabled":false}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if !hookCalled {
		t.Fatal("expected beforeMutateShare hook to run")
	}
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}

	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil || payload.Error.Code != "SHARE_NOT_FOUND" {
		t.Fatalf("expected SHARE_NOT_FOUND, got %+v", payload.Error)
	}
}

func TestDeleteShare_ReturnsNotFoundWhenShareDeletedAfterAuthorization(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	hookCalled := false
	handler.beforeMutateShare = func(id string) error {
		if hookCalled {
			return nil
		}
		hookCalled = true
		return store.Delete(id)
	}

	req := newRouteRequest(http.MethodDelete, "/api/v1/shares/"+share.ID, share.ID, nil)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.DeleteShare(recorder, req)

	if !hookCalled {
		t.Fatal("expected beforeMutateShare hook to run")
	}
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}

	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil || payload.Error.Code != "SHARE_NOT_FOUND" {
		t.Fatalf("expected SHARE_NOT_FOUND, got %+v", payload.Error)
	}
}

func TestDeleteShare_ReturnsForbiddenForDifferentNonAdminUser(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "owner",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := newRouteRequest(http.MethodDelete, "/api/v1/shares/"+share.ID, share.ID, nil)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "other", Role: auth.RoleUser}))
	recorder := httptest.NewRecorder()

	handler.DeleteShare(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", recorder.Code)
	}

	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil || payload.Error.Code != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %+v", payload.Error)
	}

	if _, err := store.Get(share.ID); err != nil {
		t.Fatalf("expected share to remain after forbidden delete, got %v", err)
	}
}

func TestUpdateShare_InvalidPermissionReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"permission":"admin"}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}

	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil {
		t.Fatalf("expected error payload, got %s", recorder.Body.String())
	}
	if payload.Error.Code != "INVALID_PERMISSION" {
		t.Fatalf("expected INVALID_PERMISSION, got %q", payload.Error.Code)
	}
	if payload.Error.Message != "invalid permission" {
		t.Fatalf("unexpected error message: %q", payload.Error.Message)
	}
}

func TestUpdateShare_ReadWritePermissionReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"permission":"read_write"}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil || payload.Error.Code != "INVALID_PERMISSION" {
		t.Fatalf("expected INVALID_PERMISSION, got %+v", payload.Error)
	}
}

func TestUpdateShare_NonPositiveExpiresInReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"expires_in":"0s"}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil || payload.Error.Code != "INVALID_EXPIRES_IN" {
		t.Fatalf("expected INVALID_EXPIRES_IN error, got %s", recorder.Body.String())
	}
}

func TestUpdateShare_NegativeMaxAccessReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"max_access":-1}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}

	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil {
		t.Fatalf("expected error payload, got %s", recorder.Body.String())
	}
	if payload.Error.Code != "INVALID_MAX_ACCESS" {
		t.Fatalf("expected INVALID_MAX_ACCESS, got %q", payload.Error.Code)
	}
	if payload.Error.Message != "invalid max_access" {
		t.Fatalf("unexpected error message: %q", payload.Error.Message)
	}

	updated, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if updated.MaxAccess != 0 {
		t.Fatalf("expected max_access to remain unchanged, got %d", updated.MaxAccess)
	}
}

func TestAccessShare_PublicInfoFile(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{
			Path:  share.Path,
			Name:  "report.pdf",
			Size:  1234,
			IsDir: false,
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	assertPublicShareJSONHeaders(t, recorder.Header())

	payload := decodeResponseBody(t, recorder)
	if payload["file_name"] != "report.pdf" {
		t.Fatalf("expected file_name report.pdf, got %v", payload["file_name"])
	}
	if payload["file_size"] != float64(1234) {
		t.Fatalf("expected file_size 1234, got %v", payload["file_size"])
	}
}

func TestAccessShare_HeadDoesNotExposeInfo(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, nil)
	req := newRouteRequest(http.MethodHead, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("HEAD public info status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	if allow := recorder.Header().Get("Allow"); allow != http.MethodGet {
		t.Fatalf("HEAD public info Allow = %q, want GET", allow)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok || errorPayload["code"] != "METHOD_NOT_ALLOWED" {
		t.Fatalf("HEAD public info error payload = %s, want METHOD_NOT_ALLOWED", recorder.Body.String())
	}
}

func TestAccessShare_PublicInfoZeroByteFileIncludesFileSize(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/empty.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{
			Path:  share.Path,
			Name:  "empty.txt",
			Size:  0,
			IsDir: false,
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	fileSize, exists := payload["file_size"]
	if !exists {
		t.Fatalf("expected zero-byte file_size field to be present, got %s", recorder.Body.String())
	}
	if fileSize != float64(0) {
		t.Fatalf("expected file_size 0, got %v", fileSize)
	}
}

func TestAccessShare_DisabledOwnerReturnsGone(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")
	userStorePath := filepath.Join(tempDir, "users.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	userStore, _, err := auth.NewUserStore(userStorePath)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}
	owner, err := userStore.Create("share-owner", "password123", "", auth.RoleUser)
	if err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}
	owner.Disabled = true
	if err := userStore.Update(owner); err != nil {
		t.Fatalf("failed to disable owner: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	handler.SetUserStore(userStore)

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "SHARE_DISABLED") {
		t.Fatalf("expected SHARE_DISABLED error, got %s", recorder.Body.String())
	}
}

func TestAccessShare_DeletedOwnerReturnsGone(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")
	userStorePath := filepath.Join(tempDir, "users.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	userStore, _, err := auth.NewUserStore(userStorePath)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}
	owner, err := userStore.Create("deleted-owner", "password123", "", auth.RoleUser)
	if err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}
	if err := userStore.Delete(owner.ID); err != nil {
		t.Fatalf("failed to delete owner: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	handler.SetUserStore(userStore)

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "SHARE_DISABLED") {
		t.Fatalf("expected SHARE_DISABLED error, got %s", recorder.Body.String())
	}
}

func TestAccessShare_DisabledOwnerAfterAuthorizationReturnsGone(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")
	userStorePath := filepath.Join(tempDir, "users.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	userStore, _, err := auth.NewUserStore(userStorePath)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}
	owner, err := userStore.Create("owner-info-race", "password123", "", auth.RoleUser)
	if err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		beforeStat: func(string) error {
			owner.Disabled = true
			return userStore.Update(owner)
		},
		statInfo: &storage.FileInfo{Path: share.Path, Name: "report.pdf", Size: 42, IsDir: false},
	})
	handler.SetUserStore(userStore)

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "SHARE_DISABLED") {
		t.Fatalf("expected SHARE_DISABLED error, got %s", recorder.Body.String())
	}
}

func TestAccessShare_PublicInfoMissingFileReturnsNotFound(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/missing.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "FILE_NOT_FOUND" {
		t.Fatalf("expected FILE_NOT_FOUND code, got %v", errorPayload["code"])
	}
}

func TestAccessShare_PublicInfoInvalidParentPathReturnsNotFound(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf/child.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statErrByPath: map[string]error{
			share.Path: storage.ErrNotDir,
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "FILE_NOT_FOUND" {
		t.Fatalf("expected FILE_NOT_FOUND code, got %v", errorPayload["code"])
	}
}

func TestAccessShare_PublicInfoTypeMismatchReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/report.pdf", Name: "report.pdf", IsDir: true},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_SHARE_TYPE" {
		t.Fatalf("expected INVALID_SHARE_TYPE code, got %v", errorPayload["code"])
	}
}

func TestAccessShare_WithPasswordDoesNotExposeInfo(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:        "/docs/secret.pdf",
		Type:        ShareTypeFile,
		CreatedBy:   "user1",
		Password:    "secret",
		Description: "confidential quarterly acquisition plan",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{
			Path:  share.Path,
			Name:  "secret.pdf",
			Size:  4321,
			IsDir: false,
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	if _, exists := payload["file_name"]; exists {
		t.Fatalf("expected file_name to be omitted for password-protected share")
	}
	if _, exists := payload["file_size"]; exists {
		t.Fatalf("expected file_size to be omitted for password-protected share")
	}
	if _, exists := payload["description"]; exists {
		t.Fatalf("expected description to be omitted for password-protected share")
	}
}

func TestAccessShareWithPassword_FolderInfo(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItems: []*storage.FileInfo{
			{Path: "/docs/a.txt", Name: "a.txt"},
			{Path: "/docs/b.txt", Name: "b.txt"},
		},
	})

	body, err := json.Marshal(map[string]string{"password": "secret"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	assertPublicShareJSONHeaders(t, recorder.Header())

	payload := decodeResponseBody(t, recorder)
	if payload["file_name"] != "docs" {
		t.Fatalf("expected file_name docs, got %v", payload["file_name"])
	}
	if payload["folder_items"] != float64(2) {
		t.Fatalf("expected folder_items 2, got %v", payload["folder_items"])
	}
	assertShareAccessCookiePaths(t, recorder.Result().Cookies(), share.ID)
}

func TestAccessShare_PublicInfoRootFolderUsesSafeDisplayName(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/": {
				{Path: "/docs", Name: "docs", IsDir: true},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	assertPublicShareJSONHeaders(t, recorder.Header())

	payload := decodeResponseBody(t, recorder)
	if payload["file_name"] != "mnemonas-share" {
		t.Fatalf("expected file_name mnemonas-share, got %v", payload["file_name"])
	}
	if payload["folder_items"] != float64(1) {
		t.Fatalf("expected folder_items 1, got %v", payload["folder_items"])
	}
}

func TestAccessShareWithPassword_FolderInfoSkipsEntriesOutsideShareRoot(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItems: []*storage.FileInfo{
			{Path: "/docs/a.txt", Name: "a.txt"},
			{Path: "/outside/secret.txt", Name: "secret.txt"},
		},
	})

	body, err := json.Marshal(map[string]string{"password": "secret"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	if payload["folder_items"] != float64(1) {
		t.Fatalf("expected folder_items 1 after skipping outside entry, got %v", payload["folder_items"])
	}
}

func TestAccessShareWithPassword_FolderInfoSkipsNonDirectReadDirChildren(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItems: []*storage.FileInfo{
			{Path: "/docs/a.txt", Name: "a.txt"},
			{Path: "/docs/nested/secret.txt", Name: "secret.txt"},
		},
	})

	body, err := json.Marshal(map[string]string{"password": "secret"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	if payload["folder_items"] != float64(1) {
		t.Fatalf("expected folder_items 1 after skipping non-direct entry, got %v", payload["folder_items"])
	}
}

func TestAccessShareWithPassword_EmptyFolderInfoIncludesFolderItems(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/empty",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItems: []*storage.FileInfo{},
	})

	body, err := json.Marshal(map[string]string{"password": "secret"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	folderItems, exists := payload["folder_items"]
	if !exists {
		t.Fatalf("expected empty folder_items field to be present, got %s", recorder.Body.String())
	}
	if folderItems != float64(0) {
		t.Fatalf("expected folder_items 0, got %v", folderItems)
	}
	assertShareAccessCookiePaths(t, recorder.Result().Cookies(), share.ID)
}

func TestAccessShareWithPassword_RejectsUnknownFields(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItems: []*storage.FileInfo{{Path: "/docs/a.txt", Name: "a.txt"}},
	})

	body := []byte(`{"password":"secret","unexpected":true}`)
	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()

	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_REQUEST" {
		t.Fatalf("expected INVALID_REQUEST code, got %v", errorPayload["code"])
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatal("expected no access cookie on rejected request")
	}
}

func TestAccessShareWithPassword_BackendFailureReturnsInternalServerErrorWithoutCookie(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{readDirErr: errors.New("database offline")})
	body := []byte(`{"password":"secret"}`)
	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()

	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "GET_SHARE_FAILED" {
		t.Fatalf("expected GET_SHARE_FAILED code, got %v", errorPayload["code"])
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("expected no access cookie on failed share info load, got %d cookies", len(recorder.Result().Cookies()))
	}
}

func TestWriteShareSuccess_InvalidPayloadReturnsInternalServerError(t *testing.T) {
	recorder := httptest.NewRecorder()

	writeShareSuccess(recorder, http.StatusOK, map[string]any{
		"bad": make(chan int),
	}, "ok")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("expected application/json content type, got %q", contentType)
	}
	var payload struct {
		Success bool `json:"success"`
		Error   *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode internal error body: %v; body=%s", err, recorder.Body.String())
	}
	if payload.Success || payload.Error == nil || payload.Error.Code != "INTERNAL_ERROR" || payload.Error.Message != "internal server error" {
		t.Fatalf("unexpected internal error payload: %s", recorder.Body.String())
	}
}

func TestWriteShareSuccess_IncludesNullDataForNilPayload(t *testing.T) {
	recorder := httptest.NewRecorder()

	writeShareSuccess(recorder, http.StatusOK, nil, "ok")

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	data, ok := payload["data"]
	if !ok {
		t.Fatalf("expected response to include data field, got %s", recorder.Body.String())
	}
	if string(data) != "null" {
		t.Fatalf("expected data field to be null, got %s", string(data))
	}
}

func TestAccessShareWithPassword_IgnoresSpoofedForwardedProtoForCookie(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{statInfo: &storage.FileInfo{Name: "docs", IsDir: true}})
	body := []byte(`{"password":"secret"}`)
	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	req.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()

	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	cookies := recorder.Result().Cookies()
	assertShareAccessCookiePaths(t, cookies, share.ID)
	for _, cookie := range cookies {
		if cookie.Secure {
			t.Fatal("expected spoofed forwarded proto to leave share cookie insecure")
		}
	}
}

func TestAccessShareWithPassword_IgnoresForwardedProtoWhenTrustedProxyHopsDisabled(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	originalHops := requestip.TrustedProxyHops()
	requestip.SetTrustedProxyHops(0)
	defer requestip.SetTrustedProxyHops(originalHops)

	handler := NewHandler(store, &fakeShareFS{statInfo: &storage.FileInfo{Name: "docs", IsDir: true}})
	body := []byte(`{"password":"secret"}`)
	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()

	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	cookies := recorder.Result().Cookies()
	assertShareAccessCookiePaths(t, cookies, share.ID)
	for _, cookie := range cookies {
		if cookie.Secure {
			t.Fatal("expected trusted proxy hops disabled to leave share cookie insecure")
		}
	}
}

func TestAccessShareWithPassword_SetsCookiePathForPublicAPIAlias(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItems: []*storage.FileInfo{{Path: "/docs/a.txt", Name: "a.txt"}},
	})
	body := []byte(`{"password":"secret"}`)
	req := newRouteRequest(http.MethodPost, "/api/v1/public/shares/"+share.ID+"/access", share.ID, body)
	recorder := httptest.NewRecorder()

	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	cookies := recorder.Result().Cookies()
	assertShareAccessCookiePaths(t, cookies, share.ID)
}

func TestAccessShareWithPassword_DoesNotIncrementAccessCount(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/secret.pdf", Name: "secret.pdf", Size: 256},
	})

	body, err := json.Marshal(map[string]string{"password": "secret"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("expected access_count 0, got %d", current.AccessCount)
	}
}

func TestAccessShareWithPassword_NonPostDoesNotRecordPasswordFailure(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: share.Path, Name: "secret.pdf", Size: 256},
	})
	handler.passwordFailureDelay = 0

	for attempt := 0; attempt < handler.passwordFailureLimit; attempt++ {
		req := newRouteRequest(http.MethodPut, "/s/"+share.ID, share.ID, []byte(`{"password":"wrong"}`))
		recorder := httptest.NewRecorder()
		handler.AccessShareWithPassword(recorder, req)

		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("PUT password attempt %d status = %d, want %d", attempt+1, recorder.Code, http.StatusMethodNotAllowed)
		}
		if allow := recorder.Header().Get("Allow"); allow != http.MethodPost {
			t.Fatalf("PUT password attempt %d Allow = %q, want POST", attempt+1, allow)
		}
	}

	successReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, []byte(`{"password":"secret"}`))
	successRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(successRecorder, successReq)

	if successRecorder.Code != http.StatusOK {
		t.Fatalf("POST correct password status = %d, want %d; body=%s", successRecorder.Code, http.StatusOK, successRecorder.Body.String())
	}
}

func TestAccessShareWithPassword_DisabledOwnerAfterAuthorizationReturnsGoneWithoutCookie(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")
	userStorePath := filepath.Join(tempDir, "users.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	userStore, _, err := auth.NewUserStore(userStorePath)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}
	owner, err := userStore.Create("owner-access-race", "password123", "", auth.RoleUser)
	if err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: owner.ID,
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		beforeReadDir: func(string) error {
			owner.Disabled = true
			return userStore.Update(owner)
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 1, IsDir: false},
			},
		},
	})
	handler.SetUserStore(userStore)

	body := []byte(`{"password":"secret"}`)
	req := newRouteRequest(http.MethodPost, "/api/v1/public/shares/"+share.ID+"/access", share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "SHARE_DISABLED") {
		t.Fatalf("expected SHARE_DISABLED error, got %s", recorder.Body.String())
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("expected no access cookie when owner is disabled mid-request, got %d cookies", len(recorder.Result().Cookies()))
	}
}

func TestDownloadShareFile_ReturnsBadRequestWhenParentPathIsFile(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{openErrByPath: map[string]error{
		"/docs/report/file.txt": storage.ErrNotDir,
	}})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/report/file.txt", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	assertPublicShareJSONHeaders(t, recorder.Header())
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_PATH" {
		t.Fatalf("expected INVALID_PATH code, got %v", errorPayload["code"])
	}
}

func TestListShareItems_PublicFolder(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 12, IsDir: false},
				{Path: "/docs/sub", Name: "sub", Size: 0, IsDir: true},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	assertPublicShareJSONHeaders(t, recorder.Header())

	var payload struct {
		Path  string           `json:"path"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Path != "" {
		t.Fatalf("expected empty path, got %q", payload.Path)
	}

	paths := map[string]bool{}
	for _, item := range payload.Items {
		pathValue, _ := item["path"].(string)
		isDirValue, _ := item["is_dir"].(bool)
		paths[pathValue] = isDirValue
	}
	if _, ok := paths["a.txt"]; !ok {
		t.Fatalf("expected a.txt entry in list")
	}
	if isDir, ok := paths["sub"]; !ok || !isDir {
		t.Fatalf("expected sub directory entry in list")
	}
}

func TestListShareItems_PublicFolderDoesNotPersistAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 12, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Warning") != "" {
		t.Fatalf("expected no persistence warning, got %q", recorder.Header().Get("Warning"))
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("expected list not to consume access count, got %d", current.AccessCount)
	}
}

func TestListShareItems_HeadDoesNotConsumeAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, nil)
	req := newRouteRequest(http.MethodHead, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("HEAD list status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	if allow := recorder.Header().Get("Allow"); allow != http.MethodGet {
		t.Fatalf("HEAD list Allow = %q, want GET", allow)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok || errorPayload["code"] != "METHOD_NOT_ALLOWED" {
		t.Fatalf("HEAD list error payload = %s, want METHOD_NOT_ALLOWED", recorder.Body.String())
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("HEAD list access count = %d, want 0", current.AccessCount)
	}
}

func TestListShareItems_PathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	for _, target := range []string{
		"/s/" + share.ID + "/items?path=../secret",
		"/s/" + share.ID + "/items?path=%2F%2F..%2Fsecret",
	} {
		req := newRouteRequest(http.MethodGet, target, share.ID, nil)
		recorder := httptest.NewRecorder()
		handler.ListShareItems(recorder, req)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("target %q expected status 400, got %d", target, recorder.Code)
		}
		current, err := store.Get(share.ID)
		if err != nil {
			t.Fatalf("failed to load share: %v", err)
		}
		if current.AccessCount != 0 {
			t.Fatalf("target %q expected invalid path request not to consume access count, got %d", target, current.AccessCount)
		}
	}
}

func TestListShareItems_RejectsAmbiguousPathWithoutConsumingAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs/public": {
				{Path: "/docs/public/report.txt", Name: "report.txt", Size: 12, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items?path=public&path=private", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous path status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_PATH" {
		t.Fatalf("expected INVALID_PATH code, got %v", errorPayload["code"])
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("ambiguous path request consumed access count: got %d, want 0", current.AccessCount)
	}
}

func TestListShareItems_BackslashTraversal(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items?path=..%5Csecret", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
}

func TestListShareItems_RequiresPassword(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", recorder.Code)
	}
}

func TestListShareItems_ExpiredProtectedShareReturnsGoneWithoutCookie(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	expiredAt := time.Now().Add(-time.Minute)
	if err := store.Update(share.ID, func(s *Share) error {
		s.ExpiresAt = &expiredAt
		return nil
	}); err != nil {
		t.Fatalf("failed to expire share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	assertPublicShareJSONHeaders(t, recorder.Header())
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "SHARE_EXPIRED" {
		t.Fatalf("expected SHARE_EXPIRED code, got %v", errorPayload["code"])
	}
}

func TestAccessShare_WithValidCookieExposesInfo(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: share.Path, Name: "secret.pdf", Size: 42, IsDir: false},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	req.AddCookie(&http.Cookie{Name: shareAccessCookieName(share.ID), Value: handler.shareAccessToken(share)})
	recorder := httptest.NewRecorder()

	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	if payload["file_name"] != "secret.pdf" {
		t.Fatalf("expected file_name secret.pdf, got %v", payload["file_name"])
	}
}

func TestAccessShare_ValidCookieWorksWhenStaleCookiePrecedesIt(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: share.Path, Name: "secret.pdf", Size: 42, IsDir: false},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	cookieName := shareAccessCookieName(share.ID)
	req.Header.Set("Cookie", cookieName+"=stale; "+cookieName+"="+handler.shareAccessToken(share))
	recorder := httptest.NewRecorder()

	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	if payload["file_name"] != "secret.pdf" {
		t.Fatalf("expected stale cookie not to shadow valid share access, got payload %v", payload)
	}
}

func TestAccessShare_ExpiredProtectedShareWithCookieReturnsGone(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	expiredAt := time.Now().Add(-time.Minute)
	if err := store.Update(share.ID, func(s *Share) error {
		s.ExpiresAt = &expiredAt
		return nil
	}); err != nil {
		t.Fatalf("failed to expire share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: share.Path, Name: "secret.pdf", Size: 42, IsDir: false},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	req.AddCookie(&http.Cookie{Name: shareAccessCookieName(share.ID), Value: handler.shareAccessToken(share)})
	recorder := httptest.NewRecorder()

	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	assertPublicShareJSONHeaders(t, recorder.Header())
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "SHARE_EXPIRED" {
		t.Fatalf("expected SHARE_EXPIRED code, got %v", errorPayload["code"])
	}
}

func TestListShareItems_UsesCookieAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 1, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	req.AddCookie(&http.Cookie{Name: shareAccessCookieName(share.ID), Value: handler.shareAccessToken(share)})
	recorder := httptest.NewRecorder()

	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
}

func TestListShareItems_DisabledOwnerAfterAuthorizationReturnsGone(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")
	userStorePath := filepath.Join(tempDir, "users.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	userStore, _, err := auth.NewUserStore(userStorePath)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}
	owner, err := userStore.Create("owner-race-list", "password123", "", auth.RoleUser)
	if err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		beforeReadDir: func(string) error {
			owner.Disabled = true
			return userStore.Update(owner)
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 1, IsDir: false},
			},
		},
	})
	handler.SetUserStore(userStore)

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "SHARE_DISABLED") {
		t.Fatalf("expected SHARE_DISABLED error, got %s", recorder.Body.String())
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("expected disabled-owner list race not to consume access count, got %d", current.AccessCount)
	}
}

func TestDownloadShareFile_PathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	for _, target := range []string{
		"/s/" + share.ID + "/download/../secret",
		"/s/" + share.ID + "/download//../secret",
		"/s/" + share.ID + "/download/./secret",
		"/s/" + share.ID + "/download/reports/./january.txt",
	} {
		req := newRouteRequest(http.MethodGet, target, share.ID, nil)
		recorder := httptest.NewRecorder()
		serveDownloadShareFileWithTicket(t, handler, recorder, req)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("target %q expected status 400, got %d", target, recorder.Code)
		}
		payload := decodeResponseBody(t, recorder)
		errorPayload, ok := payload["error"].(map[string]any)
		if !ok {
			t.Fatalf("target %q expected error payload, got %v", target, payload)
		}
		if errorPayload["code"] != "INVALID_PATH" {
			t.Fatalf("target %q expected INVALID_PATH code, got %v", target, errorPayload["code"])
		}
	}
}

func TestDownloadShareFile_BackslashTraversal(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/..%5Csecret", share.ID, nil)
	recorder := httptest.NewRecorder()
	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_PATH" {
		t.Fatalf("expected INVALID_PATH code, got %v", errorPayload["code"])
	}
}

func TestDownloadShareFile_RejectsEncodedUnsafePathWithoutConsumingAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		beforeOpenFile: func(filePath string) error {
			t.Fatalf("OpenFile should not run for invalid encoded path %q", filePath)
			return nil
		},
	})

	tests := []string{
		"/s/" + share.ID + "/download/%2e%2e/secret.txt",
		"/s/" + share.ID + "/download/reports/%2e/secret.txt",
		"/s/" + share.ID + "/download/%2E%2E%2fsecret.txt",
		"/s/" + share.ID + "/download/report%00.txt",
		"/api/v1/public/shares/" + share.ID + "/download/%2e%2e/secret.txt",
	}

	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			req := newRouteRequest(http.MethodGet, target, share.ID, nil)
			recorder := httptest.NewRecorder()
			serveDownloadShareFileWithTicket(t, handler, recorder, req)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("target %q expected status 400, got %d; body=%s", target, recorder.Code, recorder.Body.String())
			}
			payload := decodeResponseBody(t, recorder)
			errorPayload, ok := payload["error"].(map[string]any)
			if !ok {
				t.Fatalf("target %q expected error payload, got %v", target, payload)
			}
			if errorPayload["code"] != "INVALID_PATH" {
				t.Fatalf("target %q expected INVALID_PATH code, got %v", target, errorPayload["code"])
			}
			current, err := store.Get(share.ID)
			if err != nil {
				t.Fatalf("Get(%q) error: %v", share.ID, err)
			}
			if current.AccessCount != 0 {
				t.Fatalf("target %q access_count = %d, want 0", target, current.AccessCount)
			}
		})
	}
}

func TestDownloadShare_NilReaderReturnsNotFound(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "FILE_NOT_FOUND" {
		t.Fatalf("expected FILE_NOT_FOUND code, got %v", errorPayload["code"])
	}
}

func TestDownloadShare_OpenFileInternalErrorReturnsInternalServerError(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openErrByPath: map[string]error{
			"/docs/report.pdf": errors.New("backend offline"),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "DOWNLOAD_SHARE_FAILED" {
		t.Fatalf("expected DOWNLOAD_SHARE_FAILED code, got %v", errorPayload["code"])
	}
	if errorPayload["message"] != "internal server error" {
		t.Fatalf("expected generic message, got %v", errorPayload["message"])
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected issued ticket to consume one access before download, got %d", current.AccessCount)
	}
}

func TestDownloadShare_OpenFileDirectoryReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openErrByPath: map[string]error{
			"/docs/report.pdf": storage.ErrIsDir,
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_SHARE_TYPE" {
		t.Fatalf("expected INVALID_SHARE_TYPE code, got %v", errorPayload["code"])
	}
}

func TestDownloadShare_OpenFileInvalidParentPathReturnsNotFound(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf/child.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openErrByPath: map[string]error{
			share.Path: storage.ErrNotDir,
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "FILE_NOT_FOUND" {
		t.Fatalf("expected FILE_NOT_FOUND code, got %v", errorPayload["code"])
	}
}

func TestDownloadShare_FolderArchiveAsZip(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 2,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	now := time.Unix(1700000000, 0)
	handler := NewHandler(store, &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			"/docs": {Path: "/docs", Name: "docs", IsDir: true, ModTime: now},
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/empty", Name: "empty", IsDir: true, ModTime: now},
				{Path: "/docs/nested", Name: "nested", IsDir: true, ModTime: now},
				{Path: "/docs/readme.txt", Name: "readme.txt", Size: 6, ModTime: now},
				{Path: "/docs/trailing.txt ", Name: "trailing.txt ", Size: 8, ModTime: now},
			},
			"/docs/empty": {},
			"/docs/nested": {
				{Path: "/docs/nested/report.txt", Name: "report.txt", Size: 6, ModTime: now},
			},
		},
		openByPath: map[string]FileReader{
			"/docs/readme.txt":        io.NopCloser(strings.NewReader("readme")),
			"/docs/nested/report.txt": io.NopCloser(strings.NewReader("report")),
			"/docs/trailing.txt ":     io.NopCloser(strings.NewReader("trailing")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("folder archive status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("folder archive Content-Type = %q, want application/zip", contentType)
	}
	mediaType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("folder archive Content-Disposition is invalid: %v", err)
	}
	if mediaType != "attachment" || params["filename"] != "docs.zip" {
		t.Fatalf("folder archive Content-Disposition = media %q params %+v, want docs.zip attachment", mediaType, params)
	}

	entries := readShareZipEntries(t, recorder.Body.Bytes())
	for _, name := range []string{"docs/", "docs/empty/", "docs/nested/", "docs/readme.txt", "docs/trailing.txt ", "docs/nested/report.txt"} {
		if _, ok := entries[name]; !ok {
			t.Fatalf("folder archive missing %q; entries=%v", name, entries)
		}
	}
	if got := string(entries["docs/readme.txt"]); got != "readme" {
		t.Fatalf("docs/readme.txt archive content = %q, want readme", got)
	}
	if got := string(entries["docs/nested/report.txt"]); got != "report" {
		t.Fatalf("docs/nested/report.txt archive content = %q, want report", got)
	}
	if got := string(entries["docs/trailing.txt "]); got != "trailing" {
		t.Fatalf("docs/trailing.txt archive content = %q, want trailing", got)
	}

	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("folder archive access_count = %d, want 1", current.AccessCount)
	}
}

func TestDownloadShare_RejectsAmbiguousArchiveParameterWithoutConsumingAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	now := time.Unix(1700000000, 0)
	handler := NewHandler(store, &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			"/docs": {Path: "/docs", Name: "docs", IsDir: true, ModTime: now},
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip&archive=tar", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous archive parameter status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_ARCHIVE_FORMAT" {
		t.Fatalf("expected INVALID_ARCHIVE_FORMAT code, got %v", errorPayload["code"])
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("ambiguous archive parameter access_count = %d, want 0", current.AccessCount)
	}
}

func TestDownloadShare_RootFolderArchiveUsesSafeDisplayName(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	now := time.Unix(1700000000, 0)
	handler := NewHandler(store, &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			"/": {Path: "/", Name: "/", IsDir: true, ModTime: now},
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/": {
				{Path: "/readme.txt", Name: "readme.txt", Size: 6, ModTime: now},
			},
		},
		openByPath: map[string]FileReader{
			"/readme.txt": io.NopCloser(strings.NewReader("readme")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("root folder archive status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	mediaType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("root folder archive Content-Disposition is invalid: %v", err)
	}
	if mediaType != "attachment" || params["filename"] != "mnemonas-share.zip" {
		t.Fatalf("root folder archive Content-Disposition = media %q params %+v, want mnemonas-share.zip attachment", mediaType, params)
	}

	entries := readShareZipEntries(t, recorder.Body.Bytes())
	if _, ok := entries["mnemonas-share/"]; !ok {
		t.Fatalf("root folder archive missing mnemonas-share/; entries=%v", entries)
	}
	if got := string(entries["mnemonas-share/readme.txt"]); got != "readme" {
		t.Fatalf("mnemonas-share/readme.txt archive content = %q, want readme", got)
	}
}

func TestShareArchiveFilenameDoesNotDuplicateZipExtension(t *testing.T) {
	tests := []struct {
		name     string
		rootPath string
		want     string
	}{
		{name: "folder", rootPath: "/docs", want: "docs.zip"},
		{name: "zip named folder", rootPath: "/backups.zip", want: "backups.zip"},
		{name: "uppercase zip named folder", rootPath: "/BACKUPS.ZIP", want: "BACKUPS.ZIP"},
		{name: "root", rootPath: "/", want: "mnemonas-share.zip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shareArchiveFilename(tt.rootPath); got != tt.want {
				t.Fatalf("shareArchiveFilename(%q) = %q, want %q", tt.rootPath, got, tt.want)
			}
		})
	}
}

func TestDownloadShare_ZipNamedFolderArchiveKeepsSingleZipSuffix(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/backups.zip",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	now := time.Unix(1700000000, 0)
	handler := NewHandler(store, &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			"/backups.zip": {Path: "/backups.zip", Name: "backups.zip", IsDir: true, ModTime: now},
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/backups.zip": {
				{Path: "/backups.zip/manifest.json", Name: "manifest.json", Size: 2, ModTime: now},
			},
		},
		openByPath: map[string]FileReader{
			"/backups.zip/manifest.json": io.NopCloser(strings.NewReader("{}")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("zip-named folder archive status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	mediaType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("zip-named folder archive Content-Disposition is invalid: %v", err)
	}
	if mediaType != "attachment" || params["filename"] != "backups.zip" {
		t.Fatalf("zip-named folder archive Content-Disposition = media %q params %+v, want backups.zip attachment", mediaType, params)
	}
}

func TestDownloadShare_FolderArchiveSkipsEntriesOutsideShareRoot(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	now := time.Unix(1700000000, 0)
	handler := NewHandler(store, &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			"/docs": {Path: "/docs", Name: "docs", IsDir: true, ModTime: now},
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/other/secret.txt", Name: "secret.txt", Size: 6, ModTime: now},
				{Path: "/docs/readme.txt", Name: "readme.txt", Size: 6, ModTime: now},
			},
		},
		openByPath: map[string]FileReader{
			"/other/secret.txt": io.NopCloser(strings.NewReader("secret")),
			"/docs/readme.txt":  io.NopCloser(strings.NewReader("readme")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("folder archive status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	entries := readShareZipEntries(t, recorder.Body.Bytes())
	if _, ok := entries["docs/secret.txt"]; ok {
		t.Fatalf("folder archive included entry outside share root; entries=%v", entries)
	}
	if got := string(entries["docs/readme.txt"]); got != "readme" {
		t.Fatalf("docs/readme.txt archive content = %q, want readme", got)
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("folder archive access_count = %d, want 1", current.AccessCount)
	}
}

func TestDownloadShare_FolderArchiveSkipsNonDirectReadDirChildren(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	now := time.Unix(1700000000, 0)
	handler := NewHandler(store, &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			"/docs": {Path: "/docs", Name: "docs", IsDir: true, ModTime: now},
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/nested/secret.txt", Name: "secret.txt", Size: 6, ModTime: now},
				{Path: "/docs/readme.txt", Name: "readme.txt", Size: 6, ModTime: now},
			},
		},
		openByPath: map[string]FileReader{
			"/docs/nested/secret.txt": io.NopCloser(strings.NewReader("secret")),
			"/docs/readme.txt":        io.NopCloser(strings.NewReader("readme")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("folder archive status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	entries := readShareZipEntries(t, recorder.Body.Bytes())
	if _, ok := entries["docs/secret.txt"]; ok {
		t.Fatalf("folder archive included flattened non-direct entry; entries=%v", entries)
	}
	if _, ok := entries["docs/nested/secret.txt"]; ok {
		t.Fatalf("folder archive included non-direct entry; entries=%v", entries)
	}
	if got := string(entries["docs/readme.txt"]); got != "readme" {
		t.Fatalf("docs/readme.txt archive content = %q, want readme", got)
	}
}

func TestDownloadShare_FolderArchiveRejectsDuplicateEntryNamesWithoutConsumingAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	now := time.Unix(1700000000, 0)
	handler := NewHandler(store, &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			"/docs": {Path: "/docs", Name: "docs", IsDir: true, ModTime: now},
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/readme.txt", Name: "readme.txt", Size: 6, ModTime: now},
				{Path: "/docs/readme.txt", Name: "readme.txt", Size: 6, ModTime: now},
			},
		},
		openByPath: map[string]FileReader{
			"/docs/readme.txt": io.NopCloser(strings.NewReader("readme")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("folder archive duplicate entry status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "ARCHIVE_DUPLICATE_ENTRY" {
		t.Fatalf("expected ARCHIVE_DUPLICATE_ENTRY code, got %v", errorPayload["code"])
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("duplicate archive access_count = %d, want 0", current.AccessCount)
	}
}

func TestDownloadShare_FolderArchiveRejectsSnapshotSizeGrowthBeforeStreaming(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	previousLimit := maxShareArchiveBytes
	maxShareArchiveBytes = 5
	t.Cleanup(func() {
		maxShareArchiveBytes = previousLimit
	})

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	snapshotHostPath := filepath.Join(tempDir, "growing.txt")
	if err := os.WriteFile(snapshotHostPath, []byte("123456"), 0644); err != nil {
		t.Fatalf("write snapshot host file: %v", err)
	}

	now := time.Unix(1700000000, 0)
	handler := NewHandler(store, &fakeShareSnapshotFS{
		fakeShareFS: &fakeShareFS{
			statInfoByPath: map[string]*storage.FileInfo{
				"/docs": {Path: "/docs", Name: "docs", IsDir: true, ModTime: now},
			},
			dirItemsByPath: map[string][]*storage.FileInfo{
				"/docs": {
					{Path: "/docs/growing.txt", Name: "growing.txt", Size: 4, ModTime: now},
				},
			},
			openByPath: map[string]FileReader{
				"/docs/growing.txt": io.NopCloser(strings.NewReader("123456")),
			},
		},
		snapshotByPath: map[string]fakeShareSnapshot{
			"/docs/growing.txt": {
				hostPath: snapshotHostPath,
				info: &storage.FileInfo{
					Path:    "/docs/growing.txt",
					Name:    "growing.txt",
					Size:    6,
					ModTime: now,
				},
			},
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("folder archive oversized snapshot status = %d, want %d; body=%s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "ARCHIVE_TOO_LARGE" {
		t.Fatalf("expected ARCHIVE_TOO_LARGE code, got %v", errorPayload["code"])
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("oversized archive access_count = %d, want preflight rejection 0", current.AccessCount)
	}
}

func TestDownloadShare_FolderArchiveRejectsSnapshotTypeChangeBeforeStreaming(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	snapshotHostPath := filepath.Join(tempDir, "changed")
	if err := os.WriteFile(snapshotHostPath, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("write snapshot host file: %v", err)
	}

	now := time.Unix(1700000000, 0)
	handler := NewHandler(store, &fakeShareSnapshotFS{
		fakeShareFS: &fakeShareFS{
			statInfoByPath: map[string]*storage.FileInfo{
				"/docs": {Path: "/docs", Name: "docs", IsDir: true, ModTime: now},
			},
			dirItemsByPath: map[string][]*storage.FileInfo{
				"/docs": {
					{Path: "/docs/changed.txt", Name: "changed.txt", Size: 15, ModTime: now},
				},
			},
		},
		snapshotByPath: map[string]fakeShareSnapshot{
			"/docs/changed.txt": {
				hostPath: snapshotHostPath,
				info: &storage.FileInfo{
					Path:    "/docs/changed.txt",
					Name:    "changed.txt",
					IsDir:   true,
					ModTime: now,
				},
			},
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download?archive=zip", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("folder archive snapshot type change status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "ARCHIVE_ENTRY_CHANGED" {
		t.Fatalf("expected ARCHIVE_ENTRY_CHANGED code, got %v", errorPayload["code"])
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("changed archive access_count = %d, want preflight rejection 0", current.AccessCount)
	}
}

func TestDownloadShareFile_SubfolderArchiveAsZip(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	now := time.Unix(1700000000, 0)
	handler := NewHandler(store, &fakeShareFS{
		statInfoByPath: map[string]*storage.FileInfo{
			"/docs/nested": {Path: "/docs/nested", Name: "nested", IsDir: true, ModTime: now},
		},
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs/nested": {
				{Path: "/docs/nested/report.txt", Name: "report.txt", Size: 6, ModTime: now},
			},
		},
		openByPath: map[string]FileReader{
			"/docs/nested/report.txt": io.NopCloser(strings.NewReader("report")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/nested?archive=zip", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("subfolder archive status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	mediaType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("subfolder archive Content-Disposition is invalid: %v", err)
	}
	if mediaType != "attachment" || params["filename"] != "nested.zip" {
		t.Fatalf("subfolder archive Content-Disposition = media %q params %+v, want nested.zip attachment", mediaType, params)
	}

	entries := readShareZipEntries(t, recorder.Body.Bytes())
	if got := string(entries["nested/report.txt"]); got != "report" {
		t.Fatalf("nested/report.txt archive content = %q, want report", got)
	}
}

func TestDownloadShare_EscapesQuotedFilenameInContentDisposition(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report\"2026.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/report\"2026.pdf": io.NopCloser(strings.NewReader("ok")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	mediaType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("expected valid Content-Disposition header, got %v", err)
	}
	if mediaType != "attachment" {
		t.Fatalf("expected attachment disposition, got %q", mediaType)
	}
	if params["filename"] != "report\"2026.pdf" {
		t.Fatalf("expected preserved filename, got %q", params["filename"])
	}
}

func TestDownloadShare_UsesExtensionContentType(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/image.png",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/image.png": io.NopCloser(strings.NewReader("png bytes")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("share download Content-Type = %q, want %q", contentType, "image/png")
	}
	assertUntrustedDownloadHeaders(t, recorder.Header())
}

func TestDownloadShare_SupportsRangeRequests(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/media/video.mp4",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			share.Path: &readSeekCloser{Reader: bytes.NewReader([]byte("abcdef"))},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	req.Header.Set("Range", "bytes=1-3")
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want %d", recorder.Code, http.StatusPartialContent)
	}
	if acceptRanges := recorder.Header().Get("Accept-Ranges"); acceptRanges != "bytes" {
		t.Fatalf("range Accept-Ranges = %q, want bytes", acceptRanges)
	}
	if contentRange := recorder.Header().Get("Content-Range"); contentRange != "bytes 1-3/6" {
		t.Fatalf("range Content-Range = %q, want bytes 1-3/6", contentRange)
	}
	if body := recorder.Body.String(); body != "bcd" {
		t.Fatalf("range body = %q, want bcd", body)
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("range download access count = %d, want 1", current.AccessCount)
	}
}

func TestDownloadShare_UnsatisfiableRangeDoesNotConsumeAccess(t *testing.T) {
	tests := []struct {
		rangeHeader      string
		content          []byte
		wantStatus       int
		wantContentRange string
		assertBody       bool
		wantBody         string
	}{
		{rangeHeader: "bytes=99-100", content: []byte("abcdef"), wantStatus: http.StatusRequestedRangeNotSatisfiable, wantContentRange: "bytes */6"},
		{rangeHeader: "bytes=-0", content: []byte("abcdef"), wantStatus: http.StatusPartialContent, assertBody: true},
		{rangeHeader: "bytes=-1", content: nil, wantStatus: http.StatusPartialContent, assertBody: true},
	}
	for _, tt := range tests {
		t.Run(tt.rangeHeader, func(t *testing.T) {
			tempDir := t.TempDir()
			storePath := filepath.Join(tempDir, "shares.json")

			store, err := NewShareStore(storePath)
			if err != nil {
				t.Fatalf("failed to create store: %v", err)
			}

			share, err := store.Create(CreateShareOptions{
				Path:      "/media/video.mp4",
				Type:      ShareTypeFile,
				CreatedBy: "user1",
				MaxAccess: 1,
			})
			if err != nil {
				t.Fatalf("failed to create share: %v", err)
			}

			handler := NewHandler(store, &fakeShareFS{
				openByPath: map[string]FileReader{
					share.Path: &readSeekCloser{Reader: bytes.NewReader(tt.content)},
				},
			})

			req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
			req.Header.Set("Range", tt.rangeHeader)
			recorder := httptest.NewRecorder()

			serveDownloadShareWithTicket(t, handler, recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("range status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if tt.wantContentRange != "" {
				if contentRange := recorder.Header().Get("Content-Range"); contentRange != tt.wantContentRange {
					t.Fatalf("range Content-Range = %q, want %q", contentRange, tt.wantContentRange)
				}
			}
			if tt.assertBody {
				if body := recorder.Body.String(); body != tt.wantBody {
					t.Fatalf("range body = %q, want %q", body, tt.wantBody)
				}
			}
			current, err := store.Get(share.ID)
			if err != nil {
				t.Fatalf("failed to reload share: %v", err)
			}
			if current.AccessCount != 1 {
				t.Fatalf("unsatisfiable range access count = %d, want ticket reservation 1", current.AccessCount)
			}
		})
	}
}

func TestDownloadShare_HeadDoesNotConsumeAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/media/video.mp4",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, nil)
	req := newRouteRequest(http.MethodHead, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("HEAD download status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	if allow := recorder.Header().Get("Allow"); allow != http.MethodGet {
		t.Fatalf("HEAD download Allow = %q, want GET", allow)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok || errorPayload["code"] != "METHOD_NOT_ALLOWED" {
		t.Fatalf("HEAD download error payload = %s, want METHOD_NOT_ALLOWED", recorder.Body.String())
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("HEAD download access count = %d, want 0", current.AccessCount)
	}
}

func TestDownloadShare_ReturnsInternalErrorWhenStreamingFailsBeforeWrite(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/report.pdf": &failingReadCloser{err: errors.New("stream failed")},
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "DOWNLOAD_SHARE_FAILED" {
		t.Fatalf("expected DOWNLOAD_SHARE_FAILED code, got %v", errorPayload["code"])
	}
	if errorPayload["message"] != "internal server error" {
		t.Fatalf("expected generic message, got %v", errorPayload["message"])
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected issued ticket to remain consumed after stream failure, got %d", current.AccessCount)
	}
}

func TestDownloadShare_FirstResponseWriteFailureDoesNotAppendJSON(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/report.pdf": io.NopCloser(strings.NewReader("download body")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	writer := &failFirstWriteResponseWriter{}

	serveDownloadShareWithTicket(t, handler, writer, req)

	assertFirstWriteFailureDidNotAttemptFallback(t, writer)
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected issued ticket to remain consumed after response failure, got %d", current.AccessCount)
	}
}

func TestDownloadShare_FirstResponseWriteFailureDoesNotAttemptRollbackAfterCommit(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	originalShareStoreWriter := shareStoreWriter
	writeCalls := 0
	shareStoreWriter = func(path string, data []byte) error {
		writeCalls++
		if writeCalls == 2 {
			return errors.New("rollback save failed")
		}
		return writeShareStoreFile(path, data)
	}
	defer func() {
		shareStoreWriter = originalShareStoreWriter
	}()

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/report.pdf": io.NopCloser(strings.NewReader("download body")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	writer := &failFirstWriteResponseWriter{}

	serveDownloadShareWithTicket(t, handler, writer, req)

	assertFirstWriteFailureDidNotAttemptFallback(t, writer)
	if writeCalls != 1 {
		t.Fatalf("share-store writes = %d, want only the ticket reservation", writeCalls)
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected rollback save failure to leave consumed access count fail-closed, got %d", current.AccessCount)
	}

	retryRec := httptest.NewRecorder()
	serveDownloadShareWithTicket(t, handler, retryRec, req)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("expected the issued ticket to support a retry, got %d", retryRec.Code)
	}
}

func TestDownloadShare_FirstResponseWriteFailureDoesNotAppendJSONAfterTicketPersistenceWarning(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/report.pdf": io.NopCloser(strings.NewReader("download body")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	writer := &failFirstWriteResponseWriter{}

	serveDownloadShareWithTicket(t, handler, writer, req)

	assertFirstWriteFailureDidNotAttemptFallback(t, writer)
	if writer.Header().Get("Warning") != "" {
		t.Fatalf("expected ticket persistence warning not to be repeated by GET, got %q", writer.Header().Get("Warning"))
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected ticket reservation to remain consumed, got %d", current.AccessCount)
	}
}

func TestDownloadShare_PartialStreamFailureConsumesAccessAndBlocksRetry(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	shareFS := &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/report.pdf": &partialFailingReadCloser{reader: io.MultiReader(strings.NewReader("partial"), &failingReadCloser{err: errors.New("stream failed")})},
		},
	}
	handler := NewHandler(store, shareFS)
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected started download to keep 200 status, got %d", recorder.Code)
	}
	if recorder.Body.String() != "partial" {
		t.Fatalf("expected partial body without appended error payload, got %q", recorder.Body.String())
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected partial stream failure after response start to consume access count, got %d", current.AccessCount)
	}

	shareFS.openByPath["/docs/report.pdf"] = io.NopCloser(strings.NewReader("complete body"))
	secondRecorder := httptest.NewRecorder()
	serveDownloadShareWithTicket(t, handler, secondRecorder, req)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("expected retry with the same ticket to succeed, got %d", secondRecorder.Code)
	}
}

func TestDownloadShare_EmptyFileConsumesAccessCount(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/empty.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/empty.txt": io.NopCloser(bytes.NewReader(nil)),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected successful empty download to consume access count, got %d", current.AccessCount)
	}
}

func TestDownloadShare_EmptyFileReturnsWarningWhenAccessPersistenceWarns(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/empty.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/empty.txt": io.NopCloser(bytes.NewReader(nil)),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if recorder.Header().Get("Warning") != "" {
		t.Fatalf("expected ticket persistence warning not to be repeated by GET, got %q", recorder.Header().Get("Warning"))
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected warned empty download to consume access count, got %d", current.AccessCount)
	}
}

func TestDownloadShare_ExpiredProtectedShareReturnsGoneWithoutCookie(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	expiredAt := time.Now().Add(-time.Minute)
	if err := store.Update(share.ID, func(s *Share) error {
		s.ExpiresAt = &expiredAt
		return nil
	}); err != nil {
		t.Fatalf("failed to expire share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	assertPublicShareJSONHeaders(t, recorder.Header())
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "SHARE_EXPIRED" {
		t.Fatalf("expected SHARE_EXPIRED code, got %v", errorPayload["code"])
	}
}

func TestDownloadShare_DisabledOwnerAfterAuthorizationReturnsGone(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")
	userStorePath := filepath.Join(tempDir, "users.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	userStore, _, err := auth.NewUserStore(userStorePath)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}
	owner, err := userStore.Create("owner-race-download", "password123", "", auth.RoleUser)
	if err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		beforeOpenFile: func(string) error {
			owner.Disabled = true
			return userStore.Update(owner)
		},
		openByPath: map[string]FileReader{
			share.Path: io.NopCloser(strings.NewReader("download body")),
		},
	})
	handler.SetUserStore(userStore)

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()
	serveDownloadShareWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "SHARE_DISABLED") {
		t.Fatalf("expected SHARE_DISABLED error, got %s", recorder.Body.String())
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected ticket reservation to remain consumed after owner race, got %d", current.AccessCount)
	}
}

func TestDownloadShareFile_PreservesWhitespaceInPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/ report .txt"
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: io.NopCloser(strings.NewReader("ok")),
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/%20report%20.txt", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
}

func TestDownloadShareFile_EscapesQuotedFilenameInContentDisposition(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/report\"2026.pdf"
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: io.NopCloser(strings.NewReader("ok")),
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/report%222026.pdf", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	mediaType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("expected valid Content-Disposition header, got %v", err)
	}
	if mediaType != "attachment" {
		t.Fatalf("expected attachment disposition, got %q", mediaType)
	}
	if params["filename"] != "report\"2026.pdf" {
		t.Fatalf("expected preserved filename, got %q", params["filename"])
	}
}

func TestDownloadShareFile_UsesExtensionContentType(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/manual.pdf"
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: io.NopCloser(strings.NewReader("pdf bytes")),
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/manual.pdf", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/pdf" {
		t.Fatalf("share file download Content-Type = %q, want %q", contentType, "application/pdf")
	}
	assertUntrustedDownloadHeaders(t, recorder.Header())
}

func TestDownloadShareFile_SupportsRangeRequests(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/media",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/media/video.mp4"
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: &readSeekCloser{Reader: bytes.NewReader([]byte("abcdef"))},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/video.mp4", share.ID, nil)
	req.Header.Set("Range", "bytes=2-4")
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want %d", recorder.Code, http.StatusPartialContent)
	}
	if acceptRanges := recorder.Header().Get("Accept-Ranges"); acceptRanges != "bytes" {
		t.Fatalf("range Accept-Ranges = %q, want bytes", acceptRanges)
	}
	if contentRange := recorder.Header().Get("Content-Range"); contentRange != "bytes 2-4/6" {
		t.Fatalf("range Content-Range = %q, want bytes 2-4/6", contentRange)
	}
	if body := recorder.Body.String(); body != "cde" {
		t.Fatalf("range body = %q, want cde", body)
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("range download access count = %d, want 1", current.AccessCount)
	}
}

func TestDownloadShareFile_UnsatisfiableRangeDoesNotConsumeAccess(t *testing.T) {
	tests := []struct {
		rangeHeader      string
		wantStatus       int
		wantContentRange string
		assertBody       bool
		wantBody         string
	}{
		{rangeHeader: "bytes=99-100", wantStatus: http.StatusRequestedRangeNotSatisfiable, wantContentRange: "bytes */6"},
		{rangeHeader: "bytes=-0", wantStatus: http.StatusPartialContent, assertBody: true},
	}
	for _, tt := range tests {
		t.Run(tt.rangeHeader, func(t *testing.T) {
			tempDir := t.TempDir()
			storePath := filepath.Join(tempDir, "shares.json")

			store, err := NewShareStore(storePath)
			if err != nil {
				t.Fatalf("failed to create store: %v", err)
			}

			share, err := store.Create(CreateShareOptions{
				Path:      "/media",
				Type:      ShareTypeFolder,
				CreatedBy: "user1",
				MaxAccess: 1,
			})
			if err != nil {
				t.Fatalf("failed to create share: %v", err)
			}

			targetPath := "/media/video.mp4"
			handler := NewHandler(store, &fakeShareFS{
				openByPath: map[string]FileReader{
					targetPath: &readSeekCloser{Reader: bytes.NewReader([]byte("abcdef"))},
				},
			})

			req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/video.mp4", share.ID, nil)
			req.Header.Set("Range", tt.rangeHeader)
			recorder := httptest.NewRecorder()

			serveDownloadShareFileWithTicket(t, handler, recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("range status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if tt.wantContentRange != "" {
				if contentRange := recorder.Header().Get("Content-Range"); contentRange != tt.wantContentRange {
					t.Fatalf("range Content-Range = %q, want %q", contentRange, tt.wantContentRange)
				}
			}
			if tt.assertBody {
				if body := recorder.Body.String(); body != tt.wantBody {
					t.Fatalf("range body = %q, want %q", body, tt.wantBody)
				}
			}
			current, err := store.Get(share.ID)
			if err != nil {
				t.Fatalf("failed to reload share: %v", err)
			}
			if current.AccessCount != 1 {
				t.Fatalf("unsatisfiable range access count = %d, want ticket reservation 1", current.AccessCount)
			}
		})
	}
}

func TestDownloadShareFile_HeadDoesNotConsumeAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/media",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, nil)
	req := newRouteRequest(http.MethodHead, "/s/"+share.ID+"/download/video.mp4", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("HEAD folder download status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	if allow := recorder.Header().Get("Allow"); allow != http.MethodGet {
		t.Fatalf("HEAD folder download Allow = %q, want GET", allow)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok || errorPayload["code"] != "METHOD_NOT_ALLOWED" {
		t.Fatalf("HEAD folder download error payload = %s, want METHOD_NOT_ALLOWED", recorder.Body.String())
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("HEAD folder download access count = %d, want 0", current.AccessCount)
	}
}

func TestDownloadShareFile_ReturnsInternalErrorWhenStreamingFailsBeforeWrite(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/report.pdf"
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: &failingReadCloser{err: errors.New("stream failed")},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/report.pdf", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "DOWNLOAD_SHARE_FAILED" {
		t.Fatalf("expected DOWNLOAD_SHARE_FAILED code, got %v", errorPayload["code"])
	}
	if errorPayload["message"] != "internal server error" {
		t.Fatalf("expected generic message, got %v", errorPayload["message"])
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected issued ticket to remain consumed after stream failure, got %d", current.AccessCount)
	}
}

func TestDownloadShareFile_FirstResponseWriteFailureDoesNotAppendJSON(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/report.pdf"
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: io.NopCloser(strings.NewReader("download body")),
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/report.pdf", share.ID, nil)
	writer := &failFirstWriteResponseWriter{}

	serveDownloadShareFileWithTicket(t, handler, writer, req)

	assertFirstWriteFailureDidNotAttemptFallback(t, writer)
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected issued ticket to remain consumed after response failure, got %d", current.AccessCount)
	}
}

func TestDownloadShareFile_FirstResponseWriteFailureDoesNotAttemptRollbackAfterCommit(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	originalShareStoreWriter := shareStoreWriter
	writeCalls := 0
	shareStoreWriter = func(path string, data []byte) error {
		writeCalls++
		if writeCalls == 2 {
			return errors.New("rollback save failed")
		}
		return writeShareStoreFile(path, data)
	}
	defer func() {
		shareStoreWriter = originalShareStoreWriter
	}()

	targetPath := "/docs/report.pdf"
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: io.NopCloser(strings.NewReader("download body")),
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/report.pdf", share.ID, nil)
	writer := &failFirstWriteResponseWriter{}

	serveDownloadShareFileWithTicket(t, handler, writer, req)

	assertFirstWriteFailureDidNotAttemptFallback(t, writer)
	if writeCalls != 1 {
		t.Fatalf("share-store writes = %d, want only the ticket reservation", writeCalls)
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected rollback save failure to leave consumed access count fail-closed, got %d", current.AccessCount)
	}

	retryRec := httptest.NewRecorder()
	serveDownloadShareFileWithTicket(t, handler, retryRec, req)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("expected the issued ticket to support a retry, got %d", retryRec.Code)
	}
}

func TestDownloadShareFile_PartialStreamFailureConsumesAccessAndBlocksRetry(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/report.pdf"
	shareFS := &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: &partialFailingReadCloser{reader: io.MultiReader(strings.NewReader("partial"), &failingReadCloser{err: errors.New("stream failed")})},
		},
	}
	handler := NewHandler(store, shareFS)

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/report.pdf", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected started folder download to keep 200 status, got %d", recorder.Code)
	}
	if recorder.Body.String() != "partial" {
		t.Fatalf("expected partial body without appended error payload, got %q", recorder.Body.String())
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 1 {
		t.Fatalf("expected partial folder download after response start to consume access count, got %d", current.AccessCount)
	}

	shareFS.openByPath[targetPath] = io.NopCloser(strings.NewReader("complete body"))
	secondRecorder := httptest.NewRecorder()
	serveDownloadShareFileWithTicket(t, handler, secondRecorder, req)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("expected retry with the same ticket to succeed, got %d", secondRecorder.Code)
	}
}

func TestDownloadShareFile_OpenFileInternalErrorReturnsInternalServerError(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/report.pdf"
	handler := NewHandler(store, &fakeShareFS{
		openErrByPath: map[string]error{
			targetPath: errors.New("backend offline"),
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/report.pdf", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "DOWNLOAD_SHARE_FAILED" {
		t.Fatalf("expected DOWNLOAD_SHARE_FAILED code, got %v", errorPayload["code"])
	}
	if errorPayload["message"] != "internal server error" {
		t.Fatalf("expected generic message, got %v", errorPayload["message"])
	}
}

func TestDownloadShareFile_OpenFileDirectoryReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/subdir"
	handler := NewHandler(store, &fakeShareFS{
		openErrByPath: map[string]error{
			targetPath: storage.ErrIsDir,
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/subdir", share.ID, nil)
	recorder := httptest.NewRecorder()

	serveDownloadShareFileWithTicket(t, handler, recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_PATH" {
		t.Fatalf("expected INVALID_PATH code, got %v", errorPayload["code"])
	}
}

func TestListShareItems_PreservesWhitespaceInPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs/ report ": {
				{Path: "/docs/ report /a.txt", Name: "a.txt", Size: 1, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items?path=%20report%20", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Path != " report " {
		t.Fatalf("expected whitespace path to be preserved, got %q", payload.Path)
	}
}

func TestListShareItems_PathIsFileReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{readDirErr: storage.ErrNotDir})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items?path=report.pdf", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_PATH" {
		t.Fatalf("expected INVALID_PATH code, got %v", errorPayload["code"])
	}
}

func TestListShareItems_RejectsDotSegmentPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	for _, target := range []string{
		"/s/" + share.ID + "/items?path=sub/..",
		"/s/" + share.ID + "/items?path=./sub",
		"/s/" + share.ID + "/items?path=sub/./report",
	} {
		req := newRouteRequest(http.MethodGet, target, share.ID, nil)
		recorder := httptest.NewRecorder()
		handler.ListShareItems(recorder, req)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("target %q expected status 400, got %d", target, recorder.Code)
		}
		payload := decodeResponseBody(t, recorder)
		errorPayload, ok := payload["error"].(map[string]any)
		if !ok {
			t.Fatalf("target %q expected error payload, got %v", target, payload)
		}
		if errorPayload["code"] != "INVALID_PATH" {
			t.Fatalf("target %q expected INVALID_PATH code, got %v", target, errorPayload["code"])
		}
		current, err := store.Get(share.ID)
		if err != nil {
			t.Fatalf("failed to load share: %v", err)
		}
		if current.AccessCount != 0 {
			t.Fatalf("target %q expected invalid path request not to consume access count, got %d", target, current.AccessCount)
		}
	}
}

func TestAccessShare_AccessLimitReached(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	err = store.Update(share.ID, func(s *Share) error {
		s.AccessCount = 1
		return nil
	})
	if err != nil {
		t.Fatalf("failed to update share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
}

func TestAccessShareWithPassword_AccessLimitReached(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	err = store.Update(share.ID, func(s *Share) error {
		s.AccessCount = 1
		return nil
	})
	if err != nil {
		t.Fatalf("failed to update share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	body, err := json.Marshal(map[string]string{"password": "secret"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
}

func TestAccessShareWithPassword_RateLimitedAfterRepeatedFailures(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	handler.passwordFailureDelay = 0

	body, err := json.Marshal(map[string]string{"password": "wrong"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	for attempt := 1; attempt < handler.passwordFailureLimit; attempt++ {
		req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
		recorder := httptest.NewRecorder()

		handler.AccessShareWithPassword(recorder, req)

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected status 401, got %d", attempt, recorder.Code)
		}
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429 on lock, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "SHARE_PASSWORD_RATE_LIMITED" {
		t.Fatalf("expected SHARE_PASSWORD_RATE_LIMITED code, got %v", errorPayload["code"])
	}

	lockedReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, []byte(`{"password":"secret"}`))
	lockedRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(lockedRecorder, lockedReq)

	if lockedRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429 while locked, got %d", lockedRecorder.Code)
	}
}

func TestAccessShareWithPassword_LockExpiresAndSuccessResetsFailures(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/secret.pdf", Name: "secret.pdf", Size: 256},
	})
	handler.passwordFailureDelay = 0
	handler.passwordLockDuration = time.Minute

	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	handler.passwordAttempts.now = func() time.Time { return now }

	wrongBody := []byte(`{"password":"wrong"}`)
	for attempt := 0; attempt < handler.passwordFailureLimit; attempt++ {
		req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, wrongBody)
		recorder := httptest.NewRecorder()
		handler.AccessShareWithPassword(recorder, req)
	}

	now = now.Add(handler.passwordLockDuration + time.Second)

	successReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, []byte(`{"password":"secret"}`))
	successRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(successRecorder, successReq)

	if successRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200 after lock expiry, got %d", successRecorder.Code)
	}

	postSuccessReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, wrongBody)
	postSuccessRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(postSuccessRecorder, postSuccessReq)

	if postSuccessRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 after successful reset, got %d", postSuccessRecorder.Code)
	}
}

func TestAccessShareWithPassword_StaleFailuresExpire(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: "/docs/secret.pdf", Name: "secret.pdf", Size: 256},
	})
	handler.passwordFailureDelay = 0
	handler.passwordFailureWindow = time.Minute
	handler.passwordLockDuration = time.Minute

	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	handler.passwordAttempts.now = func() time.Time { return now }

	wrongBody := []byte(`{"password":"wrong"}`)
	for attempt := 0; attempt < handler.passwordFailureLimit-1; attempt++ {
		req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, wrongBody)
		recorder := httptest.NewRecorder()
		handler.AccessShareWithPassword(recorder, req)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", attempt+1, recorder.Code)
		}
	}

	now = now.Add(handler.passwordFailureWindow + time.Second)

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, wrongBody)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected stale failures to expire and return 401, got %d", recorder.Code)
	}

	lockedReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, []byte(`{"password":"secret"}`))
	lockedRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(lockedRecorder, lockedReq)

	if lockedRecorder.Code != http.StatusOK {
		t.Fatalf("expected valid password after stale failures expiry, got %d", lockedRecorder.Code)
	}
}

func TestPasswordAttemptTracker_PrunesStaleEntriesOnNewFailure(t *testing.T) {
	tracker := newPasswordAttemptTracker()
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	tracker.partitions["share-1"] = &passwordAttemptPartition{attempts: map[string]passwordAttemptState{
		"stale": {failures: 1, lastFailure: now.Add(-2 * time.Minute)},
	}}
	tracker.total = 1

	finish, _, admitted := tracker.begin("share-1", "fresh", 5, time.Minute, time.Minute)
	if !admitted {
		t.Fatal("expected fresh tracker entry to be admitted")
	}
	finish(passwordAttemptFailed)

	partition := tracker.partitions["share-1"]
	if _, ok := partition.attempts["stale"]; ok {
		t.Fatal("expected stale tracker entry to be pruned")
	}
	if _, ok := partition.attempts["fresh"]; !ok {
		t.Fatal("expected fresh tracker entry to remain")
	}
}

func TestPasswordAttemptTracker_PrunesStaleEntriesOnLockCheck(t *testing.T) {
	tracker := newPasswordAttemptTracker()
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	tracker.partitions["share-1"] = &passwordAttemptPartition{attempts: map[string]passwordAttemptState{
		"stale":   {failures: 1, lastFailure: now.Add(-2 * time.Minute)},
		"current": {failures: 1, lastFailure: now},
	}}
	tracker.total = 2

	finish, _, admitted := tracker.begin("share-1", "current", 5, time.Minute, time.Minute)
	if !admitted {
		t.Fatal("expected current tracker entry not to be locked")
	}
	finish(passwordAttemptFailed)
	partition := tracker.partitions["share-1"]
	if _, ok := partition.attempts["stale"]; ok {
		t.Fatal("expected stale tracker entry to be pruned during lock check")
	}
	if _, ok := partition.attempts["current"]; !ok {
		t.Fatal("expected current tracker entry to remain after lock check")
	}
}

func TestClientIdentifier_IgnoresSpoofedForwardedHeadersFromUntrustedSource(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/s/share-1", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	req.Header.Set("X-Real-IP", "198.51.100.21")

	if got := clientIdentifier(req); got != "203.0.113.5" {
		t.Fatalf("clientIdentifier() = %q, want %q", got, "203.0.113.5")
	}
}

func TestClientIdentifier_UsesLastForwardedAddressFromTrustedProxy(t *testing.T) {
	originalHops := requestip.TrustedProxyHops()
	requestip.SetTrustedProxyHops(1)
	defer requestip.SetTrustedProxyHops(originalHops)

	req := httptest.NewRequest(http.MethodGet, "/s/share-1", nil)
	req.RemoteAddr = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "198.51.100.99, 198.51.100.20")

	if got := clientIdentifier(req); got != "198.51.100.20" {
		t.Fatalf("clientIdentifier() = %q, want %q", got, "198.51.100.20")
	}
}

func TestAccessShareWithPassword_SpoofedForwardedHeaderDoesNotBypassRateLimit(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	handler.passwordFailureDelay = 0

	body := []byte(`{"password":"wrong"}`)
	for attempt := 0; attempt < handler.passwordFailureLimit; attempt++ {
		req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
		req.Header.Set("X-Forwarded-For", net.IPv4(198, 51, 100, byte(20+attempt)).String())
		recorder := httptest.NewRecorder()

		handler.AccessShareWithPassword(recorder, req)
	}

	bypassReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, []byte(`{"password":"secret"}`))
	bypassReq.Header.Set("X-Forwarded-For", "198.51.100.250")
	bypassRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(bypassRecorder, bypassReq)

	if bypassRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected spoofed forwarded header to stay rate limited, got %d", bypassRecorder.Code)
	}
}

func TestListShareItems_DoesNotLeakInternalErrors(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{readDirErr: errors.New("database offline")})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	body := recorder.Body.String()
	if strings.Contains(body, "database offline") {
		t.Fatalf("expected internal error details to be hidden, got %q", body)
	}
	if !strings.Contains(body, "internal server error") {
		t.Fatalf("expected generic public error message, got %q", body)
	}
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "LIST_SHARE_ITEMS_FAILED" {
		t.Fatalf("expected LIST_SHARE_ITEMS_FAILED code, got %v", errorPayload["code"])
	}
}

func TestListShareItems_FirstResponseWriteFailureDoesNotAppendJSONOrConsumeAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 12, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	writer := &failFirstWriteResponseWriter{}

	handler.ListShareItems(writer, req)

	assertFirstWriteFailureDidNotAttemptFallback(t, writer)
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("expected first response write failure not to consume access count, got %d", current.AccessCount)
	}
}

func TestListShareItems_FirstResponseWriteFailureDoesNotAttemptFallbackStoreWrite(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	originalShareStoreWriter := shareStoreWriter
	writeCalls := 0
	shareStoreWriter = func(path string, data []byte) error {
		writeCalls++
		if writeCalls == 2 {
			return errors.New("rollback save failed")
		}
		return writeShareStoreFile(path, data)
	}
	defer func() {
		shareStoreWriter = originalShareStoreWriter
	}()

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 12, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	writer := &failFirstWriteResponseWriter{}

	handler.ListShareItems(writer, req)

	assertFirstWriteFailureDidNotAttemptFallback(t, writer)
	if writeCalls != 0 {
		t.Fatalf("share-store writes = %d, want 0", writeCalls)
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("expected listing not to consume access count, got %d", current.AccessCount)
	}

	retryRec := httptest.NewRecorder()
	handler.ListShareItems(retryRec, req)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("expected listing retry to remain available, got %d", retryRec.Code)
	}
}

func TestListShareItems_FirstResponseWriteFailureDoesNotAppendJSONWithPersistenceWarningHook(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 12, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	writer := &failFirstWriteResponseWriter{}

	handler.ListShareItems(writer, req)

	assertFirstWriteFailureDidNotAttemptFallback(t, writer)
	if writer.Header().Get("Warning") != "" {
		t.Fatalf("expected no persistence warning, got %q", writer.Header().Get("Warning"))
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("expected listing not to consume access count, got %d", current.AccessCount)
	}
}

func TestListShareItems_PartialResponseWriteFailureConsumesAccessAndBlocksRetry(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 12, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	writer := &failPartialWriteResponseWriter{limit: 8}

	handler.ListShareItems(writer, req)

	if writer.status != http.StatusOK {
		t.Fatalf("expected partial response write failure to keep 200 status, got %d", writer.status)
	}
	if writer.body.Len() == 0 {
		t.Fatal("expected partial response body to remain written")
	}
	if strings.Contains(writer.body.String(), "LIST_SHARE_ITEMS_FAILED") {
		t.Fatalf("expected no appended error payload, got %q", writer.body.String())
	}
	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("expected partial listing response not to consume access count, got %d", current.AccessCount)
	}

	secondWriter := httptest.NewRecorder()
	handler.ListShareItems(secondWriter, req)
	if secondWriter.Code != http.StatusOK {
		t.Fatalf("expected listing retry to remain available, got %d", secondWriter.Code)
	}
}

func TestListShareItems_SkipsEntriesOutsideShareRoot(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/other/secret.txt", Name: "secret.txt", Size: 1, IsDir: false},
				{Path: "/docs/readme.txt", Name: "readme.txt", Size: 6, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("expected only the direct share entry, got %+v", payload.Items)
	}
	if payload.Items[0]["path"] != "readme.txt" {
		t.Fatalf("expected readme.txt entry after skipping outside entry, got %+v", payload.Items[0])
	}
}

func TestListShareItems_SkipsNonDirectReadDirChildren(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/nested/secret.txt", Name: "secret.txt", Size: 1, IsDir: false},
				{Path: "/docs/readme.txt", Name: "readme.txt", Size: 6, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("expected only the direct share entry, got %+v", payload.Items)
	}
	if payload.Items[0]["path"] != "readme.txt" {
		t.Fatalf("expected readme.txt entry after skipping non-direct entry, got %+v", payload.Items[0])
	}
}
