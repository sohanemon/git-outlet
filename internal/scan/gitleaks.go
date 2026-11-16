package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// NOTE: Return a sentinel so callers can warn and continue when gitleaks is absent.
var ErrUnavailable = errors.New("gitleaks is not installed")

// NOTE: Abstract execution so tests can replace the gitleaks binary.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (Outcome, error)
}

// NOTE: Keep exit codes separate because code 1 means findings rather than tool failure.
type Outcome struct {
	Stdout   string
	ExitCode int
}

// NOTE: Resolve gitleaks from PATH so installation stays external to the program.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir string, args ...string) (Outcome, error) {
	cmd := exec.CommandContext(ctx, "gitleaks", args...)
	cmd.Dir = dir

	var stdout bytes.Buffer
	if len(args) > 0 && args[0] == "version" {
		cmd.Stdout = &stdout
	} else {
		cmd.Stdout = io.Discard
	}
	cmd.Stderr = io.Discard

	err := cmd.Run()
	if err == nil {
		return Outcome{Stdout: stdout.String()}, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return Outcome{Stdout: stdout.String(), ExitCode: exitErr.ExitCode()}, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return Outcome{}, ErrUnavailable
	}

	return Outcome{}, fmt.Errorf("gitleaks command failed: %w", err)
}

// SECURITY: Store identifiers only so findings never retain matched text.
type Finding struct {
	RuleID string
	File   string
	Line   int
	Commit string
}

// SECURITY: Render identifiers only to keep secrets out of diagnostics.
func (f Finding) String() string {
	line := fmt.Sprintf("%s  %s:%d", f.RuleID, f.File, f.Line)
	if f.Commit != "" {
		line += "  " + f.Commit
	}

	return line
}

// NOTE: Share this narrow scanning API between check and publish.
type Scanner interface {
	Version(ctx context.Context) (string, error)
	File(ctx context.Context, path, name string) ([]Finding, error)
	Files(ctx context.Context, source string, files []string) ([]Finding, error)
	History(ctx context.Context, root, path string) ([]Finding, error)
}

// NOTE: Keep version detection and scans together for one publish run.
type Client struct {
	runner       Runner
	version      string
	major, minor int
	probed       bool
	probeErr     error
}

// NOTE: Inject the runner so callers can test without an installed binary.
func New(runner Runner) *Client {
	return &Client{runner: runner}
}

// NOTE: Probe once so every entry sees the same installed version.
func (c *Client) Version(ctx context.Context) (string, error) {
	if !c.probed {
		c.probed = true
		c.version, c.major, c.minor, c.probeErr = c.detect(ctx)
	}

	return c.version, c.probeErr
}

func (c *Client) detect(ctx context.Context) (string, int, int, error) {
	outcome, err := c.runner.Run(ctx, "", "version")
	if err != nil {
		return "", 0, 0, err
	}
	if outcome.ExitCode != 0 {
		return "", 0, 0, fmt.Errorf("gitleaks version exited with %d", outcome.ExitCode)
	}

	major, minor, ok := parseVersion(outcome.Stdout)
	if !ok {
		return "", 0, 0, fmt.Errorf("parse gitleaks version %q", strings.TrimSpace(outcome.Stdout))
	}

	return fmt.Sprintf("%d.%d", major, minor), major, minor, nil
}

var versionPattern = regexp.MustCompile(`v?(\d+)\.(\d+)`)

func parseVersion(out string) (int, int, bool) {
	match := versionPattern.FindStringSubmatch(out)
	if len(match) != 3 {
		return 0, 0, false
	}

	major, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(match[2])
	if err != nil {
		return 0, 0, false
	}

	return major, minor, true
}

// COMPAT: Select renamed scan commands only for gitleaks v8.19 and newer.
func (c *Client) modern() bool {
	return c.major > 8 || (c.major == 8 && c.minor >= 19)
}

// NOTE: Stage one file under its Gist name so report paths stay meaningful.
func (c *Client) File(ctx context.Context, path, name string) ([]Finding, error) {
	if _, err := c.Version(ctx); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "git-outlet-scan-*")
	if err != nil {
		return nil, fmt.Errorf("create scan directory: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := stageFile(path, filepath.Join(dir, filepath.Base(name))); err != nil {
		return nil, err
	}

	return c.scan(ctx, dir, c.dirArgs(dir))
}

// NOTE: Stage only selected files so scan contents match the publish.
func (c *Client) Files(ctx context.Context, source string, files []string) ([]Finding, error) {
	if _, err := c.Version(ctx); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "git-outlet-scan-*")
	if err != nil {
		return nil, fmt.Errorf("create scan directory: %w", err)
	}
	defer os.RemoveAll(dir)

	for _, file := range files {
		rel := filepath.FromSlash(file)
		target := filepath.Join(dir, rel)
		if target == dir || !within(dir, target) {
			return nil, fmt.Errorf("path %q escapes the scan directory", file)
		}
		if err := stageFile(filepath.Join(source, rel), target); err != nil {
			return nil, err
		}
	}

	return c.scan(ctx, dir, c.dirArgs(dir))
}

// NOTE: Scan path history because subtree publishes deleted files and old versions.
func (c *Client) History(ctx context.Context, root, path string) ([]Finding, error) {
	if _, err := c.Version(ctx); err != nil {
		return nil, err
	}
	return c.scan(ctx, root, c.historyArgs(root, path))
}

func (c *Client) dirArgs(target string) []string {
	if c.modern() {
		return []string{"dir", target}
	}

	return []string{"detect", "--no-git", "--source", target}
}

func (c *Client) historyArgs(repo, path string) []string {
	// INFO: Keep log options in one argument so git receives the path separator.
	logOpts := "--log-opts=-- " + path
	if c.modern() {
		return []string{"git", repo, logOpts}
	}

	return []string{"detect", "--source", repo, logOpts}
}

func (c *Client) scan(ctx context.Context, dir string, args []string) ([]Finding, error) {
	if _, err := c.Version(ctx); err != nil {
		return nil, err
	}

	report, err := os.CreateTemp("", "git-outlet-gitleaks-*.json")
	if err != nil {
		return nil, fmt.Errorf("create gitleaks report: %w", err)
	}
	reportPath := report.Name()
	if err := report.Close(); err != nil {
		os.Remove(reportPath)
		return nil, fmt.Errorf("close gitleaks report: %w", err)
	}
	defer os.Remove(reportPath)

	args = append(args, "--redact", "--report-format", "json", "--report-path", reportPath)
	outcome, err := c.runner.Run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	if outcome.ExitCode != 0 && outcome.ExitCode != 1 {
		return nil, fmt.Errorf("gitleaks exited with %d", outcome.ExitCode)
	}

	return readReport(reportPath)
}

type reportFinding struct {
	RuleID    string `json:"RuleID"`
	File      string `json:"File"`
	StartLine int    `json:"StartLine"`
	Commit    string `json:"Commit"`
}

// SECURITY: Decode identifiers only so matched text never enters findings.
func readReport(path string) ([]Finding, error) {
	report, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read gitleaks report: %w", err)
	}
	defer report.Close()

	var raw []reportFinding
	if err := json.NewDecoder(report).Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("parse gitleaks report: %w", err)
	}

	findings := make([]Finding, 0, len(raw))
	for _, item := range raw {
		findings = append(findings, Finding{
			RuleID: item.RuleID,
			File:   item.File,
			Line:   item.StartLine,
			Commit: item.Commit,
		})
	}

	return findings, nil
}

func stageFile(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create scan directory for %s: %w", target, err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		return fmt.Errorf("stage %s: %w", target, err)
	}

	return nil
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
