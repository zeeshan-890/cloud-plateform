package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the currently authenticated user",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}

		user, err := client.Me()
		if err != nil {
			return err
		}

		if user.Email != "" {
			fmt.Printf("Email: %s\n", user.Email)
		}
		if user.Name != "" {
			fmt.Printf("Name:  %s\n", user.Name)
		}
		if user.ID != "" {
			fmt.Printf("ID:    %s\n", user.ID)
		}
		if cfg.CurrentOrgID != "" {
			fmt.Printf("Org:   %s\n", cfg.CurrentOrgID)
		}
		fmt.Printf("API:   %s\n", cfg.APIURL)
		return nil
	},
}
