package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	addonProjectID string
	addonEngine    string
)

var addonCmd = &cobra.Command{
	Use:   "addon",
	Short: "Manage one-click add-ons (Redis, MySQL, Mongo, Kafka, …)",
}

var addonCatalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List available add-on engines",
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
		if addonProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		list, mode, err := client.AddonCatalog(cfg.CurrentOrgID, addonProjectID)
		if err != nil {
			return err
		}
		fmt.Printf("mode=%s\n", mode)
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tCATEGORY\tDESCRIPTION")
		for _, it := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", it.ID, it.Name, it.Category, it.Description)
		}
		return w.Flush()
	},
}

var addonListCmd = &cobra.Command{
	Use:   "list",
	Short: "List provisioned add-ons",
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
		if addonProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		list, mode, err := client.ListAddons(cfg.CurrentOrgID, addonProjectID, addonEngine)
		if err != nil {
			return err
		}
		fmt.Printf("mode=%s\n", mode)
		if len(list) == 0 {
			fmt.Println("No add-ons.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tENGINE\tNAME\tSTATUS\tMODE\tSECRET_REF")
		for _, a := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", a.ID, a.Engine, a.Name, a.Status, a.Mode, a.SecretRef)
		}
		return w.Flush()
	},
}

var addonCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Provision an add-on (requires --engine)",
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
		if addonProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		if addonEngine == "" {
			return fmt.Errorf("--engine is required (redis|postgres|mysql|mongodb|rabbitmq|kafka|sqlite)")
		}
		a, err := client.CreateAddon(cfg.CurrentOrgID, addonProjectID, addonEngine, args[0], "development")
		if err != nil {
			return err
		}
		fmt.Printf("Created %s/%s id=%s mode=%s secret_ref=%s\n", a.Engine, a.Name, a.ID, a.Mode, a.SecretRef)
		fmt.Printf("Hint: %s\n", a.ConnectionHint)
		return nil
	},
}

var addonDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a managed add-on",
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
		if addonProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		if err := client.DeleteAddon(cfg.CurrentOrgID, addonProjectID, args[0]); err != nil {
			return err
		}
		fmt.Println("Deleted.")
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{addonCatalogCmd, addonListCmd, addonCreateCmd, addonDeleteCmd} {
		c.Flags().StringVar(&addonProjectID, "project", "", "Project ID")
	}
	addonListCmd.Flags().StringVar(&addonEngine, "engine", "", "Filter by engine")
	addonCreateCmd.Flags().StringVar(&addonEngine, "engine", "", "Engine: redis|postgres|mysql|mongodb|rabbitmq|kafka|sqlite")
	addonCmd.AddCommand(addonCatalogCmd, addonListCmd, addonCreateCmd, addonDeleteCmd)
	rootCmd.AddCommand(addonCmd)
}
