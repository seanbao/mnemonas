package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
	quotareservation "github.com/seanbao/mnemonas/internal/quota"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/webdav"
)

func TestSharedQuotaCoordinatorRejectsCrossProtocolOverbooking(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	coordinator := quotareservation.NewCoordinator()
	server.quotaCoordinator = coordinator
	setDirectoryQuotasForTest(t, server, []config.DirectoryQuotaConfig{{
		Path:       "/team",
		QuotaBytes: 10,
	}})
	if err := fs.Mkdir(t.Context(), "/team"); err != nil {
		t.Fatalf("Mkdir(/team) error = %v", err)
	}

	handler := webdav.NewHandler(webdav.Config{
		FileSystem:       fs,
		Prefix:           "/dav",
		AuthType:         "none",
		QuotaCoordinator: coordinator,
		DirectoryQuotas: []webdav.DirectoryQuota{{
			Path:       "/team",
			QuotaBytes: 10,
		}},
	})
	t.Cleanup(handler.Close)

	apiReader := newAPIBlockingUploadReader()
	t.Cleanup(apiReader.Release)
	apiRequest := httptest.NewRequest(http.MethodPost, "/api/v1/files/team/api.bin", apiReader)
	apiRequest.ContentLength = 6
	apiResponse := httptest.NewRecorder()
	apiDone := make(chan struct{})
	go func() {
		server.Router().ServeHTTP(apiResponse, apiRequest)
		close(apiDone)
	}()
	<-apiReader.started

	firstRequest := httptest.NewRequest(http.MethodPut, "/dav/team/webdav.bin", strings.NewReader("123456"))
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, firstRequest)
	if firstResponse.Code != http.StatusInsufficientStorage {
		t.Fatalf("cross-protocol overbooked PUT status = %d, want %d; body=%s", firstResponse.Code, http.StatusInsufficientStorage, firstResponse.Body.String())
	}

	apiReader.Release()
	<-apiDone
	if apiResponse.Code != http.StatusCreated {
		t.Fatalf("API upload status = %d, want %d; body=%s", apiResponse.Code, http.StatusCreated, apiResponse.Body.String())
	}

	secondRequest := httptest.NewRequest(http.MethodPut, "/dav/team/webdav.bin", strings.NewReader("123456"))
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusCreated {
		t.Fatalf("PUT after API reservation release status = %d, want %d; body=%s", secondResponse.Code, http.StatusCreated, secondResponse.Body.String())
	}
}

func TestSharedQuotaCoordinatorRefreshRejectsCrossProtocolSourceGrowth(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	coordinator := quotareservation.NewCoordinator()
	server.quotaCoordinator = coordinator
	setDirectoryQuotasForTest(t, server, []config.DirectoryQuotaConfig{{
		Path:       "/team",
		QuotaBytes: 150,
	}})
	if err := fs.Mkdir(t.Context(), "/team"); err != nil {
		t.Fatalf("Mkdir(/team) error = %v", err)
	}
	if err := fs.WriteFile(t.Context(), "/team/source.bin", strings.NewReader("s")); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}

	handler := webdav.NewHandler(webdav.Config{
		FileSystem:       fs,
		Prefix:           "/dav",
		AuthType:         "none",
		QuotaCoordinator: coordinator,
		DirectoryQuotas: []webdav.DirectoryQuota{{
			Path:       "/team",
			QuotaBytes: 150,
		}},
	})
	t.Cleanup(handler.Close)

	copyCommitReached := make(chan struct{})
	allowCopyCommit := make(chan struct{})
	server.beforeQuotaMutationCommit = func(operation string) {
		if operation != "copy" {
			return
		}
		close(copyCommitReached)
		<-allowCopyCommit
	}

	copyRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/files-copy",
		strings.NewReader(`{"from":"/team/source.bin","to":"/team/copied.bin"}`),
	)
	copyRequest.Header.Set("Content-Type", "application/json")
	copyResponse := httptest.NewRecorder()
	copyDone := make(chan struct{})
	go func() {
		server.Router().ServeHTTP(copyResponse, copyRequest)
		close(copyDone)
	}()

	select {
	case <-copyCommitReached:
	case <-time.After(5 * time.Second):
		close(allowCopyCommit)
		t.Fatal("API COPY did not reach the final quota commit boundary")
	}

	putRequest := httptest.NewRequest(
		http.MethodPut,
		"/dav/team/source.bin",
		strings.NewReader(strings.Repeat("p", 100)),
	)
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, putRequest)
	if putResponse.Code != http.StatusNoContent {
		close(allowCopyCommit)
		t.Fatalf("concurrent WebDAV overwrite status = %d, want %d; body=%s", putResponse.Code, http.StatusNoContent, putResponse.Body.String())
	}

	close(allowCopyCommit)
	select {
	case <-copyDone:
	case <-time.After(5 * time.Second):
		t.Fatal("API COPY did not finish after releasing the commit hook")
	}
	if copyResponse.Code != http.StatusInsufficientStorage {
		t.Fatalf("refreshed API COPY status = %d, want %d; body=%s", copyResponse.Code, http.StatusInsufficientStorage, copyResponse.Body.String())
	}
	if !strings.Contains(copyResponse.Body.String(), ErrCodeQuotaExceeded) {
		t.Fatalf("refreshed API COPY response = %s, want %s", copyResponse.Body.String(), ErrCodeQuotaExceeded)
	}
	if _, err := fs.Stat(t.Context(), "/team/copied.bin"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("quota-rejected COPY destination error = %v, want storage.ErrNotFound", err)
	}
	reader, err := fs.OpenFile(t.Context(), "/team/source.bin")
	if err != nil {
		t.Fatalf("OpenFile(source) error = %v", err)
	}
	sourceContent, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		t.Fatalf("ReadAll(source) error = %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("Close(source) error = %v", closeErr)
	}
	if len(sourceContent) != 100 {
		t.Fatalf("source size = %d, want 100", len(sourceContent))
	}
	usedBytes, err := server.pathLogicalSize(t.Context(), "/team")
	if err != nil {
		t.Fatalf("pathLogicalSize(/team) error = %v", err)
	}
	if usedBytes > 150 {
		t.Fatalf("final directory usage = %d, want <= 150", usedBytes)
	}
}

func TestQuotaUsageScanBlocksUploadCommit(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	coordinator := quotareservation.NewCoordinator()
	server.quotaCoordinator = coordinator
	for _, dir := range []string{"/tree", "/tree/nested"} {
		if err := fs.Mkdir(t.Context(), dir); err != nil {
			t.Fatalf("Mkdir(%s) error = %v", dir, err)
		}
	}
	if err := fs.WriteFile(t.Context(), "/tree/nested/file.bin", strings.NewReader("scan")); err != nil {
		t.Fatalf("WriteFile(scan tree) error = %v", err)
	}

	uploadReader := newAPIBlockingUploadReader()
	t.Cleanup(uploadReader.Release)
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/v1/files/commit.bin", uploadReader)
	uploadRequest.ContentLength = 1
	uploadResponse := httptest.NewRecorder()
	uploadDone := make(chan struct{})
	commitAttempted := make(chan struct{})
	var commitOnce sync.Once
	server.beforeQuotaMutationCommit = func(operation string) {
		if operation == "put" {
			commitOnce.Do(func() { close(commitAttempted) })
		}
	}
	go func() {
		server.Router().ServeHTTP(uploadResponse, uploadRequest)
		close(uploadDone)
	}()
	select {
	case <-uploadReader.started:
	case <-time.After(5 * time.Second):
		t.Fatal("API upload did not begin reading its request body")
	}

	scanStarted := make(chan struct{})
	allowScan := make(chan struct{})
	var scanStartOnce sync.Once
	var allowScanOnce sync.Once
	releaseScan := func() {
		allowScanOnce.Do(func() { close(allowScan) })
	}
	t.Cleanup(releaseScan)
	server.quotaUsageScanVisit = func(scanPath string) {
		if scanPath != "/tree" {
			return
		}
		scanStartOnce.Do(func() { close(scanStarted) })
		<-allowScan
	}

	scanDone := make(chan error, 1)
	go func() {
		reservation, err := server.reserveQuotaChecks(
			t.Context(),
			func(ctx context.Context, _ quotareservation.View) ([]quotaCheck, error) {
				_, scanErr := server.pathLogicalSize(ctx, "/tree")
				return nil, scanErr
			},
		)
		if reservation != nil {
			reservation.Release()
		}
		scanDone <- err
	}()
	select {
	case <-scanStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("quota usage scan did not reach the recursive tree")
	}

	uploadReader.Release()
	select {
	case <-commitAttempted:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not reach its EOF commit boundary")
	}
	select {
	case <-uploadDone:
		t.Fatalf("upload committed while quota usage scan held the mutation gate; status=%d body=%s", uploadResponse.Code, uploadResponse.Body.String())
	case <-time.After(100 * time.Millisecond):
	}

	releaseScan()
	if err := <-scanDone; err != nil {
		t.Fatalf("quota usage scan error = %v", err)
	}
	select {
	case <-uploadDone:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not commit after quota usage scan released the mutation gate")
	}
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d; body=%s", uploadResponse.Code, http.StatusCreated, uploadResponse.Body.String())
	}
}

func TestQuotaLogicalSizeStopsAfterContextCancellation(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	for _, dir := range []string{"/tree", "/tree/nested"} {
		if err := fs.Mkdir(t.Context(), dir); err != nil {
			t.Fatalf("Mkdir(%s) error = %v", dir, err)
		}
	}
	if err := fs.WriteFile(t.Context(), "/tree/nested/file.bin", strings.NewReader("scan")); err != nil {
		t.Fatalf("WriteFile(scan tree) error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	var cancelOnce sync.Once
	server.quotaUsageScanVisit = func(string) {
		cancelOnce.Do(cancel)
	}
	_, err := server.pathLogicalSize(ctx, "/tree")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pathLogicalSize() error = %v, want context.Canceled", err)
	}
}
