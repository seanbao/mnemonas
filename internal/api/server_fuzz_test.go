package api

import (
	"path"
	"strings"
	"testing"
	"unicode"
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
		"/report\n2026.txt",
		"/report\x7f2026.txt",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		got, err := validatePath(input)
		normalizedInput := strings.ReplaceAll(input, "\\", "/")

		if err != nil {
			if !hasDotSegment(normalizedInput) && strings.IndexFunc(normalizedInput, unicode.IsControl) < 0 {
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
		if strings.IndexFunc(got, unicode.IsControl) >= 0 {
			t.Fatalf("validatePath(%q) = %q, want no control characters", input, got)
		}
		if strings.Contains(got, "\\") {
			t.Fatalf("validatePath(%q) = %q, want normalized separators", input, got)
		}
		if hasDotSegment(got) {
			t.Fatalf("validatePath(%q) = %q, want no dot segment", input, got)
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
