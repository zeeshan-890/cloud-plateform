package cmd

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const jpYAMLTemplateNode = `# jp.yaml - Cloud Platform app manifest
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

//go:embed templates/go-http/*
var goHTTPTemplates embed.FS

var (
	initForce   bool
	initRuntime string
)

var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: "Scaffold a jp.yaml (and optional starter app) in the current directory",
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

		runtime := strings.ToLower(strings.TrimSpace(initRuntime))
		if runtime == "" {
			runtime = "nodejs"
		}
		switch runtime {
		case "nodejs", "node":
			return initNode(cwd, name)
		case "go", "golang":
			return initGo(cwd, name)
		default:
			return fmt.Errorf("unsupported runtime %q (use nodejs or go)", runtime)
		}
	},
}

func initNode(cwd, name string) error {
	path := filepath.Join(cwd, "jp.yaml")
	if _, err := os.Stat(path); err == nil && !initForce {
		return fmt.Errorf("%s already exists (use --force to overwrite)", path)
	}
	content := fmt.Sprintf(jpYAMLTemplateNode, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write jp.yaml: %w", err)
	}
	fmt.Printf("Created %s (runtime=nodejs)\n", path)
	return nil
}

func initGo(cwd, name string) error {
	entries := []string{"main.go", "go.mod", "jp.yaml", "README.md"}
	for _, f := range entries {
		dest := filepath.Join(cwd, f)
		if _, err := os.Stat(dest); err == nil && !initForce {
			return fmt.Errorf("%s already exists (use --force to overwrite)", dest)
		}
	}
	return fs.WalkDir(goHTTPTemplates, "templates/go-http", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel("templates/go-http", path)
		if err != nil {
			rel = filepath.Base(path)
		}
		rel = filepath.ToSlash(rel)
		data, err := goHTTPTemplates.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)
		if rel == "jp.yaml" {
			content = strings.ReplaceAll(content, "{{NAME}}", name)
		}
		dest := filepath.Join(cwd, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Printf("Created %s\n", dest)
		return nil
	})
}

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing scaffold files")
	initCmd.Flags().StringVar(&initRuntime, "runtime", "nodejs", "Starter runtime: nodejs | go")
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
