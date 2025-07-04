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
