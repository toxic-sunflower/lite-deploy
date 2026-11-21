package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// bundle is the package for creating a project from the interface: the description, the secret ciphertexts and the hash the person saw in the preview.
type bundle struct {
	Project     json.RawMessage   `json:"project"`
	Ciphertexts map[string]string `json:"ciphertexts"`
	ExpectHash  string            `json:"expect_project_hash"`
}

const bundleUsage = "usage: lite-deploy project bundle --dry-run|--yes (a JSON bundle on standard input)"

// cmdProjectBundle: project.json, ciphertexts and flags as one JSON on standard input. With --dry-run it returns the step preview
// and the description hash, with --yes it applies the plan. It is run by the executor (root) on the agent request.
func cmdProjectBundle(args []string, env Env) result {
	const command = "project.bundle"
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "")
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 || *dry == *yes {
		return fail(command, CodeUsage, bundleUsage)
	}
	if !env.root() {
		return fail(command, "permission_denied", "project bundle runs as root through the executor")
	}
	cfg, bad := loadConfig(env, command)
	if bad != nil {
		return *bad
	}
	if env.Stdin == nil {
		return fail(command, CodeUsage, bundleUsage)
	}
	raw, err := io.ReadAll(io.LimitReader(env.Stdin, int64(cfg.Limits.ProjectBytes)*4))
	if err != nil {
		return fail(command, CodeIO, err.Error())
	}
	var b bundle
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || len(b.Project) == 0 {
		return fail(command, CodeUsage, "bundle: invalid JSON or no project field")
	}
	var p *project.Project
	var issues []schema.Issue
	if len(b.Project) > cfg.Limits.ProjectBytes {
		issues = []schema.Issue{{Path: "/project", Code: "too_large", Message: fmt.Sprintf("the description is larger than %d bytes", cfg.Limits.ProjectBytes)}}
	} else if p, issues = project.Parse(b.Project); len(issues) == 0 {
		issues = project.Validate(p)
	}
	if len(issues) > 0 {
		return result{command: command, err: &ErrorBody{Code: plan.CodeInvalidProject, Message: "the project description did not pass validation", Details: issues, Hint: "fix the fields listed in details"}}
	}
	pr, badPlan := buildPlan(command, p, "", b.Ciphertexts, env)
	if badPlan != nil {
		return *badPlan
	}
	if b.ExpectHash != "" && b.ExpectHash != pr.pl.ProjectHash {
		return fail(command, plan.CodePrecondition, "the description changed after the preview: build the preview again")
	}
	planBytes, err := json.Marshal(pr.pl)
	if err != nil {
		return fail(command, CodeInternal, err.Error())
	}
	res := applyPlan(command, env, planBytes, *dry, *yes, b.Project)
	if m, ok := res.data.(map[string]any); ok {
		m["project_hash"] = pr.pl.ProjectHash
		m["project"] = pr.pl.Project
	}
	return res
}
