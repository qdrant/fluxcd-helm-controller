// Package cmd is the command line of fork-sync.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/config"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/forksync"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/git"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/github"
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "fork-sync",
	Short: "Brings the releases of a fork's upstream into the fork, as pull requests",
	Long: `fork-sync keeps a fork up to date with the releases of its upstream. The
sync-upstream workflow runs it, and it can be run locally to reproduce a sync.
It is configured in ` + config.DefaultPath + `.`,
	SilenceUsage: true,
}

var (
	repoDir    string
	configPath string
	logLevel   string
)

// Execute runs the command line. It is called by main.main.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&repoDir, "repo", ".", "root of the fork's working tree")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", config.DefaultPath, "configuration file, relative to the repository root")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "debug, info, warn or error")
}

func newSyncer(cmd *cobra.Command) (*forksync.Syncer, error) {
	logger, err := newLogger(logLevel)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(repoDir, configPath)
	if err != nil {
		return nil, err
	}
	repo := git.Repo{Dir: repoDir, Output: cmd.ErrOrStderr()}
	return &forksync.Syncer{Repo: repo, Config: cfg, Logger: logger}, nil
}

// newLogger returns a logger that writes readable lines to stderr, without
// stack traces: the logs are read in the output of a workflow run. It is
// qdrant-cloud-go-helpers' log.New with WithDevelopment, which this public
// repository cannot import.
func newLogger(level string) (logr.Logger, error) {
	zapLevel, err := zapcore.ParseLevel(level)
	if err != nil {
		return logr.Discard(), fmt.Errorf("invalid log level: %w", err)
	}
	zc := zap.NewDevelopmentConfig()
	zc.Level = zap.NewAtomicLevelAt(zapLevel)
	zc.DisableStacktrace = true
	zapLogger, err := zc.Build()
	if err != nil {
		return logr.Discard(), fmt.Errorf("failed to create logger: %w", err)
	}
	return zapr.NewLogger(zapLogger), nil
}

// newGitHubClient returns a client for the repository GitHub Actions runs in
// (GITHUB_REPOSITORY), authenticated with the GH_TOKEN environment variable.
func newGitHubClient() (*github.Client, error) {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		return nil, errors.New("the GH_TOKEN environment variable is not set")
	}
	repository := os.Getenv("GITHUB_REPOSITORY")
	if repository == "" {
		return nil, errors.New("the GITHUB_REPOSITORY environment variable (owner/name) is not set")
	}
	return github.New(token, repository, os.Getenv("GITHUB_API_URL"))
}
