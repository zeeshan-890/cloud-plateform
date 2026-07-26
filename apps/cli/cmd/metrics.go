package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var metricsProjectID string

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Show project metrics summary",
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
		if metricsProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		list, mode, err := client.ProjectMetrics(cfg.CurrentOrgID, metricsProjectID)
		if err != nil {
			return err
		}
		fmt.Printf("mode=%s\n", mode)
		if len(list) == 0 {
			fmt.Println("No metrics yet.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tLATEST\tSAMPLES")
		for _, m := range list {
			fmt.Fprintf(w, "%s\t%g\t%d\n", m.Name, m.Latest, m.Count)
		}
		return w.Flush()
	},
}

func init() {
	metricsCmd.Flags().StringVar(&metricsProjectID, "project", "", "Project ID")
	rootCmd.AddCommand(metricsCmd)
}
