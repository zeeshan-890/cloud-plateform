package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

type stubSpec struct {
	use     string
	short   string
	phase   string
	exitTwo bool
}

var stubCommands = []stubSpec{
	{use: "trace", short: "Inspect distributed traces (requires --profile monitoring / Tempo)", phase: "Phase 5 monitoring", exitTwo: false},
}

func registerStubs(parent *cobra.Command) {
	for _, s := range stubCommands {
		spec := s
		parent.AddCommand(&cobra.Command{
			Use:   spec.use,
			Short: spec.short + " (not yet available)",
			Run: func(cmd *cobra.Command, args []string) {
				fmt.Fprintf(os.Stderr, "`jp %s` requires %s - not implemented yet.\n", spec.use, spec.phase)
				if spec.exitTwo {
					os.Exit(2)
				}
			},
		})
	}
}
