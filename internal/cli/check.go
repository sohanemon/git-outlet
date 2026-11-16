package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sohanemon/git-outlet/internal/check"
	"github.com/sohanemon/git-outlet/internal/config"
	"github.com/sohanemon/git-outlet/internal/gitx"
	"github.com/sohanemon/git-outlet/internal/scan"
)

func newCheckCmd() *cobra.Command {
	var configPath string
	var scanEnabled bool

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Verify the outlet configuration and tracked files",
		Long: `check reads outlet.yml at the repository root, validates every entry against
the files tracked by git, and reports problems and warnings. No files are
published. With --scan it also runs gitleaks over the selected files and history.`,
		Example: `  git-outlet check
  git-outlet check --scan
  git outlet check`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := gitx.Root(cmd.Context(), ".")
			if err != nil {
				return err
			}

			path := filepath.Join(root, "outlet.yml")
			if cmd.Flags().Changed("config") {
				path = configPath
			}

			var scanner scan.Scanner
			if scanEnabled {
				scanner = scan.New(scan.ExecRunner{})
			}

			report, err := runChecks(cmd, path, root, scanner)
			if err != nil {
				return err
			}
			if err := writeReport(cmd.OutOrStdout(), report); err != nil {
				return err
			}
			if report.Errors() > 0 {
				return errors.New("check failed")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "outlet.yml", "path to the outlet configuration file")
	cmd.Flags().BoolVar(&scanEnabled, "scan", false, "scan selected files and history with gitleaks")

	return cmd
}

func runChecks(cmd *cobra.Command, path, root string, scanner scan.Scanner) (check.Report, error) {
	cfg, err := config.Load(path)
	if err != nil {
		var cfgErr *config.Error
		if !errors.As(err, &cfgErr) {
			return check.Report{}, err
		}

		report := check.Report{}
		for _, problem := range cfgErr.Problems {
			report.Problems = append(report.Problems, check.Problem{
				Severity: check.SeverityError,
				Message:  fmt.Sprintf("%s:%d: %s", cfgErr.Path, problem.Line, problem.Message),
			})
		}

		return report, nil
	}

	return check.Run(cmd.Context(), cfg, check.Options{Root: root, Scan: scanner})
}

func writeReport(w io.Writer, report check.Report) error {
	width := 0
	for _, entry := range report.Entries {
		if len(entry.Name) > width {
			width = len(entry.Name)
		}
	}

	var out strings.Builder
	for _, entry := range report.Entries {
		if entry.Failed {
			continue
		}
		fmt.Fprintf(&out, "✔ %-*s  %s  %s\n", width, entry.Name, entry.Kind, fileCount(entry))
	}
	for _, problem := range report.Problems {
		icon := "⚠"
		if problem.Severity == check.SeverityError {
			icon = "✖"
		}
		if problem.Entry == "" {
			fmt.Fprintf(&out, "%s %s\n", icon, problem.Message)
			continue
		}
		fmt.Fprintf(&out, "%s %s: %s\n", icon, problem.Entry, problem.Message)
	}

	if out.Len() > 0 {
		out.WriteString("\n")
	}
	fmt.Fprintf(&out, "%d %s, %d %s\n", report.Errors(), plural(report.Errors(), "error"), report.Warnings(), plural(report.Warnings(), "warning"))

	_, err := io.WriteString(w, out.String())

	return err
}

func fileCount(entry check.Entry) string {
	count := fmt.Sprintf("%d %s", entry.Files, plural(entry.Files, "file"))
	if entry.Excluded > 0 {
		count += fmt.Sprintf(", %d excluded", entry.Excluded)
	}

	return count
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}

	return word + "s"
}
