package publish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sohanemon/git-outlet/internal/config"
	"github.com/sohanemon/git-outlet/internal/gist"
	"github.com/sohanemon/git-outlet/internal/state"
)

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
			{Name: "my-cool-app", Path: "projects/my-cool-app"},
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

	err := Run(context.Background(), opts)

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

	want := "✔ helpers.js    gist  created    https://gist.github.com/gist1\n" +
		"- my-cool-app   repo  skipped (repo publishing not implemented yet)\n" +
		"\n1 published, 0 unchanged, 1 skipped, 0 failed\n"
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
		"✔ helpers.js    gist  updated    https://gist.github.com/gist1",
		"1 published, 0 unchanged, 1 skipped, 0 failed",
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
		"✔ helpers.js    gist  unchanged",
		"0 published, 1 unchanged, 1 skipped, 0 failed",
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
	for _, want := range []string{`unknown entry "nope"`, "helpers.js, my-cool-app"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
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

		want := "✔ helpers.js    gist  would create\n" +
			"- my-cool-app   repo  skipped (repo publishing not implemented yet)\n" +
			"\n1 would publish, 0 unchanged, 1 skipped, 0 failed\n"
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
			"✔ helpers.js    gist  would update    https://gist.github.com/abc123",
			"1 would publish, 0 unchanged, 1 skipped, 0 failed",
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
			"✔ helpers.js    gist  unchanged",
			"0 would publish, 1 unchanged, 1 skipped, 0 failed",
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
