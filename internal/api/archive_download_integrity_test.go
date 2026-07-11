package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/activity"
	"github.com/seanbao/mnemonas/internal/storage"
)

func TestServer_DownloadArchiveProbeValidatesWithoutWritingArchive(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/probe-docs"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/probe-docs/readme.txt", strings.NewReader("readme")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	previousWriter := downloadArchiveWriter
	writerCalled := false
	downloadArchiveWriter = func(_ *Server, _ context.Context, _ *zip.Writer, _ []downloadArchiveEntry) error {
		writerCalled = true
		return errors.New("archive writer must not run for a probe")
	}
	t.Cleanup(func() {
		downloadArchiveWriter = previousWriter
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/probe-docs?archive=zip&download=true", nil)
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set(downloadProbeHeader, downloadProbeHeaderValue)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("archive probe status = %d, want %d; body=%q", w.Code, http.StatusOK, w.Body.String())
	}
	if writerCalled {
		t.Fatal("archive probe invoked the archive writer")
	}
	if w.Body.Len() != 0 {
		t.Fatalf("archive probe body length = %d, want 0", w.Body.Len())
	}
	if got := w.Header().Get("Content-Length"); got != "0" {
		t.Fatalf("archive probe Content-Length = %q, want 0", got)
	}
	if got := w.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("archive probe Content-Type = %q, want application/zip", got)
	}
	mediaType, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("archive probe Content-Disposition error: %v", err)
	}
	if mediaType != "attachment" || params["filename"] != "probe-docs.zip" {
		t.Fatalf("archive probe Content-Disposition = %q %+v, want probe-docs.zip attachment", mediaType, params)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("archive probe X-Content-Type-Options = %q, want nosniff", got)
	}
	_, total := server.activity.List(10, 0, activity.ActionDownload, "")
	if total != 0 {
		t.Fatalf("archive probe activity count = %d, want 0", total)
	}
}

func TestServer_DownloadArchiveGateRejectsBeforeFilesystemAccess(t *testing.T) {
	server := &Server{
		logger:              zerolog.Nop(),
		downloadArchiveGate: make(chan struct{}, 1),
	}
	server.downloadArchiveGate <- struct{}{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/docs?archive=zip", nil)
	recorder := httptest.NewRecorder()
	server.handleDownloadArchive(recorder, req, "/docs")

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusTooManyRequests, recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q, want empty", got)
	}

	var response APIError
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != ErrCodeArchiveDownloadRateLimited {
		t.Fatalf("error code = %q, want %q", response.Code, ErrCodeArchiveDownloadRateLimited)
	}
}

func TestServer_DownloadArchiveGateCoversArchiveWriting(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/report.txt", strings.NewReader("report")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	server.downloadArchiveGate = make(chan struct{}, 1)

	writerStarted := make(chan struct{})
	releaseWriter := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseWriter) }) })
	previousWriter := downloadArchiveWriter
	downloadArchiveWriter = func(_ *Server, _ context.Context, _ *zip.Writer, _ []downloadArchiveEntry) error {
		close(writerStarted)
		<-releaseWriter
		return nil
	}
	t.Cleanup(func() { downloadArchiveWriter = previousWriter })

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/download/report.txt?archive=zip", nil)
		server.handleDownloadArchive(recorder, req, "/report.txt")
		firstDone <- recorder
	}()

	select {
	case <-writerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("archive writer did not start")
	}

	secondRecorder := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodGet, "/api/v1/download/report.txt?archive=zip", nil)
	server.handleDownloadArchive(secondRecorder, secondReq, "/report.txt")
	if secondRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("concurrent status = %d, want %d; body=%s", secondRecorder.Code, http.StatusTooManyRequests, secondRecorder.Body.String())
	}

	releaseOnce.Do(func() { close(releaseWriter) })
	select {
	case firstRecorder := <-firstDone:
		if firstRecorder.Code != http.StatusOK {
			t.Fatalf("first status = %d, want %d; body=%s", firstRecorder.Code, http.StatusOK, firstRecorder.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first archive did not finish")
	}
}

func TestServer_DownloadArchiveProbeKeepsCollectionErrorsStructured(t *testing.T) {
	server, _, _ := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/missing-probe?archive=zip", nil)
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set(downloadProbeHeader, downloadProbeHeaderValue)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("missing archive probe status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("missing archive probe Content-Type = %q, want application/json", got)
	}
	if got := w.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("missing archive probe Content-Disposition = %q, want empty", got)
	}
}

func TestServer_DownloadArchiveProbeKeepsSnapshotOpenErrorsStructured(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	if err := fs.WriteFile(context.Background(), "/missing-snapshot.txt", strings.NewReader("source")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	previousOpener := downloadArchiveSnapshotOpener
	downloadArchiveSnapshotOpener = func(_ *Server, _ context.Context, _ string) (*os.File, *storage.FileInfo, error) {
		return nil, nil, storage.ErrNotFound
	}
	t.Cleanup(func() {
		downloadArchiveSnapshotOpener = previousOpener
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/missing-snapshot.txt?archive=zip", nil)
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set(downloadProbeHeader, downloadProbeHeaderValue)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("snapshot-open probe status = %d, want %d; body=%q", w.Code, http.StatusNotFound, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("snapshot-open probe Content-Type = %q, want application/json", got)
	}
	if got := w.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("snapshot-open probe Content-Disposition = %q, want empty", got)
	}
}

func TestDownloadArchiveCollectorReservesOneGlobalDiscoveryBudget(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()
	for _, directory := range []string{"/budget", "/budget/a-dir"} {
		if err := fs.Mkdir(ctx, directory); err != nil {
			t.Fatalf("Mkdir(%q) error: %v", directory, err)
		}
	}
	for _, filePath := range []string{
		"/budget/a-dir/one.txt",
		"/budget/a-dir/two.txt",
		"/budget/z-sibling.txt",
	} {
		if err := fs.WriteFile(ctx, filePath, strings.NewReader("x")); err != nil {
			t.Fatalf("WriteFile(%q) error: %v", filePath, err)
		}
	}
	root, err := fs.StatMetadata(ctx, "/budget")
	if err != nil {
		t.Fatalf("StatMetadata() error: %v", err)
	}

	collector := &downloadArchiveCollector{
		server:     server,
		ctx:        ctx,
		discovered: 1,
		maxEntries: 4,
	}
	err = collector.walkDirectory("/budget", "budget", root)
	if !errors.Is(err, errDownloadArchiveTooManyEntries) {
		t.Fatalf("walkDirectory() error = %v, want errDownloadArchiveTooManyEntries", err)
	}
	if len(collector.entries) != 2 {
		t.Fatalf("collector retained %d entries before rejecting nested discovery, want 2", len(collector.entries))
	}
}

func TestValidateDownloadArchiveEntriesRejectsNonRegularFile(t *testing.T) {
	err := validateDownloadArchiveEntries([]downloadArchiveEntry{
		{
			sourcePath: "/special.fifo",
			zipName:    "special.fifo",
			info: &storage.FileInfo{
				Path: "/special.fifo",
				Name: "special.fifo",
				Mode: os.ModeNamedPipe | 0o600,
			},
		},
	})

	if !errors.Is(err, storage.ErrNotRegular) {
		t.Fatalf("validateDownloadArchiveEntries() error = %v, want storage.ErrNotRegular", err)
	}
}

func TestServer_WriteDownloadArchiveRejectsShortSnapshot(t *testing.T) {
	server, _, _ := setupTestServer(t)
	content := []byte("short snapshot")
	hostPath := writeArchiveSnapshotTestFile(t, content)

	previousOpener := downloadArchiveSnapshotOpener
	downloadArchiveSnapshotOpener = func(_ *Server, _ context.Context, _ string) (*os.File, *storage.FileInfo, error) {
		file, err := os.Open(hostPath)
		if err != nil {
			return nil, nil, err
		}
		return file, &storage.FileInfo{
			Path:    "/short.bin",
			Name:    "short.bin",
			Size:    int64(len(content) + 1),
			ModTime: time.Unix(1700000000, 0),
		}, nil
	}
	t.Cleanup(func() {
		downloadArchiveSnapshotOpener = previousOpener
	})

	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	defer zipWriter.Close()
	err := server.writeDownloadArchive(context.Background(), zipWriter, []downloadArchiveEntry{
		{
			sourcePath: "/short.bin",
			zipName:    "short.bin",
			info: &storage.FileInfo{
				Path:    "/short.bin",
				Name:    "short.bin",
				Size:    int64(len(content) + 1),
				ModTime: time.Unix(1700000000, 0),
			},
		},
	})

	if !errors.Is(err, errDownloadArchiveSnapshotChanged) {
		t.Fatalf("writeDownloadArchive() error = %v, want errDownloadArchiveSnapshotChanged", err)
	}
}

func TestServer_DownloadArchiveStartedFailureLeavesInvalidZip(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/partial.bin", strings.NewReader("source")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	previousWriter := downloadArchiveWriter
	downloadArchiveWriter = func(_ *Server, _ context.Context, zipWriter *zip.Writer, _ []downloadArchiveEntry) error {
		header := &zip.FileHeader{Name: "partial.bin", Method: zip.Store}
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}
		payload := make([]byte, 64*1024)
		for i := range payload {
			payload[i] = byte(i * 31)
		}
		if _, err := writer.Write(payload); err != nil {
			return err
		}
		return errDownloadArchiveSnapshotChanged
	}
	t.Cleanup(func() {
		downloadArchiveWriter = previousWriter
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/partial.bin?archive=zip", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("started archive failure status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.Len() == 0 {
		t.Fatal("started archive failure wrote no partial response")
	}
	if _, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len())); err == nil {
		t.Fatal("started archive failure was finalized into a valid partial ZIP")
	}
	_, total := server.activity.List(10, 0, activity.ActionDownload, "")
	if total != 0 {
		t.Fatalf("started archive failure activity count = %d, want 0", total)
	}
}

func writeArchiveSnapshotTestFile(t *testing.T, content []byte) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "archive-snapshot-*.bin")
	if err != nil {
		t.Fatalf("CreateTemp() error: %v", err)
	}
	hostPath := file.Name()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		t.Fatalf("Write() error: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	return hostPath
}
