package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Runner runs a command in dir and returns its standard output.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}

	return stdout.String(), nil
}

// Root returns the top level of the git repository containing dir.
func Root(ctx context.Context, dir string) (string, error) {
	return root(ctx, execRunner{}, dir)
}

func root(ctx context.Context, runner Runner, dir string) (string, error) {
	out, err := runner.Run(ctx, dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git repository: %w", dir, err)
	}

	return filepath.Clean(strings.TrimSpace(out)), nil
}

// TrackedFiles returns path-relative tracked files plus the count of skipped symlinks and missing files.
func TrackedFiles(ctx context.Context, root, path string) ([]string, int, error) {
	return trackedFiles(ctx, execRunner{}, root, path)
}

func trackedFiles(ctx context.Context, runner Runner, root, path string) ([]string, int, error) {
	out, err := runner.Run(ctx, root, "git", "ls-files", "-z", "--", path)
	if err != nil {
		return nil, 0, err
	}

	files := make([]string, 0, strings.Count(out, "\x00"))
	skipped := 0

	for _, file := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		if file == "" {
			continue
		}

		abs := filepath.Join(root, filepath.FromSlash(file))
		info, err := os.Lstat(abs)
		if errors.Is(err, fs.ErrNotExist) {
			skipped++
			continue
		}
		if err != nil {
			return nil, skipped, fmt.Errorf("stat %s: %w", abs, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			skipped++
			continue
		}

		rel, err := filepath.Rel(filepath.FromSlash(path), filepath.FromSlash(file))
		if err != nil {
			return nil, skipped, fmt.Errorf("relative path for %s: %w", file, err)
		}
		if rel == "." {
			rel = filepath.Base(filepath.FromSlash(file))
		}
		files = append(files, filepath.ToSlash(rel))
	}

	sort.Strings(files)

	return files, skipped, nil
}
