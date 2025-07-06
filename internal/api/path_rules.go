package api

import (
	"path"
	"strings"
	"unicode"
)

func cleanRuntimePathRulePath(rulePath string) string {
	trimmed := strings.TrimSpace(rulePath)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return ""
	}
	if strings.ContainsAny(trimmed, "\\?#") || hasDotSegment(trimmed) {
		return ""
	}
	if strings.IndexFunc(trimmed, unicode.IsControl) >= 0 {
		return ""
	}
	return path.Clean(trimmed)
}
