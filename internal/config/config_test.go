package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "outlet.yml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func mustLoad(t *testing.T, contents string) *Config {
	t.Helper()

	cfg, err := Load(writeConfig(t, contents))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	return cfg
}

func mustProblems(t *testing.T, contents string) *Error {
	t.Helper()

	_, err := Load(writeConfig(t, contents))
	if err == nil {
		t.Fatal("Load() error = nil, want config errors")
	}

	var cfgErr *Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("Load() error = %v, want *config.Error", err)
	}

	return cfgErr
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want *Config
	}{
		{
			name: "full form",
			yaml: `settings:
  owner: sohanemon
  mode: subtree
  branch: dev
  message: "Ship it"
  secret_scan: false
publish:
  helpers.js: src/utils/helpers.js
  my-cool-app:
    path: projects/my-cool-app
    description: "Demo app"
    include: ["src/**", "README.md"]
    exclude: ["*.test.js"]
    message: "Update my-cool-app"
    mode: copy
`,
			want: &Config{
				Settings: Settings{
					Owner:      "sohanemon",
					Mode:       "subtree",
					Branch:     "dev",
					Message:    "Ship it",
					SecretScan: false,
				},
				Entries: []Entry{
					{Name: "helpers.js", Path: "src/utils/helpers.js"},
					{
						Name:        "my-cool-app",
						Path:        "projects/my-cool-app",
						Description: "Demo app",
						Include:     []string{"src/**", "README.md"},
						Exclude:     []string{"*.test.js"},
						Message:     "Update my-cool-app",
						Mode:        "copy",
					},
				},
			},
		},
		{
			name: "defaults keep entry mode and message empty",
			yaml: `publish:
  plain: src/plain
  mapped:
    path: src/mapped
`,
			want: &Config{
				Settings: Settings{
					Mode:       "copy",
					Branch:     "main",
					Message:    "Update from private repo",
					SecretScan: true,
				},
				Entries: []Entry{
					{Name: "plain", Path: "src/plain"},
					{Name: "mapped", Path: "src/mapped"},
				},
			},
		},
		{
			name: "shorthand and mapping with only path decode alike",
			yaml: `publish:
  mixed:
    path: src/mixed
`,
			want: &Config{
				Settings: Settings{
					Mode:       "copy",
					Branch:     "main",
					Message:    "Update from private repo",
					SecretScan: true,
				},
				Entries: []Entry{
					{Name: "mixed", Path: "src/mixed"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustLoad(t, tt.yaml)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Load() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestLoadShorthand(t *testing.T) {
	shorthand := mustLoad(t, "publish:\n  same: src/same\n")
	full := mustLoad(t, "publish:\n  same:\n    path: src/same\n")

	if !reflect.DeepEqual(shorthand.Entries, full.Entries) {
		t.Errorf("shorthand entries = %#v, full entries = %#v", shorthand.Entries, full.Entries)
	}
}

func TestLoadKeepsEntryOrder(t *testing.T) {
	cfg := mustLoad(t, `publish:
  zebra: src/zebra
  alpha: src/alpha
  middle: src/middle
`)

	var names []string
	for _, entry := range cfg.Entries {
		names = append(names, entry.Name)
	}

	want := []string{"zebra", "alpha", "middle"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("entry names = %v, want %v", names, want)
	}
}

func TestLoadStrictErrors(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		wantLine int
		wantMsg  string
	}{
		{
			name: "unknown top-level key",
			yaml: `publish:
  app: src/app
extra: true
`,
			wantLine: 3,
			wantMsg:  `unknown key "extra" at top level`,
		},
		{
			name: "unknown settings key",
			yaml: `settings:
  secret_scan: true
  exlude: ["*.js"]
`,
			wantLine: 3,
			wantMsg:  `unknown key "exlude" in settings`,
		},
		{
			name: "unknown key inside entry",
			yaml: `publish:
  app:
    path: src/app
    exlude: ["*.js"]
`,
			wantLine: 4,
			wantMsg:  `entry "app": unknown key "exlude"`,
		},
		{
			name: "include as string",
			yaml: `publish:
  app:
    path: src/app
    include: "src/**"
`,
			wantLine: 4,
			wantMsg:  "include: expected list of strings, got string",
		},
		{
			name: "secret_scan as string",
			yaml: `settings:
  secret_scan: "true"
`,
			wantLine: 2,
			wantMsg:  "secret_scan: expected bool, got string",
		},
		{
			name: "duplicate publish name",
			yaml: `publish:
  dup: src/one
  dup: src/two
`,
			wantLine: 3,
			wantMsg:  `duplicate publish name "dup"`,
		},
		{
			name: "entry without path",
			yaml: `publish:
  app:
    description: demo
`,
			wantLine: 2,
			wantMsg:  `entry "app": missing path`,
		},
		{
			name: "invalid settings mode",
			yaml: `settings:
  mode: zip
`,
			wantLine: 2,
			wantMsg:  `invalid mode "zip"`,
		},
		{
			name: "invalid entry mode",
			yaml: `publish:
  app:
    path: src/app
    mode: symlink
`,
			wantLine: 4,
			wantMsg:  `invalid mode "symlink"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgErr := mustProblems(t, tt.yaml)

			if len(cfgErr.Problems) != 1 {
				t.Fatalf("problems = %v, want exactly 1", cfgErr.Problems)
			}

			problem := cfgErr.Problems[0]
			if problem.Line != tt.wantLine {
				t.Errorf("problem line = %d, want %d", problem.Line, tt.wantLine)
			}
			if !strings.Contains(problem.Message, tt.wantMsg) {
				t.Errorf("problem message = %q, want it to contain %q", problem.Message, tt.wantMsg)
			}
		})
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	cfgErr := mustProblems(t, `settings:
  mode: zip
publish:
  app:
    path: src/app
    exlude: ["*.js"]
  app: src/other
`)

	if len(cfgErr.Problems) != 3 {
		t.Fatalf("problems = %v, want 3", cfgErr.Problems)
	}

	text := cfgErr.Error()
	for _, want := range []string{
		`invalid mode "zip"`,
		`unknown key "exlude"`,
		`duplicate publish name "app"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Error() = %q, want it to contain %q", text, want)
		}
	}
	if !strings.Contains(text, cfgErr.Path+":2:") {
		t.Errorf("Error() = %q, want it to name the file and line", text)
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yml")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load() error = %v, want fs.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), "absent.yml") {
		t.Errorf("Load() error = %q, want it to name the file", err)
	}
}
