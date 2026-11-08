// Package builder drives the docker CLI (buildx build / push / login) with time limits,
// capped + redacted output and a minimal environment.
package builder

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Docker runs docker CLI commands.
type Docker struct {
	Bin         string // default "docker"
	MemoryLimit string // e.g. "2g"; "" or "0" disables the flag
	Ulimits     []string
	// Run executes a command streaming combined output to out (tests inject a fake).
	Run func(ctx context.Context, env []string, out io.Writer, name string, args ...string) error
}

// DefaultDocker returns a Docker with production defaults.
func DefaultDocker() *Docker {
	mem := os.Getenv("WORKER_BUILD_MEMORY")
	if mem == "" {
		mem = "2g"
	}
	return &Docker{Bin: "docker", MemoryLimit: mem, Ulimits: []string{"nofile=65536:65536", "nproc=4096:4096"}}
}

func (d *Docker) bin() string {
	if d.Bin == "" {
		return "docker"
	}
	return d.Bin
}

// env is the minimal environment for docker: no service credentials.
func env() []string {
	e := []string{"BUILDKIT_PROGRESS=plain", "DOCKER_BUILDKIT=1"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PATH=") {
			e = append(e, kv)
		}
	}
	for _, k := range []string{"HOME", "DOCKER_HOST", "DOCKER_CONFIG", "TMPDIR"} {
		if v := os.Getenv(k); v != "" {
			e = append(e, k+"="+v)
		}
	}
	return e
}

func (d *Docker) run(ctx context.Context, out io.Writer, args ...string) error {
	if d.Run != nil {
		return d.Run(ctx, env(), out, d.bin(), args...)
	}
	cmd := exec.CommandContext(ctx, d.bin(), args...)
	cmd.Env = env()
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 10 * time.Second
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s %s timed out", d.bin(), args[0])
	}
	return err
}

// Available checks that the daemon answers.
func (d *Docker) Available(ctx context.Context) error {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return d.run(c, io.Discard, "info", "--format", "{{.ServerVersion}}")
}

// BuildArgs renders the buildx invocation. Only plain (non secret) build args are passed.
func (d *Docker) BuildArgs(imageRef, dockerfile, contextDir string, buildArgs map[string]string) []string {
	args := []string{"buildx", "build", "--progress=plain", "--load", "--pull", "-t", imageRef, "-f", dockerfile}
	if m := d.MemoryLimit; m != "" && m != "0" {
		args = append(args, "--memory", m)
	}
	for _, u := range d.Ulimits {
		args = append(args, "--ulimit", u)
	}
	keys := make([]string, 0, len(buildArgs))
	for k := range buildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+buildArgs[k])
	}
	return append(args, contextDir)
}

// Build builds and loads the image into the daemon.
func (d *Docker) Build(ctx context.Context, imageRef, dockerfile, contextDir string, buildArgs map[string]string, out io.Writer) error {
	if strings.HasPrefix(imageRef, "-") {
		return fmt.Errorf("invalid image ref")
	}
	return d.run(ctx, out, d.BuildArgs(imageRef, dockerfile, contextDir, buildArgs)...)
}

// Push pushes the image; a failed push is a failed build.
func (d *Docker) Push(ctx context.Context, imageRef string, out io.Writer) error {
	if strings.HasPrefix(imageRef, "-") {
		return fmt.Errorf("invalid image ref")
	}
	return d.run(ctx, out, "push", imageRef)
}

// Remove deletes a local image (best effort cleanup after push).
func (d *Docker) Remove(ctx context.Context, imageRef string) {
	_ = d.run(ctx, io.Discard, "image", "rm", "--force", imageRef)
}
