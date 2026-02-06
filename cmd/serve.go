package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sottey/vaultmail/internal/vault"
	"github.com/sottey/vaultmail/web"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the VaultMail web server",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, err := cmd.Flags().GetString("addr")
		if err != nil {
			return err
		}
		vaultDir, err := cmd.Flags().GetString("vault")
		if err != nil {
			return err
		}

		return runServer(vaultDir, addr)
	},
}

func init() {
	serveCmd.Flags().String("vault", "", "path to vault directory")
	_ = serveCmd.MarkFlagRequired("vault")
	serveCmd.Flags().String("addr", "127.0.0.1:8080", "server listen address")
}

func runServer(vaultDir, addr string) error {
	if vaultDir == "" {
		return errors.New("--vault requires a path to a vault directory")
	}

	v, err := vault.Open(vaultDir)
	if err != nil {
		return err
	}
	defer v.Close()

	app, err := web.NewApp(v)
	if err != nil {
		return err
	}

	fmt.Printf("Server listening at %s\n", formatServerURL(addr))
	return http.ListenAndServe(addr, app.Router())
}

func formatServerURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "http://127.0.0.1:8080"
	}
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}
	return "http://" + addr
}
