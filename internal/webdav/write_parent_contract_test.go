package webdav

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

type webDAVRejectReadBody struct {
	reads int
}

func (b *webDAVRejectReadBody) Read([]byte) (int, error) {
	b.reads++
	return 0, errors.New("request body must not be read")
}

func (b *webDAVRejectReadBody) Close() error {
	return nil
}

func TestHandler_PUT_RequiresExistingDirectParent(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	rejectedBody := &webDAVRejectReadBody{}
	rejectedRequest := httptest.NewRequest(
		http.MethodPut,
		"/dav/contract-parent/file.txt",
		rejectedBody,
	)
	rejectedRequest.ContentLength = int64(len("must-not-be-read"))
	rejectedResponse := httptest.NewRecorder()

	handler.ServeHTTP(rejectedResponse, rejectedRequest)

	if rejectedResponse.Code != http.StatusConflict {
		t.Fatalf(
			"PUT with missing direct parent status = %d, want %d; body=%s",
			rejectedResponse.Code,
			http.StatusConflict,
			rejectedResponse.Body.String(),
		)
	}
	if !strings.Contains(rejectedResponse.Body.String(), "parent directory not found") {
		t.Fatalf("missing-parent PUT response = %q, want parent conflict", rejectedResponse.Body.String())
	}
	if rejectedBody.reads != 0 {
		t.Fatalf("missing-parent PUT read request body %d times, want 0", rejectedBody.reads)
	}
	if _, err := fs.Stat(ctx, "/contract-parent"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing-parent PUT parent status = %v, want ErrNotFound", err)
	}
	if _, err := fs.Stat(ctx, "/contract-parent/file.txt"); !errors.Is(err, storage.ErrNotFound) &&
		!errors.Is(err, storage.ErrNotDir) {
		t.Fatalf("missing-parent PUT target status = %v, want absent target", err)
	}

	createDirectoryRequest := httptest.NewRequest("MKCOL", "/dav/contract-parent", nil)
	createDirectoryResponse := httptest.NewRecorder()
	handler.ServeHTTP(createDirectoryResponse, createDirectoryRequest)
	if createDirectoryResponse.Code != http.StatusCreated {
		t.Fatalf(
			"MKCOL direct parent status = %d, want %d; body=%s",
			createDirectoryResponse.Code,
			http.StatusCreated,
			createDirectoryResponse.Body.String(),
		)
	}

	content := "created after MKCOL"
	putRequest := httptest.NewRequest(
		http.MethodPut,
		"/dav/contract-parent/file.txt",
		strings.NewReader(content),
	)
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, putRequest)
	if putResponse.Code != http.StatusCreated {
		t.Fatalf(
			"PUT after MKCOL status = %d, want %d; body=%s",
			putResponse.Code,
			http.StatusCreated,
			putResponse.Body.String(),
		)
	}

	file, err := fs.OpenFile(ctx, "/contract-parent/file.txt")
	if err != nil {
		t.Fatalf("OpenFile() after MKCOL error: %v", err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll() after MKCOL error: %v", err)
	}
	if string(data) != content {
		t.Fatalf("PUT content = %q, want %q", data, content)
	}
}
