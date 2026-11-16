package scan

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	version  string
	exitCode int
	report   string
	args     [][]string
	paths    []string
}

func (f *fakeRunner) Run(_ context.Context, _ string, args ...string) (Outcome, error) {
	f.args = append(f.args, append([]string(nil), args...))
	if len(args) == 1 && args[0] == "version" {
		return Outcome{Stdout: f.version}, nil
	}
	for i, arg := range args {
		if arg == "--report-path" && i+1 < len(args) {
			f.paths = append(f.paths, args[i+1])
			if f.report != "" {
				if err := os.WriteFile(args[i+1], []byte(f.report), 0o600); err != nil {
					return Outcome{}, err
				}
			}
		}
	}

	return Outcome{ExitCode: f.exitCode}, nil
}

func TestClientUsesVersionSpecificCommands(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{version: "gitleaks version 8.18.4", want: "detect"},
		{version: "gitleaks version 8.19.0", want: "dir"},
		{version: "gitleaks version 9.0.0", want: "dir"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			runner := &fakeRunner{version: tt.version}
			client := New(runner)
			path := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(path, []byte("safe"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := client.File(context.Background(), path, "file"); err != nil {
				t.Fatalf("File() error: %v", err)
			}
			got := runner.args[1][0]
			if got != tt.want {
				t.Errorf("scan command = %q, want %q", got, tt.want)
			}
			joined := strings.Join(runner.args[1], " ")
			if !strings.Contains(joined, "--redact") || !strings.Contains(joined, "--report-format json") {
				t.Errorf("scan args = %v, missing redaction or JSON report", runner.args[1])
			}
		})
	}
}

func TestHistoryUsesPathLimitedGitLog(t *testing.T) {
	tests := []struct {
		version string
		command string
	}{
		{version: "8.18.4", command: "detect"},
		{version: "8.19.0", command: "git"},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			runner := &fakeRunner{version: tt.version}
			if _, err := New(runner).History(context.Background(), "/repo", "a folder"); err != nil {
				t.Fatalf("History() error: %v", err)
			}
			args := runner.args[1]
			if args[0] != tt.command {
				t.Errorf("history command = %q, want %q", args[0], tt.command)
			}
			if !containsArg(args, "--log-opts=-- a folder") {
				t.Errorf("history args = %v, missing path-limited log option", args)
			}
			if !containsArg(args, "--redact") {
				t.Errorf("history args = %v, missing --redact", args)
			}
		})
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}

	return false
}

func TestClientReportDoesNotExposeMatchedTextAndIsRemoved(t *testing.T) {
	secret := "PLANTED_FAKE_SECRET_123"
	runner := &fakeRunner{
		version:  "8.19.0",
		exitCode: 1,
		report:   `[{"RuleID":"aws-access-token","File":"a.env","StartLine":4,"Commit":"0123456789abcdef","Secret":"` + secret + `","Match":"` + secret + `"}]`,
	}
	client := New(runner)
	findings, err := client.Files(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Files() error: %v", err)
	}
	want := []Finding{{RuleID: "aws-access-token", File: "a.env", Line: 4, Commit: "0123456789abcdef"}}
	if !reflect.DeepEqual(findings, want) {
		t.Errorf("findings = %+v, want %+v", findings, want)
	}
	if strings.Contains(findings[0].String(), secret) {
		t.Fatal("finding output contains matched text")
	}
	if got := runner.args[1]; got[0] != "dir" {
		t.Errorf("scan command = %v, want dir", got)
	}
	for _, path := range runner.paths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("report %q still exists: %v", path, err)
		}
	}
}

func TestClientTreatsOnlyExitOneAsFindings(t *testing.T) {
	for _, code := range []int{1, 2} {
		t.Run(string(rune('0'+code)), func(t *testing.T) {
			runner := &fakeRunner{version: "8.19.0", exitCode: code}
			findings, err := New(runner).Files(context.Background(), t.TempDir(), nil)
			if code == 1 && err != nil {
				t.Fatalf("exit 1 error = %v", err)
			}
			if code == 1 && len(findings) != 0 {
				t.Errorf("findings = %v, want empty report", findings)
			}
			if code != 1 && err == nil {
				t.Fatal("non-one exit code returned nil error")
			}
		})
	}
}

func TestExecRunnerDoesNotReturnStderr(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "gitleaks")
	secret := "PLANTED_FAKE_SECRET_456"
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' '"+secret+"' >&2\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	outcome, err := (ExecRunner{}).Run(context.Background(), "", "dir", ".")
	if err == nil {
		if outcome.ExitCode != 2 {
			t.Fatalf("Run() outcome = %+v, want exit code 2", outcome)
		}
	}
	if err != nil && strings.Contains(err.Error(), secret) {
		t.Fatal("Run() error contains stderr matched text")
	}
	if strings.Contains(outcome.Stdout, secret) {
		t.Fatal("Run() stdout contains stderr matched text")
	}
}

func TestUnavailableBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := (ExecRunner{}).Run(context.Background(), "", "version")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("Run() error = %v, want ErrUnavailable", err)
	}
}
