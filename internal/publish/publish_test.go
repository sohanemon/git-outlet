package publish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/sohanemon/git-outlet/internal/config"
	"github.com/sohanemon/git-outlet/internal/gist"
	"github.com/sohanemon/git-outlet/internal/gitx"
	"github.com/sohanemon/git-outlet/internal/repo"
	"github.com/sohanemon/git-outlet/internal/scan"
	"github.com/sohanemon/git-outlet/internal/state"
)

type fakeScanner struct {
	versions     int
	versionErr   error
	fileFindings map[string][]scan.Finding
	fileCalls    []string
	filesCalls   [][]string
	historyCalls []string
}

func (f *fakeScanner) Version(context.Context) (string, error) {
	f.versions++
	return "8.19", f.versionErr
}

func (f *fakeScanner) File(_ context.Context, _, name string) ([]scan.Finding, error) {
	f.fileCalls = append(f.fileCalls, name)
	return f.fileFindings[name], nil
}

func (f *fakeScanner) Files(_ context.Context, _ string, files []string) ([]scan.Finding, error) {
	f.filesCalls = append(f.filesCalls, append([]string(nil), files...))
	return nil, nil
}

func (f *fakeScanner) History(_ context.Context, _, path string) ([]scan.Finding, error) {
	f.historyCalls = append(f.historyCalls, path)
	return nil, nil
}

type createCall struct {
	Name        string
	Description string
	Content     string
}

type updateCall struct {
	ID          string
	Name        string
	Description string
	Content     string
}

type fakeGister struct {
	calls        int
	creates      []createCall
	updates      []updateCall
	createErrFor map[string]error
	updateErrFor map[string]error
}

func (f *fakeGister) Create(_ context.Context, name, description, content string) (string, string, error) {
	f.calls++
	f.creates = append(f.creates, createCall{Name: name, Description: description, Content: content})
	if err := f.createErrFor[name]; err != nil {
		return "", "", err
	}
	id := fmt.Sprintf("gist%d", len(f.creates))

	return id, "https://gist.github.com/" + id, nil
}

func (f *fakeGister) Update(_ context.Context, id, name, description, content string) (string, error) {
	f.calls++
	f.updates = append(f.updates, updateCall{ID: id, Name: name, Description: description, Content: content})
	if err := f.updateErrFor[name]; err != nil {
		return "", err
	}

	return "https://gist.github.com/" + id, nil
}

type fakeRepositor struct {
	owner      string
	ownerErr   error
	ownerCalls int

	info       repo.Info
	inspectErr error

	createErr error
	creates   int
	created   []string

	syncResult repo.Result
	syncErr    error
	syncErrFor map[string]error
	syncs      int
	lastSync   repo.SyncOptions
	syncCalls  []repo.SyncOptions

	previewResult repo.Result
	previewErr    error
	previews      int

	splitResult   string
	splitErr      error
	splits        int
	lastSplitRoot string
	lastSplitPath string

	pushErr  error
	pushes   int
	lastPush repo.SubtreeOptions
}

func newFakeRepositor() *fakeRepositor {
	return &fakeRepositor{
		owner: "octocat",
		syncResult: repo.Result{
			Commit: "sha1",
			URL:    "https://github.com/octocat/my-cool-app",
			Added:  2,
		},
	}
}

func (f *fakeRepositor) Owner(_ context.Context, settingsOwner string) (string, error) {
	f.ownerCalls++
	if f.ownerErr != nil {
		return "", f.ownerErr
	}
	if f.owner != "" {
		return f.owner, nil
	}
	if settingsOwner != "" {
		return settingsOwner, nil
	}

	return "octocat", nil
}

func (f *fakeRepositor) Inspect(_ context.Context, owner, name string) (repo.Info, error) {
	return f.info, f.inspectErr
}

func (f *fakeRepositor) Create(_ context.Context, owner, name, description string) error {
	f.creates++
	f.created = append(f.created, owner+"/"+name)

	return f.createErr
}

func (f *fakeRepositor) Sync(_ context.Context, opts repo.SyncOptions) (repo.Result, error) {
	f.syncs++
	f.lastSync = opts
	f.syncCalls = append(f.syncCalls, opts)
	if err := f.syncErrFor[opts.Name]; err != nil {
		return repo.Result{}, err
	}

	return f.syncResult, f.syncErr
}

func (f *fakeRepositor) Preview(_ context.Context, opts repo.SyncOptions) (repo.Result, error) {
	f.previews++
	f.lastSync = opts

	return f.previewResult, f.previewErr
}

func (f *fakeRepositor) SubtreeSplit(_ context.Context, root, path string) (string, error) {
	f.splits++
	f.lastSplitRoot = root
	f.lastSplitPath = path

	return f.splitResult, f.splitErr
}

func (f *fakeRepositor) SubtreePush(_ context.Context, opts repo.SubtreeOptions) error {
	f.pushes++
	f.lastPush = opts

	return f.pushErr
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

	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	runGit(t, root, "config", "commit.gpgsign", "false")

	writeFile(t, root, "src/helpers.js", "one")
	writeFile(t, root, "projects/my-cool-app/a.js", "a")
	writeFile(t, root, "projects/my-cool-app/README.md", "readme")

	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "init")

	return root
}

func sampleConfig() *config.Config {
	return &config.Config{
		Settings: config.Settings{Mode: "copy"},
		Entries: []config.Entry{
			{Name: "helpers.js", Path: "src/helpers.js"},
		},
	}
}

func runPublish(t *testing.T, root string, cfg *config.Config, g *fakeGister, change func(*Options)) (string, error) {
	t.Helper()

	var out bytes.Buffer
	opts := Options{Config: cfg, Root: root, Out: &out, Gist: g}
	if change != nil {
		change(&opts)
	}

	err := runLegacy(context.Background(), opts)

	return out.String(), err
}

func statePath(root string) string {
	return filepath.Join(root, stateFile)
}

func TestPublishCreateUpdateUnchanged(t *testing.T) {
	root := newRepo(t)
	cfg := sampleConfig()

	create := &fakeGister{}
	out, err := runPublish(t, root, cfg, create, nil)
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}

	if len(create.creates) != 1 {
		t.Fatalf("creates = %v, want one", create.creates)
	}
	if got := create.creates[0]; got.Name != "helpers.js" || got.Description != "helpers.js" || got.Content != "one" {
		t.Errorf("create = %+v", got)
	}

	want := "✔ helpers.js   gist  created    https://gist.github.com/gist1\n" +
		"\n1 published, 0 unchanged, 0 skipped, 0 failed\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}

	st, err := state.Load(statePath(root))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	entry, ok := st.Get("helpers.js")
	if !ok || entry.GistID != "gist1" || entry.Hash == "" {
		t.Errorf("state entry = %+v, ok = %v", entry, ok)
	}

	writeFile(t, root, "src/helpers.js", "two")

	update := &fakeGister{}
	out, err = runPublish(t, root, cfg, update, nil)
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}
	if len(update.updates) != 1 {
		t.Fatalf("updates = %v, want one", update.updates)
	}
	if got := update.updates[0]; got.ID != "gist1" || got.Content != "two" || got.Description != "helpers.js" {
		t.Errorf("update = %+v", got)
	}
	for _, want := range []string{
		"✔ helpers.js   gist  updated    https://gist.github.com/gist1",
		"1 published, 0 unchanged, 0 skipped, 0 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}

	unchanged := &fakeGister{}
	out, err = runPublish(t, root, cfg, unchanged, nil)
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}
	if unchanged.calls != 0 {
		t.Errorf("calls = %d, want none", unchanged.calls)
	}
	for _, want := range []string{
		"✔ helpers.js   gist  unchanged",
		"0 published, 1 unchanged, 0 skipped, 0 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func TestPublishFailureDoesNotStopLaterEntries(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "src/bad.js", "bad")
	writeFile(t, root, "src/notes.md", "notes")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "more")

	cfg := &config.Config{
		Settings: config.Settings{Mode: "copy"},
		Entries: []config.Entry{
			{Name: "bad.js", Path: "src/bad.js"},
			{Name: "helpers.js", Path: "src/helpers.js"},
			{Name: "notes.md", Path: "src/notes.md"},
		},
	}

	fake := &fakeGister{createErrFor: map[string]error{"bad.js": errors.New("boom")}}
	out, err := runPublish(t, root, cfg, fake, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want the failed entry to surface")
	}
	if !strings.Contains(err.Error(), "bad.js") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("Run() error = %v, want it to name bad.js and boom", err)
	}

	if len(fake.creates) != 3 {
		t.Errorf("creates = %d, want all three entries attempted", len(fake.creates))
	}
	for _, want := range []string{
		"✖ bad.js       gist  failed: boom",
		"✔ helpers.js   gist  created",
		"✔ notes.md     gist  created",
		"2 published, 0 unchanged, 0 skipped, 1 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}

	st, err := state.Load(statePath(root))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if _, ok := st.Get("bad.js"); ok {
		t.Error("state records the failed entry")
	}
	for _, name := range []string{"helpers.js", "notes.md"} {
		if _, ok := st.Get(name); !ok {
			t.Errorf("state does not record %s", name)
		}
	}
}

func TestPublishOnly(t *testing.T) {
	root := newRepo(t)
	cfg := sampleConfig()

	fake := &fakeGister{}
	out, err := runPublish(t, root, cfg, fake, func(opts *Options) { opts.Only = "helpers.js" })
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}
	if len(fake.creates) != 1 || fake.creates[0].Name != "helpers.js" {
		t.Errorf("creates = %v, want only helpers.js", fake.creates)
	}
	if strings.Contains(out, "my-cool-app") {
		t.Errorf("output %q includes an unselected entry", out)
	}
	if !strings.Contains(out, "1 published, 0 unchanged, 0 skipped, 0 failed") {
		t.Errorf("output %q has the wrong summary", out)
	}
}

func TestPublishOnlyUnknown(t *testing.T) {
	root := newRepo(t)

	fake := &fakeGister{}
	out, err := runPublish(t, root, sampleConfig(), fake, func(opts *Options) { opts.Only = "nope" })
	if err == nil {
		t.Fatal("Run() error = nil, want an unknown entry error")
	}
	if !strings.Contains(err.Error(), `unknown entry "nope"`) || !strings.Contains(err.Error(), "helpers.js") {
		t.Errorf("error %q does not list the valid entries", err)
	}
	if fake.calls != 0 {
		t.Errorf("calls = %d, want none", fake.calls)
	}
	if out != "" {
		t.Errorf("output = %q, want none", out)
	}
}

func TestPublishCheckErrorsBlock(t *testing.T) {
	root := newRepo(t)
	cfg := &config.Config{
		Settings: config.Settings{Mode: "copy"},
		Entries: []config.Entry{
			{Name: "docs-site", Path: "docs/site"},
			{Name: "helpers.js", Path: "src/helpers.js"},
		},
	}

	fake := &fakeGister{}
	out, err := runPublish(t, root, cfg, fake, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want a blocked publish")
	}
	if !strings.Contains(err.Error(), "publish stopped") {
		t.Errorf("Run() error = %v, want a publish stopped error", err)
	}
	if !strings.Contains(out, `✖ docs-site: path "docs/site" does not exist`) {
		t.Errorf("output %q does not report the check error", out)
	}
	if fake.calls != 0 {
		t.Errorf("calls = %d, want none", fake.calls)
	}
	if _, err := os.Stat(statePath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state file exists after a blocked publish: %v", err)
	}
}

func TestPublishCheckWarningsDoNotBlock(t *testing.T) {
	root := newRepo(t)
	cfg := &config.Config{
		Settings: config.Settings{Mode: "copy"},
		Entries: []config.Entry{
			{Name: "helpers.js", Path: "src/helpers.js", Include: []string{"*.js"}},
		},
	}

	fake := &fakeGister{}
	out, err := runPublish(t, root, cfg, fake, nil)
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}
	if len(fake.creates) != 1 {
		t.Errorf("creates = %v, want one", fake.creates)
	}
	for _, want := range []string{
		"⚠ helpers.js: include is ignored for file entries",
		"1 published, 0 unchanged, 0 skipped, 0 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func TestPublishDryRun(t *testing.T) {
	t.Run("no state", func(t *testing.T) {
		root := newRepo(t)

		fake := &fakeGister{}
		out, err := runPublish(t, root, sampleConfig(), fake, func(opts *Options) { opts.DryRun = true })
		if err != nil {
			t.Fatalf("Run() error: %v (output %q)", err, out)
		}
		if fake.calls != 0 {
			t.Errorf("calls = %d, want none", fake.calls)
		}

		want := "✔ helpers.js   gist  would create\n" +
			"\n1 would publish, 0 unchanged, 0 skipped, 0 failed\n"
		if out != want {
			t.Errorf("output =\n%s\nwant\n%s", out, want)
		}
		if _, err := os.Stat(statePath(root)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("state file exists after a dry run: %v", err)
		}
	})

	t.Run("stale state", func(t *testing.T) {
		root := newRepo(t)
		if err := state.Save(statePath(root), state.State{
			"helpers.js": {GistID: "abc123", Hash: "stale"},
		}); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(statePath(root))
		if err != nil {
			t.Fatal(err)
		}

		fake := &fakeGister{}
		out, err := runPublish(t, root, sampleConfig(), fake, func(opts *Options) { opts.DryRun = true })
		if err != nil {
			t.Fatalf("Run() error: %v (output %q)", err, out)
		}
		if fake.calls != 0 {
			t.Errorf("calls = %d, want none", fake.calls)
		}
		for _, want := range []string{
			"✔ helpers.js   gist  would update    https://gist.github.com/abc123",
			"1 would publish, 0 unchanged, 0 skipped, 0 failed",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output %q does not contain %q", out, want)
			}
		}

		after, err := os.ReadFile(statePath(root))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("dry run changed the state file:\n%s\n%s", before, after)
		}
	})

	t.Run("unchanged state", func(t *testing.T) {
		root := newRepo(t)
		if err := state.Save(statePath(root), state.State{
			"helpers.js": {GistID: "abc123", Hash: hashContent("helpers.js", "helpers.js", "one")},
		}); err != nil {
			t.Fatal(err)
		}

		fake := &fakeGister{}
		out, err := runPublish(t, root, sampleConfig(), fake, func(opts *Options) { opts.DryRun = true })
		if err != nil {
			t.Fatalf("Run() error: %v (output %q)", err, out)
		}
		for _, want := range []string{
			"✔ helpers.js   gist  unchanged",
			"0 would publish, 1 unchanged, 0 skipped, 0 failed",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output %q does not contain %q", out, want)
			}
		}
	})
}

func TestPublishNotFoundHint(t *testing.T) {
	root := newRepo(t)
	if err := state.Save(statePath(root), state.State{
		"helpers.js": {GistID: "gone", Hash: "stale"},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Settings: config.Settings{Mode: "copy"},
		Entries:  []config.Entry{{Name: "helpers.js", Path: "src/helpers.js"}},
	}
	fake := &fakeGister{updateErrFor: map[string]error{"helpers.js": &gist.NotFoundError{ID: "gone"}}}

	out, err := runPublish(t, root, cfg, fake, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want a failed update")
	}
	for _, want := range []string{
		`failed: gist gone not found; remove "helpers.js" from .git-outlet-state.json`,
		"0 published, 0 unchanged, 0 skipped, 1 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func repoConfig(entries ...config.Entry) *config.Config {
	return &config.Config{
		Settings: config.Settings{Mode: "copy", Branch: "main"},
		Entries:  entries,
	}
}

func saveRepoState(t *testing.T, root, name string) {
	t.Helper()

	if err := state.Save(statePath(root), state.State{
		name: {Repo: &state.Repo{Owner: "octocat", Name: name, Commit: "old"}},
	}); err != nil {
		t.Fatal(err)
	}
}

func stateEntry(t *testing.T, root, name string) (state.Entry, bool) {
	t.Helper()

	st, err := state.Load(statePath(root))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := st.Get(name)

	return entry, ok
}

func TestPublishRepoCreated(t *testing.T) {
	root := newRepo(t)
	cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app"})
	repos := newFakeRepositor()

	out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) { opts.Repos = repos })
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}

	want := "✔ my-cool-app   repo  created    +2        https://github.com/octocat/my-cool-app\n" +
		"\n1 published, 0 unchanged, 0 skipped, 0 failed\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
	if repos.creates != 1 || repos.syncs != 1 {
		t.Errorf("creates = %d, syncs = %d, want one each", repos.creates, repos.syncs)
	}

	st, err := state.Load(statePath(root))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	entry, ok := st.Get("my-cool-app")
	if !ok || entry.Repo == nil {
		t.Fatalf("state entry = %+v, ok = %v", entry, ok)
	}
	if entry.Repo.Owner != "octocat" || entry.Repo.Name != "my-cool-app" || entry.Repo.Commit != "sha1" {
		t.Errorf("state repo = %+v", entry.Repo)
	}
}

func TestPublishRepoUpdated(t *testing.T) {
	root := newRepo(t)
	cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app"})
	saveRepoState(t, root, "my-cool-app")

	repos := newFakeRepositor()
	repos.info = repo.Info{Exists: true, DefaultBranch: "main"}
	repos.syncResult = repo.Result{
		Commit:  "sha2",
		URL:     "https://github.com/octocat/my-cool-app",
		Added:   1,
		Changed: 1,
		Removed: 1,
	}

	out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) { opts.Repos = repos })
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}

	want := "✔ my-cool-app   repo  updated    +1 ~1 -1  https://github.com/octocat/my-cool-app\n" +
		"\n1 published, 0 unchanged, 0 skipped, 0 failed\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
	if repos.creates != 0 || repos.syncs != 1 {
		t.Errorf("creates = %d, syncs = %d, want 0 and 1", repos.creates, repos.syncs)
	}
}

func TestPublishRepoUnchanged(t *testing.T) {
	root := newRepo(t)
	cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app"})
	saveRepoState(t, root, "my-cool-app")

	repos := newFakeRepositor()
	repos.info = repo.Info{Exists: true, DefaultBranch: "main"}
	repos.syncResult = repo.Result{Commit: "head"}
	repos.syncErr = repo.NoChanges

	out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) { opts.Repos = repos })
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}

	want := "✔ my-cool-app   repo  unchanged\n" +
		"\n0 published, 1 unchanged, 0 skipped, 0 failed\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestPublishRepoSafety(t *testing.T) {
	tests := []struct {
		name      string
		info      repo.Info
		recorded  bool
		adopt     bool
		wantErr   string
		wantSyncs int
	}{
		{
			name:    "existing non-empty not recorded",
			info:    repo.Info{Exists: true},
			wantErr: "already exists and was not created by git-outlet; pass --adopt to take it over",
		},
		{
			name:      "adopt takes over",
			info:      repo.Info{Exists: true},
			adopt:     true,
			wantSyncs: 1,
		},
		{
			name:      "recorded repo is reused",
			info:      repo.Info{Exists: true},
			recorded:  true,
			wantSyncs: 1,
		},
		{
			name:      "empty existing repo is allowed",
			info:      repo.Info{Exists: true, Empty: true},
			wantSyncs: 1,
		},
		{
			name:    "private repo is refused",
			info:    repo.Info{Exists: true, Private: true},
			wantErr: "only publishes to public repositories",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepo(t)
			if tt.recorded {
				saveRepoState(t, root, "my-cool-app")
			}
			cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app"})

			repos := newFakeRepositor()
			repos.info = tt.info

			out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
				opts.Repos = repos
				opts.Adopt = tt.adopt
			})

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Run() error = nil, want %q (output %q)", tt.wantErr, out)
				}
				if !strings.Contains(out, tt.wantErr) {
					t.Errorf("output %q does not contain %q", out, tt.wantErr)
				}
				if repos.creates != 0 || repos.syncs != 0 {
					t.Errorf("creates = %d, syncs = %d, want none", repos.creates, repos.syncs)
				}
				return
			}

			if err != nil {
				t.Fatalf("Run() error: %v (output %q)", err, out)
			}
			if repos.syncs != tt.wantSyncs {
				t.Errorf("syncs = %d, want %d", repos.syncs, tt.wantSyncs)
			}
		})
	}
}

func TestPublishRepoOwnerResolvedOnce(t *testing.T) {
	root := newRepo(t)
	cfg := repoConfig(
		config.Entry{Name: "app-one", Path: "projects/my-cool-app"},
		config.Entry{Name: "app-two", Path: "projects/my-cool-app"},
	)

	repos := newFakeRepositor()
	repos.syncResult = repo.Result{Commit: "sha", URL: "https://github.com/octocat/app", Added: 2}

	out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) { opts.Repos = repos })
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}
	if repos.ownerCalls != 1 {
		t.Errorf("Owner() calls = %d, want 1", repos.ownerCalls)
	}
	if repos.syncs != 2 || repos.creates != 2 {
		t.Errorf("syncs = %d, creates = %d, want 2 each", repos.syncs, repos.creates)
	}
}

func TestPublishRepoFailureDoesNotStopLaterEntries(t *testing.T) {
	root := newRepo(t)
	cfg := repoConfig(
		config.Entry{Name: "broken", Path: "projects/my-cool-app"},
		config.Entry{Name: "helpers.js", Path: "src/helpers.js"},
	)

	repos := newFakeRepositor()
	repos.syncErr = errors.New("boom")

	gists := &fakeGister{}
	out, err := runPublish(t, root, cfg, gists, func(opts *Options) { opts.Repos = repos })
	if err == nil {
		t.Fatal("Run() error = nil, want the failed entry to surface")
	}
	if !strings.Contains(err.Error(), "broken") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("Run() error = %v, want it to name broken and boom", err)
	}
	if len(gists.creates) != 1 {
		t.Errorf("creates = %v, want the later Gist published", gists.creates)
	}
	for _, want := range []string{
		"✖ broken",
		"repo  failed: boom",
		"✔ helpers.js",
		"gist  created",
		"1 published, 0 unchanged, 0 skipped, 1 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func TestPublishRepoStateSavedPerEntry(t *testing.T) {
	root := newRepo(t)
	cfg := repoConfig(
		config.Entry{Name: "app-one", Path: "projects/my-cool-app"},
		config.Entry{Name: "app-two", Path: "projects/my-cool-app"},
	)

	repos := newFakeRepositor()
	repos.syncResult = repo.Result{Commit: "sha", URL: "https://github.com/octocat/app", Added: 2}
	repos.syncErrFor = map[string]error{"app-two": errors.New("boom")}

	_, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) { opts.Repos = repos })
	if err == nil {
		t.Fatal("Run() error = nil, want app-two to fail")
	}

	st, err := state.Load(statePath(root))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if entry, ok := st.Get("app-one"); !ok || entry.Repo == nil || entry.Repo.Commit != "sha" {
		t.Errorf("state entry for app-one = %+v, ok = %v", entry, ok)
	}
	if _, ok := st.Get("app-two"); ok {
		t.Error("state records the failed app-two")
	}
}

func TestPublishRepoMessagePrecedence(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		entry    string
		settings string
		want     string
	}{
		{name: "flag wins", flag: "from flag", entry: "from entry", settings: "from settings", want: "from flag"},
		{name: "entry wins over settings", entry: "from entry", settings: "from settings", want: "from entry"},
		{name: "settings wins over default", settings: "from settings", want: "from settings"},
		{name: "default uses the private HEAD", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepo(t)
			cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app", Message: tt.entry})
			cfg.Settings.Message = tt.settings

			repos := newFakeRepositor()
			_, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
				opts.Repos = repos
				opts.Message = tt.flag
			})
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}

			want := tt.want
			if want == "" {
				sha, err := gitx.ShortHead(context.Background(), root)
				if err != nil {
					t.Fatal(err)
				}
				want = "Update from private repo (" + sha + ")"
			}
			if repos.lastSync.Message != want {
				t.Errorf("commit message = %q, want %q", repos.lastSync.Message, want)
			}
		})
	}
}

func TestPublishMessageIgnoredForGistAndSubtree(t *testing.T) {
	t.Run("gist", func(t *testing.T) {
		root := newRepo(t)
		fake := &fakeGister{}
		out, err := runPublish(t, root, sampleConfig(), fake, func(opts *Options) {
			opts.Message = "ignored by Gists"
		})
		if err != nil {
			t.Fatalf("Run() error: %v (output %q)", err, out)
		}
		if len(fake.creates) != 1 {
			t.Errorf("Gist creates = %d, want one", len(fake.creates))
		}
	})

	t.Run("subtree", func(t *testing.T) {
		root := newRepo(t)
		cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app", Mode: "subtree"})
		repos := newFakeRepositor()
		repos.splitResult = "0123456789abcdef"
		out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
			opts.Repos = repos
			opts.Message = "ignored by subtree"
		})
		if err != nil {
			t.Fatalf("Run() error: %v (output %q)", err, out)
		}
		if repos.pushes != 1 {
			t.Errorf("subtree pushes = %d, want one", repos.pushes)
		}
	})
}

func TestPublishOnlyWithMessage(t *testing.T) {
	root := newRepo(t)
	cfg := repoConfig(
		config.Entry{Name: "app-one", Path: "projects/my-cool-app"},
		config.Entry{Name: "app-two", Path: "projects/my-cool-app"},
	)
	repos := newFakeRepositor()
	out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
		opts.Repos = repos
		opts.Only = "app-two"
		opts.Message = "selected app message"
	})
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}
	if len(repos.syncCalls) != 1 {
		t.Fatalf("sync calls = %d, want one", len(repos.syncCalls))
	}
	if repos.syncCalls[0].Name != "app-two" || repos.syncCalls[0].Message != "selected app message" {
		t.Errorf("selected sync = %+v, want app-two with message", repos.syncCalls[0])
	}
	if strings.Contains(out, "app-one") {
		t.Errorf("output includes unselected app-one: %q", out)
	}
}

func TestPublishMessageAppliesToEveryCopyEntry(t *testing.T) {
	root := newRepo(t)
	cfg := repoConfig(
		config.Entry{Name: "app-one", Path: "projects/my-cool-app"},
		config.Entry{Name: "app-two", Path: "projects/my-cool-app"},
	)
	repos := newFakeRepositor()
	out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
		opts.Repos = repos
		opts.Message = "shared release message"
	})
	if err != nil {
		t.Fatalf("Run() error: %v (output %q)", err, out)
	}
	if len(repos.syncCalls) != 2 {
		t.Fatalf("sync calls = %d, want two", len(repos.syncCalls))
	}
	for _, call := range repos.syncCalls {
		if call.Message != "shared release message" {
			t.Errorf("%s message = %q, want shared release message", call.Name, call.Message)
		}
	}
}

func TestPublishDryRunCopyMessages(t *testing.T) {
	tests := []struct {
		name        string
		message     string
		info        repo.Info
		preview     repo.Result
		want        string
		wantMissing string
	}{
		{
			name:    "would create",
			message: "Add auth demo",
			want:    `repo  would create (+2) "Add auth demo"`,
		},
		{
			name:        "would update with first line only",
			message:     "Add auth demo\nKeep details in history",
			info:        repo.Info{Exists: true},
			preview:     repo.Result{URL: "https://github.com/octocat/my-cool-app", Added: 3, Changed: 2, Removed: 1},
			want:        `repo  would update (+3 ~2 -1) "Add auth demo…"`,
			wantMissing: "Keep details in history",
		},
		{
			name:        "unchanged omits message",
			message:     "Add auth demo",
			info:        repo.Info{Exists: true},
			want:        "repo  unchanged",
			wantMissing: "Add auth demo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepo(t)
			cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app"})
			if tt.info.Exists {
				saveRepoState(t, root, "my-cool-app")
			}
			repos := newFakeRepositor()
			repos.info = tt.info
			repos.previewResult = tt.preview
			out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
				opts.Repos = repos
				opts.DryRun = true
				opts.Message = tt.message
			})
			if err != nil {
				t.Fatalf("Run() error: %v (output %q)", err, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("output %q does not contain %q", out, tt.want)
			}
			if tt.wantMissing != "" && strings.Contains(out, tt.wantMissing) {
				t.Errorf("output %q unexpectedly contains %q", out, tt.wantMissing)
			}
			if repos.syncs != 0 || repos.pushes != 0 || repos.creates != 0 {
				t.Errorf("dry run writes: syncs=%d pushes=%d creates=%d", repos.syncs, repos.pushes, repos.creates)
			}
		})
	}
}

func TestPlanIsReadOnly(t *testing.T) {
	root := newRepo(t)
	cfg := repoConfig(
		config.Entry{Name: "helpers.js", Path: "src/helpers.js"},
		config.Entry{Name: "new-app", Path: "projects/my-cool-app"},
	)
	repos := newFakeRepositor()
	gister := &fakeGister{}
	var out bytes.Buffer
	plan, err := Plan(context.Background(), Options{
		Config: cfg, Root: root, Repos: repos, Gist: gister, Out: &out,
	})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(plan.Entries) != 2 || plan.Entries[0].Action != ActionCreate || plan.Entries[1].Action != ActionCreate {
		t.Fatalf("plan entries = %+v", plan.Entries)
	}
	if !plan.Entries[0].CreatesPublic || !plan.Entries[1].CreatesPublic {
		t.Errorf("create flags = %v/%v, want true", plan.Entries[0].CreatesPublic, plan.Entries[1].CreatesPublic)
	}
	if plan.Entries[0].Fingerprint == "" || plan.Entries[1].Fingerprint == "" {
		t.Errorf("fingerprints are empty: %+v", plan.Entries)
	}
	if repos.creates != 0 || repos.syncs != 0 || repos.pushes != 0 || gister.calls != 0 {
		t.Errorf("Plan() wrote: repo creates=%d syncs=%d pushes=%d, gist calls=%d", repos.creates, repos.syncs, repos.pushes, gister.calls)
	}
	if _, err := os.Stat(statePath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Plan() wrote state: %v", err)
	}
}

func TestApplyPublishesApprovedPlan(t *testing.T) {
	root := newRepo(t)
	cfg := repoConfig(config.Entry{Name: "new-app", Path: "projects/my-cool-app"})
	repos := newFakeRepositor()
	var out bytes.Buffer
	plan, err := Plan(context.Background(), Options{Config: cfg, Root: root, Repos: repos, Out: &out})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	result, err := Apply(context.Background(), plan, plan.ChangedIndexes())
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if result.Published != 1 || repos.creates != 1 || repos.syncs != 1 {
		t.Errorf("result=%+v creates=%d syncs=%d", result, repos.creates, repos.syncs)
	}
	entry, ok := stateEntry(t, root, "new-app")
	if !ok || entry.Repo == nil || entry.Repo.Commit != repos.syncResult.Commit {
		t.Errorf("saved state = %+v, found = %v", entry, ok)
	}
}

func TestApplyPublishesToLocalBareRemote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh shell command is not executable on Windows")
	}
	root := newRepo(t)
	remoteDir := t.TempDir()
	binDir := t.TempDir()
	gh := `#!/bin/sh
set -eu
case "$1:$2" in
  api:repos/octocat/new-app)
    if [ -d "$REMOTE_DIR/new-app.git" ]; then
      printf '{"private":false,"default_branch":"main","clone_url":"file://%s/new-app.git"}\n' "$REMOTE_DIR"
    else
      echo 'HTTP 404 Not Found' >&2
      exit 1
    fi
    ;;
  repo:create)
    git init --bare -q --initial-branch=main "$REMOTE_DIR/new-app.git"
    ;;
  repo:clone)
    git clone -q "$REMOTE_DIR/new-app.git" "$4"
    ;;
  *)
    echo "unexpected gh call: $*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(gh), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REMOTE_DIR", remoteDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := repoConfig(config.Entry{Name: "new-app", Path: "projects/my-cool-app"})
	cfg.Settings.Owner = "octocat"
	var out bytes.Buffer
	plan, err := Plan(context.Background(), Options{
		Config: cfg, Root: root, Repos: repo.New(repo.ExecRunner{}), Out: &out,
	})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	result, err := Apply(context.Background(), plan, plan.ChangedIndexes())
	if err != nil {
		t.Fatalf("Apply() error: %v (output %q)", err, out.String())
	}
	if result.Published != 1 {
		t.Errorf("Published = %d, want 1", result.Published)
	}
	remote := filepath.Join(remoteDir, "new-app.git")
	got := outputGit(t, remote, "--git-dir", remote, "show", "main:a.js")
	if strings.TrimSpace(got) != "a" {
		t.Errorf("remote a.js = %q, want a", got)
	}
	commit := outputGit(t, root, "--git-dir", remote, "rev-parse", "refs/heads/main")
	entry, ok := stateEntry(t, root, "new-app")
	if !ok || entry.Repo == nil || entry.Repo.Commit != strings.TrimSpace(commit) {
		t.Errorf("state entry = %+v, found=%v; remote commit=%q", entry, ok, strings.TrimSpace(commit))
	}
}

func outputGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestApplyRejectsChangedEntryAndContinues(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "projects/second/second.txt", "second")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "second")
	cfg := repoConfig(
		config.Entry{Name: "first", Path: "projects/my-cool-app"},
		config.Entry{Name: "second", Path: "projects/second"},
	)
	repos := newFakeRepositor()
	var out bytes.Buffer
	plan, err := Plan(context.Background(), Options{Config: cfg, Root: root, Repos: repos, Out: &out})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	writeFile(t, root, "projects/my-cool-app/a.js", "changed after approval")
	result, err := Apply(context.Background(), plan, plan.ChangedIndexes())
	if err == nil || !strings.Contains(err.Error(), "changed since you approved it; run again") {
		t.Fatalf("Apply() error = %v, want changed-entry error", err)
	}
	if result.Failed != 1 || result.Published != 1 || repos.syncs != 1 {
		t.Errorf("result=%+v syncs=%d", result, repos.syncs)
	}
	if _, ok := stateEntry(t, root, "first"); ok {
		t.Error("state was written for changed entry")
	}
	if _, ok := stateEntry(t, root, "second"); !ok {
		t.Error("unchanged approved entry was not applied")
	}
}

func TestAllBlockedPlanFails(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, ".env", "secret-like path")
	runGit(t, root, "add", ".env")
	runGit(t, root, "commit", "-q", "-m", "add env")
	cfg := repoConfig(config.Entry{Name: "env", Path: ".env"})
	repos := newFakeRepositor()
	var out bytes.Buffer
	plan, err := Plan(context.Background(), Options{Config: cfg, Root: root, Repos: repos, Out: &out})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if plan.Entries[0].Action != ActionBlocked {
		t.Fatalf("action = %q, want blocked", plan.Entries[0].Action)
	}
	result, err := Apply(context.Background(), plan, nil)
	if err == nil || result.Failed != 1 || repos.syncs != 0 {
		t.Errorf("Apply() result=%+v err=%v syncs=%d", result, err, repos.syncs)
	}
}

func TestPublishSubtreeStateAndDryRun(t *testing.T) {
	t.Run("create then unchanged", func(t *testing.T) {
		root := newRepo(t)
		cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app", Mode: "subtree"})
		repos := newFakeRepositor()
		repos.splitResult = "0123456789abcdef"

		out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) { opts.Repos = repos })
		if err != nil {
			t.Fatalf("first Run() error: %v (output %q)", err, out)
		}
		if repos.creates != 1 || repos.pushes != 1 || repos.lastPush.Commit != repos.splitResult {
			t.Fatalf("create/push calls = %d/%d, push = %+v", repos.creates, repos.pushes, repos.lastPush)
		}
		st, err := state.Load(statePath(root))
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}
		entry, ok := st.Get("my-cool-app")
		if !ok || entry.Repo == nil || entry.Repo.Commit != repos.splitResult {
			t.Fatalf("saved subtree state = %+v, ok = %v", entry, ok)
		}

		repos = newFakeRepositor()
		repos.info = repo.Info{Exists: true}
		repos.splitResult = "0123456789abcdef"
		out, err = runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) { opts.Repos = repos })
		if err != nil {
			t.Fatalf("second Run() error: %v (output %q)", err, out)
		}
		if repos.pushes != 0 || !strings.Contains(out, "repo  unchanged") {
			t.Errorf("second run output = %q, pushes = %d", out, repos.pushes)
		}

		repos = newFakeRepositor()
		repos.info = repo.Info{Exists: true}
		repos.splitResult = "fedcba9876543210"
		out, err = runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) { opts.Repos = repos })
		if err != nil {
			t.Fatalf("updated Run() error: %v (output %q)", err, out)
		}
		if repos.pushes != 1 || !strings.Contains(out, "repo  updated") {
			t.Errorf("updated run output = %q, pushes = %d", out, repos.pushes)
		}
	})

	t.Run("dry run does not create push or save state", func(t *testing.T) {
		root := newRepo(t)
		cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app", Mode: "subtree"})
		repos := newFakeRepositor()
		repos.splitResult = "0123456789abcdef"

		out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
			opts.Repos = repos
			opts.DryRun = true
		})
		if err != nil {
			t.Fatalf("Run() error: %v (output %q)", err, out)
		}
		if repos.creates != 0 || repos.pushes != 0 || !strings.Contains(out, "would create") {
			t.Errorf("output = %q; creates = %d, pushes = %d", out, repos.creates, repos.pushes)
		}
		if _, err := os.Stat(statePath(root)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("state file exists after dry run: %v", err)
		}
	})

	t.Run("private repo is rejected before push", func(t *testing.T) {
		root := newRepo(t)
		cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app", Mode: "subtree"})
		repos := newFakeRepositor()
		repos.info = repo.Info{Exists: true, Private: true}
		repos.splitResult = "0123456789abcdef"

		out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) { opts.Repos = repos })
		if err == nil || !strings.Contains(err.Error(), "only publishes to public repositories") {
			t.Fatalf("Run() error = %v, output %q", err, out)
		}
		if repos.pushes != 0 || repos.creates != 0 {
			t.Errorf("private repo calls: creates=%d pushes=%d", repos.creates, repos.pushes)
		}
	})
}

func TestPublishSecretScanAndSuspiciousOverride(t *testing.T) {
	t.Run("finding blocks only its gist", func(t *testing.T) {
		root := newRepo(t)
		writeFile(t, root, "src/notes.md", "notes")
		runGit(t, root, "add", "src/notes.md")
		runGit(t, root, "commit", "-q", "-m", "notes")
		cfg := &config.Config{
			Settings: config.Settings{Mode: "copy", SecretScan: true},
			Entries: []config.Entry{
				{Name: "helpers.js", Path: "src/helpers.js"},
				{Name: "notes.md", Path: "src/notes.md"},
			},
		}
		scanner := &fakeScanner{fileFindings: map[string][]scan.Finding{
			"helpers.js": {{RuleID: "fake-rule", File: "helpers.js", Line: 2}},
		}}
		gister := &fakeGister{}
		out, err := runPublish(t, root, cfg, gister, func(opts *Options) { opts.Scan = scanner })
		if err == nil || !strings.Contains(out, "secret scan found 1 issue") {
			t.Fatalf("Run() error = %v, output %q", err, out)
		}
		if len(gister.creates) != 1 || gister.creates[0].Name != "notes.md" {
			t.Errorf("created Gists = %+v, want only notes.md", gister.creates)
		}
		if strings.Contains(out, "PLANTED_FAKE_SECRET") {
			t.Fatal("publish output contains matched text")
		}
	})

	t.Run("allow suspicious does not bypass gitleaks", func(t *testing.T) {
		root := newRepo(t)
		writeFile(t, root, ".env", "PLANTED_FAKE_SECRET")
		runGit(t, root, "add", ".env")
		runGit(t, root, "commit", "-q", "-m", "env")
		cfg := &config.Config{
			Settings: config.Settings{Mode: "copy", SecretScan: true},
			Entries:  []config.Entry{{Name: "environment", Path: ".env"}},
		}
		gister := &fakeGister{}
		scanner := &fakeScanner{fileFindings: map[string][]scan.Finding{
			"environment": {{RuleID: "fake-rule", File: ".env", Line: 1}},
		}}
		out, err := runPublish(t, root, cfg, gister, func(opts *Options) {
			opts.AllowSuspicious = true
			opts.Scan = scanner
		})
		if err == nil || !strings.Contains(out, "allowed suspicious file .env") || !strings.Contains(out, "secret scan found 1 issue") {
			t.Fatalf("Run() error = %v, output %q", err, out)
		}
		if len(gister.creates) != 0 {
			t.Errorf("created Gists = %+v, want none", gister.creates)
		}
	})

	t.Run("disabled secret scan still blocks suspicious names", func(t *testing.T) {
		root := newRepo(t)
		writeFile(t, root, ".env", "fake")
		runGit(t, root, "add", ".env")
		runGit(t, root, "commit", "-q", "-m", "env")
		cfg := &config.Config{
			Settings: config.Settings{Mode: "copy", SecretScan: false},
			Entries:  []config.Entry{{Name: "environment", Path: ".env"}},
		}
		scanner := &fakeScanner{}
		out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) { opts.Scan = scanner })
		if err == nil || !strings.Contains(out, "suspicious files selected") {
			t.Fatalf("Run() error = %v, output %q", err, out)
		}
		if scanner.versions != 0 {
			t.Errorf("Version() calls = %d, want none", scanner.versions)
		}
	})

	t.Run("allow suspicious publishes when gitleaks finds nothing", func(t *testing.T) {
		root := newRepo(t)
		writeFile(t, root, ".env", "fake")
		runGit(t, root, "add", ".env")
		runGit(t, root, "commit", "-q", "-m", "env")
		cfg := &config.Config{
			Settings: config.Settings{Mode: "copy", SecretScan: true},
			Entries:  []config.Entry{{Name: "environment", Path: ".env"}},
		}
		gister := &fakeGister{}
		scanner := &fakeScanner{}
		out, err := runPublish(t, root, cfg, gister, func(opts *Options) {
			opts.AllowSuspicious = true
			opts.Scan = scanner
		})
		if err != nil {
			t.Fatalf("Run() error: %v (output %q)", err, out)
		}
		if len(gister.creates) != 1 || !strings.Contains(out, "allowed suspicious file .env") {
			t.Errorf("created = %+v, output = %q", gister.creates, out)
		}
	})

	t.Run("missing gitleaks warns once", func(t *testing.T) {
		root := newRepo(t)
		cfg := &config.Config{
			Settings: config.Settings{Mode: "copy", SecretScan: true},
			Entries: []config.Entry{
				{Name: "helpers.js", Path: "src/helpers.js"},
				{Name: "readme.md", Path: "projects/my-cool-app/README.md"},
			},
		}
		scanner := &fakeScanner{versionErr: scan.ErrUnavailable}
		out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
			opts.Scan = scanner
			opts.DryRun = true
		})
		if err != nil {
			t.Fatalf("dry-run error: %v", err)
		}
		if strings.Count(out, "secret scan skipped: gitleaks is not installed") != 1 {
			t.Errorf("missing-binary warning count in %q", out)
		}
	})

	t.Run("copy scans exactly selected files", func(t *testing.T) {
		root := newRepo(t)
		cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app", Include: []string{"README.md"}})
		cfg.Settings.SecretScan = true
		scanner := &fakeScanner{}
		_, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
			opts.Repos = newFakeRepositor()
			opts.Scan = scanner
		})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if len(scanner.filesCalls) != 1 || strings.Join(scanner.filesCalls[0], ",") != "README.md" {
			t.Errorf("scanned files = %v, want only README.md", scanner.filesCalls)
		}
	})

	t.Run("subtree scans history", func(t *testing.T) {
		root := newRepo(t)
		cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app", Mode: "subtree"})
		cfg.Settings.SecretScan = true
		scanner := &fakeScanner{}
		repos := newFakeRepositor()
		repos.splitResult = "0123456789abcdef"
		_, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
			opts.Repos = repos
			opts.Scan = scanner
		})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if len(scanner.historyCalls) != 1 || scanner.historyCalls[0] != "projects/my-cool-app" {
			t.Errorf("history scans = %v", scanner.historyCalls)
		}
	})
}

func TestPublishRepoDryRun(t *testing.T) {
	t.Run("missing repo", func(t *testing.T) {
		root := newRepo(t)
		cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app"})
		repos := newFakeRepositor()

		out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
			opts.Repos = repos
			opts.DryRun = true
		})
		if err != nil {
			t.Fatalf("Run() error: %v (output %q)", err, out)
		}

		short, err := gitx.ShortHead(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		message := "Update from private repo (" + short + ")"
		want := "✔ my-cool-app   repo  would create (+2) " + strconv.Quote(message) + "\n" +
			"\n1 would publish, 0 unchanged, 0 skipped, 0 failed\n"
		if out != want {
			t.Errorf("output =\n%s\nwant\n%s", out, want)
		}
		if repos.creates != 0 || repos.syncs != 0 || repos.previews != 0 {
			t.Errorf("creates = %d, syncs = %d, previews = %d, want none", repos.creates, repos.syncs, repos.previews)
		}
		if _, err := os.Stat(statePath(root)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("state file exists after a dry run: %v", err)
		}
	})

	t.Run("would update", func(t *testing.T) {
		root := newRepo(t)
		cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app"})
		saveRepoState(t, root, "my-cool-app")
		before, err := os.ReadFile(statePath(root))
		if err != nil {
			t.Fatal(err)
		}

		repos := newFakeRepositor()
		repos.info = repo.Info{Exists: true, DefaultBranch: "main"}
		repos.previewResult = repo.Result{
			URL:     "https://github.com/octocat/my-cool-app",
			Added:   1,
			Changed: 1,
			Removed: 1,
		}

		out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
			opts.Repos = repos
			opts.DryRun = true
		})
		if err != nil {
			t.Fatalf("Run() error: %v (output %q)", err, out)
		}

		short, err := gitx.ShortHead(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		message := "Update from private repo (" + short + ")"
		want := "✔ my-cool-app   repo  would update (+1 ~1 -1) " + strconv.Quote(message) + "  https://github.com/octocat/my-cool-app\n" +
			"\n1 would publish, 0 unchanged, 0 skipped, 0 failed\n"
		if out != want {
			t.Errorf("output =\n%s\nwant\n%s", out, want)
		}
		if repos.previews != 1 || repos.creates != 0 || repos.syncs != 0 {
			t.Errorf("creates = %d, syncs = %d, previews = %d, want one preview", repos.creates, repos.syncs, repos.previews)
		}

		after, err := os.ReadFile(statePath(root))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("dry run changed the state file:\n%s\n%s", before, after)
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		root := newRepo(t)
		cfg := repoConfig(config.Entry{Name: "my-cool-app", Path: "projects/my-cool-app"})
		saveRepoState(t, root, "my-cool-app")

		repos := newFakeRepositor()
		repos.info = repo.Info{Exists: true, DefaultBranch: "main"}

		out, err := runPublish(t, root, cfg, &fakeGister{}, func(opts *Options) {
			opts.Repos = repos
			opts.DryRun = true
		})
		if err != nil {
			t.Fatalf("Run() error: %v (output %q)", err, out)
		}

		want := "✔ my-cool-app   repo  unchanged\n" +
			"\n0 would publish, 1 unchanged, 0 skipped, 0 failed\n"
		if out != want {
			t.Errorf("output =\n%s\nwant\n%s", out, want)
		}
	})
}
