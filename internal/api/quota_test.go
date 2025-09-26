package api

import (
	"errors"
	"io"
	"testing"

	"github.com/seanbao/mnemonas/internal/config"
)

type noProgressQuotaReader struct{}

func (noProgressQuotaReader) Read([]byte) (int, error) {
	return 0, nil
}

func TestDirectoryQuotaRulesForTargetNormalizesRuntimePath(t *testing.T) {
	rules := []config.DirectoryQuotaConfig{
		{Path: "/team/", QuotaBytes: 10},
		{Path: "/shared//uploads", QuotaBytes: 20},
	}

	matched := directoryQuotaRulesForTarget(rules, "/team/report.txt")
	if len(matched) != 1 {
		t.Fatalf("matched /team quota rules = %+v, want one", matched)
	}
	if matched[0].Path != "/team" {
		t.Fatalf("matched /team quota path = %q, want /team", matched[0].Path)
	}

	matched = directoryQuotaRulesForTarget(rules, "/shared/uploads/file.txt")
	if len(matched) != 1 {
		t.Fatalf("matched shared quota rules = %+v, want one", matched)
	}
	if matched[0].Path != "/shared/uploads" {
		t.Fatalf("matched shared quota path = %q, want /shared/uploads", matched[0].Path)
	}
}

func TestDirectoryQuotaRulesForTargetRejectsDirtyRuntimePath(t *testing.T) {
	rules := []config.DirectoryQuotaConfig{
		{Path: "/team/../private", QuotaBytes: 1},
		{Path: `\private`, QuotaBytes: 1},
		{Path: "private", QuotaBytes: 1},
		{Path: "/private?token=secret", QuotaBytes: 1},
		{Path: "/private\x00secret", QuotaBytes: 1},
	}

	if matched := directoryQuotaRulesForTarget(rules, "/private/file.txt"); len(matched) != 0 {
		t.Fatalf("dirty runtime quota rules matched unexpectedly: %+v", matched)
	}
}

func TestDirectoryQuotaRulesForTargetRejectsDirtyTargetPath(t *testing.T) {
	rules := []config.DirectoryQuotaConfig{
		{Path: "/private", QuotaBytes: 1},
	}

	tests := []string{
		"/team/../private/file.txt",
		`/private\file.txt`,
		"/private?token=secret",
		"/private#fragment",
		"/private\x00file.txt",
	}

	for _, targetPath := range tests {
		t.Run(targetPath, func(t *testing.T) {
			if matched := directoryQuotaRulesForTarget(rules, targetPath); len(matched) != 0 {
				t.Fatalf("dirty target path matched unexpectedly: %+v", matched)
			}
		})
	}
}

func TestDirectoryQuotaRulesForTargetRejectsSiblingPrefix(t *testing.T) {
	rules := []config.DirectoryQuotaConfig{
		{Path: "/team", QuotaBytes: 1},
	}

	if matched := directoryQuotaRulesForTarget(rules, "/team2/file.txt"); len(matched) != 0 {
		t.Fatalf("sibling-prefixed quota path matched unexpectedly: %+v", matched)
	}
}

func TestHasDirectoryQuotaRulesRejectsDirtyRuntimePath(t *testing.T) {
	rules := []config.DirectoryQuotaConfig{
		{Path: "/team/../private", QuotaBytes: 1},
		{Path: `\private`, QuotaBytes: 1},
	}

	if hasDirectoryQuotaRules(rules) {
		t.Fatal("dirty runtime quota rules should not be considered active")
	}
}

func TestMappedTreePathForQuota(t *testing.T) {
	tests := []struct {
		name       string
		treeRoot   string
		mappedRoot string
		quotaPath  string
		wantPath   string
		wantOK     bool
	}{
		{
			name:       "quota covers destination root",
			treeRoot:   "/src",
			mappedRoot: "/dst",
			quotaPath:  "/dst",
			wantPath:   "/src",
			wantOK:     true,
		},
		{
			name:       "quota covers destination descendant",
			treeRoot:   "/src",
			mappedRoot: "/dst",
			quotaPath:  "/dst/private",
			wantPath:   "/src/private",
			wantOK:     true,
		},
		{
			name:       "destination outside quota",
			treeRoot:   "/src",
			mappedRoot: "/dst",
			quotaPath:  "/archive/private",
			wantOK:     false,
		},
		{
			name:       "reject dirty tree root",
			treeRoot:   "/src/../secret",
			mappedRoot: "/dst",
			quotaPath:  "/dst/private",
			wantOK:     false,
		},
		{
			name:       "reject dirty mapped root",
			treeRoot:   "/src",
			mappedRoot: `/dst\private`,
			quotaPath:  "/dst/private",
			wantOK:     false,
		},
		{
			name:       "reject dirty quota path",
			treeRoot:   "/src",
			mappedRoot: "/dst",
			quotaPath:  "/dst/../private",
			wantOK:     false,
		},
		{
			name:       "reject relative quota path",
			treeRoot:   "/src",
			mappedRoot: "/dst",
			quotaPath:  "dst/private",
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotOK := mappedTreePathForQuota(tt.treeRoot, tt.mappedRoot, tt.quotaPath)
			if gotPath != tt.wantPath || gotOK != tt.wantOK {
				t.Fatalf("mappedTreePathForQuota() = (%q, %v), want (%q, %v)", gotPath, gotOK, tt.wantPath, tt.wantOK)
			}
		})
	}
}

func TestQuotaLimitedReaderReturnsNoProgressWhenProbeStalls(t *testing.T) {
	reader := &quotaLimitedReader{
		reader:    noProgressQuotaReader{},
		remaining: 0,
		err:       errors.New("quota exceeded"),
	}

	n, err := reader.Read(make([]byte, 1))
	if n != 0 || !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("Read() = (%d, %v), want (0, io.ErrNoProgress)", n, err)
	}
}

func TestQuotaLimitedReaderAllowsZeroLengthRead(t *testing.T) {
	reader := &quotaLimitedReader{
		reader:    noProgressQuotaReader{},
		remaining: 0,
		err:       errors.New("quota exceeded"),
	}

	n, err := reader.Read(nil)
	if n != 0 || err != nil {
		t.Fatalf("zero-length Read() = (%d, %v), want (0, nil)", n, err)
	}
}
