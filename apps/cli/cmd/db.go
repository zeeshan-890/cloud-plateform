package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var dbProjectID string

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Manage project databases",
}

var dbListCmd = &cobra.Command{
	Use:   "list",
	Short: "List provisioned databases",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		if cfg.CurrentOrgID == "" {
			return fmt.Errorf("no organization selected; run `jp org use <slug|id>`")
		}
		if dbProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		list, err := client.ListDatabases(cfg.CurrentOrgID, dbProjectID)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No databases.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSTATUS\tMODE\tSECRET_REF")
		for _, d := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", d.ID, d.Name, d.Status, d.Mode, d.SecretRef)
		}
		return w.Flush()
	},
}

var dbCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Provision a Postgres database (schema-per-db or simulate)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		if cfg.CurrentOrgID == "" {
			return fmt.Errorf("no organization selected; run `jp org use <slug|id>`")
		}
		if dbProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		d, err := client.CreateDatabase(cfg.CurrentOrgID, dbProjectID, args[0], "development")
		if err != nil {
			return err
		}
		fmt.Printf("Created %s id=%s mode=%s secret_ref=%s\n", d.Name, d.ID, d.Mode, d.SecretRef)
		fmt.Printf("Hint: %s\n", d.ConnectionHint)
		return nil
	},
}

var dbDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a managed database",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		if cfg.CurrentOrgID == "" {
			return fmt.Errorf("no organization selected; run `jp org use <slug|id>`")
		}
		if dbProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		if err := client.DeleteDatabase(cfg.CurrentOrgID, dbProjectID, args[0]); err != nil {
			return err
		}
		fmt.Println("Deleted.")
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{dbListCmd, dbCreateCmd, dbDeleteCmd} {
		c.Flags().StringVar(&dbProjectID, "project", "", "Project ID")
	}
	dbCmd.AddCommand(dbListCmd, dbCreateCmd, dbDeleteCmd)
	rootCmd.AddCommand(dbCmd)
}
