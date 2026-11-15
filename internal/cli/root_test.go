package cli

import (
	"bytes"
	"strings"
	"testing"
)

func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), err
}

func TestRootCmd(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantErr      bool
		wantContains []string
	}{
		{
			name:         "help lists check subcommand",
			args:         []string{"--help"},
			wantContains: []string{"check", "git-outlet"},
		},
		{
			name:         "check runs without error",
			args:         []string{"check"},
			wantContains: []string{"not implemented yet"},
		},
		{
			name:    "check rejects positional arguments",
			args:    []string{"check", "extra"},
			wantErr: true,
		},
		{
			name:    "unknown flag returns error",
			args:    []string{"--nope"},
			wantErr: true,
		},
		{
			name:         "root flags parse",
			args:         []string{"--dry-run", "-m", "publish", "--only", "cli"},
			wantContains: []string{"not implemented yet"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runRoot(t, tt.args...)

			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil (output %q)", out)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v (output %q)", err, out)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("output %q does not contain %q", out, want)
				}
			}
		})
	}
}
