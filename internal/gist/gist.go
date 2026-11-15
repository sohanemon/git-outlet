package gist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs gh with args and optional standard input, returning its standard output.
type Runner interface {
	Run(ctx context.Context, args []string, stdin string) (string, error)
}

// ExecRunner runs the gh command found on PATH.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, args []string, stdin string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stdin = strings.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	command := "gh " + strings.Join(args, " ")
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return "", fmt.Errorf("%s: %w", command, err)
		}
		return "", fmt.Errorf("%s: %s: %w", command, message, err)
	}

	return stdout.String(), nil
}

// NotFoundError reports a Gist that GitHub no longer has.
type NotFoundError struct {
	ID string
}

// Error names the missing Gist, or Gists in general when the ID is empty.
func (e *NotFoundError) Error() string {
	if e.ID == "" {
		return "gist not found"
	}

	return fmt.Sprintf("gist %s not found", e.ID)
}

// Client creates and updates Gists through gh.
type Client struct {
	runner Runner
}

// New returns a Client that runs commands through runner.
func New(runner Runner) *Client {
	return &Client{runner: runner}
}

// Description returns description, or name when it is empty.
func Description(name, description string) string {
	if description == "" {
		return name
	}

	return description
}

type file struct {
	Content string `json:"content"`
}

type createRequest struct {
	Description string          `json:"description"`
	Public      bool            `json:"public"`
	Files       map[string]file `json:"files"`
}

type updateRequest struct {
	Description string          `json:"description"`
	Files       map[string]file `json:"files"`
}

type response struct {
	ID      string `json:"id"`
	HTMLURL string `json:"html_url"`
}

// Create creates a public Gist holding one file.
func (c *Client) Create(ctx context.Context, name, description, content string) (string, string, error) {
	body, err := json.Marshal(createRequest{
		Description: Description(name, description),
		Public:      true,
		Files:       map[string]file{name: {Content: content}},
	})
	if err != nil {
		return "", "", fmt.Errorf("encode gist request: %w", err)
	}

	out, err := c.runner.Run(ctx, []string{"api", "--method", "POST", "gists", "--input", "-"}, string(body))
	if err != nil {
		if isNotFound(err) {
			return "", "", &NotFoundError{}
		}
		return "", "", err
	}

	var resp response
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", "", fmt.Errorf("parse gist response: %w", err)
	}

	return resp.ID, resp.HTMLURL, nil
}

// Update replaces the file in an existing Gist.
func (c *Client) Update(ctx context.Context, id, name, description, content string) (string, error) {
	body, err := json.Marshal(updateRequest{
		Description: Description(name, description),
		Files:       map[string]file{name: {Content: content}},
	})
	if err != nil {
		return "", fmt.Errorf("encode gist request: %w", err)
	}

	out, err := c.runner.Run(ctx, []string{"api", "--method", "PATCH", "gists/" + id, "--input", "-"}, string(body))
	if err != nil {
		if isNotFound(err) {
			return "", &NotFoundError{ID: id}
		}
		return "", err
	}

	var resp response
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("parse gist response: %w", err)
	}

	return resp.HTMLURL, nil
}

func isNotFound(err error) bool {
	return strings.Contains(err.Error(), "HTTP 404")
}
