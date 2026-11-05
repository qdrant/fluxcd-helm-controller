package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/config"
)

var renameCmd = &cobra.Command{
	Use:   "rename",
	Short: "Rename upstream's names in the working tree",
	Long: `Applies the rename configured in ` + config.DefaultPath + ` to the working tree,
as a sync does after merging, and prints the files it wrote. Use it after
bringing in upstream code by hand. Nothing is staged or committed.`,
	Args: cobra.NoArgs,
	RunE: runRename,
}

func init() {
	rootCmd.AddCommand(renameCmd)
}

func runRename(cmd *cobra.Command, _ []string) error {
	s, err := newSyncer(cmd)
	if err != nil {
		return err
	}
	if s.Config.Rename == nil {
		return errors.New("there is no rename in " + configPath)
	}
	written, err := s.Rename()
	if err != nil {
		return err
	}
	for _, file := range written {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), file)
		if err != nil {
			return err
		}
	}
	return nil
}
