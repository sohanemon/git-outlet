package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sohanemon/git-outlet/internal/check"
	"github.com/sohanemon/git-outlet/internal/config"
	"github.com/sohanemon/git-outlet/internal/gist"
	"github.com/sohanemon/git-outlet/internal/repo"
	"github.com/sohanemon/git-outlet/internal/scan"
	"github.com/sohanemon/git-outlet/internal/state"
)

type Action string

const (
	ActionCreate    Action = "create"
	ActionUpdate    Action = "update"
	ActionUnchanged Action = "unchanged"
	ActionBlocked   Action = "blocked"
	ActionSkipped   Action = "skipped"
)

type PlannedFile struct {
	Status string
	Path   string
}

type PlannedEntry struct {
	Name          string
	Kind          check.Kind
	Mode          string
	Action        Action
	Added         int
	Changed       int
	Removed       int
	Files         []PlannedFile
	Message       string
	Target        string
	CreatesPublic bool
	BlockReason   string
	BlockDetails  []string
	Fingerprint   string
	entry         config.Entry
}

type PlanResult struct {
	Entries  []PlannedEntry
	Problems []check.Problem
	Notices  []string
	opts     Options
	scanning bool
}

type Result struct {
	Published int
	Unchanged int
	Skipped   int
	Failed    int
}

func Plan(ctx context.Context, opts Options) (*PlanResult, error) {
	opts.Config = cloneConfig(opts.Config)
	if err := validateOnly(opts.Config, opts.Only); err != nil {
		return nil, err
	}

	report, err := check.Run(ctx, opts.Config, check.Options{Root: opts.Root})
	if err != nil {
		return nil, err
	}

	problems := filterProblems(report.Problems, opts.Only)
	for _, problem := range problems {
		if problem.Entry == "" && problem.Severity == check.SeverityError {
			return nil, fmt.Errorf("publish stopped: %s", problem.Message)
		}
	}

	st, err := state.Load(filepath.Join(opts.Root, stateFile))
	if err != nil {
		return nil, err
	}

	plan := &PlanResult{Problems: problems, opts: opts}
	var notices strings.Builder
	scanning := false
	if opts.Config.Settings.SecretScan && opts.Scan != nil {
		switch _, err := opts.Scan.Version(ctx); {
		case errors.Is(err, scan.ErrUnavailable):
			plan.Notices = append(plan.Notices, "⚠ secret scan skipped: gitleaks is not installed")
		case err != nil:
			return nil, fmt.Errorf("detect gitleaks: %w", err)
		default:
			scanning = true
		}
	}
	plan.scanning = scanning
	r := &run{
		ctx:       ctx,
		opts:      opts,
		state:     st,
		statePath: filepath.Join(opts.Root, stateFile),
		scanning:  scanning,
		out:       &notices,
	}

	kinds := make(map[string]check.Kind, len(report.Entries))
	failed := make(map[string]bool, len(report.Entries))
	for _, entry := range report.Entries {
		kinds[entry.Name] = entry.Kind
		failed[entry.Name] = entry.Failed
	}
	problemsByEntry := make(map[string][]check.Problem)
	for _, problem := range problems {
		if problem.Entry != "" {
			problemsByEntry[problem.Entry] = append(problemsByEntry[problem.Entry], problem)
		}
	}

	for _, entry := range opts.Config.Entries {
		if opts.Only != "" && entry.Name != opts.Only {
			continue
		}
		kind := kinds[entry.Name]
		planned := PlannedEntry{Name: entry.Name, Kind: kind, entry: entry}
		if kind == check.KindGist {
			planned.Mode = "gist"
		} else {
			planned.Mode = entry.Mode
			if planned.Mode == "" {
				planned.Mode = opts.Config.Settings.Mode
			}
		}
		if failed[entry.Name] {
			messages := make([]string, 0, len(problemsByEntry[entry.Name]))
			for _, problem := range problemsByEntry[entry.Name] {
				if problem.Severity == check.SeverityError {
					messages = append(messages, problem.Message)
				}
			}
			planned.setBlocked(errors.New(strings.Join(messages, "; ")))
		} else if kind == check.KindGist {
			r.planGist(&planned)
		} else if planned.Mode == "subtree" {
			r.planSubtree(&planned)
		} else {
			r.planCopy(&planned)
		}
		plan.Entries = append(plan.Entries, planned)
	}
	if notices.Len() > 0 {
		plan.Notices = append(plan.Notices, strings.TrimSpace(notices.String()))
	}

	return plan, nil
}

func cloneConfig(cfg *config.Config) *config.Config {
	cloned := *cfg
	cloned.Entries = append([]config.Entry(nil), cfg.Entries...)
	for i := range cloned.Entries {
		cloned.Entries[i].Include = append([]string(nil), cfg.Entries[i].Include...)
		cloned.Entries[i].Exclude = append([]string(nil), cfg.Entries[i].Exclude...)
	}

	return &cloned
}

func (r *run) planGist(planned *PlannedEntry) {
	entry := planned.entry
	if err := r.guardSuspicious(entry.Name, []string{entry.Path}); err != nil {
		planned.setBlocked(err)
		return
	}
	path := filepath.Join(r.opts.Root, filepath.FromSlash(filepath.Clean(entry.Path)))
	if err := r.scanFile(entry.Name, path); err != nil {
		planned.setBlocked(err)
		return
	}
	content, err := os.ReadFile(path)
	if err != nil {
		planned.setBlocked(fmt.Errorf("read %s: %w", entry.Path, err))
		return
	}
	description := gist.Description(entry.Name, entry.Description)
	planned.Fingerprint = hashContent(description, entry.Name, string(content))
	planned.Target = entry.Name
	previous, recorded := r.state.Get(entry.Name)
	if recorded && previous.GistID != "" {
		planned.Target = gistURL(previous.GistID)
	}
	if recorded && previous.GistID != "" && previous.Hash == planned.Fingerprint {
		planned.Action = ActionUnchanged
		planned.Files = []PlannedFile{{Status: "=", Path: entry.Path}}
		return
	}
	if recorded && previous.GistID != "" {
		planned.Action = ActionUpdate
		planned.Files = []PlannedFile{{Status: "~", Path: entry.Path}}
		return
	}
	planned.Action = ActionCreate
	planned.CreatesPublic = true
	planned.Files = []PlannedFile{{Status: "+", Path: entry.Path}}
}

func (r *run) planCopy(planned *PlannedEntry) {
	entry := planned.entry
	owner, err := r.repoOwner()
	if err != nil {
		planned.setBlocked(err)
		return
	}
	planned.Target = owner + "/" + entry.Name
	rel := filepath.ToSlash(filepath.Clean(entry.Path))
	files, err := r.selectedFiles(entry)
	if err != nil {
		planned.setBlocked(err)
		return
	}
	if err := r.guardSuspicious(entry.Name, files); err != nil {
		planned.setBlocked(err)
		return
	}
	if err := r.scanFiles(entry.Name, rel, files); err != nil {
		planned.setBlocked(err)
		return
	}
	message, err := r.entryMessage(entry)
	if err != nil {
		planned.setBlocked(err)
		return
	}
	planned.Message = message
	source := filepath.Join(r.opts.Root, filepath.FromSlash(rel))
	planned.Fingerprint, err = copyFingerprint(source, files, message)
	if err != nil {
		planned.setBlocked(err)
		return
	}
	info, err := r.inspectRepo(owner, entry.Name)
	if err != nil {
		planned.setBlocked(err)
		return
	}
	planned.CreatesPublic = !info.Exists
	if !info.Exists {
		planned.Action = ActionCreate
		planned.Added = len(files)
		planned.Files = addFileStatuses(files)
		return
	}
	preview, err := r.opts.Repos.Preview(r.ctx, repo.SyncOptions{
		Owner: owner, Name: entry.Name, Branch: r.opts.Config.Settings.Branch,
		Message: message, Source: source, Files: files,
	})
	if err != nil {
		planned.setBlocked(err)
		return
	}
	planned.Target = preview.URL
	planned.Added, planned.Changed, planned.Removed = preview.Added, preview.Changed, preview.Removed
	planned.Files = plannedFiles(preview.Files)
	if noDelta(preview) {
		planned.Action = ActionUnchanged
		if len(planned.Files) == 0 {
			planned.Files = unchangedFileStatuses(files)
		}
		return
	}
	planned.Action = ActionUpdate
}

func (r *run) planSubtree(planned *PlannedEntry) {
	entry := planned.entry
	owner, err := r.repoOwner()
	if err != nil {
		planned.setBlocked(err)
		return
	}
	planned.Target = owner + "/" + entry.Name
	rel := filepath.ToSlash(filepath.Clean(entry.Path))
	files, err := r.selectedFiles(entry)
	if err != nil {
		planned.setBlocked(err)
		return
	}
	if err := r.guardSuspicious(entry.Name, files); err != nil {
		planned.setBlocked(err)
		return
	}
	if err := r.scanHistory(entry.Name, rel); err != nil {
		planned.setBlocked(err)
		return
	}
	commit, err := r.opts.Repos.SubtreeSplit(r.ctx, r.opts.Root, rel)
	if err != nil {
		planned.setBlocked(err)
		return
	}
	planned.Fingerprint = commit
	info, err := r.inspectRepo(owner, entry.Name)
	if err != nil {
		planned.setBlocked(err)
		return
	}
	planned.CreatesPublic = !info.Exists
	previous := r.recordedCommit(entry.Name, owner)
	if previous == commit && info.Exists && !info.Empty {
		planned.Action = ActionUnchanged
		planned.Files = unchangedFileStatuses(files)
		return
	}
	planned.Action = ActionUpdate
	if !info.Exists {
		planned.Action = ActionCreate
	}
	changes, err := subtreeFileChanges(r.ctx, r.opts.Root, rel, previous, commit)
	if err != nil {
		planned.setBlocked(err)
		return
	}
	planned.Files = changes
	for _, file := range changes {
		switch file.Status {
		case "+":
			planned.Added++
		case "-":
			planned.Removed++
		default:
			planned.Changed++
		}
	}
}

func (p *PlannedEntry) setBlocked(err error) {
	p.Action = ActionBlocked
	p.BlockReason = shortReason(err)
	var blocked *blockedError
	if errors.As(err, &blocked) {
		p.BlockDetails = append([]string(nil), blocked.lines...)
	}
}

func (p *PlanResult) ChangedIndexes() []int {
	indexes := make([]int, 0, len(p.Entries))
	for i, entry := range p.Entries {
		if entry.Action == ActionCreate || entry.Action == ActionUpdate {
			indexes = append(indexes, i)
		}
	}

	return indexes
}

func (p *PlanResult) BlockedCount() int {
	count := 0
	for _, entry := range p.Entries {
		if entry.Action == ActionBlocked {
			count++
		}
	}

	return count
}

func Apply(ctx context.Context, plan *PlanResult, selected []int) (*Result, error) {
	if plan == nil {
		return nil, errors.New("plan is nil")
	}
	selectedSet := make(map[int]struct{}, len(selected))
	for _, index := range selected {
		if index < 0 || index >= len(plan.Entries) {
			return nil, fmt.Errorf("plan entry index %d is out of range", index)
		}
		selectedSet[index] = struct{}{}
	}
	st, err := state.Load(filepath.Join(plan.opts.Root, stateFile))
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	applyOpts := plan.opts
	applyOpts.DryRun = false
	r := &run{
		ctx:       ctx,
		opts:      applyOpts,
		state:     st,
		statePath: filepath.Join(plan.opts.Root, stateFile),
		width:     planWidth(plan.Entries),
		scanning:  plan.scanning,
		out:       &out,
	}
	for _, entry := range plan.Entries {
		if entry.Action == ActionUnchanged {
			r.counts.unchanged++
		}
		if entry.Action == ActionSkipped {
			r.counts.skipped++
		}
	}

	for index, entry := range plan.Entries {
		if entry.Action == ActionBlocked {
			block := &blockedError{reason: entry.BlockReason, lines: entry.BlockDetails}
			r.fail(entry.Name, string(entry.Kind), entry.BlockReason, block)
			continue
		}
		if entry.Action != ActionCreate && entry.Action != ActionUpdate {
			continue
		}
		if _, ok := selectedSet[index]; !ok {
			r.counts.skipped++
			continue
		}
		fingerprint, err := currentFingerprint(ctx, plan.opts, entry)
		if err != nil || fingerprint != entry.Fingerprint {
			message := "changed since you approved it; run again"
			if err != nil {
				message += ": " + shortReason(err)
			}
			r.fail(entry.Name, string(entry.Kind), message, errors.New(message))
			continue
		}
		r.publish(entry.entry, entry.Kind)
	}
	fmt.Fprintf(&out, "\n%s\n", r.summary())
	if err := writeOutput(plan.opts.Out, out.String()); err != nil {
		return nil, err
	}
	result := &Result{
		Published: r.counts.published,
		Unchanged: r.counts.unchanged,
		Skipped:   r.counts.skipped,
		Failed:    r.counts.failed,
	}

	return result, errors.Join(r.failures...)
}

func currentFingerprint(ctx context.Context, opts Options, entry PlannedEntry) (string, error) {
	switch entry.Kind {
	case check.KindGist:
		path := filepath.Join(opts.Root, filepath.FromSlash(filepath.Clean(entry.entry.Path)))
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return hashContent(gist.Description(entry.Name, entry.entry.Description), entry.Name, string(content)), nil
	case check.KindRepo:
		mode := entry.Mode
		if mode == "subtree" {
			return opts.Repos.SubtreeSplit(ctx, opts.Root, filepath.ToSlash(filepath.Clean(entry.entry.Path)))
		}
		r := &run{ctx: ctx, opts: opts}
		files, err := r.selectedFiles(entry.entry)
		if err != nil {
			return "", err
		}
		message, err := r.entryMessage(entry.entry)
		if err != nil {
			return "", err
		}
		source := filepath.Join(opts.Root, filepath.FromSlash(filepath.Clean(entry.entry.Path)))
		return copyFingerprint(source, files, message)
	default:
		return "", fmt.Errorf("unknown plan kind %q", entry.Kind)
	}
}

func copyFingerprint(root string, files []string, message string) (string, error) {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, file := range sorted {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", file, err)
		}
		contentHash := sha256.Sum256(content)
		fmt.Fprintf(h, "%d:%s:%x;", len(file), file, contentHash)
	}
	fmt.Fprintf(h, "%d:%s", len(message), message)

	return hex.EncodeToString(h.Sum(nil)), nil
}

func plannedFiles(changes []repo.FileChange) []PlannedFile {
	files := make([]PlannedFile, 0, len(changes))
	for _, change := range changes {
		files = append(files, PlannedFile{Status: change.Status, Path: change.Path})
	}

	return files
}

func addFileStatuses(files []string) []PlannedFile {
	planned := make([]PlannedFile, 0, len(files))
	for _, file := range files {
		planned = append(planned, PlannedFile{Status: "+", Path: file})
	}

	return planned
}

func unchangedFileStatuses(files []string) []PlannedFile {
	planned := make([]PlannedFile, 0, len(files))
	for _, file := range files {
		planned = append(planned, PlannedFile{Status: "=", Path: file})
	}

	return planned
}

func subtreeFileChanges(ctx context.Context, root, path, from, to string) ([]PlannedFile, error) {
	var args []string
	if from == "" {
		args = []string{"ls-tree", "-r", "--name-only", "-z", to}
	} else if from == to {
		args = []string{"ls-tree", "-r", "--name-only", "-z", to}
	} else {
		args = []string{"diff", "--no-renames", "--name-status", "-z", from, to}
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list subtree changes: %w", err)
	}
	parts := strings.Split(string(out), "\x00")
	files := make([]PlannedFile, 0, len(parts))
	if from == "" || from == to {
		status := "+"
		if from == to {
			status = "="
		}
		for _, file := range parts {
			if file != "" {
				files = append(files, PlannedFile{Status: status, Path: filepath.ToSlash(filepath.Join(path, file))})
			}
		}
		return files, nil
	}
	for i := 0; i+1 < len(parts); {
		status := parts[i]
		file := parts[i+1]
		i += 2
		if status == "" || file == "" {
			continue
		}
		mark := "~"
		switch status[0] {
		case 'A':
			mark = "+"
		case 'D':
			mark = "-"
		}
		files = append(files, PlannedFile{Status: mark, Path: filepath.ToSlash(filepath.Join(path, file))})
	}

	return files, nil
}

func planWidth(entries []PlannedEntry) int {
	width := 0
	for _, entry := range entries {
		if len(entry.Name) > width {
			width = len(entry.Name)
		}
	}

	return width + 1
}

func writePlanFiles(out io.Writer, entry PlannedEntry) error {
	if _, err := fmt.Fprintf(out, "\n%s:\n", entry.Name); err != nil {
		return err
	}
	for _, file := range entry.Files {
		if _, err := fmt.Fprintf(out, "  %s %s\n", file.Status, file.Path); err != nil {
			return err
		}
	}

	return nil
}
