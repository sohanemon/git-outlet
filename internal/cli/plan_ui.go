package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"unicode"

	"github.com/sohanemon/git-outlet/internal/publish"
)

// NOTE: ErrAborted signals that confirmation ended without approval.
var ErrAborted = errors.New("publish aborted")

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	nullInfo, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(info, nullInfo)
}

func outputIsTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	nullInfo, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(info, nullInfo)
}

func colorEnabled(terminal bool) bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return terminal && !noColor
}

func previewMessage(message string) string {
	line, _, multiline := strings.Cut(message, "\n")
	if multiline {
		line = strings.TrimSuffix(line, "\r") + "…"
	}

	return line
}

func writePlanFiles(out io.Writer, entry publish.PlannedEntry) error {
	if _, err := fmt.Fprintf(out, "\n%s:\n", entry.Name); err != nil {
		return err
	}
	for _, file := range entry.Files {
		if _, err := fmt.Fprintf(out, "  %s %s\n", file.Status, file.Path); err != nil {
			return err
		}
	}
	if entry.Message != "" {
		if _, err := fmt.Fprintf(out, "  message:\n    %s\n", strings.ReplaceAll(entry.Message, "\n", "\n    ")); err != nil {
			return err
		}
	}

	return nil
}

func writePlan(out io.Writer, plan *publish.PlanResult, color bool) error {
	return writePlanSubset(out, plan, color, nil)
}

func writePlanSubset(out io.Writer, plan *publish.PlanResult, color bool, subset map[int]struct{}) error {
	visible := make([]int, 0, len(plan.Entries))
	for i := range plan.Entries {
		if subset == nil {
			visible = append(visible, i)
		} else if _, ok := subset[i]; ok {
			visible = append(visible, i)
		}
	}
	if _, err := fmt.Fprintf(out, "Plan for %d %s\n", len(visible), planPlural(len(visible))); err != nil {
		return err
	}
	for _, notice := range plan.Notices {
		if _, err := fmt.Fprintln(out, notice); err != nil {
			return err
		}
	}
	for _, problem := range plan.Problems {
		icon := "⚠"
		if problem.Severity == "error" {
			icon = "✖"
		}
		line := problem.Message
		if problem.Entry != "" {
			line = problem.Entry + ": " + line
		}
		if _, err := fmt.Fprintf(out, "%s %s\n", icon, line); err != nil {
			return err
		}
	}

	width := 0
	for _, index := range visible {
		if len(plan.Entries[index].Name) > width {
			width = len(plan.Entries[index].Name)
		}
	}
	numbers := displayNumbers(plan.Entries)
	createsPublic := false
	for _, index := range visible {
		entry := plan.Entries[index]
		if entry.CreatesPublic && (entry.Action == publish.ActionCreate || entry.Action == publish.ActionUpdate) {
			createsPublic = true
		}
		lineNumber := "-"
		if number, ok := numbers[index]; ok {
			lineNumber = strconv.Itoa(number)
		}
		action := string(entry.Action)
		if color {
			switch entry.Action {
			case publish.ActionCreate, publish.ActionUpdate:
				action = ansiGreen + action + ansiReset
			case publish.ActionBlocked:
				action = ansiRed + action + ansiReset
			case publish.ActionUnchanged:
				action = ansiDim + action + ansiReset
			}
		}
		actionPadding := strings.Repeat(" ", max(0, 10-len(string(entry.Action))))
		if color {
			action = action + actionPadding
		} else {
			action = fmt.Sprintf("%-10s", action)
		}
		if _, err := fmt.Fprintf(out, "  %-2s %-*s %-4s %s", lineNumber, width, entry.Name, entry.Kind, action); err != nil {
			return err
		}
		if delta := plannedDelta(entry); delta != "" {
			if _, err := fmt.Fprintf(out, "  %s", delta); err != nil {
				return err
			}
		}
		if entry.Message != "" && entry.Mode != "subtree" && (entry.Action == publish.ActionCreate || entry.Action == publish.ActionUpdate) {
			if _, err := fmt.Fprintf(out, "  %q", previewMessage(entry.Message)); err != nil {
				return err
			}
		}
		if entry.CreatesPublic && (entry.Action == publish.ActionCreate || entry.Action == publish.ActionUpdate) {
			label := "NEW PUBLIC REPO"
			if entry.Kind == "gist" {
				label = "NEW PUBLIC GIST"
			}
			if color {
				label = ansiYellow + label + ansiReset
			}
			target := entry.Target
			if entry.Kind == "repo" {
				target = "github.com/" + entry.Target
			}
			if _, err := fmt.Fprintf(out, "  %s %s", label, target); err != nil {
				return err
			}
		} else if entry.Target != "" {
			if _, err := fmt.Fprintf(out, "  %s", entry.Target); err != nil {
				return err
			}
		}
		if entry.Action == publish.ActionBlocked && entry.BlockReason != "" {
			reason := entry.BlockReason
			if len(entry.BlockDetails) > 0 {
				reason += ": " + strings.Join(entry.BlockDetails, ", ")
			}
			if _, err := fmt.Fprintf(out, "  %s", reason); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
	}
	if createsPublic {
		if _, err := fmt.Fprintln(out, "Creating public repositories and gists cannot be undone by this tool."); err != nil {
			return err
		}
	}

	return nil
}

func planPlural(count int) string {
	if count == 1 {
		return "entry"
	}
	return "entries"
}

const (
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiDim    = "\x1b[2m"
	ansiReset  = "\x1b[0m"
)

func plannedDelta(entry publish.PlannedEntry) string {
	parts := make([]string, 0, 3)
	if entry.Added > 0 {
		parts = append(parts, fmt.Sprintf("+%d", entry.Added))
	}
	if entry.Changed > 0 {
		parts = append(parts, fmt.Sprintf("~%d", entry.Changed))
	}
	if entry.Removed > 0 {
		parts = append(parts, fmt.Sprintf("-%d", entry.Removed))
	}

	return strings.Join(parts, " ")
}

func displayNumbers(entries []publish.PlannedEntry) map[int]int {
	numbers := make(map[int]int, len(entries))
	number := 0
	for index, entry := range entries {
		if entry.Action == publish.ActionUnchanged || entry.Action == publish.ActionSkipped {
			continue
		}
		number++
		numbers[index] = number
	}

	return numbers
}

func confirmPlan(ctx context.Context, in io.Reader, out io.Writer, plan *publish.PlanResult) ([]int, error) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	reader := bufio.NewReader(in)
	invalid := 0
	selected := plan.ChangedIndexes()
	subset := map[int]struct{}(nil)
	prompt := fmt.Sprintf("Publish %d changes? [y/N/d/s] ", len(selected))

	for {
		if _, err := fmt.Fprint(out, prompt); err != nil {
			return nil, err
		}
		answer, err := readAnswer(ctx, reader)
		if err != nil {
			if errors.Is(err, ErrAborted) {
				return abortPlan(out)
			}
			return nil, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			return selected, nil
		case "", "n", "no":
			return abortPlan(out)
		case "d":
			for _, index := range visibleIndexes(plan.Entries, subset) {
				if err := writePlanFiles(out, plan.Entries[index]); err != nil {
					return nil, err
				}
			}
		case "s":
			selected, subset, err = selectChanges(ctx, reader, out, plan)
			if err != nil {
				if errors.Is(err, ErrAborted) {
					return abortPlan(out)
				}
				return nil, err
			}
			if err := writePlanSubset(out, plan, colorEnabled(outputIsTerminal(out)), subset); err != nil {
				return nil, err
			}
			prompt = fmt.Sprintf("Publish %d changes? [y/N] ", len(selected))
		default:
			invalid++
			if _, err := fmt.Fprintln(out, "Please answer y, n, d or s."); err != nil {
				return nil, err
			}
			if invalid >= 3 {
				return abortPlan(out)
			}
		}
	}
}

func selectChanges(ctx context.Context, reader *bufio.Reader, out io.Writer, plan *publish.PlanResult) ([]int, map[int]struct{}, error) {
	numbersByIndex := displayNumbers(plan.Entries)
	numbers := make(map[int]int, len(numbersByIndex))
	for index, number := range numbersByIndex {
		numbers[number] = index
	}
	selectable := make(map[int]int)
	if _, err := fmt.Fprintln(out, "Changed entries:"); err != nil {
		return nil, nil, err
	}
	for index, entry := range plan.Entries {
		number, numbered := numbersByIndex[index]
		if !numbered {
			continue
		}
		if entry.Action == publish.ActionCreate || entry.Action == publish.ActionUpdate {
			selectable[number] = index
		}
		if _, err := fmt.Fprintf(out, "  %d  %s  %s\n", number, entry.Name, entry.Action); err != nil {
			return nil, nil, err
		}
	}
	for {
		if _, err := fmt.Fprint(out, "Select changes (e.g. 1,3 or 1-3): "); err != nil {
			return nil, nil, err
		}
		answer, err := readAnswer(ctx, reader)
		if err != nil {
			return nil, nil, err
		}
		indexes, err := parseSelection(answer, numbers, selectable)
		if err != nil {
			if _, writeErr := fmt.Fprintf(out, "Invalid selection: %v\n", err); writeErr != nil {
				return nil, nil, writeErr
			}
			continue
		}
		subset := make(map[int]struct{}, len(indexes))
		for _, index := range indexes {
			subset[index] = struct{}{}
		}
		return indexes, subset, nil
	}
}

func parseSelection(input string, numbers, selectable map[int]int) ([]int, error) {
	input = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, input)
	if input == "" {
		return nil, ErrAborted
	}
	selected := make(map[int]struct{})
	for _, part := range strings.Split(input, ",") {
		if part == "" {
			return nil, errors.New("empty selection")
		}
		bounds := strings.Split(part, "-")
		if len(bounds) > 2 {
			return nil, fmt.Errorf("invalid range %q", part)
		}
		start, err := strconv.Atoi(bounds[0])
		if err != nil || start < 1 {
			return nil, fmt.Errorf("invalid entry %q", bounds[0])
		}
		end := start
		if len(bounds) == 2 {
			end, err = strconv.Atoi(bounds[1])
			if err != nil || end < start {
				return nil, fmt.Errorf("invalid range %q", part)
			}
		}
		for number := start; number <= end; number++ {
			index, numbered := numbers[number]
			if !numbered {
				return nil, fmt.Errorf("entry %d is out of range", number)
			}
			if _, ok := selectable[number]; !ok {
				return nil, fmt.Errorf("entry %d is blocked or cannot be selected", number)
			}
			selected[index] = struct{}{}
		}
	}
	indexes := make([]int, 0, len(selected))
	for index := range selected {
		indexes = append(indexes, index)
	}
	for i := 0; i < len(indexes); i++ {
		for j := i + 1; j < len(indexes); j++ {
			if indexes[j] < indexes[i] {
				indexes[i], indexes[j] = indexes[j], indexes[i]
			}
		}
	}

	return indexes, nil
}

func readAnswer(ctx context.Context, reader *bufio.Reader) (string, error) {
	type answer struct {
		line string
		err  error
	}
	read := make(chan answer, 1)
	go func() {
		line, err := reader.ReadString('\n')
		read <- answer{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ErrAborted
	case result := <-read:
		if errors.Is(result.err, io.EOF) {
			return "", ErrAborted
		}
		if result.err != nil {
			return "", result.err
		}
		return result.line, nil
	}
}

func abortPlan(out io.Writer) ([]int, error) {
	if _, err := fmt.Fprintln(out, "Aborted. Nothing was published."); err != nil {
		return nil, err
	}
	return nil, ErrAborted
}

func visibleIndexes(entries []publish.PlannedEntry, subset map[int]struct{}) []int {
	indexes := make([]int, 0, len(entries))
	for index := range entries {
		if subset == nil {
			indexes = append(indexes, index)
		} else if _, ok := subset[index]; ok {
			indexes = append(indexes, index)
		}
	}

	return indexes
}
