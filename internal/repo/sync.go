package repo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// NOTE: NoChanges lets publish report unchanged without treating it as a failure.
var NoChanges = errors.New("no changes")

// NOTE: SyncOptions carries what Sync needs for one copy-mode publish.
type SyncOptions struct {
	Owner   string
	Name    string
	Branch  string
	Message string
	Source  string
	Files   []string
}

// NOTE: Result reports the pushed commit and change counts for display and state.
type Result struct {
	Commit  string
	URL     string
	Added   int
	Changed int
	Removed int
}

// NOTE: Sync replaces the public repository content with the selection, then commits and pushes.
func (c *Client) Sync(ctx context.Context, opts SyncOptions) (Result, error) {
	prepared, err := c.prepare(ctx, opts)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(prepared.temp)

	repoURL := url(opts.Owner, opts.Name)
	if prepared.noChanges {
		return Result{Commit: prepared.head, URL: repoURL}, NoChanges
	}

	name, email, err := c.identity(ctx, opts.Source)
	if err != nil {
		return Result{}, err
	}

	commitArgs := []string{"-c", "user.name=" + name, "-c", "user.email=" + email, "commit", "-F", "-"}
	runner, ok := c.runner.(interface {
		RunInput(context.Context, string, string, []byte, ...string) (string, error)
	})
	if !ok {
		return Result{}, errors.New("git runner does not support command input")
	}
	if _, err := runner.RunInput(ctx, prepared.dir, "git", []byte(opts.Message), commitArgs...); err != nil {
		return Result{}, fmt.Errorf("commit %s/%s: %w", opts.Owner, opts.Name, err)
	}

	if _, err := c.runner.Run(ctx, prepared.dir, "git", "push", "origin", opts.Branch); err != nil {
		if isRejected(err) {
			return Result{}, fmt.Errorf("push %s/%s: the public repository was changed outside git-outlet; delete it or reconcile it and run again: %w", opts.Owner, opts.Name, err)
		}
		return Result{}, fmt.Errorf("push %s/%s: %w", opts.Owner, opts.Name, err)
	}

	commit, err := c.head(ctx, prepared.dir)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Commit:  commit,
		URL:     repoURL,
		Added:   prepared.added,
		Changed: prepared.changed,
		Removed: prepared.removed,
	}, nil
}

// NOTE: Preview runs the same staging as Sync so dry runs report real counts.
func (c *Client) Preview(ctx context.Context, opts SyncOptions) (Result, error) {
	prepared, err := c.prepare(ctx, opts)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(prepared.temp)

	return Result{
		Commit:  prepared.head,
		URL:     url(opts.Owner, opts.Name),
		Added:   prepared.added,
		Changed: prepared.changed,
		Removed: prepared.removed,
	}, nil
}

type prepared struct {
	temp      string
	dir       string
	head      string
	added     int
	changed   int
	removed   int
	noChanges bool
}

func (c *Client) prepare(ctx context.Context, opts SyncOptions) (prepared, error) {
	temp, err := os.MkdirTemp("", "git-outlet-*")
	if err != nil {
		return prepared{}, fmt.Errorf("create temp directory: %w", err)
	}

	p := prepared{temp: temp, dir: filepath.Join(temp, "repo")}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(temp)
		}
	}()

	if _, err := c.runner.Run(ctx, "", "gh", "repo", "clone", opts.Owner+"/"+opts.Name, p.dir); err != nil {
		return prepared{}, fmt.Errorf("clone %s/%s: %w", opts.Owner, opts.Name, err)
	}
	if err := c.checkout(ctx, p.dir, opts.Branch); err != nil {
		return prepared{}, err
	}
	if err := clearWorktree(p.dir); err != nil {
		return prepared{}, err
	}
	modes, err := c.fileModes(ctx, opts.Source, opts.Files)
	if err != nil {
		return prepared{}, err
	}
	if err := copyFiles(opts.Source, p.dir, opts.Files, modes); err != nil {
		return prepared{}, err
	}
	if _, err := c.runner.Run(ctx, p.dir, "git", "add", "-A"); err != nil {
		return prepared{}, fmt.Errorf("stage files: %w", err)
	}
	for _, file := range opts.Files {
		change := "-x"
		if modes[filepath.ToSlash(file)]&0o111 != 0 {
			change = "+x"
		}
		if _, err := c.runner.Run(ctx, p.dir, "git", "update-index", "--chmod="+change, "--", file); err != nil {
			return prepared{}, fmt.Errorf("set tracked mode for %s: %w", file, err)
		}
	}

	status, err := c.runner.Run(ctx, p.dir, "git", "status", "--porcelain")
	if err != nil {
		return prepared{}, fmt.Errorf("read worktree status: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		diff, err := c.runner.Run(ctx, p.dir, "git", "diff", "--cached", "--name-status")
		if err != nil {
			return prepared{}, fmt.Errorf("read staged changes: %w", err)
		}
		p.added, p.changed, p.removed = nameStatus(diff)
		keep = true
		return p, nil
	}

	head, err := c.head(ctx, p.dir)
	if err != nil {
		return prepared{}, err
	}
	p.head = head
	p.noChanges = true
	keep = true

	return p, nil
}

func (c *Client) checkout(ctx context.Context, dir, branch string) error {
	ref := "origin/" + branch
	out, err := c.runner.Run(ctx, dir, "git", "branch", "--list", "--remote", ref)
	if err != nil {
		return fmt.Errorf("list remote branch %s: %w", ref, err)
	}

	args := []string{"checkout", "-B", branch}
	if strings.TrimSpace(out) != "" {
		args = append(args, ref)
	}
	if _, err := c.runner.Run(ctx, dir, "git", args...); err != nil {
		return fmt.Errorf("checkout branch %s: %w", branch, err)
	}

	return nil
}

func (c *Client) head(ctx context.Context, dir string) (string, error) {
	out, err := c.runner.Run(ctx, dir, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read HEAD: %w", err)
	}

	return strings.TrimSpace(out), nil
}

func (c *Client) identity(ctx context.Context, dir string) (string, string, error) {
	name, _ := c.configValue(ctx, dir, "user.name")
	email, _ := c.configValue(ctx, dir, "user.email")
	if name == "" || email == "" {
		return "", "", errors.New(`git identity is not configured; run "git config user.name <name>" and "git config user.email <email>"`)
	}

	return name, email, nil
}

func (c *Client) configValue(ctx context.Context, dir, key string) (string, error) {
	out, err := c.runner.Run(ctx, dir, "git", "config", "--get", key)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

func clearWorktree(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read worktree: %w", err)
	}

	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("remove %s: %w", entry.Name(), err)
		}
	}

	return nil
}

func (c *Client) fileModes(ctx context.Context, source string, files []string) (map[string]fs.FileMode, error) {
	out, err := c.runner.Run(ctx, source, "git", "ls-files", "--stage", "-z")
	if err != nil {
		return nil, fmt.Errorf("read tracked file modes: %w", err)
	}

	wanted := make(map[string]struct{}, len(files))
	for _, file := range files {
		wanted[filepath.ToSlash(file)] = struct{}{}
	}
	modes := make(map[string]fs.FileMode, len(files))
	for _, record := range strings.Split(out, "\x00") {
		metadata, path, ok := strings.Cut(record, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(metadata)
		if len(fields) != 3 || fields[2] != "0" {
			continue
		}
		path = filepath.ToSlash(path)
		if _, ok := wanted[path]; !ok {
			continue
		}
		mode := fs.FileMode(0o644)
		if fields[0] == "100755" {
			mode = 0o755
		}
		modes[path] = mode
	}
	return modes, nil
}

func copyFiles(source, dest string, files []string, modes map[string]fs.FileMode) error {
	for _, file := range files {
		rel := filepath.FromSlash(file)
		target := filepath.Join(dest, rel)
		if target == dest || !within(dest, target) {
			return fmt.Errorf("path %q escapes the public repository", file)
		}

		src := filepath.Join(source, rel)
		info, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("path %q is not a regular file", file)
		}

		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}

		mode, ok := modes[filepath.ToSlash(file)]
		if !ok {
			mode = 0o644
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create directory for %s: %w", file, err)
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return fmt.Errorf("write %s: %w", file, err)
		}
		if err := os.Chmod(target, mode); err != nil {
			return fmt.Errorf("set mode of %s: %w", file, err)
		}
	}

	return nil
}

func nameStatus(diff string) (added, changed, removed int) {
	for _, line := range strings.Split(strings.TrimSpace(diff), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || fields[0] == "" {
			continue
		}
		switch fields[0][0] {
		case 'A':
			added++
		case 'D':
			removed++
		default:
			changed++
		}
	}

	return added, changed, removed
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func isRejected(err error) bool {
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"[rejected]", "non-fast-forward", "fetch first", "failed to push some refs"} {
		if strings.Contains(message, marker) {
			return true
		}
	}

	return false
}

func url(owner, name string) string {
	return "https://github.com/" + owner + "/" + name
}
