package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const jpYAMLTemplate = `# jp.yaml - Cloud Platform app manifest
name: %s
runtime: nodejs

build:
  # Command to produce a deployable artifact
  command: npm run build
  # Directory containing the build output (optional)
  # output: dist

deploy:
  strategy: rolling   # rolling | blue_green
  # Health check path after deploy (optional)
  # healthcheck: /health
  # Region preference (optional)
  # region: us-east-1
  # replicas: 1
  # port: 8080

# domains:
#   - app.example.com

# env:
#   NODE_ENV: production
`

var initForce bool

var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: "Scaffold a jp.yaml in the current directory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		name := ""
		if len(args) == 1 {
			name = args[0]
		} else {
			name = filepath.Base(cwd)
		}
		name = sanitizeName(name)
		if name == "" {
			name = "app"
		}

		path := filepath.Join(cwd, "jp.yaml")
		if _, err := os.Stat(path); err == nil && !initForce {
			return fmt.Errorf("%s already exists (use --force to overwrite)", path)
		}

		content := fmt.Sprintf(jpYAMLTemplate, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write jp.yaml: %w", err)
		}

		fmt.Printf("Created %s\n", path)
		return nil
	},
}

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing jp.yaml")
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}
