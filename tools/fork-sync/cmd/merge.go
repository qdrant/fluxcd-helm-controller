package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/forksync"
)

var mergeOpts struct {
	bodyFile    string
	summaryFile string
}

var mergeCmd = &cobra.Command{
	Use:   "merge TAG",
	Short: "Create the sync branch for an upstream release",
	Long: `Creates sync/upstream-TAG from HEAD, with the merge of the upstream release
and, when needed, a commit with the fork's adaptations. Prints tag, branch, base
and status (merged or conflict) as name=value lines, which the workflow appends
to $GITHUB_OUTPUT.

It needs no credentials, and must not get any: the adaptations run the
merged code (make tidy runs upstream's Makefile).`,
	Args: cobra.ExactArgs(1),
	RunE: runMerge,
}

func init() {
	mergeCmd.Flags().StringVar(&mergeOpts.bodyFile, "body-file", "", "write the pull request body to this file")
	mergeCmd.Flags().StringVar(&mergeOpts.summaryFile, "summary-file", "", "append a Markdown report of the sync to this file ($GITHUB_STEP_SUMMARY)")
	rootCmd.AddCommand(mergeCmd)
}

func runMerge(cmd *cobra.Command, args []string) error {
	s, err := newSyncer(cmd)
	if err != nil {
		return err
	}
	// Read the template before merging: it is the fork's, not upstream's.
	tmpl, err := os.ReadFile(filepath.Join(repoDir, s.Config.PRBodyTemplate))
	if err != nil {
		return fmt.Errorf("failed to read the pull request body template: %w", err)
	}

	res, err := s.Merge(args[0])
	if err != nil {
		return err
	}

	body, err := forksync.PRBody(string(tmpl), res, s.Config.Upstream)
	if err != nil {
		return err
	}
	if mergeOpts.bodyFile != "" {
		err = os.WriteFile(mergeOpts.bodyFile, []byte(body), 0o644)
		if err != nil {
			return err
		}
	}
	if mergeOpts.summaryFile != "" {
		err = appendSummary(s, res, body)
		if err != nil {
			return err
		}
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "tag=%s\nbranch=%s\nbase=%s\nstatus=%s\n", res.Tag, res.Branch, res.Base, res.Status)
	return err
}

// appendSummary appends a report of the sync to the summary file: its status,
// the commits it made and the pull request body.
func appendSummary(s *forksync.Syncer, res forksync.Result, body string) error {
	// The commits the sync made on top of the fork, one line each.
	commits, err := s.Repo.Run("log", "--first-parent", "--oneline", res.Base+"..HEAD")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(mergeOpts.summaryFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(forksync.Summary(res, commits, body))
	if err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
