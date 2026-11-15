package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

type options struct {
	dryRun  bool
	message string
	only    string
}

// Execute runs the git-outlet root command.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "git-outlet",
		Short: "Publish selected files and folders from a private repository",
		Long: `git-outlet publishes selected files and folders from a private repository.

Files are pushed to public Gists and folders to public repositories, driven by
an outlet.yml configuration. Run it as "git outlet" or "git-outlet".`,
		// NOTE: main prints the returned error, so cobra must not print it again.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
			return err
		},
	}

	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "show what would be published without publishing")
	cmd.Flags().StringVarP(&opts.message, "message", "m", "", "commit message for the published Gist or repository")
	cmd.Flags().StringVar(&opts.only, "only", "", "publish only the named outlet entry")

	cmd.AddCommand(newCheckCmd())

	return cmd
}
