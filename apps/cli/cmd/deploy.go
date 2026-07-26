package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/jp-cloud/go-common/jpconfig"
	"github.com/spf13/cobra"
)

var (
	deployProjectID string
	deployBranch    string
	deploySHA       string
	deployFullName  string
	deployCloneURL  string
	deployStrategy  string
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Create a deployment for a project",
	Long:  "Creates a deployment via the API (queues a build). Reads local jp.yaml when present (validates + applies config, uses deploy.strategy).",
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
		if deployProjectID == "" {
			return fmt.Errorf("--project is required (project id)")
		}

		body := map[string]any{
			"git_branch": deployBranch,
			"git_sha":    deploySHA,
			"full_name":  deployFullName,
			"clone_url":  deployCloneURL,
			"message":    "jp deploy",
		}
		strategy := deployStrategy
		if m, raw, err := loadLocalJPYAML(); err == nil {
			if err := jpconfig.Validate(m); err != nil {
				return fmt.Errorf("jp.yaml validation failed: %w", err)
			}
			if _, err := client.ApplyProjectConfig(cfg.CurrentOrgID, deployProjectID, map[string]any{
				"raw": raw, "manifest": m, "config": m.ToMap(),
			}); err != nil {
				return fmt.Errorf("apply jp.yaml: %w", err)
			}
			fmt.Println("Applied local jp.yaml desired state.")
			if strategy == "" {
				strategy = m.Strategy()
			}
			body["jp_config"] = m.ToMap()
			body["message"] = "jp deploy (from jp.yaml)"
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("jp.yaml: %w", err)
		}
		if strategy == "" {
			strategy = "rolling"
		}
		body["strategy"] = strategy

		d, err := client.CreateDeployment(cfg.CurrentOrgID, deployProjectID, body)
		if err != nil {
			return err
		}

		fmt.Printf("Deployment created: %s\n", d.ID)
		fmt.Printf("  status:   %s\n", d.Status)
		fmt.Printf("  strategy: %s\n", strategy)
		fmt.Printf("  branch:   %s\n", d.GitBranch)
		fmt.Printf("  sha:      %s\n", d.GitSHA)
		if d.BuildID != "" {
			fmt.Printf("  build:    %s\n", d.BuildID)
		}
		return nil
	},
}

var buildsCmd = &cobra.Command{
	Use:   "builds",
	Short: "List builds for a project",
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
		if deployProjectID == "" {
			return fmt.Errorf("--project is required (project id)")
		}
		list, err := client.ListBuilds(cfg.CurrentOrgID, deployProjectID)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No builds found.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATUS\tFRAMEWORK\tSHA\tIMAGE")
		for _, b := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", b.ID, b.Status, b.Framework, short(b.GitSHA), b.ImageRef)
		}
		return w.Flush()
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show recent deployments and runtime for a project",
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
		if deployProjectID == "" {
			return fmt.Errorf("--project is required (project id)")
		}
		list, err := client.ListDeployments(cfg.CurrentOrgID, deployProjectID)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No deployments found.")
		} else {
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATUS\tSOURCE\tSHA\tIMAGE")
			for _, d := range list {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", d.ID, d.Status, d.Source, short(d.GitSHA), d.ImageRef)
			}
			_ = w.Flush()
		}

		instances, mode, err := client.ListRuntime(cfg.CurrentOrgID, deployProjectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "runtime: %v\n", err)
			return nil
		}
		fmt.Printf("\nRuntime (mode=%s)\n", mode)
		if len(instances) == 0 {
			fmt.Println("No runtime instances.")
			return nil
		}
		w2 := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w2, "ID\tSTATUS\tHEALTH\tKIND\tIMAGE")
		for _, in := range instances {
			fmt.Fprintf(w2, "%s\t%s\t%s\t%s\t%s\n", in.ID, in.Status, in.HealthStatus, in.Kind, in.ImageRef)
		}
		return w2.Flush()
	},
}

var rollbackCmd = &cobra.Command{
	Use:   "rollback [deployment-id]",
	Short: "Roll back to a previous successful deployment",
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
		if deployProjectID == "" {
			return fmt.Errorf("--project is required (project id)")
		}
		target := ""
		if len(args) > 0 {
			target = args[0]
		}
		d, err := client.RollbackDeployment(cfg.CurrentOrgID, deployProjectID, target)
		if err != nil {
			return err
		}
		fmt.Printf("Rollback deployment created: %s (rollback_of=%s)\n", d.ID, d.RollbackOf)
		fmt.Printf("  status: %s\n  image:  %s\n", d.Status, d.ImageRef)
		return nil
	},
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func init() {
	for _, c := range []*cobra.Command{deployCmd, buildsCmd, statusCmd, rollbackCmd} {
		c.Flags().StringVar(&deployProjectID, "project", "", "Project ID")
	}
	deployCmd.Flags().StringVar(&deployBranch, "branch", "main", "Git branch")
	deployCmd.Flags().StringVar(&deploySHA, "sha", "HEAD", "Git SHA")
	deployCmd.Flags().StringVar(&deployFullName, "repo", "", "GitHub full_name (owner/repo)")
	deployCmd.Flags().StringVar(&deployCloneURL, "clone-url", "", "Git clone URL")
	deployCmd.Flags().StringVar(&deployStrategy, "strategy", "", "Deploy strategy: rolling|blue_green (from jp.yaml if unset)")

	rootCmd.AddCommand(deployCmd, buildsCmd, statusCmd, rollbackCmd)
}
