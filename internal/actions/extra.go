package actions

import (
	"fmt"
	"strconv"

	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

func reservedProject(v any) []schema.Issue {
	p := v.(*CreateProjectUser)
	if project.Reserved(p.Project) {
		return []schema.Issue{{Path: "/project", Code: "reserved", Message: fmt.Sprintf("имя %q зарезервировано", p.Project)}}
	}
	return nil
}

func checkEnv(v any) []schema.Issue {
	w := v.(*WriteServiceEnv)
	var out []schema.Issue
	seen := map[string]bool{}
	bad := func(i int, field, code, msg string) {
		out = append(out, schema.Issue{Path: "/env/" + strconv.Itoa(i) + field, Code: code, Message: msg})
	}
	for i, e := range w.Env {
		if seen[e.Name] {
			bad(i, "/name", "duplicate", "переменная повторяется")
		}
		seen[e.Name] = true
		switch e.Kind {
		case "literal":
			if e.Secret != "" {
				bad(i, "/secret", "unexpected", "для literal нужно value, не secret")
			}
		case "secret":
			if e.Secret == "" {
				bad(i, "/secret", "required", "для secret нужно имя секрета")
			}
			if e.Value != "" {
				bad(i, "/value", "unexpected", "для secret значение не задаётся")
			}
		case "database":
			if e.Value != "npgsql" && e.Value != "url" && e.Value != "libpq" {
				bad(i, "/value", "enum", "для database нужен формат: npgsql, url или libpq")
			}
			if e.Secret != "" {
				bad(i, "/secret", "unexpected", "для database secret не задаётся")
			}
		}
	}
	return out
}
