package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jp-cloud/jp/internal/config"
)

var passwdCmd = &cobra.Command{
	Use:   "passwd",
	Short: "Change your password (signs you out everywhere)",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, "Current password: ")
		cur, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		next, err := promptNewPassword()
		if err != nil {
			return err
		}
		if err := client.ChangePassword(string(cur), next); err != nil {
			return err
		}
		config.ClearAuth(cfg)
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Println("Password changed. All sessions were signed out; run `jp login`.")
		return nil
	},
}

func init() { rootCmd.AddCommand(passwdCmd) }
