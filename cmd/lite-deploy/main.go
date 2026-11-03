// Command lite-deploy is the single binary for CLI, executor and gate.
package main

import (
	"os"

	"github.com/toxic-sunflower/lite-deploy/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
