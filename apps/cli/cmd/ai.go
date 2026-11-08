package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	aiProjectID string
)

var aiCmd = &cobra.Command{
	Use:   "ai [prompt]",
	Short: "Ask AI ops to explain failures or answer a prompt",
	Long:  "Uses the LLM configured for the platform or organization. Without a configured AI key the service reports that AI is not configured.",
	Args:  cobra.MinimumNArgs(1),
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
		prompt := strings.Join(args, " ")
		if aiProjectID != "" {
			out, err := client.AIExplain(cfg.CurrentOrgID, aiProjectID, prompt, "", "")
			if err != nil {
				return err
			}
			fmt.Printf("mode: %s\n\n%s\n", out.Mode, out.Explanation)
			return nil
		}
		out, err := client.AIAsk(cfg.CurrentOrgID, prompt, "")
		if err != nil {
			return err
		}
		fmt.Printf("mode: %s\n\n%s\n", out.Mode, out.Answer)
		return nil
	},
}

func init() {
	aiCmd.Flags().StringVar(&aiProjectID, "project", "", "Project ID (uses explain endpoint with log tools)")
	rootCmd.AddCommand(aiCmd)
}
