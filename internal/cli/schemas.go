package cli

import (
	"encoding/json"
	"sort"

	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// SchemaNames are the schema names in output order; the files are schemas/<name>.schema.json.
var SchemaNames = []string{"project", "plan", "result", "error"}

// Schemas builds all JSON Schemas from the Go types. The committed files must match the result (a test).
func Schemas() map[string][]byte {
	raw := map[string]map[string]any{
		"project": schema.Reflect(project.Project{}, project.SchemaID, "lite-deploy project description"),
		"plan":    schema.Reflect(plan.Plan{}, plan.SchemaID, "lite-deploy plan"),
		"result":  schema.Reflect(Envelope{}, "lite-deploy/result/v1", "CLI and API response envelope (success or error)"),
		"error":   schema.Reflect(ErrorBody{}, "lite-deploy/error/v1", "Error body"),
	}
	out := map[string][]byte{}
	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := json.MarshalIndent(raw[n], "", "  ")
		if err != nil {
			panic(err)
		}
		out[n] = append(b, '\n')
	}
	return out
}
