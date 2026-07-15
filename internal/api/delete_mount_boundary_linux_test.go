//go:build linux

package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/seanbao/mnemonas/internal/activity"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/storage"
)

const apiBindMountDeleteHelperEnv = "MNEMONAS_API_BIND_MOUNT_DELETE_HELPER"

func TestServer_DeleteMountBoundaryReturnsConflictBeforeEmptyTrashMutation(t *testing.T) {
	if os.Getenv(apiBindMountDeleteHelperEnv) == "1" {
		runAPIBindMountDeleteHelper(t)
		return
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skipf("unshare is unavailable: %v", err)
	}

	command := exec.Command(
		"unshare",
		"--user",
		"--map-root-user",
		"--mount",
		"--propagation", "private",
		os.Args[0],
		"-test.run=^TestServer_DeleteMountBoundaryReturnsConflictBeforeEmptyTrashMutation$",
		"-test.count=1",
	)
	command.Env = append(os.Environ(), apiBindMountDeleteHelperEnv+"=1")
	output, err := command.CombinedOutput()
	if err == nil {
		return
	}
	lowerOutput := strings.ToLower(string(output))
	if strings.Contains(lowerOutput, "operation not permitted") || strings.Contains(lowerOutput, "permission denied") {
		t.Skipf("user or mount namespaces are unavailable: %v: %s", err, output)
	}
	t.Fatalf("API bind mount helper failed: %v\n%s", err, output)
}

func runAPIBindMountDeleteHelper(t *testing.T) {
	t.Run("file delete", testServerRejectsFileDeleteInsideBindMount)
	t.Run("admin exact empty", func(t *testing.T) {
		server, fs, tmpDir := setupTestServer(t)
		testServerEmptyTrashMountedItem(t, server, fs, tmpDir, "", "")
	})
	t.Run("scoped exact empty", func(t *testing.T) {
		server, fs, tmpDir, username, password := setupAuthServer(t)
		setUserHomeDirForTest(t, server, username, "/tester")
		setDirectoryAccessRulesForTest(t, server, []config.DirectoryAccessRuleConfig{{
			Path:       "/tester",
			WriteUsers: []string{username},
		}})
		token := loginAndGetAccessToken(t, server, username, password)
		testServerEmptyTrashMountedItem(t, server, fs, tmpDir, "/tester", token)
	})
	t.Run("directory quota restore prewalk", testServerRejectsMountedTrashRestoreDuringDirectoryQuotaWalk)
	t.Run("restore destination bind mount", testServerRejectsRestoreDestinationInsideBindMount)
}

func testServerRejectsFileDeleteInsideBindMount(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()
	for _, dir := range []string{"/tree", "/tree/mounted"} {
		if err := fs.Mkdir(ctx, dir); err != nil {
			t.Fatalf("Mkdir(%s) error: %v", dir, err)
		}
	}
	if err := fs.WriteFile(ctx, "/tree/mounted/value.txt", strings.NewReader("underlying")); err != nil {
		t.Fatalf("WriteFile(underlying) error: %v", err)
	}
	sourceRoot := t.TempDir()
	sourceFile := filepath.Join(sourceRoot, "value.txt")
	if err := os.WriteFile(sourceFile, []byte("bind-source"), 0o600); err != nil {
		t.Fatalf("WriteFile(bind source) error: %v", err)
	}
	mountPoint := filepath.Join(tmpDir, "files", "tree", "mounted")
	mountAPIDeleteTest(t, sourceRoot, mountPoint)

	intentReq := httptest.NewRequest(http.MethodPost, "/api/v1/files-delete-intents", bytes.NewReader(observedDeleteIntentRequestBody(t, fs, "/tree/mounted/value.txt")))
	intentReq.Header.Set("Content-Type", "application/json")
	intentRec := httptest.NewRecorder()
	server.Router().ServeHTTP(intentRec, intentReq)
	if intentRec.Code != http.StatusConflict {
		t.Fatalf("delete intent inside bind mount status = %d, want %d; body=%s", intentRec.Code, http.StatusConflict, intentRec.Body.String())
	}

	if err := unix.Unmount(mountPoint, 0); err != nil {
		t.Fatalf("Unmount(bind target before intent) error: %v", err)
	}
	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/tree/mounted/value.txt"}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents(unmounted target) error: %v", err)
	}
	if err := unix.Mount(sourceRoot, mountPoint, "", unix.MS_BIND, ""); err != nil {
		t.Fatalf("Mount(bind target before REST delete) error: %v", err)
	}
	requestURL := deleteFileRequestURLWithTokens(
		t,
		"/api/v1/files/tree/mounted/value.txt",
		storage.DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token},
		intent.Targets[0].Token,
	)
	deleteReq := httptest.NewRequest(http.MethodDelete, requestURL, nil)
	deleteRec := httptest.NewRecorder()
	server.Router().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusConflict {
		t.Fatalf("REST delete inside bind mount status = %d, want %d; body=%s", deleteRec.Code, http.StatusConflict, deleteRec.Body.String())
	}
	assertAPIBindSource(t, sourceFile)
	if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
		t.Fatalf("trash after rejected REST delete = %+v, %v; want empty", items, err)
	}
	if _, total := server.activity.List(10, 0, activity.ActionDelete, ""); total != 0 {
		t.Fatalf("delete activities after rejected REST delete = %d, want 0", total)
	}
}

func testServerEmptyTrashMountedItem(t *testing.T, server *Server, fs *storage.FileSystem, tmpDir, basePath, token string) {
	ctx := context.Background()
	mountedRoot := basePath + "/mounted-trash"
	ordinaryPath := basePath + "/ordinary-trash.txt"
	for _, dir := range []string{mountedRoot, mountedRoot + "/nested"} {
		if err := fs.Mkdir(ctx, dir); err != nil {
			t.Fatalf("Mkdir(%s) error: %v", dir, err)
		}
	}
	if err := fs.WriteFile(ctx, mountedRoot+"/nested/local.txt", strings.NewReader("local")); err != nil {
		t.Fatalf("WriteFile(mounted trash local) error: %v", err)
	}
	if err := fs.Delete(ctx, mountedRoot); err != nil {
		t.Fatalf("Delete(mounted trash source) error: %v", err)
	}
	if err := fs.WriteFile(ctx, ordinaryPath, strings.NewReader("ordinary")); err != nil {
		t.Fatalf("WriteFile(ordinary trash) error: %v", err)
	}
	if err := fs.Delete(ctx, ordinaryPath); err != nil {
		t.Fatalf("Delete(ordinary trash source) error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("ListTrash() = %+v, %v; want two items", items, err)
	}
	var mountedItem *storage.TrashItem
	var ordinaryItem *storage.TrashItem
	for _, item := range items {
		if item.OriginalPath == mountedRoot {
			mountedItem = item
		}
		if item.OriginalPath == ordinaryPath {
			ordinaryItem = item
		}
	}
	if mountedItem == nil {
		t.Fatalf("mounted trash item not found in %+v", items)
	}
	if ordinaryItem == nil {
		t.Fatalf("ordinary trash item not found in %+v", items)
	}

	sourceRoot := t.TempDir()
	sourceFile := filepath.Join(sourceRoot, "source.txt")
	if err := os.WriteFile(sourceFile, []byte("bind-source"), 0o600); err != nil {
		t.Fatalf("WriteFile(trash bind source) error: %v", err)
	}
	trashMountPoint := filepath.Join(tmpDir, ".mnemonas", "trash", mountedItem.ID, "content", "nested")
	mountAPIDeleteTest(t, sourceRoot, trashMountPoint)

	restoreRequest := httptest.NewRequest(http.MethodPost, "/api/v1/trash/"+mountedItem.ID+"/restore", nil)
	if token != "" {
		restoreRequest.Header.Set("Authorization", "Bearer "+token)
	}
	restoreResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(restoreResponse, restoreRequest)
	if restoreResponse.Code != http.StatusConflict {
		t.Fatalf("mounted trash restore status = %d, want %d; body=%s", restoreResponse.Code, http.StatusConflict, restoreResponse.Body.String())
	}
	if _, err := fs.GetTrashItem(ctx, mountedItem.ID); err != nil {
		t.Fatalf("mounted trash item after rejected restore error: %v", err)
	}
	assertAPIBindSource(t, sourceFile)

	request := newEmptyTrashSelectionRequest(t, []string{ordinaryItem.ID, mountedItem.ID})
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("preflight empty trash status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	remaining, err := fs.ListTrash(ctx)
	if err != nil || len(remaining) != 2 {
		t.Fatalf("remaining trash items = %+v, %v; want both selected items", remaining, err)
	}
	assertAPIBindSource(t, sourceFile)
	entries, total := server.activity.List(10, 0, activity.ActionTrashEmpty, "")
	if total != 0 || len(entries) != 0 {
		t.Fatalf("preflight trash activity total=%d entries=%+v, want none", total, entries)
	}

	secondRequest := newEmptyTrashSelectionRequest(t, []string{mountedItem.ID})
	if token != "" {
		secondRequest.Header.Set("Authorization", "Bearer "+token)
	}
	secondResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusConflict {
		t.Fatalf("zero-delete mounted trash status = %d, want %d; body=%s", secondResponse.Code, http.StatusConflict, secondResponse.Body.String())
	}
	if _, total := server.activity.List(10, 0, activity.ActionTrashEmpty, ""); total != 0 {
		t.Fatalf("trash empty activities after zero-delete conflict = %d, want 0", total)
	}

	itemRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/trash/"+mountedItem.ID, nil)
	if token != "" {
		itemRequest.Header.Set("Authorization", "Bearer "+token)
	}
	itemResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(itemResponse, itemRequest)
	if itemResponse.Code != http.StatusConflict {
		t.Fatalf("single mounted trash delete status = %d, want %d; body=%s", itemResponse.Code, http.StatusConflict, itemResponse.Body.String())
	}
	if _, err := fs.GetTrashItem(ctx, mountedItem.ID); err != nil {
		t.Fatalf("mounted trash item after conflicts error: %v", err)
	}
	assertAPIBindSource(t, sourceFile)
}

func testServerRejectsMountedTrashRestoreDuringDirectoryQuotaWalk(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	setDirectoryQuotasForTest(t, server, []config.DirectoryQuotaConfig{{Path: "/quota-tree", QuotaBytes: 1 << 20}})
	ctx := context.Background()
	for _, dir := range []string{"/quota-tree", "/quota-tree/mounted"} {
		if err := fs.Mkdir(ctx, dir); err != nil {
			t.Fatalf("Mkdir(%s) error: %v", dir, err)
		}
	}
	if err := fs.WriteFile(ctx, "/quota-tree/mounted/local.txt", strings.NewReader("local")); err != nil {
		t.Fatalf("WriteFile(quota local) error: %v", err)
	}
	if err := fs.Delete(ctx, "/quota-tree"); err != nil {
		t.Fatalf("Delete(quota tree) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	sourceRoot := t.TempDir()
	sourceFile := filepath.Join(sourceRoot, "source.txt")
	if err := os.WriteFile(sourceFile, []byte("bind-source"), 0o600); err != nil {
		t.Fatalf("WriteFile(quota bind source) error: %v", err)
	}
	mountPoint := filepath.Join(tmpDir, ".mnemonas", "trash", items[0].ID, "content", "mounted")
	mountAPIDeleteTest(t, sourceRoot, mountPoint)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/trash/"+items[0].ID+"/restore", nil)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("directory quota mounted prewalk restore status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	if _, err := fs.GetTrashItem(ctx, items[0].ID); err != nil {
		t.Fatalf("quota trash item after rejected restore error: %v", err)
	}
	assertAPIBindSource(t, sourceFile)
	if _, total := server.activity.List(10, 0, activity.ActionTrashRestore, ""); total != 0 {
		t.Fatalf("trash restore activities after quota prewalk conflict = %d, want 0", total)
	}
}

func testServerRejectsRestoreDestinationInsideBindMount(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/restore-source"); err != nil {
		t.Fatalf("Mkdir(restore source) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-source/value.txt", strings.NewReader("value")); err != nil {
		t.Fatalf("WriteFile(restore source) error: %v", err)
	}
	if err := fs.Delete(ctx, "/restore-source"); err != nil {
		t.Fatalf("Delete(restore source) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash() = %+v, %v; want one item", items, err)
	}
	if err := fs.Mkdir(ctx, "/restore-parent"); err != nil {
		t.Fatalf("Mkdir(restore parent) error: %v", err)
	}
	externalRoot := t.TempDir()
	externalSentinel := filepath.Join(externalRoot, "sentinel.txt")
	if err := os.WriteFile(externalSentinel, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(destination sentinel) error: %v", err)
	}
	mountPoint := filepath.Join(tmpDir, "files", "restore-parent")
	mountAPIDeleteTest(t, externalRoot, mountPoint)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/trash/"+items[0].ID+"/restore?path=%2Frestore-parent%2Frestored-tree", nil)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("restore inside destination bind mount status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	if _, err := fs.GetTrashItem(ctx, items[0].ID); err != nil {
		t.Fatalf("trash item after destination mount conflict error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(externalRoot, "restored-tree")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("external restore destination was created: %v", statErr)
	}
	if data, readErr := os.ReadFile(externalSentinel); readErr != nil || string(data) != "outside" {
		t.Fatalf("external restore sentinel after conflict = %q, %v", data, readErr)
	}
	if _, total := server.activity.List(10, 0, activity.ActionTrashRestore, ""); total != 0 {
		t.Fatalf("trash restore activities after destination conflict = %d, want 0", total)
	}
}

func mountAPIDeleteTest(t *testing.T, source, target string) {
	t.Helper()
	if err := unix.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		t.Fatalf("Mount(%s -> %s) error: %v", source, target, err)
	}
	t.Cleanup(func() {
		if err := unix.Unmount(target, unix.MNT_DETACH); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
			t.Errorf("Unmount(%s) cleanup error: %v", target, err)
		}
	})
}

func assertAPIBindSource(t *testing.T, sourceFile string) {
	t.Helper()
	data, err := os.ReadFile(sourceFile)
	if err != nil || !bytes.Equal(data, []byte("bind-source")) {
		t.Fatalf("bind source after rejected API deletion = %q, %v", data, err)
	}
}
