package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/sohanemon/git-outlet/internal/check"
	"github.com/sohanemon/git-outlet/internal/publish"
)

func TestConfirmPlanAnswers(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		want       []int
		wantAbort  bool
		wantOutput []string
	}{
		{name: "yes", input: "Y\n", want: []int{0, 2}},
		{name: "no", input: "N\n", wantAbort: true, wantOutput: []string{"Aborted. Nothing was published."}},
		{name: "empty", input: "\n", wantAbort: true},
		{name: "eof", input: "", wantAbort: true},
		{name: "three invalid answers", input: "x\nx\nx\n", wantAbort: true, wantOutput: []string{"Please answer y, n, d or s."}},
		{name: "details then yes", input: "d\ny\n", want: []int{0, 2}, wantOutput: []string{"src/helpers.js", "README.md", "second line"}},
		{name: "select list", input: "s\n1,3\ny\n", want: []int{0, 2}, wantOutput: []string{"Select changes", "Plan for 2 entries", "NEW PUBLIC REPO"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := promptPlan()
			var out bytes.Buffer
			selected, err := confirmPlan(context.Background(), strings.NewReader(tt.input), &out, plan)
			if tt.wantAbort {
				if !errors.Is(err, ErrAborted) {
					t.Fatalf("confirmPlan() error = %v, want ErrAborted", err)
				}
			} else if err != nil {
				t.Fatalf("confirmPlan() error: %v", err)
			}
			if !tt.wantAbort && !equalInts(selected, tt.want) {
				t.Errorf("selected = %v, want %v", selected, tt.want)
			}
			for _, want := range tt.wantOutput {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output %q does not contain %q", out.String(), want)
				}
			}
		})
	}
}

func TestConfirmPlanRejectsSelectionRanges(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []int
		text  string
	}{
		{name: "blocked entry rejected then valid", input: "s\n1-3\n1,3\ny\n", want: []int{0, 2}, text: "blocked or cannot be selected"},
		{name: "out of range rejected then valid", input: "s\n9\n1,3\ny\n", want: []int{0, 2}, text: "out of range"},
		{name: "duplicates collapse", input: "s\n1, 1,3,3\ny\n", want: []int{0, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			selected, err := confirmPlan(context.Background(), strings.NewReader(tt.input), &out, promptPlan())
			if err != nil {
				t.Fatalf("confirmPlan() error: %v", err)
			}
			if !equalInts(selected, tt.want) {
				t.Errorf("selected = %v, want %v", selected, tt.want)
			}
			if tt.text != "" && !strings.Contains(out.String(), tt.text) {
				t.Errorf("output %q does not contain %q", out.String(), tt.text)
			}
		})
	}
}

func TestPlanColorOutput(t *testing.T) {
	plan := promptPlan()
	var colored bytes.Buffer
	if err := writePlan(&colored, plan, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{ansiGreen, ansiYellow, ansiRed, ansiDim, "NEW PUBLIC GIST", "Creating public repositories"} {
		if !strings.Contains(colored.String(), want) {
			t.Errorf("colored plan %q does not contain %q", colored.String(), want)
		}
	}
	var plain bytes.Buffer
	if err := writePlan(&plain, plan, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Errorf("plain plan contains ANSI escapes: %q", plain.String())
	}
	if !strings.Contains(plain.String(), `"first line…"`) || strings.Contains(plain.String(), `"second line"`) {
		t.Errorf("multi-line plan message preview = %q", plain.String())
	}
	t.Setenv("NO_COLOR", "")
	if colorEnabled(true) {
		t.Error("colorEnabled(true) = true with NO_COLOR set")
	}
	if colorEnabled(false) {
		t.Error("colorEnabled(false) = true for non-terminal output")
	}
}

func TestPlanWarningOnlyAppearsForNewPublicThings(t *testing.T) {
	plan := promptPlan()
	for i := range plan.Entries {
		plan.Entries[i].CreatesPublic = false
	}
	var out bytes.Buffer
	if err := writePlan(&out, plan, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Creating public repositories and gists cannot be undone by this tool.") {
		t.Errorf("plan warning appeared without a public create: %q", out.String())
	}
}

func promptPlan() *publish.PlanResult {
	return &publish.PlanResult{Entries: []publish.PlannedEntry{
		{Name: "helpers.js", Kind: check.KindGist, Mode: "gist", Action: publish.ActionCreate, CreatesPublic: true, Target: "helpers.js", Files: []publish.PlannedFile{{Status: "+", Path: "src/helpers.js"}}},
		{Name: "blocked", Kind: check.KindRepo, Mode: "copy", Action: publish.ActionBlocked, BlockReason: "suspicious files selected", BlockDetails: []string{".env"}, Files: []publish.PlannedFile{{Status: "+", Path: ".env"}}},
		{Name: "new-demo", Kind: check.KindRepo, Mode: "copy", Action: publish.ActionCreate, CreatesPublic: true, Target: "octocat/new-demo", Message: "first line\nsecond line", Files: []publish.PlannedFile{{Status: "+", Path: "README.md"}}},
		{Name: "notes.md", Kind: check.KindGist, Mode: "gist", Action: publish.ActionUnchanged, Target: "https://gist.github.com/id", Files: []publish.PlannedFile{{Status: "=", Path: "notes.md"}}},
	}}
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestOutputIsTerminalHonorsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if outputIsTerminal(os.Stdout) && colorEnabled(outputIsTerminal(os.Stdout)) {
		t.Error("color enabled with NO_COLOR set")
	}
}

func TestStdinIsNotTerminalForNullDevice(t *testing.T) {
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdin
	os.Stdin = input
	t.Cleanup(func() {
		os.Stdin = original
		input.Close()
	})
	if stdinIsTerminal() {
		t.Error("stdinIsTerminal() = true for the null device")
	}
}
