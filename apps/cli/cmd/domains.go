package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	domainsProjectID string
	domainsForce     bool
)

var domainsCmd = &cobra.Command{
	Use:   "domains",
	Short: "Manage custom domains for a project",
}

var domainsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List domains",
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
		if domainsProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		list, err := client.ListDomains(cfg.CurrentOrgID, domainsProjectID)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No domains found.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tHOSTNAME\tSTATUS\tTYPE")
		for _, d := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.ID, d.Hostname, d.Status, d.VerificationType)
		}
		return w.Flush()
	},
}

var domainsAddCmd = &cobra.Command{
	Use:   "add <hostname>",
	Short: "Add a custom domain",
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
		if domainsProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		d, err := client.AddDomain(cfg.CurrentOrgID, domainsProjectID, args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Domain added: %s (%s)\n", d.Hostname, d.ID)
		fmt.Printf("  status: %s\n", d.Status)
		fmt.Println("Verify with: jp domains verify", d.ID, "--project", domainsProjectID)
		return nil
	},
}

var domainsVerifyCmd = &cobra.Command{
	Use:   "verify <domain-id>",
	Short: "Verify domain DNS (use --force to stub locally)",
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
		if domainsProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		d, err := client.VerifyDomain(cfg.CurrentOrgID, domainsProjectID, args[0], domainsForce)
		if err != nil {
			return err
		}
		fmt.Printf("Domain verified: %s status=%s\n", d.Hostname, d.Status)
		return nil
	},
}

var domainsDeleteCmd = &cobra.Command{
	Use:   "delete <domain-id>",
	Short: "Delete a domain",
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
		if domainsProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		if err := client.DeleteDomain(cfg.CurrentOrgID, domainsProjectID, args[0]); err != nil {
			return err
		}
		fmt.Println("Domain deleted.")
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{domainsListCmd, domainsAddCmd, domainsVerifyCmd, domainsDeleteCmd} {
		c.Flags().StringVar(&domainsProjectID, "project", "", "Project ID")
	}
	domainsVerifyCmd.Flags().BoolVar(&domainsForce, "force", false, "Force verify (skip real DNS)")
	domainsCmd.AddCommand(domainsListCmd, domainsAddCmd, domainsVerifyCmd, domainsDeleteCmd)
	rootCmd.AddCommand(domainsCmd)
}
