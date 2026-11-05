package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const (
	statusUpToDate  = "up-to-date"
	statusNeedsSync = "needs-sync"
	statusExists    = "exists"
)

var targetOpts struct {
	tag           string
	checkExisting bool
}

var targetCmd = &cobra.Command{
	Use:   "target",
	Short: "Find the upstream release to sync",
	Long: `Finds the upstream release to sync (the latest stable one, unless --tag is
given) and prints tag, branch and status as name=value lines, which the
workflow appends to $GITHUB_OUTPUT. The status is one of:

  needs-sync   the release is not in the fork yet
  up-to-date   the release is already merged
  exists       with --check-existing: a pull request for it is already
               there, including one closed without merging, which is a
               decision not to take that release.

--check-existing reads pull requests with the GH_TOKEN environment variable.`,
	Args: cobra.NoArgs,
	RunE: runTarget,
}

func init() {
	targetCmd.Flags().StringVar(&targetOpts.tag, "tag", "", "upstream release to sync, vX.Y.Z (default: the latest stable release)")
	targetCmd.Flags().BoolVar(&targetOpts.checkExisting, "check-existing", false, "report a release that already has a pull request")
	rootCmd.AddCommand(targetCmd)
}

func runTarget(cmd *cobra.Command, _ []string) error {
	s, err := newSyncer(cmd)
	if err != nil {
		return err
	}
	target, err := s.FindTarget(targetOpts.tag)
	if err != nil {
		return err
	}

	status := statusNeedsSync
	switch {
	case target.UpToDate:
		status = statusUpToDate
	case targetOpts.checkExisting:
		client, err := newGitHubClient()
		if err != nil {
			return err
		}
		exists, err := client.PullRequestExists(cmd.Context(), target.Branch())
		if err != nil {
			return err
		}
		if exists {
			status = statusExists
		}
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "tag=%s\nbranch=%s\nstatus=%s\n", target.Tag, target.Branch(), status)
	return err
}
