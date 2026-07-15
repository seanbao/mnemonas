package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/storage"
)

type apiRejectReadBody struct {
	reads int
}

func (b *apiRejectReadBody) Read([]byte) (int, error) {
	b.reads++
	return 0, errors.New("request body must not be read")
}

func (b *apiRejectReadBody) Close() error {
	return nil
}

func TestServer_UploadFile_RequiresExistingDirectParent(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	rejectedBody := &apiRejectReadBody{}
	rejectedRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/files/contract-parent/file.txt",
		rejectedBody,
	)
	rejectedRequest.ContentLength = int64(len("must-not-be-read"))
	rejectedResponse := httptest.NewRecorder()

	server.Router().ServeHTTP(rejectedResponse, rejectedRequest)

	if rejectedResponse.Code != http.StatusConflict {
		t.Fatalf(
			"upload with missing direct parent status = %d, want %d; body=%s",
			rejectedResponse.Code,
			http.StatusConflict,
			rejectedResponse.Body.String(),
		)
	}
	if !strings.Contains(rejectedResponse.Body.String(), "parent path is not a directory") {
		t.Fatalf("missing-parent response = %q, want parent conflict", rejectedResponse.Body.String())
	}
	if rejectedBody.reads != 0 {
		t.Fatalf("missing-parent upload read request body %d times, want 0", rejectedBody.reads)
	}
	if _, err := fs.Stat(ctx, "/contract-parent"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing-parent upload parent status = %v, want ErrNotFound", err)
	}
	if _, err := fs.Stat(ctx, "/contract-parent/file.txt"); !errors.Is(err, storage.ErrNotFound) &&
		!errors.Is(err, storage.ErrNotDir) {
		t.Fatalf("missing-parent upload target status = %v, want absent target", err)
	}

	createDirectoryRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/directories/contract-parent",
		nil,
	)
	createDirectoryResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(createDirectoryResponse, createDirectoryRequest)
	if createDirectoryResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create direct parent status = %d, want %d; body=%s",
			createDirectoryResponse.Code,
			http.StatusCreated,
			createDirectoryResponse.Body.String(),
		)
	}

	content := "created after explicit parent"
	uploadRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/files/contract-parent/file.txt",
		strings.NewReader(content),
	)
	uploadResponse := httptest.NewRecorder()
	server.Router().ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf(
			"upload after explicit parent status = %d, want %d; body=%s",
			uploadResponse.Code,
			http.StatusCreated,
			uploadResponse.Body.String(),
		)
	}

	file, err := fs.OpenFile(ctx, "/contract-parent/file.txt")
	if err != nil {
		t.Fatalf("OpenFile() after explicit parent error: %v", err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll() after explicit parent error: %v", err)
	}
	if string(data) != content {
		t.Fatalf("uploaded content = %q, want %q", data, content)
	}
}
