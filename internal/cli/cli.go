// Package cli implements the lite-deploy command line.
package cli

import (
	"fmt"
	"io"
)

// Версии схем (SPEC-v0.md, раздел 0).
const (
	Version       = "0.0.0-dev"
	ProjectSchema = "lite-deploy/project/v1"
	PlanSchema    = "lite-deploy/plan/v1"
)

// Run выполняет команду и возвращает код выхода (SPEC-v0.md, 6.3).
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "использование: lite-deploy <команда>")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "lite-deploy %s\nсхемы: %s, %s\n", Version, ProjectSchema, PlanSchema)
		return 0
	default:
		fmt.Fprintf(stderr, "неизвестная команда: %s\n", args[0])
		return 2
	}
}
