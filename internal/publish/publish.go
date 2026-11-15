package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sohanemon/git-outlet/internal/check"
	"github.com/sohanemon/git-outlet/internal/config"
	"github.com/sohanemon/git-outlet/internal/gist"
	"github.com/sohanemon/git-outlet/internal/state"
)

const stateFile = ".git-outlet-state.json"

// Gister creates and updates the Gists backing file entries.
type Gister interface {
	Create(ctx context.Context, name, description, content string) (id, url string, err error)
	Update(ctx context.Context, id, name, description, content string) (url string, err error)
}

// Options controls one publish run.
type Options struct {
	Config *config.Config
	Root   string
	DryRun bool
	Only   string
	Out    io.Writer
	Gist   Gister
}

// Run publishes the selected entries, continuing after per-entry failures.
func Run(ctx context.Context, opts Options) error {
	if err := validateOnly(opts.Config, opts.Only); err != nil {
		return err
	}

	report, err := check.Run(ctx, opts.Config, opts.Root)
	if err != nil {
		return err
	}

	problems := filterProblems(report.Problems, opts.Only)

	var out strings.Builder
	for _, problem := range problems {
		writeProblem(&out, problem)
	}
	if len(problems) > 0 {
		out.WriteString("\n")
	}

	if failed := countErrors(problems); failed > 0 {
		if err := writeOutput(opts.Out, out.String()); err != nil {
			return err
		}
		return fmt.Errorf("publish stopped: %d %s", failed, plural(failed, "error"))
	}

	st, err := state.Load(filepath.Join(opts.Root, stateFile))
	if err != nil {
		return err
	}

	r := &run{
		ctx:       ctx,
		opts:      opts,
		state:     st,
		statePath: filepath.Join(opts.Root, stateFile),
		width:     nameWidth(opts.Config, opts.Only) + 1,
		out:       &out,
	}

	kinds := make(map[string]check.Kind, len(report.Entries))
	for _, entry := range report.Entries {
		kinds[entry.Name] = entry.Kind
	}

	for _, entry := range opts.Config.Entries {
		if opts.Only != "" && entry.Name != opts.Only {
			continue
		}
		r.publish(entry, kinds[entry.Name])
	}

	fmt.Fprintf(&out, "\n%s\n", r.summary())

	if err := writeOutput(opts.Out, out.String()); err != nil {
		return err
	}

	return errors.Join(r.failures...)
}

type run struct {
	ctx       context.Context
	opts      Options
	state     state.State
	statePath string
	width     int
	out       *strings.Builder
	counts    counts
	failures  []error
}

type counts struct {
	published int
	unchanged int
	skipped   int
	failed    int
}

type result struct {
	status string
	url    string
	err    error
}

func (r *run) publish(entry config.Entry, kind check.Kind) {
	if kind == check.KindRepo {
		fmt.Fprintf(r.out, "- %-*s  repo  skipped (repo publishing not implemented yet)\n", r.width, entry.Name)
		r.counts.skipped++
		return
	}

	res := r.publishFile(entry)
	if res.err != nil {
		fmt.Fprintf(r.out, "✖ %-*s  gist  %s\n", r.width, entry.Name, failureMessage(entry.Name, res.err))
		r.failures = append(r.failures, fmt.Errorf("%s: %w", entry.Name, res.err))
		r.counts.failed++
		return
	}

	if res.url == "" {
		fmt.Fprintf(r.out, "✔ %-*s  gist  %s\n", r.width, entry.Name, res.status)
	} else {
		fmt.Fprintf(r.out, "✔ %-*s  gist  %s    %s\n", r.width, entry.Name, res.status, res.url)
	}

	if res.status == "unchanged" {
		r.counts.unchanged++
	} else {
		r.counts.published++
	}
}

func (r *run) publishFile(entry config.Entry) result {
	path := filepath.Join(r.opts.Root, filepath.FromSlash(filepath.Clean(entry.Path)))
	content, err := os.ReadFile(path)
	if err != nil {
		return result{err: fmt.Errorf("read %s: %w", entry.Path, err)}
	}

	description := gist.Description(entry.Name, entry.Description)
	hash := hashContent(description, entry.Name, string(content))
	previous, recorded := r.state.Get(entry.Name)
	hasGist := recorded && previous.GistID != ""

	if hasGist && previous.Hash == hash {
		return result{status: "unchanged"}
	}

	if r.opts.DryRun {
		if hasGist {
			return result{status: "would update", url: gistURL(previous.GistID)}
		}
		return result{status: "would create"}
	}

	if hasGist {
		url, err := r.opts.Gist.Update(r.ctx, previous.GistID, entry.Name, description, string(content))
		if err != nil {
			return result{err: err}
		}
		if err := r.save(entry.Name, previous.GistID, hash); err != nil {
			return result{err: err}
		}
		return result{status: "updated", url: url}
	}

	id, url, err := r.opts.Gist.Create(r.ctx, entry.Name, description, string(content))
	if err != nil {
		return result{err: err}
	}
	if err := r.save(entry.Name, id, hash); err != nil {
		return result{err: err}
	}

	return result{status: "created", url: url}
}

func (r *run) save(name, id, hash string) error {
	r.state.Set(name, state.Entry{GistID: id, Hash: hash})
	if err := state.Save(r.statePath, r.state); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	return nil
}

func (r *run) summary() string {
	published := "published"
	if r.opts.DryRun {
		published = "would publish"
	}

	return fmt.Sprintf("%d %s, %d unchanged, %d skipped, %d failed",
		r.counts.published, published, r.counts.unchanged, r.counts.skipped, r.counts.failed)
}

func validateOnly(cfg *config.Config, only string) error {
	if only == "" {
		return nil
	}

	names := make([]string, 0, len(cfg.Entries))
	for _, entry := range cfg.Entries {
		if entry.Name == only {
			return nil
		}
		names = append(names, entry.Name)
	}

	if len(names) == 0 {
		return fmt.Errorf("unknown entry %q; the config has no publish entries", only)
	}

	return fmt.Errorf("unknown entry %q; valid entries: %s", only, strings.Join(names, ", "))
}

func filterProblems(problems []check.Problem, only string) []check.Problem {
	if only == "" {
		return problems
	}

	filtered := make([]check.Problem, 0, len(problems))
	for _, problem := range problems {
		if problem.Entry == "" || problem.Entry == only {
			filtered = append(filtered, problem)
		}
	}

	return filtered
}

func countErrors(problems []check.Problem) int {
	n := 0
	for _, problem := range problems {
		if problem.Severity == check.SeverityError {
			n++
		}
	}

	return n
}

func writeProblem(out *strings.Builder, problem check.Problem) {
	icon := "⚠"
	if problem.Severity == check.SeverityError {
		icon = "✖"
	}
	if problem.Entry == "" {
		fmt.Fprintf(out, "%s %s\n", icon, problem.Message)
		return
	}
	fmt.Fprintf(out, "%s %s: %s\n", icon, problem.Entry, problem.Message)
}

func nameWidth(cfg *config.Config, only string) int {
	width := 0
	for _, entry := range cfg.Entries {
		if only != "" && entry.Name != only {
			continue
		}
		if len(entry.Name) > width {
			width = len(entry.Name)
		}
	}

	return width
}

func hashContent(description, name, content string) string {
	sum := sha256.Sum256([]byte(description + name + content))

	return hex.EncodeToString(sum[:])
}

func gistURL(id string) string {
	return "https://gist.github.com/" + id
}

func failureMessage(name string, err error) string {
	var notFound *gist.NotFoundError
	if errors.As(err, &notFound) {
		return fmt.Sprintf("failed: %v; remove %q from %s", notFound, name, stateFile)
	}

	return "failed: " + err.Error()
}

func writeOutput(w io.Writer, text string) error {
	_, err := io.WriteString(w, text)

	return err
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}

	return word + "s"
}
