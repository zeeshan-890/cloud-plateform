package main

import (
	"os"

	"github.com/jp-cloud/jp/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
