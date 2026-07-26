package cmd

import (
	"fmt"

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
			// Best-effort server logout; always clear local credentials.
			_ = client.Logout()
		}

		config.ClearAuth(cfg)
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Println("Logged out.")
		return nil
	},
}
