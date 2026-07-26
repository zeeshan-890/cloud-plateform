package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	envProjectID string
	envName      string
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage project environments and env vars (secrets)",
}

var envListCmd = &cobra.Command{
	Use:   "list",
	Short: "List environments or secrets in --env",
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
		if envProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		if envName == "" {
			list, err := client.ListEnvironments(cfg.CurrentOrgID, envProjectID)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tID")
			for _, e := range list {
				fmt.Fprintf(w, "%s\t%s\n", e.Name, e.ID)
			}
			return w.Flush()
		}
		list, err := client.ListSecrets(cfg.CurrentOrgID, envProjectID, envName)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tVERSION\tHINT")
		for _, s := range list {
			fmt.Fprintf(w, "%s\t%d\t%s\n", s.Name, s.CurrentVersion, s.ValueHint)
		}
		return w.Flush()
	},
}

var envSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set an environment variable (encrypted secret)",
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
		if envProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		if envName == "" {
			envName = "development"
		}
		s, err := client.SetSecret(cfg.CurrentOrgID, envProjectID, envName, args[0], args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Set %s=%s (v%d) in %s\n", s.Name, s.ValueHint, s.CurrentVersion, envName)
		return nil
	},
}

var envUnsetCmd = &cobra.Command{
	Use:   "unset <key>",
	Short: "Unset an environment variable",
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
		if envProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		if envName == "" {
			envName = "development"
		}
		if err := client.DeleteSecret(cfg.CurrentOrgID, envProjectID, envName, args[0]); err != nil {
			return err
		}
		fmt.Println("Unset", args[0])
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{envListCmd, envSetCmd, envUnsetCmd} {
		c.Flags().StringVar(&envProjectID, "project", "", "Project ID")
		c.Flags().StringVar(&envName, "env", "", "Environment name")
	}
	envCmd.AddCommand(envListCmd, envSetCmd, envUnsetCmd)
	rootCmd.AddCommand(envCmd)
}
