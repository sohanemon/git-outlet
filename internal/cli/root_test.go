package cli

import (
	"bytes"
	"os"
	"path/filepath"
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

func writeOutletConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "outlet.yml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func TestCheckCmd(t *testing.T) {
	t.Run("valid file", func(t *testing.T) {
		path := writeOutletConfig(t, `publish:
  one: src/one
  two:
    path: src/two
`)

		out, err := runRoot(t, "check", "--config", path)
		if err != nil {
			t.Fatalf("unexpected error: %v (output %q)", err, out)
		}
		if !strings.Contains(out, "ok: 2 entries") {
			t.Errorf("output %q does not contain %q", out, "ok: 2 entries")
		}
	})

	t.Run("invalid file lists every problem", func(t *testing.T) {
		path := writeOutletConfig(t, `settings:
  mode: zip
publish:
  app:
    path: src/app
    exlude: ["*.js"]
  app: src/other
`)

		out, err := runRoot(t, "check", "--config", path)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		for _, want := range []string{
			`invalid mode "zip"`,
			`unknown key "exlude"`,
			`duplicate publish name "app"`,
			path,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output %q does not contain %q", out, want)
			}
		}
	})

	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent.yml")

		_, err := runRoot(t, "check", "--config", path)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "absent.yml") {
			t.Errorf("error %q does not name the file", err)
		}
	})
}
