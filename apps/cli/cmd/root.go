package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jp-cloud/jp/internal/api"
	"github.com/jp-cloud/jp/internal/config"
)

var rootCmd = &cobra.Command{
	Use:   "jp",
	Short: "jp - Cloud Platform CLI",
	Long:  "jp is the command-line interface for the Cloud Platform.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return err
	}
	return nil
}

func init() {
	rootCmd.AddCommand(
		loginCmd,
		logoutCmd,
		whoamiCmd,
		orgsCmd,
		orgCmd,
		projectsCmd,
		deployCmd,
		statusCmd,
		initCmd,
		configCmd,
	)
	registerStubs(rootCmd)
}

func loadConfig() (*config.Config, error) {
	return config.Load()
}

func newClient() (*api.Client, *config.Config, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, nil, err
	}
	return api.New(cfg), cfg, nil
}

func requireLogin(cfg *config.Config) error {
	if cfg.AccessToken == "" {
		return fmt.Errorf("not logged in; run `jp login`")
	}
	return nil
}
