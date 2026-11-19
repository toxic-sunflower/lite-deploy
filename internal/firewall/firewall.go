// Package firewall manages the ufw firewall: showing rules, opening and closing ports, enabling.
// The SSH port and the Caddy ports (80, 443) are protected: they cannot be closed through lite-deploy.
// External commands run only through host.Runner.
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

// Error codes.
const (
	CodeUsage        = "usage"
	CodePrecondition = "precondition_failed"
	CodeProtected    = "protected_rule"
	CodeNotFound     = "rule_not_found"
	CodeFailed       = "step_failed"
)

// Error is an error with a code for the CLI.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func fail(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Rule is one ufw rule.
type Rule struct {
	Port      int    `json:"port"`   // 0: a rule of a non-standard shape, shown as is
	Proto     string `json:"proto"`  // tcp, udp or empty (any)
	Action    string `json:"action"` // allow, limit, deny, reject
	From      string `json:"from"`   // any or an address/network
	Protected bool   `json:"protected"`
	Raw       string `json:"raw"` // a line from `ufw status`
}

// Raw0 is the port and protocol for showing to people (for example 8080/tcp); for a non-standard rule, the original line.
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

// State is the firewall state.
type State struct {
	Active  bool   `json:"active"`
	SSHPort int    `json:"ssh_port"`
	Rules   []Rule `json:"rules"`
}

// Manager manages ufw.
type Manager struct {
	Runner host.Runner
}

func (m *Manager) run(ctx context.Context, name string, args ...string) (host.Result, error) {
	return m.Runner.Run(ctx, host.Cmd{Name: name, Args: args})
}

func (m *Manager) ufw(ctx context.Context, args ...string) (string, *Error) {
	res, err := m.run(ctx, "ufw", args...)
	if err != nil {
		return "", fail(CodePrecondition, "could not run ufw (is it installed?): %v", err)
	}
	out := strings.TrimSpace(string(res.Stdout) + "\n" + string(res.Stderr))
	if res.ExitCode != 0 {
		return "", fail(CodeFailed, "ufw %s: %s", strings.Join(args, " "), out)
	}
	return out, nil
}

// sshPort reads the sshd port; 22 if that failed.
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

// parseRule parses a line of the `ufw status` table; ok=false if it is not a rule.
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

// Status shows the firewall state.
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
	for _, r := range v6 { // a rule for IPv6 only is shown too
		if !seen[key{r.Port, r.Proto, r.Action, r.From}] {
			st.Rules = append(st.Rules, r)
		}
	}
	return st, nil
}

// ValidateRule validates a port, protocol and source.
func ValidateRule(port int, proto, from string) *Error {
	if port < 1 || port > 65535 {
		return fail(CodeUsage, "the port must be from 1 to 65535")
	}
	if proto != "tcp" && proto != "udp" {
		return fail(CodeUsage, "protocol: tcp or udp")
	}
	if from != "" && from != "any" {
		if net.ParseIP(from) == nil {
			if _, _, err := net.ParseCIDR(from); err != nil {
				return fail(CodeUsage, "source: an address or a network like 10.0.0.0/8, or empty for any")
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

// Allow opens a port (for everyone or only from the given address) and returns the new state.
func (m *Manager) Allow(ctx context.Context, port int, proto, from string) (*State, *Error) {
	if e := ValidateRule(port, proto, from); e != nil {
		return nil, e
	}
	if _, e := m.ufw(ctx, ruleArgs("allow", port, proto, from)...); e != nil {
		return nil, e
	}
	return m.Status(ctx)
}

// Remove deletes an allow rule (allow or limit). Protected ports cannot be removed.
func (m *Manager) Remove(ctx context.Context, port int, proto, from, action string) (*State, *Error) {
	if e := ValidateRule(port, proto, from); e != nil {
		return nil, e
	}
	if action != "allow" && action != "limit" && action != "deny" {
		return nil, fail(CodeUsage, "rule action: allow, limit or deny")
	}
	st, e := m.Status(ctx)
	if e != nil {
		return nil, e
	}
	if port == st.SSHPort || port == 80 || port == 443 {
		return nil, fail(CodeProtected, "port %d is protected (SSH or Caddy incoming traffic): it cannot be closed through lite-deploy", port)
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
		return nil, fail(CodeNotFound, "there is no %s rule for %d/%s from %s", action, port, proto, want)
	}
	args := append([]string{"delete", action}, ruleArgs("", port, proto, from)[1:]...)
	if _, e := m.ufw(ctx, args...); e != nil {
		return nil, e
	}
	return m.Status(ctx)
}

// Enable turns the firewall on. Before that it guarantees rules for SSH (with rate limiting), 80 and 443,
// so that enabling does not cut off access to the server and the sites.
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

// Firewall modes for `init --ufw`.
const (
	ModeOff  = "off"  // there is no filtering
	ModeHTTP = "http" // SSH and web ports (80/tcp, 443/tcp, 443/udp: HTTP, HTTPS, WebSocket, HTTP/2, HTTP/3)
	ModeMax  = "max"  // SSH only
)

// ModeSteps returns the ufw commands for a mode (without the word ufw), in execution order.
// The SSH rule is always added before enabling, so as not to lose access.
func ModeSteps(mode string, sshPort int) ([][]string, *Error) {
	switch mode {
	case ModeOff:
		return [][]string{{"disable"}}, nil
	case ModeHTTP, ModeMax:
		steps := [][]string{
			{"--force", "reset"},
			{"default", "deny", "incoming"},
			{"default", "allow", "outgoing"},
			{"limit", fmt.Sprintf("%d/tcp", sshPort)},
		}
		if mode == ModeHTTP {
			steps = append(steps, []string{"allow", "80/tcp"}, []string{"allow", "443/tcp"}, []string{"allow", "443/udp"})
		}
		return append(steps, []string{"--force", "enable"}), nil
	}
	return nil, fail(CodeUsage, "firewall mode: off, http or max")
}

// SSHPort is the sshd port (22 if it could not be read).
func (m *Manager) SSHPort(ctx context.Context) int { return m.sshPort(ctx) }

// SetMode brings ufw to a mode: the previous rules are reset (except mode off, which only turns it off).
func (m *Manager) SetMode(ctx context.Context, mode string) *Error {
	steps, e := ModeSteps(mode, m.sshPort(ctx))
	if e != nil {
		return e
	}
	for _, s := range steps {
		if _, e := m.ufw(ctx, s...); e != nil {
			return e
		}
	}
	return nil
}
