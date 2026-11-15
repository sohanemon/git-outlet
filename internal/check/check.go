package check

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sohanemon/git-outlet/internal/config"
	"github.com/sohanemon/git-outlet/internal/gitx"
	"github.com/sohanemon/git-outlet/internal/selection"
)

// Severity classifies a problem found while checking the config.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Kind is the publish target of an entry.
type Kind string

const (
	KindGist Kind = "gist"
	KindRepo Kind = "repo"
)

// Problem is one issue found while checking the config.
type Problem struct {
	Severity Severity
	Entry    string
	Message  string
}

// Entry summarises one publish entry for the check output.
type Entry struct {
	Name     string
	Kind     Kind
	Files    int
	Excluded int
	Failed   bool
}

// Report is the result of checking a whole config.
type Report struct {
	Entries  []Entry
	Problems []Problem
}

// Errors returns the number of error problems.
func (r Report) Errors() int {
	return r.count(SeverityError)
}

// Warnings returns the number of warning problems.
func (r Report) Warnings() int {
	return r.count(SeverityWarning)
}

func (r Report) count(severity Severity) int {
	n := 0
	for _, problem := range r.Problems {
		if problem.Severity == severity {
			n++
		}
	}

	return n
}

// Run checks every entry of cfg against the repository at root.
func Run(ctx context.Context, cfg *config.Config, root string) (Report, error) {
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Report{}, fmt.Errorf("resolve repository root %q: %w", root, err)
	}

	report := Report{Entries: make([]Entry, 0, len(cfg.Entries))}

	for _, entry := range cfg.Entries {
		c := &collector{report: &report, entry: entry.Name}
		status := Entry{Name: entry.Name}
		checkEntry(ctx, cfg.Settings, root, rootResolved, entry, &status, c)
		status.Failed = c.failed
		report.Entries = append(report.Entries, status)
	}

	return report, nil
}

type collector struct {
	report *Report
	entry  string
	failed bool
}

func (c *collector) errorf(format string, args ...any) {
	c.add(SeverityError, format, args...)
	c.failed = true
}

func (c *collector) warnf(format string, args ...any) {
	c.add(SeverityWarning, format, args...)
}

func (c *collector) add(severity Severity, format string, args ...any) {
	c.report.Problems = append(c.report.Problems, Problem{
		Severity: severity,
		Entry:    c.entry,
		Message:  fmt.Sprintf(format, args...),
	})
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

func checkEntry(ctx context.Context, settings config.Settings, root, rootResolved string, entry config.Entry, status *Entry, c *collector) {
	if filepath.IsAbs(entry.Path) {
		c.errorf("path %q is absolute", entry.Path)
		return
	}

	rel := filepath.ToSlash(filepath.Clean(entry.Path))
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if !within(root, abs) {
		c.errorf("path %q escapes the repository root", entry.Path)
		return
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			c.errorf("path %q does not exist", entry.Path)
		} else {
			c.errorf("path %q: %v", entry.Path, err)
		}
		return
	}
	if !within(rootResolved, resolved) {
		c.errorf("path %q resolves outside the repository root", entry.Path)
		return
	}

	info, err := os.Stat(resolved)
	if err != nil {
		c.errorf("path %q: %v", entry.Path, err)
		return
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		c.errorf("path %q is not a file or folder", entry.Path)
		return
	}

	folder := info.IsDir()
	if folder {
		status.Kind = KindRepo
		c.checkName(entry.Name, true)
	} else {
		status.Kind = KindGist
		c.checkName(entry.Name, false)
	}

	mode := entry.Mode
	if mode == "" {
		mode = settings.Mode
	}
	subtree := mode == "subtree"

	if subtree && (len(entry.Include) > 0 || len(entry.Exclude) > 0) {
		c.errorf("subtree mode sends the whole folder and cannot use include or exclude")
	}
	if !folder && entry.Mode == "subtree" {
		c.errorf("mode %q is not allowed on a file entry; Gists always use copy", entry.Mode)
	}
	if folder && subtree && entry.Message != "" {
		c.warnf("message is ignored for subtree entries")
	}
	if !folder {
		if len(entry.Include) > 0 {
			c.warnf("include is ignored for file entries")
		}
		if len(entry.Exclude) > 0 {
			c.warnf("exclude is ignored for file entries")
		}
	}

	files, skipped, err := gitx.TrackedFiles(ctx, root, rel)
	if err != nil {
		c.errorf("list tracked files: %v", err)
		return
	}
	if len(files) == 0 {
		if folder {
			c.errorf("folder %q has no tracked files", entry.Path)
		} else {
			c.errorf("file %q is not tracked by git", entry.Path)
		}
		return
	}

	if !folder {
		status.Files = 1
		return
	}

	if skipped > 0 {
		c.warnf("skipped %d symlink(s) or missing file(s)", skipped)
	}

	// INFO: excluded counts files dropped by exclude, not files skipped by include.
	included, _, err := selection.Filter(files, entry.Include, nil)
	if err != nil {
		c.errorf("%v", err)
		return
	}
	selected, unmatched, err := selection.Filter(files, entry.Include, entry.Exclude)
	if err != nil {
		c.errorf("%v", err)
		return
	}

	for _, pattern := range unmatched.Include {
		c.warnf("include %q matches nothing", pattern)
	}
	for _, pattern := range unmatched.Exclude {
		c.warnf("exclude %q matches nothing", pattern)
	}

	status.Files = len(selected)
	status.Excluded = len(included) - len(selected)
	if len(selected) == 0 {
		c.errorf("no files selected after include and exclude")
	}
}

func (c *collector) checkName(name string, folder bool) {
	if folder {
		if name == "." || name == ".." || !repoName.MatchString(name) {
			c.errorf("name %q is not a valid GitHub repository name", name)
		}
		return
	}

	if name == "" || strings.Contains(name, "/") {
		c.errorf("name %q is not a valid Gist file name", name)
	}
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
