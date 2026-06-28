//go:build linux

package webdav

import (
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
)

const webDAVBindMountDeleteHelperEnv = "MNEMONAS_WEBDAV_BIND_MOUNT_DELETE_HELPER"

func TestHandler_DELETERejectsRealBindMountWithoutMutation(t *testing.T) {
	if os.Getenv(webDAVBindMountDeleteHelperEnv) == "1" {
		runWebDAVBindMountDeleteHelper(t)
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
		"-test.run=^TestHandler_DELETERejectsRealBindMountWithoutMutation$",
		"-test.count=1",
	)
	command.Env = append(os.Environ(), webDAVBindMountDeleteHelperEnv+"=1")
	output, err := command.CombinedOutput()
	if err == nil {
		return
	}
	lowerOutput := strings.ToLower(string(output))
	if strings.Contains(lowerOutput, "operation not permitted") || strings.Contains(lowerOutput, "permission denied") {
		t.Skipf("user or mount namespaces are unavailable: %v: %s", err, output)
	}
	t.Fatalf("WebDAV bind mount helper failed: %v\n%s", err, output)
}

func runWebDAVBindMountDeleteHelper(t *testing.T) {
	handler, fs, tmpDir := setupTestHandler(t)
	ctx := context.Background()
	for _, dir := range []string{"/tree", "/tree/mounted"} {
		if err := fs.Mkdir(ctx, dir); err != nil {
			t.Fatalf("Mkdir(%s) error: %v", dir, err)
		}
	}
	if err := fs.WriteFile(ctx, "/tree/mounted/local.txt", strings.NewReader("local")); err != nil {
		t.Fatalf("WriteFile(local) error: %v", err)
	}
	sourceRoot := t.TempDir()
	sourceFile := filepath.Join(sourceRoot, "value.txt")
	if err := os.WriteFile(sourceFile, []byte("bind-source"), 0o600); err != nil {
		t.Fatalf("WriteFile(bind source) error: %v", err)
	}
	mountPoint := filepath.Join(tmpDir, "files", "tree", "mounted")
	if err := unix.Mount(sourceRoot, mountPoint, "", unix.MS_BIND, ""); err != nil {
		t.Fatalf("Mount(%s -> %s) error: %v", sourceRoot, mountPoint, err)
	}
	t.Cleanup(func() {
		if err := unix.Unmount(mountPoint, unix.MNT_DETACH); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
			t.Errorf("Unmount(%s) cleanup error: %v", mountPoint, err)
		}
	})

	for _, testCase := range []struct {
		name   string
		header string
		value  string
	}{
		{name: "unconditional"},
		{name: "conditional", header: "If-Match", value: `"missing"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodDelete, "/dav/tree/mounted/value.txt", nil)
			if testCase.header != "" {
				request.Header.Set(testCase.header, testCase.value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusConflict || response.Body.String() != "resource type conflict\n" {
				t.Fatalf("DELETE bind mount status = %d, body=%q; want 409 resource type conflict", response.Code, response.Body.String())
			}
			if data, err := os.ReadFile(sourceFile); err != nil || string(data) != "bind-source" {
				t.Fatalf("bind source after rejected WebDAV DELETE = %q, %v", data, err)
			}
			if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
				t.Fatalf("trash after rejected WebDAV DELETE = %+v, %v; want empty", items, err)
			}
		})
	}
}
