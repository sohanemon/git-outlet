package repo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	remote     string
	login      string
	loginErr   error
	api        string
	apiErr     error
	createErr  error
	ghCalls    []string
	gitCalls   []string
	beforePush func()
}

func (f *fakeRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "git" {
		f.gitCalls = append(f.gitCalls, strings.Join(args, " "))
		if len(args) > 0 && args[0] == "push" && f.beforePush != nil {
			hook := f.beforePush
			f.beforePush = nil
			hook()
		}
		return ExecRunner{}.Run(ctx, dir, name, args...)
	}

	call := strings.Join(args, " ")
	f.ghCalls = append(f.ghCalls, call)

	switch {
	case len(args) >= 4 && args[0] == "repo" && args[1] == "clone":
		out, err := exec.CommandContext(ctx, "git", "clone", "-q", f.remote, args[3]).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git clone: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "", nil
	case len(args) >= 2 && args[0] == "api" && args[1] == "user":
		return f.login, f.loginErr
	case len(args) >= 2 && args[0] == "api":
		return f.api, f.apiErr
	case len(args) >= 2 && args[0] == "repo" && args[1] == "create":
		return "", f.createErr
	default:
		return "", fmt.Errorf("unexpected gh call: %s", call)
	}
}

func TestOwner(t *testing.T) {
	tests := []struct {
		name     string
		settings string
		login    string
		loginErr error
		want     string
		wantErr  bool
	}{
		{name: "settings owner wins", settings: "sohanemon", want: "sohanemon"},
		{name: "falls back to gh login", login: "octocat\n", want: "octocat"},
		{name: "empty login", wantErr: true},
		{name: "gh error", loginErr: errors.New("boom"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{login: tt.login, loginErr: tt.loginErr}
			got, err := New(runner).Owner(context.Background(), tt.settings)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Owner() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Owner() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Owner() = %q, want %q", got, tt.want)
			}
			if tt.settings != "" && len(runner.ghCalls) != 0 {
				t.Errorf("Owner() called gh %v, want none", runner.ghCalls)
			}
		})
	}
}

func TestInspect(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		runner := &fakeRunner{apiErr: errors.New(`gh api repos/a/b: Not Found (HTTP 404): exit status 1`)}
		info, err := New(runner).Inspect(context.Background(), "a", "b")
		if err != nil {
			t.Fatalf("Inspect() error: %v", err)
		}
		if info != (Info{}) {
			t.Errorf("Inspect() = %+v, want zero Info", info)
		}
	})

	t.Run("public non-empty", func(t *testing.T) {
		bare := newBareRepo(t)
		seedRemote(t, bare, map[string]string{"a.txt": "a"})
		runner := &fakeRunner{api: fmt.Sprintf(`{"private":false,"default_branch":"main","clone_url":%q}`, bare)}

		info, err := New(runner).Inspect(context.Background(), "a", "b")
		if err != nil {
			t.Fatalf("Inspect() error: %v", err)
		}
		want := Info{Exists: true, DefaultBranch: "main"}
		if info != want {
			t.Errorf("Inspect() = %+v, want %+v", info, want)
		}
	})

	t.Run("public empty", func(t *testing.T) {
		bare := newBareRepo(t)
		runner := &fakeRunner{api: fmt.Sprintf(`{"private":false,"default_branch":"main","clone_url":%q}`, bare)}

		info, err := New(runner).Inspect(context.Background(), "a", "b")
		if err != nil {
			t.Fatalf("Inspect() error: %v", err)
		}
		if !info.Exists || !info.Empty || info.Private {
			t.Errorf("Inspect() = %+v, want an existing empty public repo", info)
		}
	})

	t.Run("private skips refs", func(t *testing.T) {
		runner := &fakeRunner{api: `{"private":true,"default_branch":"main","clone_url":"unused"}`}

		info, err := New(runner).Inspect(context.Background(), "a", "b")
		if err != nil {
			t.Fatalf("Inspect() error: %v", err)
		}
		if !info.Exists || !info.Private {
			t.Errorf("Inspect() = %+v, want an existing private repo", info)
		}
		if len(runner.gitCalls) != 0 {
			t.Errorf("Inspect() listed refs for a private repo: %v", runner.gitCalls)
		}
	})

	t.Run("bad response", func(t *testing.T) {
		runner := &fakeRunner{api: "not json"}
		if _, err := New(runner).Inspect(context.Background(), "a", "b"); err == nil {
			t.Fatal("Inspect() error = nil, want a parse error")
		}
	})
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name        string
		description string
		want        string
	}{
		{name: "entry description", description: "Demo app", want: "Demo app"},
		{name: "fallback description", want: "Showcase: my-app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{}
			if err := New(runner).Create(context.Background(), "a", "my-app", tt.description); err != nil {
				t.Fatalf("Create() error: %v", err)
			}

			want := []string{"repo create a/my-app --public --description " + tt.want}
			if !reflect.DeepEqual(runner.ghCalls, want) {
				t.Errorf("Create() calls = %v, want %v", runner.ghCalls, want)
			}
		})
	}
}

func TestSyncAddsAndPreservesExecutableBit(t *testing.T) {
	source := newSourceRepo(t, map[string]string{"a.js": "a", "sub/b.sh": "b"})
	writeFileMode(t, source, "run.sh", "#!/bin/sh\n", 0o755)
	runGit(t, source, "add", "-A")
	runGit(t, source, "update-index", "--chmod=+x", "run.sh")
	runGit(t, source, "commit", "-q", "-m", "exec")

	bare := newBareRepo(t)
	runner := &fakeRunner{remote: bare}

	res, err := New(runner).Sync(context.Background(), syncOptions(source, "a", "app", "add", "a.js", "sub/b.sh", "run.sh"))
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	if res.Added != 3 || res.Changed != 0 || res.Removed != 0 {
		t.Errorf("Sync() delta = +%d ~%d -%d, want +3", res.Added, res.Changed, res.Removed)
	}
	if res.Commit == "" {
		t.Error("Sync() commit is empty")
	}
	if res.URL != "https://github.com/a/app" {
		t.Errorf("Sync() url = %q", res.URL)
	}
	if got := remoteContent(t, bare, "a.js"); got != "a" {
		t.Errorf("remote a.js = %q, want a", got)
	}
	if got := remoteContent(t, bare, "sub/b.sh"); got != "b" {
		t.Errorf("remote sub/b.sh = %q, want b", got)
	}
	if got := remoteMode(t, bare, "run.sh"); got != "100755" {
		t.Errorf("remote run.sh mode = %q, want 100755", got)
	}
}

func TestSyncUpdatesAndRemoves(t *testing.T) {
	bare := newBareRepo(t)
	seedRemote(t, bare, map[string]string{"a.js": "one", "old.txt": "old", "keep.txt": "keep"})

	source := newSourceRepo(t, map[string]string{"a.js": "two", "keep.txt": "keep", "new.txt": "new"})
	runner := &fakeRunner{remote: bare}

	res, err := New(runner).Sync(context.Background(), syncOptions(source, "a", "app", "update", "a.js", "keep.txt", "new.txt"))
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	if res.Added != 1 || res.Changed != 1 || res.Removed != 1 {
		t.Errorf("Sync() delta = +%d ~%d -%d, want +1 ~1 -1", res.Added, res.Changed, res.Removed)
	}
	want := []string{"a.js", "keep.txt", "new.txt"}
	if got := remoteNames(t, bare); !reflect.DeepEqual(got, want) {
		t.Errorf("remote files = %v, want %v", got, want)
	}
	if got := remoteContent(t, bare, "a.js"); got != "two" {
		t.Errorf("remote a.js = %q, want two", got)
	}
}

func TestSyncNoChanges(t *testing.T) {
	bare := newBareRepo(t)
	seedRemote(t, bare, map[string]string{"a.js": "same"})
	before := remoteHead(t, bare)

	source := newSourceRepo(t, map[string]string{"a.js": "same"})
	runner := &fakeRunner{remote: bare}

	res, err := New(runner).Sync(context.Background(), syncOptions(source, "a", "app", "update", "a.js"))
	if !errors.Is(err, NoChanges) {
		t.Fatalf("Sync() error = %v, want NoChanges", err)
	}
	if res.Commit != before {
		t.Errorf("Sync() commit = %q, want %q", res.Commit, before)
	}
	if after := remoteHead(t, bare); after != before {
		t.Errorf("remote head changed from %s to %s", before, after)
	}
}

func TestSyncMissingIdentity(t *testing.T) {
	bare := newBareRepo(t)
	seedRemote(t, bare, map[string]string{"a.js": "one"})
	before := remoteHead(t, bare)

	source := newIdentitylessRepo(t, map[string]string{"a.js": "two"})
	runner := &fakeRunner{remote: bare}

	_, err := New(runner).Sync(context.Background(), syncOptions(source, "a", "app", "update", "a.js"))
	if err == nil {
		t.Fatal("Sync() error = nil, want a missing identity error")
	}
	for _, want := range []string{"git config user.name", "git config user.email"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Sync() error = %v, want it to contain %q", err, want)
		}
	}
	if after := remoteHead(t, bare); after != before {
		t.Errorf("remote head changed from %s to %s", before, after)
	}
}

func TestSyncRejectedPush(t *testing.T) {
	bare := newBareRepo(t)
	seedRemote(t, bare, map[string]string{"a.js": "one"})

	source := newSourceRepo(t, map[string]string{"a.js": "two"})
	runner := &fakeRunner{remote: bare, beforePush: func() {
		other := t.TempDir()
		runGit(t, other, "clone", "-q", bare, ".")
		runGit(t, other, "config", "user.email", "other@example.com")
		runGit(t, other, "config", "user.name", "Other")
		runGit(t, other, "config", "commit.gpgsign", "false")
		writeFile(t, other, "diverge.txt", "diverge")
		runGit(t, other, "add", "-A")
		runGit(t, other, "commit", "-q", "-m", "diverge")
		runGit(t, other, "push", "-q", "origin", "main")
	}}

	_, err := New(runner).Sync(context.Background(), syncOptions(source, "a", "app", "update", "a.js"))
	if err == nil {
		t.Fatal("Sync() error = nil, want a rejected push")
	}
	if !strings.Contains(err.Error(), "changed outside git-outlet") {
		t.Errorf("Sync() error = %v, want a takeover hint", err)
	}
	for _, call := range runner.gitCalls {
		if strings.Contains(call, "--force") || strings.Contains(call, "-f ") {
			t.Errorf("Sync() force-pushed: %s", call)
		}
	}
	if got := remoteContent(t, bare, "a.js"); got != "one" {
		t.Errorf("remote a.js = %q, want the divergent commit to survive", got)
	}
	if got := remoteContent(t, bare, "diverge.txt"); got != "diverge" {
		t.Errorf("remote diverge.txt = %q, want diverge", got)
	}
}

func TestSyncRemovesTempDirectory(t *testing.T) {
	source := newSourceRepo(t, map[string]string{"a.js": "a"})
	bare := newBareRepo(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	runner := &fakeRunner{remote: bare}
	if _, err := New(runner).Sync(context.Background(), syncOptions(source, "a", "app", "add", "a.js")); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temp directory still holds %v", entries)
	}
}

func TestPreviewLeavesRemoteUnchanged(t *testing.T) {
	bare := newBareRepo(t)
	seedRemote(t, bare, map[string]string{"a.js": "one", "old.txt": "old"})
	before := remoteHead(t, bare)

	source := newSourceRepo(t, map[string]string{"a.js": "two", "new.txt": "new"})
	runner := &fakeRunner{remote: bare}

	res, err := New(runner).Preview(context.Background(), syncOptions(source, "a", "app", "update", "a.js", "new.txt"))
	if err != nil {
		t.Fatalf("Preview() error: %v", err)
	}

	if res.Added != 1 || res.Changed != 1 || res.Removed != 1 {
		t.Errorf("Preview() delta = +%d ~%d -%d, want +1 ~1 -1", res.Added, res.Changed, res.Removed)
	}
	if after := remoteHead(t, bare); after != before {
		t.Errorf("remote head changed from %s to %s", before, after)
	}
	for _, call := range runner.gitCalls {
		if strings.HasPrefix(call, "commit") || strings.HasPrefix(call, "push") {
			t.Errorf("Preview() ran %q, want no commit or push", call)
		}
	}
}

func TestSyncRejectsEscape(t *testing.T) {
	source := newSourceRepo(t, map[string]string{"a.js": "a"})
	bare := newBareRepo(t)

	_, err := New(&fakeRunner{remote: bare}).Sync(context.Background(), syncOptions(source, "a", "app", "add", "../evil.txt"))
	if err == nil {
		t.Fatal("Sync() error = nil, want an escape error")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("Sync() error = %v, want an escape error", err)
	}
	if got := strings.TrimSpace(runGit(t, "", "--git-dir", bare, "branch", "--list")); got != "" {
		t.Errorf("remote branches = %q, want none", got)
	}
}

func TestClearWorktreePreservesGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "stale.txt", "stale")

	if err := clearWorktree(dir); err != nil {
		t.Fatalf("clearWorktree() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git", "objects")); err != nil {
		t.Errorf(".git was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale.txt still exists: %v", err)
	}
}

func TestCopyFilesRejectsEscape(t *testing.T) {
	source := t.TempDir()
	dest := t.TempDir()
	writeFile(t, source, "ok.txt", "ok")

	err := copyFiles(source, dest, []string{"../evil.txt"}, nil)
	if err == nil {
		t.Fatal("copyFiles() error = nil, want an escape error")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("copyFiles() error = %v, want an escape error", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("escape wrote outside the destination: %v", err)
	}
}

func TestExecRunner(t *testing.T) {
	out, err := (ExecRunner{}).Run(context.Background(), "", "sh", "-c", "printf hi")
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if out != "hi" {
		t.Errorf("Run() = %q, want hi", out)
	}

	_, err = (ExecRunner{}).Run(context.Background(), "", "sh", "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("Run() error = nil, want a failure")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("Run() error = %v, want it to keep stderr", err)
	}
}

func syncOptions(source, owner, name, message string, files ...string) SyncOptions {
	return SyncOptions{
		Owner:   owner,
		Name:    name,
		Branch:  "main",
		Message: message,
		Source:  source,
		Files:   files,
	}
}

func isolateGit(t *testing.T) {
	t.Helper()

	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return string(out)
}

func newSourceRepo(t *testing.T, files map[string]string) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	isolateGit(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")

	for name, contents := range files {
		writeFile(t, dir, name, contents)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")

	return dir
}

func newIdentitylessRepo(t *testing.T, files map[string]string) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	isolateGit(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "commit.gpgsign", "false")

	for name, contents := range files {
		writeFile(t, dir, name, contents)
	}
	runGit(t, dir, "add", "-A")

	return dir
}

func newBareRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	isolateGit(t)

	dir := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "-q", "--bare", "--initial-branch=main", dir)

	return dir
}

func seedRemote(t *testing.T, bare string, files map[string]string) {
	t.Helper()

	work := t.TempDir()
	runGit(t, work, "clone", "-q", bare, ".")
	runGit(t, work, "config", "user.email", "seed@example.com")
	runGit(t, work, "config", "user.name", "Seed")
	runGit(t, work, "config", "commit.gpgsign", "false")

	for name, contents := range files {
		writeFile(t, work, name, contents)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-q", "-m", "seed")
	runGit(t, work, "push", "-q", "origin", "main")
}

func writeFile(t *testing.T, root, name, contents string) {
	t.Helper()

	writeFileMode(t, root, name, contents, 0o600)
}

func writeFileMode(t *testing.T, root, name, contents string, mode fs.FileMode) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", name, err)
	}
}

func remoteContent(t *testing.T, bare, name string) string {
	t.Helper()

	return runGit(t, "", "--git-dir", bare, "show", "HEAD:"+name)
}

func remoteNames(t *testing.T, bare string) []string {
	t.Helper()

	out := runGit(t, "", "--git-dir", bare, "ls-tree", "-r", "--name-only", "HEAD")
	if strings.TrimSpace(out) == "" {
		return nil
	}

	return strings.Fields(out)
}

func remoteMode(t *testing.T, bare, name string) string {
	t.Helper()

	fields := strings.Fields(runGit(t, "", "--git-dir", bare, "ls-tree", "HEAD", name))
	if len(fields) == 0 {
		return ""
	}

	return fields[0]
}

func remoteHead(t *testing.T, bare string) string {
	t.Helper()

	return strings.TrimSpace(runGit(t, "", "--git-dir", bare, "rev-parse", "HEAD"))
}
