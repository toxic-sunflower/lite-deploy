package builtin

import (
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

const (
	hbaBegin = "# --- lite-deploy: begin (do not edit by hand) ---"
	hbaEnd   = "# --- lite-deploy: end ---"
	pgPort   = 5432
)

// hbaRule is one remote access rule.
type hbaRule struct{ DB, Role, From string }

func (r hbaRule) line() string {
	return fmt.Sprintf("hostssl %s %s %s scram-sha-256", r.DB, r.Role, r.From)
}

// normalizeFrom validates a source and turns a single address into a /32 or /128 network. Any address and 0.0.0.0/0 are rejected.
func normalizeFrom(s string) (string, error) {
	if a, err := netip.ParseAddr(s); err == nil {
		if a.IsUnspecified() {
			return "", fmt.Errorf("a specific address is required, not %s", s)
		}
		return netip.PrefixFrom(a, a.BitLen()).String(), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return "", fmt.Errorf("source: an address (10.0.0.5) or a network (10.0.0.0/24)")
	}
	if p.Bits() == 0 {
		return "", fmt.Errorf("the network /0 lets the whole internet in; give a specific address or network")
	}
	return p.Masked().String(), nil
}

// hbaSplit splits the file into the text outside the lite-deploy block and the rules inside the block.
func hbaSplit(content string) (outside string, rules []hbaRule) {
	var keep []string
	in := false
	for _, l := range strings.Split(content, "\n") {
		switch {
		case strings.HasPrefix(l, "# --- lite-deploy: begin"):
			in = true
		case strings.HasPrefix(l, "# --- lite-deploy: end"):
			in = false
		case in:
			if f := strings.Fields(l); len(f) == 5 && f[0] == "hostssl" {
				rules = append(rules, hbaRule{DB: f[1], Role: f[2], From: f[3]})
			}
		default:
			keep = append(keep, l)
		}
	}
	return strings.TrimRight(strings.Join(keep, "\n"), "\n"), rules
}

// hbaJoin builds the file: the previous text and the rule block at the end (an empty block is not written).
func hbaJoin(outside string, rules []hbaRule) string {
	s := outside + "\n"
	if len(rules) == 0 {
		return s
	}
	s += "\n" + hbaBegin + "\n"
	for _, r := range rules {
		s += r.line() + "\n"
	}
	return s + hbaEnd + "\n"
}

func (t tools) hbaFile() (string, error) {
	out, err := t.psql("show hba_file")
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(out)
	if p == "" {
		return "", plugin.Errorf("step_failed", "PostgreSQL did not report the pg_hba.conf file")
	}
	return p, nil
}

// applyHBA writes the new rules, reloads the configuration and restores the previous content if the file has errors.
func (t tools) applyHBA(path, old string, outside string, rules []hbaRule) error {
	if err := t.env.FS.Overwrite(path, []byte(hbaJoin(outside, rules))); err != nil {
		return plugin.Errorf("step_failed", "write %s: %v", path, err)
	}
	bad, err := t.psql("select pg_reload_conf(); select count(*) from pg_hba_file_rules where error is not null")
	if err == nil {
		lines := strings.Fields(bad)
		err = nil
		if len(lines) > 0 && lines[len(lines)-1] != "0" {
			err = plugin.Errorf("step_failed", "pg_hba.conf contains errors")
		}
	}
	if err != nil {
		_ = t.env.FS.Overwrite(path, []byte(old))
		_, _ = t.psql("select pg_reload_conf()")
		return err
	}
	return nil
}

func (t tools) fw() *firewall.Manager { return &firewall.Manager{Runner: t.env.Runner} }

func hbaArgs(args []string) (db, role, from string, err error) {
	if len(args) != 3 {
		return "", "", "", plugin.Errorf("usage", "usage: lite pg allow|deny DATABASE ROLE FROM")
	}
	if !pgNameRe.MatchString(args[0]) || !pgNameRe.MatchString(args[1]) {
		return "", "", "", plugin.Errorf("usage", "invalid database or role name")
	}
	f, e := normalizeFrom(args[2])
	if e != nil {
		return "", "", "", plugin.Errorf("usage", "%v", e)
	}
	return args[0], args[1], f, nil
}

func (t tools) pgAllow(args []string) (plugin.Output, error) {
	db, role, from, err := hbaArgs(args)
	if err != nil {
		return plugin.Output{}, err
	}
	if on, e := t.psql("show ssl"); e != nil || strings.TrimSpace(on) != "on" {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "ssl is off in PostgreSQL; remote access without encryption is not opened")
	}
	path, err := t.hbaFile()
	if err != nil {
		return plugin.Output{}, err
	}
	b, err := t.env.FS.ReadFile(path)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "read %s: %v", path, err)
	}
	outside, rules := hbaSplit(string(b))
	r := hbaRule{db, role, from}
	have := false
	for _, x := range rules {
		have = have || x == r
	}
	if !have {
		rules = append(rules, r)
		if err := t.applyHBA(path, string(b), outside, rules); err != nil {
			return plugin.Output{}, err
		}
	}
	if _, fe := t.fw().Allow(t.ctx, pgPort, "tcp", from); fe != nil {
		return plugin.Output{}, fwErr(fe)
	}
	return plugin.Output{Text: fmt.Sprintf("Allowed: database %s, role %s, from %s (ssl, password), port %d is open for this address.\n", db, role, from, pgPort),
		Data: map[string]any{"database": db, "role": role, "from": from}}, nil
}

func (t tools) pgDeny(args []string) (plugin.Output, error) {
	db, role, from, err := hbaArgs(args)
	if err != nil {
		return plugin.Output{}, err
	}
	path, err := t.hbaFile()
	if err != nil {
		return plugin.Output{}, err
	}
	b, err := t.env.FS.ReadFile(path)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "read %s: %v", path, err)
	}
	outside, rules := hbaSplit(string(b))
	var keep []hbaRule
	found, stillFrom := false, false
	for _, x := range rules {
		if x == (hbaRule{db, role, from}) {
			found = true
			continue
		}
		stillFrom = stillFrom || x.From == from
		keep = append(keep, x)
	}
	if !found {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "there is no such rule (lite pg access)")
	}
	if err := t.applyHBA(path, string(b), outside, keep); err != nil {
		return plugin.Output{}, err
	}
	if !stillFrom { // the port is closed only if nothing else is allowed from this address
		if _, fe := t.fw().Remove(t.ctx, pgPort, "tcp", from, "allow"); fe != nil && fe.Code != firewall.CodeNotFound {
			return plugin.Output{}, fwErr(fe)
		}
	}
	return textOut(fmt.Sprintf("Denied: database %s, role %s, from %s.\n", db, role, from)), nil
}

func (t tools) pgAccess() (plugin.Output, error) {
	path, err := t.hbaFile()
	if err != nil {
		return plugin.Output{}, err
	}
	b, err := t.env.FS.ReadFile(path)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "read %s: %v", path, err)
	}
	_, rules := hbaSplit(string(b))
	var sb strings.Builder
	list := []map[string]string{}
	for _, r := range rules {
		fmt.Fprintf(&sb, "%s\t%s\t%s\n", r.DB, r.Role, r.From)
		list = append(list, map[string]string{"database": r.DB, "role": r.Role, "from": r.From})
	}
	if sb.Len() == 0 {
		sb.WriteString("Remote access is not open.\n")
	}
	return plugin.Output{Text: sb.String(), Data: map[string]any{"rules": list}}, nil
}

// pgListen sets the address PostgreSQL accepts connections on besides localhost, and restarts the service.
func (t tools) pgListen(args []string) (plugin.Output, error) {
	if len(args) != 1 {
		return plugin.Output{}, plugin.Errorf("usage", "usage: lite pg listen ADDRESS (this server's address on the internal network) or lite pg listen localhost")
	}
	value := "localhost"
	if args[0] != "localhost" {
		a, err := netip.ParseAddr(args[0])
		if err != nil || a.IsUnspecified() || a.IsLoopback() || net.ParseIP(args[0]) == nil {
			return plugin.Output{}, plugin.Errorf("usage", "one specific address of this server is required, not 0.0.0.0 and not *")
		}
		value = "localhost," + a.String()
	}
	if _, err := t.psql(fmt.Sprintf("alter system set listen_addresses = '%s'", value)); err != nil {
		return plugin.Output{}, err
	}
	if err := t.must("systemctl", "restart", "postgresql"); err != nil {
		return plugin.Output{}, err
	}
	return textOut("PostgreSQL listens on: " + value + ". Allow access from an address with lite pg allow.\n"), nil
}

// hbaWideRules finds the rules of pg_hba.conf (outside the block lite manages) that open PostgreSQL wider than the
// isolation of projects allows: a rule without a password (trust), or a rule for every database and every user from an
// address that is not this server. The result has one description per rule and whether it is serious (trust).
func hbaWideRules(text string) (wide []string, trust []string) {
	for _, l := range strings.Split(text, "\n") {
		if i := strings.Index(l, "#"); i >= 0 {
			l = l[:i]
		}
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		typ := f[0]
		method := f[len(f)-1]
		if strings.HasPrefix(method, "trust") || method == "trust" {
			trust = append(trust, strings.Join(f, " "))
			continue
		}
		if typ != "host" && typ != "hostssl" && typ != "hostnossl" && typ != "hostgssenc" && typ != "hostnogssenc" {
			continue
		}
		if len(f) < 5 {
			continue
		}
		db, user, addr := f[1], f[2], f[3]
		if !strings.Contains(","+db+",", ",all,") || !strings.Contains(","+user+",", ",all,") {
			continue
		}
		switch addr {
		case "127.0.0.1/32", "::1/128", "localhost", "127.0.0.1", "::1":
			continue
		}
		wide = append(wide, strings.Join(f, " "))
	}
	return wide, trust
}

// hbaFindings reports the wide rules of pg_hba.conf found outside the lite block.
func (t tools) hbaFindings() []plugin.Finding {
	path, err := t.hbaFile()
	if err != nil {
		return nil
	}
	b, err := t.env.FS.ReadFile(path)
	if err != nil {
		return nil
	}
	outside, _ := hbaSplit(string(b))
	wide, trust := hbaWideRules(outside)
	var out []plugin.Finding
	if len(trust) > 0 {
		out = append(out, finding("pg", "hba-trust", plugin.LevelFail, "pg_hba.conf lets someone in without a password: "+trust[0], "remove the rule with method trust from "+path))
	}
	if len(wide) > 0 {
		out = append(out, finding("pg", "hba-wide", plugin.LevelWarn, "pg_hba.conf lets other addresses reach every database as every user: "+wide[0]+". One project could reach the data of another.", "narrow the rule in "+path+" to one database and one user"))
	}
	if len(out) == 0 {
		out = append(out, finding("pg", "hba", plugin.LevelOK, "the PostgreSQL access rules keep projects apart", ""))
	}
	return out
}
