package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jp-cloud/jp/internal/api"
	"github.com/jp-cloud/jp/internal/config"
)

var orgCmd = &cobra.Command{
	Use:   "org",
	Short: "Manage the current organization context",
}

var orgUseCmd = &cobra.Command{
	Use:   "use <slug|id>",
	Short: "Set the current organization",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}

		orgs, err := client.ListOrgs()
		if err != nil {
			return err
		}
		org, err := api.FindOrg(orgs, args[0])
		if err != nil {
			return err
		}

		cfg.CurrentOrgID = org.ID
		if err := config.Save(cfg); err != nil {
			return err
		}

		label := org.Slug
		if label == "" {
			label = org.Name
		}
		if label == "" {
			label = org.ID
		}
		fmt.Printf("Current organization set to %s (%s)\n", label, org.ID)
		return nil
	},
}

func init() {
	orgCmd.AddCommand(orgUseCmd)
}
