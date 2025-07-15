package api

import (
	"path"
	"strings"
	"testing"
)

func FuzzValidatePath(f *testing.F) {
	seeds := []string{
		"",
		"/",
		"docs/report.txt",
		"/docs//nested/",
		"foo..txt",
		"../etc/passwd",
		"/docs/../secret",
		"..\\etc\\passwd",
		"/nul\x00byte",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		got, err := validatePath(input)
		normalizedInput := strings.ReplaceAll(input, "\\", "/")

		if err != nil {
			if !hasTraversalSegment(normalizedInput) && !strings.ContainsRune(normalizedInput, '\x00') {
				t.Fatalf("validatePath(%q) returned unexpected error: %v", input, err)
			}
			return
		}

		if got == "" {
			t.Fatalf("validatePath(%q) returned empty path without error", input)
		}
		if !strings.HasPrefix(got, "/") {
			t.Fatalf("validatePath(%q) = %q, want absolute path", input, got)
		}
		if strings.ContainsRune(got, '\x00') {
			t.Fatalf("validatePath(%q) = %q, want no NUL bytes", input, got)
		}
		if strings.Contains(got, "\\") {
			t.Fatalf("validatePath(%q) = %q, want normalized separators", input, got)
		}
		if hasTraversalSegment(got) {
			t.Fatalf("validatePath(%q) = %q, want no traversal segment", input, got)
		}
		if got != path.Clean(got) {
			t.Fatalf("validatePath(%q) = %q, want clean path %q", input, got, path.Clean(got))
		}

		again, err := validatePath(got)
		if err != nil {
			t.Fatalf("validatePath(%q) second pass returned error: %v", got, err)
		}
		if again != got {
			t.Fatalf("validatePath(%q) second pass = %q, want %q", input, again, got)
		}
	})
}

func FuzzPathWithinBase(f *testing.F) {
	seeds := [][2]string{
		{"/", "/"},
		{"/", "/docs"},
		{"/docs", "/docs"},
		{"/docs", "/docs/report.txt"},
		{"/docs", "/docs2/report.txt"},
		{"/docs", "/doc"},
		{"docs", "docs/report.txt"},
	}

	for _, seed := range seeds {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, basePath string, targetPath string) {
		cleanBase := path.Clean(basePath)
		cleanTarget := path.Clean(targetPath)
		want := cleanTarget == cleanBase || strings.HasPrefix(cleanTarget, cleanBase+"/")
		if cleanBase == "/" {
			want = strings.HasPrefix(cleanTarget, "/")
		}

		if got := pathWithinBase(basePath, targetPath); got != want {
			t.Fatalf("pathWithinBase(%q, %q) = %v, want %v", basePath, targetPath, got, want)
		}
	})
}
