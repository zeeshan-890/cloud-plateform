package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jp-cloud/go-common/jpconfig"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var applyProjectID string

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Validate and apply local jp.yaml desired state to a project",
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
		if applyProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		m, raw, err := loadLocalJPYAML()
		if err != nil {
			return err
		}
		if err := jpconfig.Validate(m); err != nil {
			return fmt.Errorf("jp.yaml validation failed: %w", err)
		}
		out, err := client.ApplyProjectConfig(cfg.CurrentOrgID, applyProjectID, map[string]any{
			"raw":      raw,
			"manifest": m,
			"config":   m.ToMap(),
		})
		if err != nil {
			return err
		}
		fmt.Println("Applied jp.yaml desired state.")
		if h, ok := out["hash"].(string); ok {
			fmt.Printf("  hash:     %s\n", h)
		}
		if s, ok := out["strategy"].(string); ok {
			fmt.Printf("  strategy: %s\n", s)
		}
		return nil
	},
}

var driftHash string

var driftCmd = &cobra.Command{
	Use:   "drift",
	Short: "Compare local jp.yaml (or a given hash) with the config applied to a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		if cfg.CurrentOrgID == "" {
			return fmt.Errorf("no organization selected")
		}
		if applyProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		var out map[string]any
		if driftHash != "" {
			out, err = client.ConfigDriftHash(cfg.CurrentOrgID, applyProjectID, driftHash)
		} else {
			m, _, lerr := loadLocalJPYAML()
			if lerr != nil {
				return lerr
			}
			if verr := jpconfig.Validate(m); verr != nil {
				return fmt.Errorf("jp.yaml validation failed: %w", verr)
			}
			out, err = client.ConfigDrift(cfg.CurrentOrgID, applyProjectID, m.ToMap())
		}
		if err != nil {
			return err
		}
		fmt.Printf("drift: %v (method: %v)\n", out["drift"], out["method"])
		if d, ok := out["details"].([]any); ok {
			for _, x := range d {
				fmt.Printf("  - %v\n", x)
			}
		}
		return nil
	},
}

func loadLocalJPYAML() (*jpconfig.Manifest, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(cwd, "jp.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var m jpconfig.Manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, "", fmt.Errorf("parse jp.yaml: %w", err)
	}
	return &m, string(b), nil
}

func init() {
	applyCmd.Flags().StringVar(&applyProjectID, "project", "", "Project ID")
	driftCmd.Flags().StringVar(&applyProjectID, "project", "", "Project ID")
	driftCmd.Flags().StringVar(&driftHash, "hash", "", "Compare against this sha256 of jp.yaml instead of the local file")
	rootCmd.AddCommand(applyCmd, driftCmd)
}
