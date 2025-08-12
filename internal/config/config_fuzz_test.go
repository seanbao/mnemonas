package config

import (
	"strings"
	"testing"
)

func FuzzNormalizeWebDAVPrefix(f *testing.F) {
	for _, seed := range []string{
		"",
		"/",
		"dav",
		"/dav/",
		" //dav//files/// ",
		"nested/path",
		"///",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, prefix string) {
		normalized := NormalizeWebDAVPrefix(prefix)
		if normalized == "" {
			t.Fatal("normalized prefix must not be empty")
		}
		if !strings.HasPrefix(normalized, "/") {
			t.Fatalf("normalized prefix %q must start with slash", normalized)
		}
		if normalized != "/" && strings.HasSuffix(normalized, "/") {
			t.Fatalf("normalized prefix %q must not have a trailing slash", normalized)
		}
		if strings.Contains(normalized, "//") {
			t.Fatalf("normalized prefix %q must not contain repeated slashes", normalized)
		}
		if again := NormalizeWebDAVPrefix(normalized); again != normalized {
			t.Fatalf("NormalizeWebDAVPrefix is not stable: %q then %q", normalized, again)
		}
	})
}
