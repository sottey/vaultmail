package cmd

import (
	"errors"

	"github.com/sottey/vaultmail/internal/import"
	"github.com/sottey/vaultmail/internal/vault"
	"github.com/spf13/cobra"
)

var reindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Rebuild the full-text search index",
	RunE: func(cmd *cobra.Command, _ []string) error {
		vaultDir, err := cmd.Flags().GetString("vault")
		if err != nil {
			return err
		}
		return runReindex(vaultDir)
	},
}

func init() {
	reindexCmd.Flags().String("vault", "", "path to vault directory")
	_ = reindexCmd.MarkFlagRequired("vault")
}

func runReindex(vaultDir string) error {
	if vaultDir == "" {
		return errors.New("--vault requires a path to a vault directory")
	}

	v, err := vault.Open(vaultDir)
	if err != nil {
		return err
	}
	defer v.Close()

	verbosef("Reindex requested for vault %s\n", vaultDir)
	result, err := importer.RebuildFTS(v)
	if err != nil {
		return err
	}
	verbosef("Reindex complete. Messages=%d Updated=%d Errors=%d\n", result.Messages, result.Updated, result.Errors)
	return nil
}
