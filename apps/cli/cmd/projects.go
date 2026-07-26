package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List projects in the current organization",
	Long:  "Lists projects for the organization selected via `jp org use <slug|id>`.",
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

		projects, err := client.ListProjects(cfg.CurrentOrgID)
		if err != nil {
			return err
		}
		if len(projects) == 0 {
			fmt.Println("No projects found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSLUG\tNAME")
		for _, p := range projects {
			fmt.Fprintf(w, "%s\t%s\t%s\n", p.ID, p.Slug, p.Name)
		}
		return w.Flush()
	},
}
