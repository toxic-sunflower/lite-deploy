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

// bundle пакет для создания проекта из интерфейса: описание, шифртексты секретов и хэш, который человек видел при просмотре.
type bundle struct {
	Project        json.RawMessage   `json:"project"`
	Ciphertexts    map[string]string `json:"ciphertexts"`
	RotatePassword bool              `json:"rotate_password"`
	ExpectHash     string            `json:"expect_project_hash"`
}

const bundleUsage = "использование: lite-deploy project bundle --dry-run|--yes (пакет JSON со стандартного ввода)"

// cmdProjectBundle: project.json, шифртексты и флаги одним JSON со стандартного ввода. С --dry-run возвращает предпросмотр шагов
// и хэш описания, с --yes применяет план. Запускается исполнителем (root) по запросу агента.
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
		return fail(command, "permission_denied", "project bundle выполняется от root через исполнителя")
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
		return fail(command, CodeUsage, "пакет: неверный JSON или нет поля project")
	}
	var p *project.Project
	var issues []schema.Issue
	if len(b.Project) > cfg.Limits.ProjectBytes {
		issues = []schema.Issue{{Path: "/project", Code: "too_large", Message: fmt.Sprintf("описание больше %d байт", cfg.Limits.ProjectBytes)}}
	} else if p, issues = project.Parse(b.Project); len(issues) == 0 {
		issues = project.Validate(p)
	}
	if len(issues) > 0 {
		return result{command: command, err: &ErrorBody{Code: plan.CodeInvalidProject, Message: "описание проекта не прошло проверку", Details: issues, Hint: "исправьте поля из details"}}
	}
	pr, badPlan := buildPlan(command, p, "", b.RotatePassword, b.Ciphertexts, env)
	if badPlan != nil {
		return *badPlan
	}
	if b.ExpectHash != "" && b.ExpectHash != pr.pl.ProjectHash {
		return fail(command, plan.CodePrecondition, "описание изменилось после просмотра: сделайте предпросмотр заново")
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
