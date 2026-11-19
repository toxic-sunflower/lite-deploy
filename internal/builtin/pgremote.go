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
	hbaBegin = "# --- lite-deploy: begin (не править вручную) ---"
	hbaEnd   = "# --- lite-deploy: end ---"
	pgPort   = 5432
)

// hbaRule одно правило удалённого доступа.
type hbaRule struct{ DB, Role, From string }

func (r hbaRule) line() string {
	return fmt.Sprintf("hostssl %s %s %s scram-sha-256", r.DB, r.Role, r.From)
}

// normalizeFrom проверяет источник и приводит одиночный адрес к сети /32 или /128. Любой адрес и 0.0.0.0/0 отклоняются.
func normalizeFrom(s string) (string, error) {
	if a, err := netip.ParseAddr(s); err == nil {
		if a.IsUnspecified() {
			return "", fmt.Errorf("нужен конкретный адрес, а не %s", s)
		}
		return netip.PrefixFrom(a, a.BitLen()).String(), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return "", fmt.Errorf("источник: адрес (10.0.0.5) или сеть (10.0.0.0/24)")
	}
	if p.Bits() == 0 {
		return "", fmt.Errorf("сеть /0 пускает весь интернет; укажите конкретный адрес или сеть")
	}
	return p.Masked().String(), nil
}

// hbaSplit делит файл на текст вне блока lite-deploy и правила внутри блока.
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

// hbaJoin собирает файл: прежний текст и блок правил в конце (пустой блок не пишется).
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
		return "", plugin.Errorf("step_failed", "PostgreSQL не назвал файл pg_hba.conf")
	}
	return p, nil
}

// applyHBA записывает новые правила, перечитывает конфигурацию и при ошибках в файле возвращает прежнее содержимое.
func (t tools) applyHBA(path, old string, outside string, rules []hbaRule) error {
	if err := t.env.FS.Overwrite(path, []byte(hbaJoin(outside, rules))); err != nil {
		return plugin.Errorf("step_failed", "запись %s: %v", path, err)
	}
	bad, err := t.psql("select pg_reload_conf(); select count(*) from pg_hba_file_rules where error is not null")
	if err == nil {
		lines := strings.Fields(bad)
		err = nil
		if len(lines) > 0 && lines[len(lines)-1] != "0" {
			err = plugin.Errorf("step_failed", "pg_hba.conf содержит ошибки")
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
		return "", "", "", plugin.Errorf("usage", "использование: lite pg allow|deny БАЗА РОЛЬ ОТКУДА")
	}
	if !pgNameRe.MatchString(args[0]) || !pgNameRe.MatchString(args[1]) {
		return "", "", "", plugin.Errorf("usage", "неверное имя базы или роли")
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
		return plugin.Output{}, plugin.Errorf("precondition_failed", "в PostgreSQL выключен ssl; удалённый доступ без шифрования не открываю")
	}
	path, err := t.hbaFile()
	if err != nil {
		return plugin.Output{}, err
	}
	b, err := t.env.FS.ReadFile(path)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "чтение %s: %v", path, err)
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
	return plugin.Output{Text: fmt.Sprintf("Разрешено: база %s, роль %s, с %s (ssl, пароль), порт %d открыт для этого адреса.\n", db, role, from, pgPort),
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
		return plugin.Output{}, plugin.Errorf("step_failed", "чтение %s: %v", path, err)
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
		return plugin.Output{}, plugin.Errorf("precondition_failed", "такого правила нет (lite pg access)")
	}
	if err := t.applyHBA(path, string(b), outside, keep); err != nil {
		return plugin.Output{}, err
	}
	if !stillFrom { // порт закрываем, только если с этого адреса больше ничего не разрешено
		if _, fe := t.fw().Remove(t.ctx, pgPort, "tcp", from, "allow"); fe != nil && fe.Code != firewall.CodeNotFound {
			return plugin.Output{}, fwErr(fe)
		}
	}
	return textOut(fmt.Sprintf("Запрещено: база %s, роль %s, с %s.\n", db, role, from)), nil
}

func (t tools) pgAccess() (plugin.Output, error) {
	path, err := t.hbaFile()
	if err != nil {
		return plugin.Output{}, err
	}
	b, err := t.env.FS.ReadFile(path)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "чтение %s: %v", path, err)
	}
	_, rules := hbaSplit(string(b))
	var sb strings.Builder
	list := []map[string]string{}
	for _, r := range rules {
		fmt.Fprintf(&sb, "%s\t%s\t%s\n", r.DB, r.Role, r.From)
		list = append(list, map[string]string{"database": r.DB, "role": r.Role, "from": r.From})
	}
	if sb.Len() == 0 {
		sb.WriteString("Удалённый доступ не открыт.\n")
	}
	return plugin.Output{Text: sb.String(), Data: map[string]any{"rules": list}}, nil
}

// pgListen задаёт адрес, на котором PostgreSQL принимает соединения кроме localhost, и перезапускает службу.
func (t tools) pgListen(args []string) (plugin.Output, error) {
	if len(args) != 1 {
		return plugin.Output{}, plugin.Errorf("usage", "использование: lite pg listen АДРЕС (адрес этого сервера во внутренней сети) или lite pg listen localhost")
	}
	value := "localhost"
	if args[0] != "localhost" {
		a, err := netip.ParseAddr(args[0])
		if err != nil || a.IsUnspecified() || a.IsLoopback() || net.ParseIP(args[0]) == nil {
			return plugin.Output{}, plugin.Errorf("usage", "нужен один конкретный адрес этого сервера, не 0.0.0.0 и не *")
		}
		value = "localhost," + a.String()
	}
	if _, err := t.psql(fmt.Sprintf("alter system set listen_addresses = '%s'", value)); err != nil {
		return plugin.Output{}, err
	}
	if err := t.must("systemctl", "restart", "postgresql"); err != nil {
		return plugin.Output{}, err
	}
	return textOut("PostgreSQL слушает: " + value + ". Доступ с адресов открывается командой lite pg allow.\n"), nil
}
