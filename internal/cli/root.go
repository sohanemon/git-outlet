package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sohanemon/git-outlet/internal/config"
	"github.com/sohanemon/git-outlet/internal/gist"
	"github.com/sohanemon/git-outlet/internal/gitx"
	"github.com/sohanemon/git-outlet/internal/publish"
	"github.com/sohanemon/git-outlet/internal/repo"
	"github.com/sohanemon/git-outlet/internal/scan"
)

type options struct {
	dryRun          bool
	yes             bool
	message         string
	only            string
	adopt           bool
	allowSuspicious bool
	configPath      string
	isTerminal      func() bool
}

// NOTE: Execute runs the git-outlet root command.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	return newRootCmdWithTerminal(stdinIsTerminal)
}

func newRootCmdWithTerminal(isTerminal func() bool) *cobra.Command {
	opts := &options{isTerminal: isTerminal}

	cmd := &cobra.Command{
		Use:     "git-outlet",
		Version: versionString(),
		Short:   "Publish selected files and folders from a private repository",
		Long: `git-outlet publishes selected files and folders from a private repository.

Files are pushed to public Gists and folders to public repositories, driven by
an outlet.yml configuration. Run it as "git outlet" or "git-outlet".`,
		// NOTE: main prints the returned error, so cobra must not print it again.
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPublish(cmd, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "show what would be published without publishing")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "publish without confirmation")
	cmd.Flags().StringVarP(&opts.message, "message", "m", "", "commit message for the published Gist or repository")
	cmd.Flags().StringVar(&opts.only, "only", "", "publish only the named outlet entry")
	cmd.Flags().BoolVar(&opts.adopt, "adopt", false, "take over an existing public repository not created by git-outlet")
	cmd.Flags().BoolVar(&opts.allowSuspicious, "allow-suspicious", false, "publish files whose names look like secrets")
	cmd.Flags().StringVar(&opts.configPath, "config", "outlet.yml", "path to the outlet configuration file")

	cmd.AddCommand(newCheckCmd())

	return cmd
}

func runPublish(cmd *cobra.Command, opts *options) error {
	if cmd.Flags().Changed("message") && strings.TrimSpace(opts.message) == "" {
		return errors.New("--message cannot be empty or whitespace")
	}
	isTerminal := opts.isTerminal
	if isTerminal == nil {
		isTerminal = stdinIsTerminal
	}
	if !opts.dryRun && !opts.yes && !isTerminal() {
		return errors.New("confirmation needed: run in a terminal or pass --yes")
	}

	if !opts.dryRun {
		if _, err := exec.LookPath("gh"); err != nil {
			return errors.New("gh is required to publish; install the GitHub CLI and run `gh auth login`")
		}
	}

	root, err := gitx.Root(cmd.Context(), ".")
	if err != nil {
		return err
	}

	path := filepath.Join(root, "outlet.yml")
	if cmd.Flags().Changed("config") {
		path = opts.configPath
	}

	cfg, err := config.Load(path)
	if err != nil {
		var cfgErr *config.Error
		if !errors.As(err, &cfgErr) {
			return err
		}
		if err := writeConfigProblems(cmd.OutOrStdout(), cfgErr); err != nil {
			return err
		}
		return errors.New("config invalid")
	}

	plan, err := publish.Plan(cmd.Context(), publish.Options{
		Config:          cfg,
		Root:            root,
		DryRun:          opts.dryRun,
		Only:            opts.only,
		Adopt:           opts.adopt,
		AllowSuspicious: opts.allowSuspicious,
		Message:         opts.message,
		Out:             cmd.OutOrStdout(),
		Gist:            gist.New(gist.ExecRunner{}),
		Repos:           repo.New(repo.ExecRunner{}),
		Scan:            scan.New(scan.ExecRunner{}),
	})
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if err := writePlan(out, plan, colorEnabled(outputIsTerminal(out))); err != nil {
		return err
	}
	if opts.dryRun {
		return nil
	}
	if len(plan.ChangedIndexes()) == 0 {
		if plan.BlockedCount() > 0 {
			_, err := publish.Apply(cmd.Context(), plan, nil)
			return err
		}
		return nil
	}
	selected := plan.ChangedIndexes()
	if !opts.yes {
		selected, err = confirmPlan(cmd.Context(), cmd.InOrStdin(), out, plan)
		if err != nil {
			return err
		}
	}
	_, err = publish.Apply(cmd.Context(), plan, selected)

	return err
}

func writeConfigProblems(w io.Writer, err *config.Error) error {
	var out strings.Builder
	for _, problem := range err.Problems {
		fmt.Fprintf(&out, "✖ %s:%d: %s\n", err.Path, problem.Line, problem.Message)
	}

	_, writeErr := io.WriteString(w, out.String())

	return writeErr
}
