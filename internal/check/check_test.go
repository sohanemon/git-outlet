package check

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sohanemon/git-outlet/internal/config"
	"github.com/sohanemon/git-outlet/internal/scan"
)

type fakeScanner struct {
	versions int
	findings []scan.Finding
}

func (f *fakeScanner) Version(context.Context) (string, error) {
	f.versions++
	return "8.19", nil
}

func (f *fakeScanner) File(context.Context, string, string) ([]scan.Finding, error) {
	return f.findings, nil
}

func (f *fakeScanner) Files(context.Context, string, []string) ([]scan.Finding, error) {
	return f.findings, nil
}

func (f *fakeScanner) History(context.Context, string, string) ([]scan.Finding, error) {
	return f.findings, nil
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, root, name, contents string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")

	writeFile(t, dir, "file.txt", "file")
	writeFile(t, dir, "app/a.js", "a")
	writeFile(t, dir, "app/b.test.js", "b")
	writeFile(t, dir, "app/fixtures/x.json", "{}")
	writeFile(t, dir, "links/target.txt", "target")
	if err := os.Symlink("target.txt", filepath.Join(dir, "links", "to-target")); err != nil {
		t.Fatal(err)
	}

	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")

	writeFile(t, dir, "untracked.txt", "not tracked")

	return dir
}

func configWith(settings config.Settings, entries ...config.Entry) *config.Config {
	if settings.Mode == "" {
		settings.Mode = "copy"
	}

	return &config.Config{Settings: settings, Entries: entries}
}

func run(t *testing.T, root string, cfg *config.Config) Report {
	t.Helper()

	report, err := Run(context.Background(), cfg, Options{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	return report
}

func messages(report Report, severity Severity) []string {
	var got []string
	for _, problem := range report.Problems {
		if problem.Severity == severity {
			got = append(got, problem.Message)
		}
	}

	return got
}

func assertProblems(t *testing.T, report Report, severity Severity, wants []string) {
	t.Helper()

	got := messages(report, severity)
	if len(got) != len(wants) {
		t.Errorf("%s problems = %v, want %d", severity, got, len(wants))
	}
	for _, want := range wants {
		found := false
		for _, msg := range got {
			if strings.Contains(msg, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s problems = %v, want one containing %q", severity, got, want)
		}
	}
}

func TestRunValid(t *testing.T) {
	root := newRepo(t)
	cfg := configWith(config.Settings{Mode: "copy"},
		config.Entry{Name: "file.txt", Path: "file.txt"},
		config.Entry{Name: "app", Path: "app", Include: []string{"*.js"}, Exclude: []string{"*.test.js"}},
	)

	report := run(t, root, cfg)

	if len(report.Problems) != 0 {
		t.Fatalf("problems = %v, want none", report.Problems)
	}
	if len(report.Entries) != 2 {
		t.Fatalf("entries = %v, want 2", report.Entries)
	}

	file := report.Entries[0]
	if file.Kind != KindGist || file.Files != 1 || file.Excluded != 0 || file.Failed {
		t.Errorf("file entry = %+v, want gist with 1 file", file)
	}

	folder := report.Entries[1]
	if folder.Kind != KindRepo || folder.Files != 1 || folder.Excluded != 1 || folder.Failed {
		t.Errorf("folder entry = %+v, want repo with 1 file and 1 excluded", folder)
	}
}

func TestSecretScanDisabledInSettings(t *testing.T) {
	root := newRepo(t)
	scanner := &fakeScanner{}
	cfg := configWith(config.Settings{Mode: "copy", SecretScan: false},
		config.Entry{Name: "file", Path: "file.txt"},
	)
	if _, err := Run(context.Background(), cfg, Options{Root: root, Scan: scanner}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if scanner.versions != 0 {
		t.Errorf("Version() calls = %d, want none", scanner.versions)
	}
}

func TestCheckReportsSuspiciousNamesAndScanFindings(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, ".env", "fake")
	runGit(t, root, "add", ".env")
	runGit(t, root, "commit", "-q", "-m", "environment")
	scanner := &fakeScanner{findings: []scan.Finding{{RuleID: "fake-rule", File: ".env", Line: 1, Commit: "abcdef123456"}}}
	cfg := configWith(config.Settings{Mode: "copy", SecretScan: true},
		config.Entry{Name: "environment", Path: ".env"},
	)
	report, err := Run(context.Background(), cfg, Options{Root: root, Scan: scanner})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if report.Warnings() != 1 || report.Errors() != 1 {
		t.Fatalf("report counts = %d errors, %d warnings", report.Errors(), report.Warnings())
	}
	assertProblems(t, report, SeverityWarning, []string{"suspicious file selected: .env"})
	assertProblems(t, report, SeverityError, []string{"fake-rule  .env:1  abcdef123456"})
}

func TestRunProblems(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Config
		setup    func(t *testing.T, root string)
		wantErrs []string
		wantWarn []string
	}{
		{
			name:     "missing path",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "docs-site", Path: "docs/site"}),
			wantErrs: []string{`path "docs/site" does not exist`},
		},
		{
			name:     "path escapes root",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "app", Path: "../outside"}),
			wantErrs: []string{`escapes the repository root`},
		},
		{
			name:     "absolute path",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "app", Path: filepath.Join(filepath.VolumeName(os.TempDir())+string(os.PathSeparator), "tmp")}),
			wantErrs: []string{`is absolute`},
		},
		{
			name: "symlink escapes root",
			setup: func(t *testing.T, root string) {
				outside := t.TempDir()
				if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
					t.Fatal(err)
				}
			},
			cfg:      configWith(config.Settings{}, config.Entry{Name: "outside", Path: "outside-link"}),
			wantErrs: []string{`resolves outside the repository root`},
		},
		{
			name: "folder without tracked files",
			setup: func(t *testing.T, root string) {
				if err := os.Mkdir(filepath.Join(root, "empty"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			cfg:      configWith(config.Settings{}, config.Entry{Name: "empty", Path: "empty"}),
			wantErrs: []string{`has no tracked files`},
		},
		{
			name:     "untracked file",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "untracked.txt", Path: "untracked.txt"}),
			wantErrs: []string{`is not tracked by git`},
		},
		{
			name:     "selection empty",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "app", Path: "app", Include: []string{"nope/**"}}),
			wantErrs: []string{`no files selected`},
			wantWarn: []string{`include "nope/**" matches nothing`},
		},
		{
			name:     "invalid pattern",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "app", Path: "app", Include: []string{"[bad"}}),
			wantErrs: []string{`[bad`},
		},
		{
			name:     "invalid repo name",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "bad name", Path: "app"}),
			wantErrs: []string{`is not a valid GitHub repository name`},
		},
		{
			name:     "invalid gist name",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "dir/file.txt", Path: "file.txt"}),
			wantErrs: []string{`is not a valid Gist file name`},
		},
		{
			name:     "explicit subtree on file",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "file.txt", Path: "file.txt", Mode: "subtree"}),
			wantErrs: []string{`not allowed on a file entry`},
		},
		{
			name: "inherited subtree on file is allowed",
			cfg:  configWith(config.Settings{Mode: "subtree"}, config.Entry{Name: "file.txt", Path: "file.txt"}),
		},
		{
			name:     "subtree with include",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "app", Path: "app", Mode: "subtree", Include: []string{"*.js"}}),
			wantErrs: []string{`cannot use include or exclude`},
			wantWarn: []string{`publishes the full history of app, including deleted files and old versions`},
		},
		{
			name: "unmatched include and exclude",
			cfg: configWith(config.Settings{},
				config.Entry{Name: "app", Path: "app", Include: []string{"*.js", "docs/**"}, Exclude: []string{"nope/**"}},
			),
			wantWarn: []string{`include "docs/**" matches nothing`, `exclude "nope/**" matches nothing`},
		},
		{
			name: "include and exclude on file",
			cfg: configWith(config.Settings{},
				config.Entry{Name: "file.txt", Path: "file.txt", Include: []string{"*.txt"}, Exclude: []string{"*.md"}},
			),
			wantWarn: []string{`include is ignored for file entries`, `exclude is ignored for file entries`},
		},
		{
			name:     "message on subtree entry",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "app", Path: "app", Mode: "subtree", Message: "hi"}),
			wantWarn: []string{`message is ignored for subtree entries`, `publishes the full history of app, including deleted files and old versions`},
		},
		{
			name:     "skipped symlink in folder",
			cfg:      configWith(config.Settings{}, config.Entry{Name: "links", Path: "links"}),
			wantWarn: []string{`skipped 1 symlink(s) or missing file(s)`},
		},
		{
			name: "several problems together",
			cfg: configWith(config.Settings{},
				config.Entry{Name: "docs-site", Path: "docs/site"},
				config.Entry{Name: "untracked.txt", Path: "untracked.txt"},
				config.Entry{Name: "app", Path: "app", Exclude: []string{"nope/**"}},
			),
			wantErrs: []string{`does not exist`, `is not tracked by git`},
			wantWarn: []string{`exclude "nope/**" matches nothing`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepo(t)
			if tt.setup != nil {
				tt.setup(t, root)
			}

			report := run(t, root, tt.cfg)

			if got, want := report.Errors(), len(tt.wantErrs); got != want {
				t.Errorf("Errors() = %d, want %d (%v)", got, want, messages(report, SeverityError))
			}
			if got, want := report.Warnings(), len(tt.wantWarn); got != want {
				t.Errorf("Warnings() = %d, want %d (%v)", got, want, messages(report, SeverityWarning))
			}
			assertProblems(t, report, SeverityError, tt.wantErrs)
			assertProblems(t, report, SeverityWarning, tt.wantWarn)
		})
	}
}
