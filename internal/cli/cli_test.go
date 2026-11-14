package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no args prints usage", nil, ExitUsage, "", "Usage: storied"},
		{"help", []string{"help"}, ExitOK, "Usage: storied", ""},
		{"version", []string{"version"}, ExitOK, "storied dev", ""},
		{"version flag", []string{"--version"}, ExitOK, "storied dev", ""},
		{"unknown command", []string{"nope"}, ExitUsage, "", `unknown command "nope"`},
		{"command outside a repository", []string{"pack", "--repo", t.TempDir()}, ExitError, "", "storied pack: git rev-parse --show-toplevel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := Run(tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("exit code = %d, want %d", got, tt.wantCode)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestEveryCommandHasSummary(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range commands() {
		if c.summary == "" || c.run == nil {
			t.Errorf("command %q is missing a summary or run function", c.name)
		}
		if seen[c.name] {
			t.Errorf("command %q is registered twice", c.name)
		}
		seen[c.name] = true
	}
}
