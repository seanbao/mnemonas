package api

import (
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
)

func TestMatchSharePolicyRuleNormalizesRuntimePath(t *testing.T) {
	rules := []config.SharePolicyRuleConfig{
		{Path: "/docs/", MaxAccess: 5},
		{Path: "/docs/sub", MaxAccess: 3},
		{Path: "/shared//reports", MaxAccess: 2},
	}

	rule, ok := matchSharePolicyRule(rules, "/docs/a.txt")
	if !ok {
		t.Fatal("expected normalized /docs rule to match")
	}
	if rule.Path != "/docs" || rule.MaxAccess != 5 {
		t.Fatalf("matched rule = %+v, want normalized /docs rule", rule)
	}

	rule, ok = matchSharePolicyRule(rules, "/docs/sub/a.txt")
	if !ok {
		t.Fatal("expected most-specific /docs/sub rule to match")
	}
	if rule.Path != "/docs/sub" || rule.MaxAccess != 3 {
		t.Fatalf("matched rule = %+v, want /docs/sub rule", rule)
	}

	rule, ok = matchSharePolicyRule(rules, "/shared/reports/a.txt")
	if !ok {
		t.Fatal("expected duplicate-slash rule to match after normalization")
	}
	if rule.Path != "/shared/reports" || rule.MaxAccess != 2 {
		t.Fatalf("matched rule = %+v, want /shared/reports rule", rule)
	}
}

func TestMatchSharePolicyRuleRejectsDirtyRuntimePath(t *testing.T) {
	rules := []config.SharePolicyRuleConfig{
		{Path: "/docs/../private", RequirePassword: true},
		{Path: `\private`, RequirePassword: true},
		{Path: "private", RequirePassword: true},
		{Path: "/private?token=secret", RequirePassword: true},
		{Path: "/private\x00secret", RequirePassword: true},
		{Path: "/private#fragment", RequirePassword: true},
		{Path: "/private", MaxExpiresIn: time.Hour},
	}

	rule, ok := matchSharePolicyRule(rules, "/private/a.txt")
	if !ok {
		t.Fatal("expected clean /private fallback rule to match")
	}
	if rule.Path != "/private" || rule.MaxExpiresIn != time.Hour || rule.RequirePassword {
		t.Fatalf("matched rule = %+v, want clean /private fallback rule", rule)
	}
}

func TestMatchSharePolicyRuleRejectsDirtyTargetPath(t *testing.T) {
	rules := []config.SharePolicyRuleConfig{
		{Path: "/private", RequirePassword: true},
	}

	tests := []string{
		"/docs/../private/report.txt",
		`/private\report.txt`,
		"/private?token=secret",
		"/private#fragment",
		"/private\x00report.txt",
	}

	for _, targetPath := range tests {
		t.Run(targetPath, func(t *testing.T) {
			if rule, ok := matchSharePolicyRule(rules, targetPath); ok {
				t.Fatalf("dirty target path matched unexpectedly: %+v", rule)
			}
		})
	}
}

func TestMatchSharePolicyRuleRejectsSiblingPrefix(t *testing.T) {
	rules := []config.SharePolicyRuleConfig{
		{Path: "/docs", RequirePassword: true},
	}

	if rule, ok := matchSharePolicyRule(rules, "/docs2/a.txt"); ok {
		t.Fatalf("sibling-prefixed share policy matched unexpectedly: %+v", rule)
	}
}
