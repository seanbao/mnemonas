package versionstore

import (
	"context"
	"testing"
)

func TestVersioningPolicy_ShouldVersion_NormalizesConfiguredEntries(t *testing.T) {
	policy := &VersioningPolicy{
		AutoVersionedExtensions: []string{" .MD ", ".TXT"},
		AutoVersionedFilenames:  []string{" Dockerfile ", " README "},
		MaxVersionedSize:        1024,
	}

	if !policy.ShouldVersion(context.Background(), "/docs/notes.MD", 16) {
		t.Fatal("expected mixed-case configured extension to match regardless of surrounding whitespace")
	}
	if !policy.ShouldVersion(context.Background(), "/Dockerfile", 16) {
		t.Fatal("expected configured filename to match after trimming surrounding whitespace")
	}

	enabled, reason := policy.GetVersioningStatus(context.Background(), "/docs/readme.TXT", 16)
	if !enabled {
		t.Fatal("expected GetVersioningStatus() to enable versioning for normalized extension matches")
	}
	if reason != "extension_match" {
		t.Fatalf("expected extension_match reason, got %q", reason)
	}

	enabled, reason = policy.GetVersioningStatus(context.Background(), "/README", 16)
	if !enabled {
		t.Fatal("expected GetVersioningStatus() to enable versioning for trimmed filename matches")
	}
	if reason != "filename_match" {
		t.Fatalf("expected filename_match reason, got %q", reason)
	}
}

func TestVersioningPolicy_DefaultPolicyCoversCommonTextAssets(t *testing.T) {
	policy := DefaultVersioningPolicy(nil)
	ctx := context.Background()

	tests := []struct {
		name     string
		path     string
		fileSize int64
		want     bool
	}{
		{name: "markdown extension", path: "/notes/README.md", fileSize: 1024, want: true},
		{name: "case insensitive extension", path: "/notes/CHANGELOG.TXT", fileSize: 1024, want: true},
		{name: "known filename without extension", path: "/project/Makefile", fileSize: 1024, want: true},
		{name: "large default versioned file is skipped", path: "/notes/large.md", fileSize: policy.MaxVersionedSize + 1, want: false},
		{name: "binary extension is skipped", path: "/photos/image.jpg", fileSize: 1024, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := policy.ShouldVersion(ctx, tt.path, tt.fileSize); got != tt.want {
				t.Fatalf("ShouldVersion(%q, %d) = %v, want %v", tt.path, tt.fileSize, got, tt.want)
			}
		})
	}
}

func TestVersioningPolicy_QueryHelpersNormalizeCallerInput(t *testing.T) {
	policy := &VersioningPolicy{
		AutoVersionedExtensions: []string{" .md "},
		AutoVersionedFilenames:  []string{" README "},
	}

	if !policy.IsVersionedExtension(" .MD ") {
		t.Fatal("expected IsVersionedExtension to trim and lower-case caller input")
	}
	if !policy.IsVersionedFilename(" README ") {
		t.Fatal("expected IsVersionedFilename to trim caller input")
	}
	if policy.IsVersionedExtension(".txt") {
		t.Fatal("unexpected .txt versioned extension match")
	}
	if policy.IsVersionedFilename("README.md") {
		t.Fatal("unexpected README.md versioned filename match")
	}
}

func TestVersioningPolicy_ShouldVersionNormalizesCallerPathInput(t *testing.T) {
	policy := &VersioningPolicy{
		AutoVersionedExtensions: []string{".md"},
		AutoVersionedFilenames:  []string{"README"},
		MaxVersionedSize:        1024,
	}
	ctx := context.Background()

	if !policy.ShouldVersion(ctx, "/docs/notes.MD ", 16) {
		t.Fatal("expected ShouldVersion to trim and lower-case caller extension input")
	}
	if !policy.ShouldVersion(ctx, "/docs/ README ", 16) {
		t.Fatal("expected ShouldVersion to trim caller filename input")
	}

	enabled, reason := policy.GetVersioningStatus(ctx, "/docs/notes.MD ", 16)
	if !enabled || reason != "extension_match" {
		t.Fatalf("GetVersioningStatus extension result = (%v, %q), want enabled extension_match", enabled, reason)
	}

	enabled, reason = policy.GetVersioningStatus(ctx, "/docs/ README ", 16)
	if !enabled || reason != "filename_match" {
		t.Fatalf("GetVersioningStatus filename result = (%v, %q), want enabled filename_match", enabled, reason)
	}
}
