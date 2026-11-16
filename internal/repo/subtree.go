package repo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// NOTE: Bundle the split and destination so subtree pushes need no private-repo remote.
type SubtreeOptions struct {
	Root   string
	Owner  string
	Name   string
	Branch string
	Commit string
	URL    string
}

// NOTE: Split path history without creating refs so the private repo stays unchanged.
func (c *Client) SubtreeSplit(ctx context.Context, root, path string) (string, error) {
	prefix := "--prefix=" + filepath.ToSlash(path)
	out, err := c.runner.Run(ctx, root, "git", "subtree", "split", prefix)
	if err != nil {
		if missingSubtree(err) {
			return "", fmt.Errorf("git subtree is not available; install git-subtree to publish %s", path)
		}
		if strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			return "", fmt.Errorf("split %s: the folder has no committed history yet: %w", path, err)
		}
		return "", fmt.Errorf("split %s: %w", path, err)
	}

	commit := splitCommit(out)
	if commit == "" {
		return "", fmt.Errorf("split %s: the folder has no committed history yet", path)
	}

	return commit, nil
}

// NOTE: Push directly to the URL so the private repo gains no remote configuration.
func (c *Client) SubtreePush(ctx context.Context, opts SubtreeOptions) error {
	target := opts.URL
	if target == "" {
		target = pushURL(opts.Owner, opts.Name)
	}

	ref := opts.Commit + ":refs/heads/" + opts.Branch
	if _, err := c.runner.Run(ctx, opts.Root, "git", "push", target, ref); err != nil {
		if isRejected(err) {
			return fmt.Errorf("push %s/%s: the public repository was changed outside git-outlet; delete it or reconcile it and run again: %w", opts.Owner, opts.Name, err)
		}
		return fmt.Errorf("push %s/%s: %w", opts.Owner, opts.Name, err)
	}

	return nil
}

func missingSubtree(err error) bool {
	message := err.Error()
	for _, marker := range []string{"'subtree' is not a git command", "git-subtree"} {
		if strings.Contains(message, marker) {
			return true
		}
	}

	return false
}

// INFO: Read the last stdout hash because subtree progress is written to stderr.
func splitCommit(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if len(line) >= 40 && isHex(line) {
			return line
		}
	}

	return ""
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}

	return true
}

func pushURL(owner, name string) string {
	return "https://github.com/" + owner + "/" + name + ".git"
}
