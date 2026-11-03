package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/forksync"
)

var publishOpts struct {
	branch   string
	bodyFile string
}

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Push a sync branch and open its pull request",
	Long: `Pushes the sync branch to origin and opens its pull request, as a draft when
the branch has unresolved conflicts. Prints the pull request's url as url=URL.

A sync branch already on origin is replaced: without a pull request (target
checks there is none), it is what an earlier publish left when it could not open
the pull request.

It authenticates with the GH_TOKEN environment variable, which must be able to
push branches, open pull requests and push changes under .github/workflows/.
GITHUB_TOKEN is not enough: what it pushes and opens does not trigger CI.`,
	Args: cobra.NoArgs,
	RunE: runPublish,
}

func init() {
	publishCmd.Flags().StringVar(&publishOpts.branch, "branch", "", "sync branch to publish, sync/upstream-vX.Y.Z")
	publishCmd.Flags().StringVar(&publishOpts.bodyFile, "body-file", "", "file with the pull request body, as written by merge")
	_ = publishCmd.MarkFlagRequired("branch")
	_ = publishCmd.MarkFlagRequired("body-file")
	rootCmd.AddCommand(publishCmd)
}

func runPublish(cmd *cobra.Command, _ []string) error {
	tag, ok := strings.CutPrefix(publishOpts.branch, forksync.BranchPrefix)
	if !ok {
		return errors.New("--branch must be a sync branch, " + forksync.BranchPrefix + "vX.Y.Z")
	}
	s, err := newSyncer(cmd)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(publishOpts.bodyFile)
	if err != nil {
		return err
	}
	client, err := newGitHubClient()
	if err != nil {
		return err
	}

	s.Logger.Info("Pushing the sync branch", "branch", publishOpts.branch)
	err = s.Repo.PushWithToken(publishOpts.branch, os.Getenv("GH_TOKEN"))
	if err != nil {
		return err
	}
	draft := s.Repo.Succeeds("cat-file", "-e", publishOpts.branch+":"+forksync.ConflictsFile)
	url, err := client.CreatePullRequest(cmd.Context(), s.Config.BaseBranch, publishOpts.branch, "Sync upstream "+tag, string(body), draft)
	if err != nil {
		return err
	}
	s.Logger.Info("Opened the pull request", "url", url, "draft", draft)
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "url=%s\n", url)
	return err
}
