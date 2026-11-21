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
		return []schema.Issue{{Path: "/project", Code: "reserved", Message: fmt.Sprintf("the name %q is reserved", p.Project)}}
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
			bad(i, "/name", "duplicate", "the variable is repeated")
		}
		seen[e.Name] = true
		switch e.Kind {
		case "literal":
			if e.Secret != "" {
				bad(i, "/secret", "unexpected", "literal needs value, not secret")
			}
		case "secret":
			if e.Secret == "" {
				bad(i, "/secret", "required", "secret needs a secret name")
			}
			if e.Value != "" {
				bad(i, "/value", "unexpected", "secret takes no value")
			}
		}
	}
	return out
}

func checkExec(v any) []schema.Issue {
	u := v.(*InstallUnit)
	var out []schema.Issue
	for i, a := range u.Exec {
		if a == "" || len(a) > 512 || strings.ContainsAny(a, "\r\n\x00") {
			out = append(out, schema.Issue{Path: "/exec/" + strconv.Itoa(i), Code: "pattern", Message: "an argument must be non-empty, without line breaks, up to 512 characters"})
		}
	}
	for i, a := range u.Stop {
		if a == "" || len(a) > 512 || strings.ContainsAny(a, "\r\n\x00") {
			out = append(out, schema.Issue{Path: "/stop/" + strconv.Itoa(i), Code: "pattern", Message: "an argument must be non-empty, without line breaks, up to 512 characters"})
		}
	}
	if len(u.Exec) > 0 {
		for _, part := range strings.Split(u.Exec[0], "/") {
			if part == ".." {
				out = append(out, schema.Issue{Path: "/exec/0", Code: "pattern", Message: "the program path must not contain \"..\""})
			}
		}
	}
	return out
}
