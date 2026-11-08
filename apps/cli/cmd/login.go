package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jp-cloud/jp/internal/api"
	"github.com/jp-cloud/jp/internal/config"
)

var loginToken string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to the Cloud Platform",
	Long: `Authenticate with email/password (plus an authentication code when MFA is enabled) or a personal access token (PAT).

Credentials are stored in ~/.jp/config.json (override with JP_CONFIG_DIR).

Use --token to authenticate with a PAT or any bearer token (stored as access_token).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := api.New(cfg)

		if loginToken != "" {
			api.ApplyToken(cfg, strings.TrimSpace(loginToken))
			if err := config.Save(cfg); err != nil {
				return err
			}
			// Best-effort verification; PAT support on the API may land later.
			if _, err := client.Me(); err != nil {
				fmt.Println("Token saved.")
				fmt.Printf("Note: could not verify token against API (%v)\n", err)
			} else {
				fmt.Println("Logged in successfully (token).")
			}
			path, _ := config.Path()
			if path != "" {
				fmt.Printf("Credentials saved to %s\n", path)
			}
			return nil
		}

		email, password, errPrompt := promptCredentials()
		if errPrompt != nil {
			return errPrompt
		}
		tokens, err := client.Login(email, password)
		if err != nil {
			return err
		}
		if tokens.MFARequired {
			if tokens.MFAToken == "" {
				return fmt.Errorf("server requested a second factor but sent no mfa_token")
			}
			code, cerr := promptLine("Authentication code (or backup code): ")
			if cerr != nil {
				return cerr
			}
			tokens, err = client.LoginMFA(tokens.MFAToken, code)
			if err != nil {
				return err
			}
		}
		if tokens.MustChangePassword {
			fmt.Fprintln(os.Stderr, "You must choose a new password before continuing.")
			cfg.AccessToken = tokens.AccessToken
			cfg.RefreshToken = ""
			next, perr := promptNewPassword()
			if perr != nil {
				return perr
			}
			if err := client.ChangePassword(password, next); err != nil {
				return err
			}
			config.ClearAuth(cfg)
			tokens, err = client.Login(email, next)
			if err != nil {
				return err
			}
		}
		if tokens.AccessToken == "" {
			return fmt.Errorf("login succeeded but no access_token returned")
		}

		api.ApplyLogin(cfg, tokens)
		if err := config.Save(cfg); err != nil {
			return err
		}

		fmt.Println("Logged in successfully.")
		path, _ := config.Path()
		if path != "" {
			fmt.Printf("Credentials saved to %s\n", path)
		}
		return nil
	},
}

func init() {
	loginCmd.Flags().StringVar(&loginToken, "token", "", "Personal access token (PAT) or bearer token")
}

func promptLine(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read input: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("a code is required")
	}
	return line, nil
}

func promptCredentials() (email, password string, err error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Fprint(os.Stderr, "Username or email: ")
	email, err = reader.ReadString('\n')
	if err != nil {
		return "", "", fmt.Errorf("read email: %w", err)
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return "", "", fmt.Errorf("username or email is required")
	}

	fmt.Fprint(os.Stderr, "Password: ")
	pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", "", fmt.Errorf("read password: %w", err)
	}
	password = string(pwBytes)
	if password == "" {
		return "", "", fmt.Errorf("password is required")
	}
	return email, password, nil
}

func promptNewPassword() (string, error) {
	fmt.Fprint(os.Stderr, "New password: ")
	a, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	fmt.Fprint(os.Stderr, "Repeat new password: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if string(a) != string(b) || len(a) < 8 {
		return "", fmt.Errorf("passwords do not match or are shorter than 8 characters")
	}
	return string(a), nil
}
