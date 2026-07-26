package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	secretsProjectID string
	secretsEnv       string
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage encrypted project secrets",
}

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List secrets in an environment (hints only)",
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
		if secretsProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		list, err := client.ListSecrets(cfg.CurrentOrgID, secretsProjectID, secretsEnv)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No secrets found.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tVERSION\tHINT")
		for _, s := range list {
			fmt.Fprintf(w, "%s\t%d\t%s\n", s.Name, s.CurrentVersion, s.ValueHint)
		}
		return w.Flush()
	},
}

var secretsSetCmd = &cobra.Command{
	Use:   "set <name> <value>",
	Short: "Create or rotate a secret (value never echoed back)",
	Args:  cobra.ExactArgs(2),
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
		if secretsProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		s, err := client.SetSecret(cfg.CurrentOrgID, secretsProjectID, secretsEnv, args[0], args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Secret set: %s version=%d hint=%s\n", s.Name, s.CurrentVersion, s.ValueHint)
		return nil
	},
}

var secretsUnsetCmd = &cobra.Command{
	Use:   "unset <name>",
	Short: "Delete a secret",
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
		if secretsProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		if err := client.DeleteSecret(cfg.CurrentOrgID, secretsProjectID, secretsEnv, args[0]); err != nil {
			return err
		}
		fmt.Println("Secret deleted.")
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{secretsListCmd, secretsSetCmd, secretsUnsetCmd} {
		c.Flags().StringVar(&secretsProjectID, "project", "", "Project ID")
		c.Flags().StringVar(&secretsEnv, "env", "development", "Environment (development|preview|staging|production)")
	}
	secretsCmd.AddCommand(secretsListCmd, secretsSetCmd, secretsUnsetCmd)
	rootCmd.AddCommand(secretsCmd)
}
