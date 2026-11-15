package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sohanemon/git-outlet/internal/config"
)

func newCheckCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Verify the outlet configuration and GitHub access",
		Long: `check reads outlet.yml, validates every entry, and verifies that the
GitHub credentials used by git-outlet can write to the configured targets.`,
		Example: `  git-outlet check
  git outlet check`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				var cfgErr *config.Error
				if !errors.As(err, &cfgErr) {
					return err
				}

				for _, problem := range cfgErr.Problems {
					if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s:%d: %s\n", cfgErr.Path, problem.Line, problem.Message); err != nil {
						return err
					}
				}

				return fmt.Errorf("%s: %d problem(s)", cfgErr.Path, len(cfgErr.Problems))
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "ok: %d entries\n", len(cfg.Entries))
			return err
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "outlet.yml", "path to the outlet configuration file")

	return cmd
}
