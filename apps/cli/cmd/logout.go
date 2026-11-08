package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jp-cloud/jp/internal/config"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out and clear stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}

		if cfg.AccessToken != "" {
			// Always clear local credentials, but say so when the server could not revoke the session.
			if err := client.Logout(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: server-side logout failed (%v); the session may remain valid until it expires.\n", err)
			}
		}

		config.ClearAuth(cfg)
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Println("Logged out.")
		return nil
	},
}
