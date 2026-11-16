package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
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
	cmd.SetContext(context.Background())

	err := cmd.Execute()

	return out.String(), err
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func newCheckRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	runGit(t, root, "config", "commit.gpgsign", "false")

	writeRepoFile(t, root, "src/helpers.js", "")
	writeRepoFile(t, root, "projects/my-cool-app/a.js", "")
	writeRepoFile(t, root, "projects/my-cool-app/README.md", "")

	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "init")

	return root
}

func writeRepoFile(t *testing.T, root, name, contents string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
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
			wantContains: []string{"check", "git-outlet", "adopt"},
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
			name:    "root rejects positional arguments",
			args:    []string{"extra"},
			wantErr: true,
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

func TestCheckOutput(t *testing.T) {
	root := newCheckRepo(t)
	writeRepoFile(t, root, "outlet.yml", `publish:
  helpers.js: src/helpers.js
  my-cool-app:
    path: projects/my-cool-app
    exclude: ["fixtures/**"]
  docs-site:
    path: docs/site
`)
	chdir(t, root)

	out, err := runRoot(t, "check")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	want := `✔ helpers.js   gist  1 file
✔ my-cool-app  repo  2 files
⚠ my-cool-app: exclude "fixtures/**" matches nothing
✖ docs-site: path "docs/site" does not exist

1 error, 1 warning
`
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestCheckWarningsOnly(t *testing.T) {
	root := newCheckRepo(t)
	writeRepoFile(t, root, "outlet.yml", `publish:
  app:
    path: projects/my-cool-app
    exclude: ["nope/**"]
`)
	chdir(t, root)

	out, err := runRoot(t, "check")
	if err != nil {
		t.Fatalf("unexpected error: %v (output %q)", err, out)
	}

	for _, want := range []string{
		`✔ app`,
		`⚠ app: exclude "nope/**" matches nothing`,
		"0 errors, 1 warning",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func TestCheckDefaultConfigAtRoot(t *testing.T) {
	root := newCheckRepo(t)
	writeRepoFile(t, root, "outlet.yml", "publish:\n  src: src\n")
	sub := filepath.Join(root, "projects", "my-cool-app")
	chdir(t, sub)

	out, err := runRoot(t, "check")
	if err != nil {
		t.Fatalf("unexpected error: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "✔ src") {
		t.Errorf("output %q does not list the root config entry", out)
	}
}

func TestCheckExplicitConfigRelativeToCwd(t *testing.T) {
	root := newCheckRepo(t)
	writeRepoFile(t, root, "outlet.yml", "publish:\n  default: src\n")
	writeRepoFile(t, root, "configs/other.yml", "publish:\n  chosen: src/helpers.js\n")
	chdir(t, root)

	out, err := runRoot(t, "check", "--config", "configs/other.yml")
	if err != nil {
		t.Fatalf("unexpected error: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "✔ chosen") {
		t.Errorf("output %q does not list the explicit config entry", out)
	}
	if strings.Contains(out, "✔ default") {
		t.Errorf("output %q used the default config", out)
	}
}

func TestCheckConfigErrorsReported(t *testing.T) {
	root := newCheckRepo(t)
	writeRepoFile(t, root, "outlet.yml", `publish:
  app: projects/my-cool-app
extra: true
`)
	chdir(t, root)

	out, err := runRoot(t, "check")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	for _, want := range []string{`unknown key "extra" at top level`, "1 error, 0 warnings"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func TestCheckMissingConfig(t *testing.T) {
	root := newCheckRepo(t)
	chdir(t, root)

	out, err := runRoot(t, "check")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "outlet.yml") {
		t.Errorf("error %q does not name the config file (output %q)", err, out)
	}
}

func TestPublishDryRunOutput(t *testing.T) {
	root := newCheckRepo(t)
	skipWithoutSubtree(t)
	setMissingGH(t)
	writeRepoFile(t, root, "outlet.yml", `settings:
  owner: octocat
publish:
  helpers.js: src/helpers.js
  my-cool-app:
    path: projects/my-cool-app
    mode: subtree
`)
	chdir(t, root)

	out, err := runRoot(t, "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v (output %q)", err, out)
	}

	for _, want := range []string{
		"✔ helpers.js    gist  would create",
		"✔ my-cool-app   repo  would create",
		"2 would publish, 0 unchanged, 0 skipped, 0 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func setMissingGH(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'Not Found (HTTP 404)' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func skipWithoutSubtree(t *testing.T) {
	t.Helper()

	if err := exec.Command("git", "subtree").Run(); err != nil {
		t.Skip("git subtree is not installed")
	}
}

func TestPublishDryRunFlagsAndOnly(t *testing.T) {
	root := newCheckRepo(t)
	writeRepoFile(t, root, "outlet.yml", `publish:
  helpers.js: src/helpers.js
  my-cool-app: projects/my-cool-app
`)
	chdir(t, root)

	out, err := runRoot(t, "--dry-run", "--adopt", "-m", "publish", "--only", "helpers.js")
	if err != nil {
		t.Fatalf("unexpected error: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "✔ helpers.js   gist  would create") {
		t.Errorf("output %q does not contain the selected entry", out)
	}
	if strings.Contains(out, "my-cool-app") {
		t.Errorf("output %q includes an unselected entry", out)
	}
}

func TestPublishOnlyUnknown(t *testing.T) {
	root := newCheckRepo(t)
	writeRepoFile(t, root, "outlet.yml", `publish:
  helpers.js: src/helpers.js
  my-cool-app: projects/my-cool-app
`)
	chdir(t, root)

	out, err := runRoot(t, "--dry-run", "--only", "nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{`unknown entry "nope"`, "helpers.js, my-cool-app"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if out != "" {
		t.Errorf("output = %q, want none", out)
	}
}

func TestPublishExplicitConfigRelativeToCwd(t *testing.T) {
	root := newCheckRepo(t)
	writeRepoFile(t, root, "outlet.yml", "publish:\n  default: src\n")
	writeRepoFile(t, root, "configs/other.yml", "publish:\n  chosen: src/helpers.js\n")
	chdir(t, root)

	out, err := runRoot(t, "--dry-run", "--config", "configs/other.yml")
	if err != nil {
		t.Fatalf("unexpected error: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "✔ chosen") {
		t.Errorf("output %q does not list the explicit config entry", out)
	}
	if strings.Contains(out, "✔ default") {
		t.Errorf("output %q used the default config", out)
	}
}

func TestPublishConfigErrorsReported(t *testing.T) {
	root := newCheckRepo(t)
	writeRepoFile(t, root, "outlet.yml", `publish:
  app: projects/my-cool-app
extra: true
`)
	chdir(t, root)

	out, err := runRoot(t, "--dry-run")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"✖", `unknown key "extra" at top level`, "config invalid"} {
		got := out
		if want == "config invalid" {
			got = err.Error()
		}
		if !strings.Contains(got, want) {
			t.Errorf("output %q / error %q does not contain %q", out, err, want)
		}
	}
}

func TestPublishMissingConfig(t *testing.T) {
	root := newCheckRepo(t)
	chdir(t, root)

	out, err := runRoot(t, "--dry-run")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "outlet.yml") {
		t.Errorf("error %q does not name the config file (output %q)", err, out)
	}
}

func TestPublishMissingGh(t *testing.T) {
	root := newCheckRepo(t)
	writeRepoFile(t, root, "outlet.yml", "publish:\n  helpers.js: src/helpers.js\n")
	chdir(t, root)
	t.Setenv("PATH", "")

	out, err := runRoot(t)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("error %q does not mention gh auth login (output %q)", err, out)
	}
}
