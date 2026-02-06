package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "vaultmail",
	Short: "VaultMail: local Gmail MBOX archive browser",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

var verbose bool

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	viper.SetEnvPrefix("vaultmail")
	viper.AutomaticEnv()

	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(reindexCmd)
}
