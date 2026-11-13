// Package agent: агент без прав (HTTP поверх unix-сокета, D-31, D-32), исполнитель по запросу (root, запускается
// systemd на каждое соединение) и клиент. Исполнитель принимает только запросы из фиксированного списка и сам
// проверяет параметры; произвольных команд нет.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
)

// MaxRequest предел размера запроса к исполнителю, байт.
const MaxRequest = 8192

var (
	projectRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	shaRe     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	specRe    = regexp.MustCompile(`^([0-9]{1,5})/(tcp|udp)$`)
	userRe    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// Request запрос агента исполнителю.
type Request struct {
	Cmd    string   `json:"cmd"`
	Args   []string `json:"args"`
	Caller string   `json:"caller"`
}

func checkSpec(spec, from string) error {
	m := specRe.FindStringSubmatch(spec)
	if m == nil {
		return errors.New("порт задаётся как 8080/tcp")
	}
	n, _ := strconv.Atoi(m[1])
	if e := firewall.ValidateRule(n, m[2], from); e != nil {
		return errors.New(e.Message)
	}
	return nil
}

// CLIArgs проверяет запрос по списку разрешённых действий и возвращает аргументы lite-deploy (без --json).
// Всё, чего нет в списке, отклоняется.
func (r Request) CLIArgs() ([]string, error) {
	a := r.Args
	need := func(n int) error {
		if len(a) != n {
			return fmt.Errorf("%s: нужно аргументов: %d", r.Cmd, n)
		}
		return nil
	}
	switch r.Cmd {
	case "status":
		if len(a) == 0 {
			return []string{"status"}, nil
		}
		if err := need(1); err != nil {
			return nil, err
		}
		if !projectRe.MatchString(a[0]) {
			return nil, errors.New("неверное имя проекта")
		}
		return []string{"status", a[0]}, nil
	case "deploy":
		if err := need(2); err != nil {
			return nil, err
		}
		if !projectRe.MatchString(a[0]) {
			return nil, errors.New("неверное имя проекта")
		}
		if !shaRe.MatchString(a[1]) {
			return nil, errors.New("нужен полный хэш коммита: 40 символов 0-9a-f")
		}
		return []string{"deploy", a[0], a[1]}, nil
	case "rollback":
		if err := need(1); err != nil {
			return nil, err
		}
		if !projectRe.MatchString(a[0]) {
			return nil, errors.New("неверное имя проекта")
		}
		return []string{"rollback", a[0]}, nil
	case "firewall.status":
		if err := need(0); err != nil {
			return nil, err
		}
		return []string{"firewall", "status"}, nil
	case "firewall.enable":
		if err := need(0); err != nil {
			return nil, err
		}
		return []string{"firewall", "enable", "--yes"}, nil
	case "firewall.allow": // аргументы: порт/протокол, источник (пусто: любой)
		if err := need(2); err != nil {
			return nil, err
		}
		if err := checkSpec(a[0], a[1]); err != nil {
			return nil, err
		}
		out := []string{"firewall", "allow", a[0]}
		if a[1] != "" {
			out = append(out, "--from", a[1])
		}
		return append(out, "--yes"), nil
	case "firewall.remove": // аргументы: порт/протокол, источник (пусто: любой), действие
		if err := need(3); err != nil {
			return nil, err
		}
		if err := checkSpec(a[0], a[1]); err != nil {
			return nil, err
		}
		if a[2] != "allow" && a[2] != "limit" && a[2] != "deny" {
			return nil, errors.New("неверное действие правила")
		}
		out := []string{"firewall", "remove", a[0], "--action", a[2]}
		if a[1] != "" && a[1] != "any" {
			out = append(out, "--from", a[1])
		}
		return append(out, "--yes"), nil
	}
	return nil, fmt.Errorf("действие %q не разрешено", r.Cmd)
}

// ErrorJSON ответ-конверт с ошибкой (формат `--json`, SPEC-v0.md, 6.2).
func ErrorJSON(command, code, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"ok": false, "api_version": "v1", "command": command,
		"error": map[string]string{"code": code, "message": msg}})
	return b
}

// Exec обрабатывает одно соединение исполнителя: читает один запрос, выполняет и пишет ответ.
// run выполняет команду lite-deploy с аргументами (включая --json) и возвращает её конверт.
// journal получает строку на каждое действие (журнал systemd, недоступный агенту для изменения).
func Exec(r io.Reader, w io.Writer, run func(args []string) []byte, journal io.Writer) {
	var req Request
	dec := json.NewDecoder(io.LimitReader(r, MaxRequest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		_, _ = w.Write(ErrorJSON("exec", "usage", "неверный запрос"))
		return
	}
	args, err := req.CLIArgs()
	if err != nil {
		fmt.Fprintf(journal, "отклонено: cmd=%q caller=%q: %v\n", req.Cmd, req.Caller, err)
		_, _ = w.Write(ErrorJSON(req.Cmd, "usage", err.Error()))
		return
	}
	caller := req.Caller
	if !userRe.MatchString(caller) {
		caller = "?"
	}
	fmt.Fprintf(journal, "выполняется: caller=%s %s\n", caller, strings.Join(args, " "))
	out := run(append(args, "--json"))
	_, _ = w.Write(out)
}
