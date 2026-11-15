package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
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

	return dir
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

func commitAll(t *testing.T, dir string) {
	t.Helper()

	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
}

func TestRootFromSubfolder(t *testing.T) {
	repo := newRepo(t)
	sub := filepath.Join(repo, "nested", "deeper")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := Root(context.Background(), sub)
	if err != nil {
		t.Fatalf("Root() error: %v", err)
	}

	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

func TestRootOutsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()

	_, err := Root(context.Background(), dir)
	if err == nil {
		t.Fatal("Root() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not inside a git repository") {
		t.Errorf("Root() error = %v, want a clear not-a-repo message", err)
	}
}

func TestTrackedFiles(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "a.txt", "a")
	writeFile(t, repo, "sub/b.txt", "b")
	writeFile(t, repo, ".git/info/exclude", "*.log\n")
	commitAll(t, repo)
	writeFile(t, repo, "untracked.txt", "nope")
	writeFile(t, repo, "ignored.log", "nope")

	files, skipped, err := TrackedFiles(context.Background(), repo, ".")
	if err != nil {
		t.Fatalf("TrackedFiles() error: %v", err)
	}
	if want := []string{"a.txt", "sub/b.txt"}; !reflect.DeepEqual(files, want) {
		t.Errorf("TrackedFiles() = %v, want %v", files, want)
	}
	if skipped != 0 {
		t.Errorf("TrackedFiles() skipped = %d, want 0", skipped)
	}

	subFiles, _, err := TrackedFiles(context.Background(), repo, "sub")
	if err != nil {
		t.Fatalf("TrackedFiles(sub) error: %v", err)
	}
	if want := []string{"b.txt"}; !reflect.DeepEqual(subFiles, want) {
		t.Errorf("TrackedFiles(sub) = %v, want %v", subFiles, want)
	}
}

func TestTrackedFilesSkipsSymlinksAndMissing(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "a.txt", "a")
	writeFile(t, repo, "removed.txt", "gone")
	if err := os.Symlink("a.txt", filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo)
	if err := os.Remove(filepath.Join(repo, "removed.txt")); err != nil {
		t.Fatal(err)
	}

	files, skipped, err := TrackedFiles(context.Background(), repo, ".")
	if err != nil {
		t.Fatalf("TrackedFiles() error: %v", err)
	}
	if want := []string{"a.txt"}; !reflect.DeepEqual(files, want) {
		t.Errorf("TrackedFiles() = %v, want %v", files, want)
	}
	if skipped != 2 {
		t.Errorf("TrackedFiles() skipped = %d, want 2", skipped)
	}
}
