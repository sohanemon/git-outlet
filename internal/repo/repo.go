package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// NOTE: Runner abstracts command execution so tests can fake gh and git.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// NOTE: ExecRunner is the production Runner and keeps trimmed stderr in errors.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return "", fmt.Errorf("%s: %w", command, err)
		}
		return "", fmt.Errorf("%s: %s: %w", command, message, err)
	}

	return stdout.String(), nil
}

// NOTE: Info carries the facts needed to decide whether a folder publish may write.
type Info struct {
	Exists        bool
	Empty         bool
	Private       bool
	DefaultBranch string
}

// NOTE: Client owns the gh and git calls behind folder entries.
type Client struct {
	runner Runner
}

// NOTE: New binds a Client to runner so tests can replace command execution.
func New(runner Runner) *Client {
	return &Client{runner: runner}
}

// NOTE: Owner prefers settingsOwner so publishing does not depend on the active gh account.
func (c *Client) Owner(ctx context.Context, settingsOwner string) (string, error) {
	if settingsOwner != "" {
		return settingsOwner, nil
	}

	out, err := c.runner.Run(ctx, "", "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return "", fmt.Errorf("resolve GitHub owner: %w", err)
	}

	login := strings.TrimSpace(out)
	if login == "" {
		return "", fmt.Errorf("resolve GitHub owner: gh returned an empty login")
	}

	return login, nil
}

// NOTE: Inspect separates missing, empty, private and non-empty repos before any write.
func (c *Client) Inspect(ctx context.Context, owner, name string) (Info, error) {
	out, err := c.runner.Run(ctx, "", "gh", "api", "repos/"+owner+"/"+name)
	if err != nil {
		if isNotFound(err) {
			return Info{}, nil
		}
		return Info{}, fmt.Errorf("inspect %s/%s: %w", owner, name, err)
	}

	var response struct {
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
		CloneURL      string `json:"clone_url"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		return Info{}, fmt.Errorf("parse repo %s/%s: %w", owner, name, err)
	}

	info := Info{
		Exists:        true,
		Private:       response.Private,
		DefaultBranch: response.DefaultBranch,
	}
	if response.Private {
		return info, nil
	}

	refs, err := c.runner.Run(ctx, "", "git", "ls-remote", "--heads", response.CloneURL)
	if err != nil {
		return Info{}, fmt.Errorf("list refs for %s/%s: %w", owner, name, err)
	}
	info.Empty = strings.TrimSpace(refs) == ""

	return info, nil
}

// NOTE: Create creates the public repository that Sync fills afterwards.
func (c *Client) Create(ctx context.Context, owner, name, description string) error {
	if description == "" {
		description = "Showcase: " + name
	}

	if _, err := c.runner.Run(ctx, "", "gh", "repo", "create", owner+"/"+name, "--public", "--description", description); err != nil {
		return fmt.Errorf("create repo %s/%s: %w", owner, name, err)
	}

	return nil
}

func isNotFound(err error) bool {
	return strings.Contains(err.Error(), "HTTP 404")
}
