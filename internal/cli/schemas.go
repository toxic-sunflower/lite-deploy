package cli

import (
	"encoding/json"
	"sort"

	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// SchemaNames имена схем в порядке вывода; файлы лежат в schemas/<имя>.schema.json.
var SchemaNames = []string{"project", "plan", "result", "error"}

// Schemas строит все JSON Schema из Go-типов. Закоммиченные файлы обязаны совпадать с результатом (тест).
func Schemas() map[string][]byte {
	raw := map[string]map[string]any{
		"project": schema.Reflect(project.Project{}, project.SchemaID, "Описание проекта lite-deploy"),
		"plan":    schema.Reflect(plan.Plan{}, plan.SchemaID, "План lite-deploy"),
		"result":  schema.Reflect(Envelope{}, "lite-deploy/result/v1", "Конверт ответа CLI и API (успех или ошибка)"),
		"error":   schema.Reflect(ErrorBody{}, "lite-deploy/error/v1", "Тело ошибки"),
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
