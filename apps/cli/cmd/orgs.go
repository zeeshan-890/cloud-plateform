package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var orgsCmd = &cobra.Command{
	Use:   "orgs",
	Short: "List organizations you belong to",
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
		if len(orgs) == 0 {
			fmt.Println("No organizations found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSLUG\tNAME\tCURRENT")
		for _, o := range orgs {
			cur := ""
			if o.ID == cfg.CurrentOrgID {
				cur = "*"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", o.ID, o.Slug, o.Name, cur)
		}
		return w.Flush()
	},
}
