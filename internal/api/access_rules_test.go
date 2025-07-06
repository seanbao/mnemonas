package api

import (
	"context"
	"errors"
	"testing"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
)

func TestMatchDirectoryAccessRuleInNormalizesRuntimePath(t *testing.T) {
	rules := []config.DirectoryAccessRuleConfig{
		{Path: "/team/", ReadUsers: []string{"alice"}},
		{Path: "/team/private", ReadUsers: []string{"bob"}},
		{Path: "/shared//uploads", WriteUsers: []string{"alice"}},
	}

	rule, ok := matchDirectoryAccessRuleIn(rules, "/team/readme.txt")
	if !ok {
		t.Fatal("expected normalized /team rule to match")
	}
	if rule.Path != "/team" {
		t.Fatalf("matched rule path = %q, want /team", rule.Path)
	}

	rule, ok = matchDirectoryAccessRuleIn(rules, "/team/private/secret.txt")
	if !ok {
		t.Fatal("expected most-specific normalized rule to match")
	}
	if rule.Path != "/team/private" || len(rule.ReadUsers) != 1 || rule.ReadUsers[0] != "bob" {
		t.Fatalf("matched specific rule = %+v, want /team/private bob rule", rule)
	}

	rule, ok = matchDirectoryAccessRuleIn(rules, "/shared/uploads/new.txt")
	if !ok {
		t.Fatal("expected normalized duplicate-slash rule to match")
	}
	if rule.Path != "/shared/uploads" {
		t.Fatalf("matched duplicate-slash rule path = %q, want /shared/uploads", rule.Path)
	}
}

func TestMatchDirectoryAccessRuleInRejectsDirtyRuntimePath(t *testing.T) {
	rules := []config.DirectoryAccessRuleConfig{
		{Path: "/team/../private", ReadUsers: []string{"alice"}},
		{Path: `\private`, ReadUsers: []string{"alice"}},
		{Path: "private", ReadUsers: []string{"alice"}},
		{Path: "/private?token=secret", ReadUsers: []string{"alice"}},
		{Path: "/private\x00secret", ReadUsers: []string{"alice"}},
	}

	if rule, ok := matchDirectoryAccessRuleIn(rules, "/private/secret.txt"); ok {
		t.Fatalf("dirty runtime rule matched unexpectedly: %+v", rule)
	}
}

func TestMatchDirectoryAccessRuleInRejectsDirtyTargetPath(t *testing.T) {
	rules := []config.DirectoryAccessRuleConfig{
		{Path: "/private", ReadUsers: []string{"alice"}},
	}

	tests := []string{
		"/team/../private/secret.txt",
		`/private\secret.txt`,
		"/private?token=secret",
		"/private#fragment",
		"/private\x00secret",
	}

	for _, targetPath := range tests {
		t.Run(targetPath, func(t *testing.T) {
			if rule, ok := matchDirectoryAccessRuleIn(rules, targetPath); ok {
				t.Fatalf("dirty target path matched unexpectedly: %+v", rule)
			}
		})
	}
}

func TestMatchDirectoryAccessRuleInRejectsSiblingPrefix(t *testing.T) {
	rules := []config.DirectoryAccessRuleConfig{
		{Path: "/team", ReadUsers: []string{"alice"}},
	}

	if rule, ok := matchDirectoryAccessRuleIn(rules, "/team2/secret.txt"); ok {
		t.Fatalf("sibling-prefixed path matched unexpectedly: %+v", rule)
	}
}

func TestReadableDirectoryAccessDescendantRulesNormalizeRuntimePath(t *testing.T) {
	user := &auth.User{Username: "alice", Role: auth.RoleUser, HomeDir: "/users/alice"}
	rules := []config.DirectoryAccessRuleConfig{
		{Path: "/team/projects/", ReadUsers: []string{"alice"}},
	}

	matched := readableDirectoryAccessDescendantRules(rules, user, "/team")
	if len(matched) != 1 {
		t.Fatalf("matched descendant rules = %+v, want one", matched)
	}
	if matched[0].Path != "/team/projects" {
		t.Fatalf("descendant rule path = %q, want /team/projects", matched[0].Path)
	}
}

func TestReadableDirectoryAccessDescendantRulesRejectDirtyRuntimePath(t *testing.T) {
	user := &auth.User{Username: "alice", Role: auth.RoleUser, HomeDir: "/users/alice"}
	rules := []config.DirectoryAccessRuleConfig{
		{Path: "/team/../private", ReadUsers: []string{"alice"}},
		{Path: `\team\projects`, ReadUsers: []string{"alice"}},
	}

	if matched := readableDirectoryAccessDescendantRules(rules, user, "/team"); len(matched) != 0 {
		t.Fatalf("dirty descendant rules matched unexpectedly: %+v", matched)
	}
}

func TestReadableDirectoryAccessDescendantRulesRejectDirtyTargetPath(t *testing.T) {
	user := &auth.User{Username: "alice", Role: auth.RoleUser, HomeDir: "/users/alice"}
	rules := []config.DirectoryAccessRuleConfig{
		{Path: "/team/projects", ReadUsers: []string{"alice"}},
	}

	for _, targetPath := range []string{"/archive/../team", `/team\projects`, "/team?token=secret", "/team\x00secret"} {
		t.Run(targetPath, func(t *testing.T) {
			if matched := readableDirectoryAccessDescendantRules(rules, user, targetPath); len(matched) != 0 {
				t.Fatalf("dirty target path matched descendant rules unexpectedly: %+v", matched)
			}
		})
	}
}

func TestReadableDirectoryAccessDescendantRulesRejectSiblingPrefix(t *testing.T) {
	user := &auth.User{Username: "alice", Role: auth.RoleUser, HomeDir: "/users/alice"}
	rules := []config.DirectoryAccessRuleConfig{
		{Path: "/team2/projects", ReadUsers: []string{"alice"}},
	}

	if matched := readableDirectoryAccessDescendantRules(rules, user, "/team"); len(matched) != 0 {
		t.Fatalf("sibling-prefixed descendant rules matched unexpectedly: %+v", matched)
	}
}

func TestDirectoryAccessRuleAllowsUserNormalizesRuntimePrincipals(t *testing.T) {
	user := &auth.User{
		Username: "Alice",
		Role:     auth.RoleUser,
		Groups:   []string{" Family "},
		HomeDir:  "/users/alice",
	}

	tests := []struct {
		name string
		rule config.DirectoryAccessRuleConfig
		mode pathAccessMode
	}{
		{
			name: "read user",
			rule: config.DirectoryAccessRuleConfig{ReadUsers: []string{" ALICE "}},
			mode: pathAccessRead,
		},
		{
			name: "write group",
			rule: config.DirectoryAccessRuleConfig{WriteGroups: []string{" FAMILY "}},
			mode: pathAccessWrite,
		},
		{
			name: "read role",
			rule: config.DirectoryAccessRuleConfig{ReadRoles: []string{" USER "}},
			mode: pathAccessRead,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !directoryAccessRuleAllowsUser(tt.rule, user, tt.mode) {
				t.Fatalf("directoryAccessRuleAllowsUser(%+v, %s) = false, want true", tt.rule, tt.mode)
			}
		})
	}
}

func TestEvaluateUserPathAccessWithRulesRejectsDirtyTargetPath(t *testing.T) {
	server := &Server{authEnabled: true}
	user := &auth.User{
		Username: "alice",
		Role:     auth.RoleUser,
		Groups:   []string{"family"},
		HomeDir:  "/users/alice",
	}
	rules := []config.DirectoryAccessRuleConfig{
		{Path: "/private", ReadUsers: []string{"alice"}, WriteUsers: []string{"alice"}},
	}

	result, err := server.evaluateUserPathAccessWithRules(context.Background(), user, "/team/../private/secret.txt", rules)
	if err != nil {
		t.Fatalf("evaluateUserPathAccessWithRules() error = %v", err)
	}
	if result.Path != "/team/../private/secret.txt" {
		t.Fatalf("result path = %q, want raw invalid path", result.Path)
	}
	for _, eval := range []pathAccessEvaluation{result.Read, result.Write} {
		if eval.Allowed {
			t.Fatalf("%s evaluation allowed dirty target path: %+v", eval.Mode, eval)
		}
		if eval.Source != "invalid_path" {
			t.Fatalf("%s evaluation source = %q, want invalid_path", eval.Mode, eval.Source)
		}
		if eval.MatchedRule != nil {
			t.Fatalf("%s evaluation matched dirty path rule unexpectedly: %+v", eval.Mode, eval.MatchedRule)
		}
	}
}

func TestAuthorizeUserPathRejectsMissingUserContext(t *testing.T) {
	server := &Server{authEnabled: true}
	ctx := context.Background()

	tests := []struct {
		name string
		auth func(context.Context, string) error
	}{
		{name: "read", auth: server.authorizeUserReadPath},
		{name: "concrete read", auth: server.authorizeUserConcreteReadPath},
		{name: "write", auth: server.authorizeUserWritePath},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.auth(ctx, "/users/alice/private.txt")
			if !errors.Is(err, errPathAccessDenied) {
				t.Fatalf("%s authorization error = %v, want %v", tt.name, err, errPathAccessDenied)
			}
		})
	}
}

func TestAuthorizeUserPathRejectsDisabledUserContext(t *testing.T) {
	server := &Server{authEnabled: true}

	tests := []struct {
		name string
		auth func(context.Context, string) error
	}{
		{name: "read", auth: server.authorizeUserReadPath},
		{name: "concrete read", auth: server.authorizeUserConcreteReadPath},
		{name: "write", auth: server.authorizeUserWritePath},
	}

	for _, role := range []auth.Role{auth.RoleUser, auth.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			ctx := context.WithValue(context.Background(), auth.ContextKeyUser, &auth.User{
				Username: "alice",
				Role:     role,
				HomeDir:  "/users/alice",
				Disabled: true,
			})

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					err := tt.auth(ctx, "/users/alice/private.txt")
					if !errors.Is(err, errPathAccessDenied) {
						t.Fatalf("%s authorization error = %v, want %v", tt.name, err, errPathAccessDenied)
					}
				})
			}
		})
	}
}

func TestAuthorizeUserPathRejectsDirtyTargetBeforeHomeDirFallback(t *testing.T) {
	server := &Server{authEnabled: true}
	ctx := context.WithValue(context.Background(), auth.ContextKeyUser, &auth.User{
		Username: "alice",
		Role:     auth.RoleUser,
		HomeDir:  "/users/alice",
	})

	tests := []string{
		"/users/alice/./private.txt",
		"/users/alice/docs/../private.txt",
		"/users/alice\x00/private.txt",
	}

	for _, targetPath := range tests {
		t.Run(targetPath, func(t *testing.T) {
			err := server.authorizeUserReadPath(ctx, targetPath)
			if !errors.Is(err, errPathOutsideHomeDir) {
				t.Fatalf("authorizeUserReadPath(%q) error = %v, want %v", targetPath, err, errPathOutsideHomeDir)
			}
		})
	}

	if err := server.authorizeUserReadPath(ctx, "/users/alice/private.txt"); err != nil {
		t.Fatalf("authorizeUserReadPath(clean path) error = %v, want nil", err)
	}
}
