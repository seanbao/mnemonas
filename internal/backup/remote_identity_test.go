package backup

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestValidateResticRepositoryFormAllowsOnlyExplicitCredentialModels(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		wantErr    bool
	}{
		{name: "absolute local", repository: filepath.Join(t.TempDir(), "repository")},
		{name: "rest https", repository: "rest:https://backup.example/repository"},
		{name: "rest http", repository: "rest:http://backup.example/repository"},
		{name: "rest missing host", repository: "rest:https:///repository", wantErr: true},
		{name: "rest unsupported scheme", repository: "rest:ftp://backup.example/repository", wantErr: true},
		{name: "relative local", repository: "backups/repository", wantErr: true},
		{name: "s3 environment credentials", repository: "s3:s3.example/bucket", wantErr: true},
		{name: "sftp agent credentials", repository: "sftp:user@backup.example:/repository", wantErr: true},
		{name: "rclone external config", repository: "rclone:backup:repository", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResticRepositoryForm(tt.repository)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateResticRepositoryForm(%q) error = %v, wantErr %v", tt.repository, err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("validateResticRepositoryForm(%q) error = %v, want ErrUnsafePath", tt.repository, err)
			}
		})
	}
}

func TestValidateResticRepositoryBoundaryRejectsProtectedTrees(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	storageRoot := filepath.Join(root, "storage")
	for _, repository := range []string{
		filepath.Join(source, "repository"),
		filepath.Join(storageRoot, "repository"),
	} {
		if err := validateResticRepositoryBoundary(repository, source, storageRoot); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("validateResticRepositoryBoundary(%q) error = %v, want ErrUnsafePath", repository, err)
		}
	}
	if err := validateResticRepositoryBoundary(filepath.Join(root, "repository"), source, storageRoot); err != nil {
		t.Fatalf("validateResticRepositoryBoundary(outside) error: %v", err)
	}
}
