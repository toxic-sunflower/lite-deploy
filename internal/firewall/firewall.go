// Package firewall управляет брандмауэром ufw: показ правил, открытие и закрытие портов, включение.
// Порт SSH и порты Caddy (80, 443) защищены: закрыть их через lite-deploy нельзя.
// Внешние команды запускаются только через host.Runner.
package firewall

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Коды ошибок.
const (
	CodeUsage        = "usage"
	CodePrecondition = "precondition_failed"
	CodeProtected    = "protected_rule"
	CodeNotFound     = "rule_not_found"
	CodeFailed       = "step_failed"
)

// Error ошибка с кодом для CLI.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func fail(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Rule одно правило ufw.
type Rule struct {
	Port      int    `json:"port"`   // 0: правило нестандартного вида, показывается как есть
	Proto     string `json:"proto"`  // tcp, udp или пусто (любой)
	Action    string `json:"action"` // allow, limit, deny, reject
	From      string `json:"from"`   // any или адрес/сеть
	Protected bool   `json:"protected"`
	Raw       string `json:"raw"` // строка из `ufw status`
}

// Raw0 порт и протокол для показа людям (например 8080/tcp); для нестандартного правила исходная строка.
func (r Rule) Raw0() string {
	if r.Port == 0 {
		f := splitCols.Split(r.Raw, -1)
		return f[0]
	}
	if r.Proto == "" {
		return strconv.Itoa(r.Port)
	}
	return fmt.Sprintf("%d/%s", r.Port, r.Proto)
}

// State состояние брандмауэра.
type State struct {
	Active  bool   `json:"active"`
	SSHPort int    `json:"ssh_port"`
	Rules   []Rule `json:"rules"`
}

// Manager управляет ufw.
type Manager struct {
	Runner host.Runner
}

func (m *Manager) run(ctx context.Context, name string, args ...string) (host.Result, error) {
	return m.Runner.Run(ctx, host.Cmd{Name: name, Args: args})
}

func (m *Manager) ufw(ctx context.Context, args ...string) (string, *Error) {
	res, err := m.run(ctx, "ufw", args...)
	if err != nil {
		return "", fail(CodePrecondition, "не удалось запустить ufw (он установлен?): %v", err)
	}
	out := strings.TrimSpace(string(res.Stdout) + "\n" + string(res.Stderr))
	if res.ExitCode != 0 {
		return "", fail(CodeFailed, "ufw %s: %s", strings.Join(args, " "), out)
	}
	return out, nil
}

// sshPort читает порт sshd; если не удалось, 22.
func (m *Manager) sshPort(ctx context.Context) int {
	res, err := m.run(ctx, "sshd", "-T")
	if err == nil && res.ExitCode == 0 {
		for _, l := range strings.Split(string(res.Stdout), "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[0] == "port" {
				if n, e := strconv.Atoi(f[1]); e == nil && n > 0 && n < 65536 {
					return n
				}
			}
		}
	}
	return 22
}

var splitCols = regexp.MustCompile(`\s{2,}`)

// parseRule разбирает строку таблицы `ufw status`; ok=false, если это не правило.
func parseRule(line string, protectedPorts map[int]bool) (Rule, bool, bool) {
	line = strings.TrimRight(line, " \t\r")
	if i := strings.Index(line, " # "); i >= 0 {
		line = line[:i]
	}
	cols := splitCols.Split(strings.TrimSpace(line), -1)
	if len(cols) < 3 {
		return Rule{}, false, false
	}
	to, action, from := cols[0], cols[1], cols[2]
	v6 := strings.Contains(to, "(v6)") || strings.Contains(from, "(v6)")
	to = strings.TrimSpace(strings.ReplaceAll(to, "(v6)", ""))
	from = strings.TrimSpace(strings.ReplaceAll(from, "(v6)", ""))
	act := strings.ToLower(strings.Fields(action)[0])
	switch act {
	case "allow", "limit", "deny", "reject":
	default:
		return Rule{}, false, false
	}
	if strings.EqualFold(from, "anywhere") {
		from = "any"
	}
	r := Rule{Action: act, From: from, Raw: strings.TrimSpace(line)}
	spec := to
	if i := strings.Index(spec, "/"); i >= 0 {
		r.Proto = spec[i+1:]
		spec = spec[:i]
	}
	if n, err := strconv.Atoi(spec); err == nil && n > 0 && n < 65536 && (r.Proto == "" || r.Proto == "tcp" || r.Proto == "udp") {
		r.Port = n
		r.Protected = protectedPorts[n]
	}
	return r, v6, true
}

// Status показывает состояние брандмауэра.
func (m *Manager) Status(ctx context.Context) (*State, *Error) {
	out, e := m.ufw(ctx, "status")
	if e != nil {
		return nil, e
	}
	st := &State{SSHPort: m.sshPort(ctx), Rules: []Rule{}}
	st.Active = strings.Contains(out, "Status: active")
	prot := map[int]bool{st.SSHPort: true, 80: true, 443: true}
	type key struct {
		port       int
		proto, act string
		from       string
	}
	seen := map[key]bool{}
	var v6 []Rule
	for _, l := range strings.Split(out, "\n") {
		r, isV6, ok := parseRule(l, prot)
		if !ok {
			continue
		}
		if isV6 {
			v6 = append(v6, r)
			continue
		}
		seen[key{r.Port, r.Proto, r.Action, r.From}] = true
		st.Rules = append(st.Rules, r)
	}
	for _, r := range v6 { // правило только для IPv6 тоже показываем
		if !seen[key{r.Port, r.Proto, r.Action, r.From}] {
			st.Rules = append(st.Rules, r)
		}
	}
	return st, nil
}

// ValidateRule проверяет порт, протокол и источник.
func ValidateRule(port int, proto, from string) *Error {
	if port < 1 || port > 65535 {
		return fail(CodeUsage, "порт должен быть от 1 до 65535")
	}
	if proto != "tcp" && proto != "udp" {
		return fail(CodeUsage, "протокол: tcp или udp")
	}
	if from != "" && from != "any" {
		if net.ParseIP(from) == nil {
			if _, _, err := net.ParseCIDR(from); err != nil {
				return fail(CodeUsage, "источник: адрес или сеть вида 10.0.0.0/8, либо пусто для любого")
			}
		}
	}
	return nil
}

func ruleArgs(verb string, port int, proto, from string) []string {
	if from == "" || from == "any" {
		return []string{verb, fmt.Sprintf("%d/%s", port, proto)}
	}
	return []string{verb, "proto", proto, "from", from, "to", "any", "port", strconv.Itoa(port)}
}

// Allow открывает порт (для всех или только с указанного адреса) и возвращает новое состояние.
func (m *Manager) Allow(ctx context.Context, port int, proto, from string) (*State, *Error) {
	if e := ValidateRule(port, proto, from); e != nil {
		return nil, e
	}
	if _, e := m.ufw(ctx, ruleArgs("allow", port, proto, from)...); e != nil {
		return nil, e
	}
	return m.Status(ctx)
}

// Remove удаляет разрешающее правило (allow или limit). Защищённые порты удалить нельзя.
func (m *Manager) Remove(ctx context.Context, port int, proto, from, action string) (*State, *Error) {
	if e := ValidateRule(port, proto, from); e != nil {
		return nil, e
	}
	if action != "allow" && action != "limit" && action != "deny" {
		return nil, fail(CodeUsage, "действие правила: allow, limit или deny")
	}
	st, e := m.Status(ctx)
	if e != nil {
		return nil, e
	}
	if port == st.SSHPort || port == 80 || port == 443 {
		return nil, fail(CodeProtected, "порт %d защищён (SSH или входящий трафик Caddy): закрыть его через lite-deploy нельзя", port)
	}
	want := from
	if want == "" {
		want = "any"
	}
	found := false
	for _, r := range st.Rules {
		if r.Port == port && r.Proto == proto && r.Action == action && r.From == want {
			found = true
		}
	}
	if !found {
		return nil, fail(CodeNotFound, "правила %s %d/%s от %s нет", action, port, proto, want)
	}
	args := append([]string{"delete", action}, ruleArgs("", port, proto, from)[1:]...)
	if _, e := m.ufw(ctx, args...); e != nil {
		return nil, e
	}
	return m.Status(ctx)
}

// Enable включает брандмауэр. Перед этим гарантирует правила для SSH (с ограничением частоты), 80 и 443,
// чтобы включение не закрыло доступ к серверу и сайтам.
func (m *Manager) Enable(ctx context.Context) (*State, *Error) {
	ssh := m.sshPort(ctx)
	steps := [][]string{
		{"default", "deny", "incoming"},
		{"default", "allow", "outgoing"},
		{"limit", fmt.Sprintf("%d/tcp", ssh)},
		{"allow", "80/tcp"},
		{"allow", "443/tcp"},
		{"allow", "443/udp"},
		{"--force", "enable"},
	}
	for _, s := range steps {
		if _, e := m.ufw(ctx, s...); e != nil {
			return nil, e
		}
	}
	return m.Status(ctx)
}
