package api

import "testing"

func TestCleanRuntimePathRulePath(t *testing.T) {
	tests := []struct {
		name     string
		rulePath string
		want     string
	}{
		{name: "root", rulePath: "/", want: "/"},
		{name: "trims whitespace", rulePath: " /team/ ", want: "/team"},
		{name: "collapses duplicate slashes", rulePath: "/shared//uploads", want: "/shared/uploads"},
		{name: "keeps spaces in segment", rulePath: "/family photos/raw", want: "/family photos/raw"},
		{name: "rejects empty", rulePath: "", want: ""},
		{name: "rejects relative", rulePath: "team", want: ""},
		{name: "rejects backslash", rulePath: `\team`, want: ""},
		{name: "rejects query", rulePath: "/team?token=secret", want: ""},
		{name: "rejects fragment", rulePath: "/team#private", want: ""},
		{name: "rejects dot segment", rulePath: "/team/./private", want: ""},
		{name: "rejects dot dot segment", rulePath: "/team/../private", want: ""},
		{name: "rejects nul", rulePath: "/team\x00private", want: ""},
		{name: "rejects delete", rulePath: "/team\x7fprivate", want: ""},
		{name: "rejects unicode control", rulePath: "/team\u0081private", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanRuntimePathRulePath(tt.rulePath); got != tt.want {
				t.Fatalf("cleanRuntimePathRulePath(%q) = %q, want %q", tt.rulePath, got, tt.want)
			}
		})
	}
}
