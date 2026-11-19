package actions

import (
	"fmt"
	"strconv"
	"strings"

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
			if e.Host != "" {
				if e.Secret == "" {
					bad(i, "/secret", "required", "для базы на другом сервере нужен секрет с паролем")
				}
			} else if e.Secret != "" || e.Port != 0 {
				bad(i, "/host", "required", "secret и port задаются только вместе с host")
			}
		}
		if e.Kind != "database" && (e.Host != "" || e.Port != 0) {
			bad(i, "/host", "unexpected", "host и port только для database")
		}
	}
	return out
}

func checkExec(v any) []schema.Issue {
	u := v.(*InstallUnit)
	var out []schema.Issue
	for i, a := range u.Exec {
		if a == "" || len(a) > 512 || strings.ContainsAny(a, "\r\n\x00") {
			out = append(out, schema.Issue{Path: "/exec/" + strconv.Itoa(i), Code: "pattern", Message: "аргумент не пустой, без переводов строки, до 512 знаков"})
		}
	}
	for i, a := range u.Stop {
		if a == "" || len(a) > 512 || strings.ContainsAny(a, "\r\n\x00") {
			out = append(out, schema.Issue{Path: "/stop/" + strconv.Itoa(i), Code: "pattern", Message: "аргумент не пустой, без переводов строки, до 512 знаков"})
		}
	}
	if len(u.Exec) > 0 {
		for _, part := range strings.Split(u.Exec[0], "/") {
			if part == ".." {
				out = append(out, schema.Issue{Path: "/exec/0", Code: "pattern", Message: "путь к программе не должен содержать «..»"})
			}
		}
	}
	return out
}
