package cmd

import (
	"errors"
	"time"

	"github.com/sottey/vaultmail/internal/import"
	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a Gmail MBOX archive",
	RunE: func(cmd *cobra.Command, _ []string) error {
		mboxPath, err := cmd.Flags().GetString("mbox")
		if err != nil {
			return err
		}
		vaultDir, err := cmd.Flags().GetString("vault")
		if err != nil {
			return err
		}

		progress, err := cmd.Flags().GetBool("progress")
		if err != nil {
			return err
		}

		errorLogEnabled, err := cmd.Flags().GetBool("error-log-enabled")
		if err != nil {
			return err
		}
		errorLogPath, err := cmd.Flags().GetString("error-log")
		if err != nil {
			return err
		}

		return runImport(vaultDir, mboxPath, progress, errorLogEnabled, errorLogPath)
	},
}

func init() {
	importCmd.Flags().String("vault", "", "path to vault directory")
	_ = importCmd.MarkFlagRequired("vault")
	importCmd.Flags().String("mbox", "", "path to the MBOX file")
	_ = importCmd.MarkFlagRequired("mbox")
	importCmd.Flags().Bool("progress", true, "print periodic import progress")
	importCmd.Flags().Bool("error-log-enabled", true, "write an import error log file")
	importCmd.Flags().String("error-log", "", "path to error log file (default: <vault>/import-errors/batch-<id>.jsonl)")
}

func runImport(vaultDir, path string, progress bool, errorLogEnabled bool, errorLogPath string) error {
	if vaultDir == "" {
		return errors.New("--vault requires a path to a vault directory")
	}
	if path == "" {
		return errors.New("--mbox requires a path to an MBOX file")
	}

	verbosef("Import requested for vault %s with MBOX: %s\n", vaultDir, path)
	verbosef("Options: progress=%v error_log=%v error_log_path=%q\n", progress, errorLogEnabled, errorLogPath)
	result, err := importer.ImportMboxWithOptions(vaultDir, path, importer.Options{
		Progress:       progress,
		Interval:       2 * time.Second,
		ErrorLog:       errorLogEnabled,
		ErrorLogPath:   errorLogPath,
		ErrorLogTopK:   3,
		ErrorLogFormat: importer.ErrorLogJSONL,
	})
	if err != nil {
		return err
	}

	verbosef("Import complete. Imported=%d Skipped=%d Errors=%d Attachments=%d\n", result.Imported, result.Skipped, result.Errors, result.Attachment)
	return nil
}
