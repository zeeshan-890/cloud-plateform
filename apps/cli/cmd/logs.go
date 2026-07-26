package cmd

import (
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	logsProjectID string
	logsSource    string
	logsBuildID   string
	logsLimit     string
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Query build and runtime logs",
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
		if logsProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		q := url.Values{}
		if logsSource != "" {
			q.Set("source", logsSource)
		}
		if logsBuildID != "" {
			q.Set("build_id", logsBuildID)
			if logsSource == "" {
				q.Set("source", "build")
			}
		}
		if logsLimit != "" {
			q.Set("limit", logsLimit)
		}
		entries, buildLogs, err := client.QueryLogs(cfg.CurrentOrgID, logsProjectID, q)
		if err != nil {
			return err
		}
		if buildLogs != "" {
			fmt.Println("--- build logs ---")
			fmt.Println(buildLogs)
		}
		if len(entries) == 0 {
			if buildLogs == "" {
				fmt.Println("No log entries.")
			}
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "TIME\tSOURCE\tLEVEL\tMESSAGE")
		for _, e := range entries {
			msg := e.Message
			if len(msg) > 80 {
				msg = msg[:77] + "..."
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.LoggedAt, e.Source, e.Level, msg)
		}
		return w.Flush()
	},
}

func init() {
	logsCmd.Flags().StringVar(&logsProjectID, "project", "", "Project ID")
	logsCmd.Flags().StringVar(&logsSource, "source", "", "Filter: build|runtime|app")
	logsCmd.Flags().StringVar(&logsBuildID, "build", "", "Build ID (also pulls build service logs)")
	logsCmd.Flags().StringVar(&logsLimit, "limit", "50", "Max entries")
	rootCmd.AddCommand(logsCmd)
}
