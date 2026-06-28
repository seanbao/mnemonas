//go:build unix

package webdav

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestHandler_DELETERejectsFIFOWithoutBlockingOrMutation(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		targetPath string
		fifoPath   string
		header     string
		value      string
	}{
		{name: "root unconditional", targetPath: "/special.fifo", fifoPath: "special.fifo"},
		{name: "descendant unconditional", targetPath: "/tree", fifoPath: filepath.Join("tree", "special.fifo")},
		{name: "descendant depth", targetPath: "/tree", fifoPath: filepath.Join("tree", "special.fifo"), header: "Depth", value: "infinity"},
		{name: "descendant conditional", targetPath: "/tree", fifoPath: filepath.Join("tree", "special.fifo"), header: "If-Match", value: `""`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler, fs, tmpDir := setupTestHandler(t)
			ctx := context.Background()
			if testCase.targetPath == "/tree" {
				if err := fs.Mkdir(ctx, "/tree"); err != nil {
					t.Fatalf("Mkdir(tree) error: %v", err)
				}
				if err := fs.WriteFile(ctx, "/tree/keep.txt", strings.NewReader("keep")); err != nil {
					t.Fatalf("WriteFile(keep) error: %v", err)
				}
			}
			hostFIFOPath := filepath.Join(tmpDir, "files", testCase.fifoPath)
			if err := unix.Mkfifo(hostFIFOPath, 0o600); err != nil {
				t.Fatalf("Mkfifo() error: %v", err)
			}

			request := httptest.NewRequest(http.MethodDelete, "/dav"+testCase.targetPath, nil)
			if testCase.header != "" {
				request.Header.Set(testCase.header, testCase.value)
			}
			deleteDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				deleteDone <- response
			}()

			var response *httptest.ResponseRecorder
			select {
			case response = <-deleteDone:
			case <-time.After(time.Second):
				if fd, err := unix.Open(hostFIFOPath, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
					_ = unix.Close(fd)
				}
				t.Fatal("WebDAV DELETE blocked waiting for a FIFO writer")
			}
			if response.Code != http.StatusConflict || response.Body.String() != "resource type conflict\n" {
				t.Fatalf("DELETE FIFO status = %d, body = %q; want 409 resource type conflict", response.Code, response.Body.String())
			}
			if info, err := os.Lstat(hostFIFOPath); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
				t.Fatalf("FIFO after rejected DELETE mode = %v, error = %v", webDAVFileMode(info), err)
			}
			if testCase.targetPath == "/tree" {
				if _, err := fs.Stat(ctx, "/tree/keep.txt"); err != nil {
					t.Fatalf("tree changed after rejected DELETE: %v", err)
				}
			}
			if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
				t.Fatalf("trash after rejected DELETE = %+v, %v; want empty", items, err)
			}

			writeDone := make(chan error, 1)
			go func() {
				writeDone <- fs.WriteFile(ctx, "/after-special-delete.txt", strings.NewReader("ok"))
			}()
			select {
			case err := <-writeDone:
				if err != nil {
					t.Fatalf("WriteFile() after rejected DELETE error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("filesystem mutation lock remained blocked after rejected WebDAV DELETE")
			}
		})
	}
}

func TestHandler_DELETERejectsSymlinkDescendantWithoutMutation(t *testing.T) {
	handler, fs, tmpDir := setupTestHandler(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/keep.txt", strings.NewReader("keep")); err != nil {
		t.Fatalf("WriteFile(keep) error: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	linkPath := filepath.Join(tmpDir, "files", "tree", "linked.txt")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	request := httptest.NewRequest(http.MethodDelete, "/dav/tree", nil)
	request.Header.Set("Depth", "infinity")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || response.Body.String() != "resource type conflict\n" {
		t.Fatalf("DELETE symlink status = %d, body = %q; want 409 resource type conflict", response.Code, response.Body.String())
	}
	if _, err := fs.Stat(ctx, "/tree/keep.txt"); err != nil {
		t.Fatalf("tree changed after rejected DELETE: %v", err)
	}
	if info, err := os.Lstat(linkPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink after rejected DELETE mode = %v, error = %v", webDAVFileMode(info), err)
	}
	if data, err := os.ReadFile(outsidePath); err != nil || string(data) != "outside" {
		t.Fatalf("outside target after rejected DELETE = %q, %v", data, err)
	}
	if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
		t.Fatalf("trash after rejected DELETE = %+v, %v; want empty", items, err)
	}
}

func TestHandler_DELETEConditionalRejectsRootSymlinkWithoutMutation(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		header string
		value  string
	}{
		{name: "depth", header: "Depth", value: "infinity"},
		{name: "if match", header: "If-Match", value: `"missing"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler, fs, tmpDir := setupTestHandler(t)
			outsidePath := filepath.Join(t.TempDir(), "outside.txt")
			if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
				t.Fatalf("WriteFile(outside) error: %v", err)
			}
			linkPath := filepath.Join(tmpDir, "files", "linked.txt")
			if err := os.Symlink(outsidePath, linkPath); err != nil {
				t.Fatalf("Symlink() error: %v", err)
			}

			request := httptest.NewRequest(http.MethodDelete, "/dav/linked.txt", nil)
			request.Header.Set(testCase.header, testCase.value)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusConflict || response.Body.String() != "resource type conflict\n" {
				t.Fatalf("conditional root symlink DELETE status = %d, body = %q; want 409 resource type conflict", response.Code, response.Body.String())
			}
			if info, err := os.Lstat(linkPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("root symlink after rejected DELETE mode = %v, error = %v", webDAVFileMode(info), err)
			}
			if data, err := os.ReadFile(outsidePath); err != nil || string(data) != "outside" {
				t.Fatalf("outside target after rejected DELETE = %q, %v", data, err)
			}
			if items, err := fs.ListTrash(context.Background()); err != nil || len(items) != 0 {
				t.Fatalf("trash after rejected root symlink DELETE = %+v, %v; want empty", items, err)
			}
		})
	}
}

func webDAVFileMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}
