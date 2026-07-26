package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jp-cloud/jp/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View or update CLI configuration",
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Long: `Supported keys:
  api-url    Base API URL (default: http://localhost:8000/api/v1)`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := strings.ToLower(args[0])
		value := args[1]

		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		switch key {
		case "api-url", "api_url", "apiurl":
			cfg.APIURL = strings.TrimRight(value, "/")
		default:
			return fmt.Errorf("unknown config key %q (supported: api-url)", args[0])
		}

		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("Set %s = %s\n", key, value)
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get [key]",
	Short: "Print configuration (or a single key)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		path, _ := config.Path()

		if len(args) == 0 {
			fmt.Printf("config_path:   %s\n", path)
			fmt.Printf("api_url:       %s\n", cfg.APIURL)
			fmt.Printf("current_org:   %s\n", emptyDash(cfg.CurrentOrgID))
			fmt.Printf("access_token:  %s\n", maskToken(cfg.AccessToken))
			fmt.Printf("refresh_token: %s\n", maskToken(cfg.RefreshToken))
			return nil
		}

		switch strings.ToLower(args[0]) {
		case "api-url", "api_url", "apiurl":
			fmt.Println(cfg.APIURL)
		case "current-org", "current_org", "current_org_id":
			fmt.Println(cfg.CurrentOrgID)
		default:
			return fmt.Errorf("unknown config key %q", args[0])
		}
		return nil
	},
}

func init() {
	configCmd.AddCommand(configSetCmd, configGetCmd)
}

func maskToken(t string) string {
	if t == "" {
		return "(not set)"
	}
	if len(t) <= 8 {
		return "********"
	}
	return t[:4] + "…" + t[len(t)-4:]
}

func emptyDash(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}
