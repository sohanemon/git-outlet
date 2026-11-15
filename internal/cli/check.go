package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Verify the outlet configuration and GitHub access",
		Long: `check reads outlet.yml, validates every entry, and verifies that the
GitHub credentials used by git-outlet can write to the configured targets.`,
		Example: `  git-outlet check
  git outlet check`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
			return err
		},
	}
}
