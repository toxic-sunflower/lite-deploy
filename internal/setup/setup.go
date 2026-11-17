// Package setup подготавливает сервер к работе (`lite init`): пользователи-администраторы, их SSH-ключи,
// вход по SSH только по ключу, режим файервола, пакеты плагинов. Пароли программа не видит и не хранит:
// их задаёт системная команда passwd.
package setup

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/agent"
	"github.com/toxic-sunflower/lite-deploy/internal/builtin"
	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/pkgmgr"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// SSHDropIn файл настроек sshd, которым управляет lite.
const SSHDropIn = "/etc/ssh/sshd_config.d/50-lite-deploy.conf"

const dropInMark = "# managed-by: lite-deploy (не правьте руками: файл переписывает lite init)"

var (
	userRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	keyRe  = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) [A-Za-z0-9+/]{32,}={0,3}( [^\n\r]*)?$`)
)

// Options что сделать.
type Options struct {
	Users    []string // имена; пусто и !AllUsers: текущий пользователь
	AllUsers bool
	Current  string   // кто запустил sudo (пусто: запуск прямо от root)
	Firewall string   // off, http, max
	Plugins  []string // podman, postgres
	Yes      bool     // не спрашивать подтверждение плана
}

// Env окружение.
type Env struct {
	Runner host.Runner
	FS     host.FS
	In     *bufio.Reader
	Out    io.Writer
	// Projects имена пользователей проектов: их init не трогает.
	Projects []string
}

// Account запись /etc/passwd.
type Account struct {
	Name, Home, Shell string
	UID               int
}

func (e Env) accounts() ([]Account, error) {
	b, err := e.FS.ReadFile("/etc/passwd")
	if err != nil {
		return nil, err
	}
	var out []Account
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) < 7 {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		out = append(out, Account{Name: f[0], UID: uid, Home: f[5], Shell: f[6]})
	}
	return out, nil
}

func hasShell(a Account) bool {
	return a.Shell != "" && !strings.HasSuffix(a.Shell, "/nologin") && !strings.HasSuffix(a.Shell, "/false")
}

// ResolveUsers определяет, кого настраивать.
func (e Env) ResolveUsers(o Options) ([]string, error) {
	accs, err := e.accounts()
	if err != nil {
		return nil, err
	}
	project := map[string]bool{}
	for _, p := range e.Projects {
		project[p] = true
	}
	var names []string
	switch {
	case o.AllUsers:
		for _, a := range accs {
			if a.UID >= 1000 && a.UID < 65000 && hasShell(a) && !project[a.Name] {
				names = append(names, a.Name)
			}
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("обычных пользователей с оболочкой нет: укажите --user имя, он будет создан")
		}
	case len(o.Users) > 0:
		names = o.Users
	case o.Current == "" || o.Current == "root":
		return nil, fmt.Errorf("при запуске от root укажите пользователя: lite init --user имя (существующего или нового)")
	default:
		names = []string{o.Current}
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !userRe.MatchString(n) || n == "root" {
			return nil, fmt.Errorf("неверное имя пользователя: %q", n)
		}
		if project[n] {
			return nil, fmt.Errorf("%s это пользователь проекта, администратором он быть не может", n)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

func (e Env) run(ctx context.Context, name string, args ...string) (host.Result, error) {
	return e.Runner.Run(ctx, host.Cmd{Name: name, Args: args})
}

func (e Env) must(ctx context.Context, name string, args ...string) error {
	res, err := e.run(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), strings.TrimSpace(string(res.Stderr)+" "+string(res.Stdout)))
	}
	return nil
}

func (e Env) say(format string, a ...any) { _, _ = fmt.Fprintf(e.Out, format+"\n", a...) }

func (e Env) ask(prompt string) (string, error) {
	_, _ = fmt.Fprint(e.Out, prompt)
	if e.In == nil {
		return "", io.EOF
	}
	l, err := e.In.ReadString('\n')
	if err != nil && l == "" {
		return "", err
	}
	return strings.TrimSpace(l), nil
}

// Plan человекочитаемый список того, что будет сделано.
func (e Env) Plan(o Options, users []string) ([]string, error) {
	accs, err := e.accounts()
	if err != nil {
		return nil, err
	}
	exists := map[string]bool{}
	for _, a := range accs {
		exists[a.Name] = true
	}
	var p []string
	for _, u := range users {
		if exists[u] {
			p = append(p, "пользователь "+u+": есть; права администратора (sudo), SSH-ключ, вход по SSH только по ключу")
		} else {
			p = append(p, "пользователь "+u+": будет создан; пароль (спросит passwd), права администратора (sudo), SSH-ключ, вход по SSH только по ключу")
		}
	}
	switch o.Firewall {
	case firewall.ModeOff:
		p = append(p, "файервол: выключить (без фильтрации трафика)")
	case firewall.ModeHTTP:
		p = append(p, "файервол: сбросить правила, открыть только SSH и веб-порты (80/tcp, 443/tcp, 443/udp)")
	case firewall.ModeMax:
		p = append(p, "файервол: сбросить правила, открыть только SSH (сайты на 80 и 443 снаружи недоступны, пока режим не сменён на http)")
	default:
		return nil, fmt.Errorf("режим файервола: off, http или max")
	}
	for _, pl := range o.Plugins {
		switch pl {
		case "caddy":
			p = append(p, "плагин caddy: установить Caddy, главный Caddyfile (подключает сайты проектов из /etc/caddy/conf.d), запустить службу")
		case "security":
			p = append(p, "плагин security: защита сервера: SSH только по ключу для всех и root только по ключу, fail2ban, автообновления безопасности, защитные параметры ядра")
		case "podman", "pg", "postgres":
			p = append(p, "плагин "+pl+": установить пакеты")
		default:
			return nil, fmt.Errorf("неизвестный плагин %q (есть: %s)", pl, strings.Join(builtin.PluginNames(), ", "))
		}
	}
	return p, nil
}

// Apply выполняет подготовку. Порядок выбран так, чтобы не потерять доступ: сначала пользователи и ключи,
// потом файервол (правило SSH до включения), в конце запрет входа по паролю (с проверкой конфигурации sshd).
func (e Env) Apply(ctx context.Context, o Options) error {
	users, err := e.ResolveUsers(o)
	if err != nil {
		return err
	}
	plan, err := e.Plan(o, users)
	if err != nil {
		return err
	}
	e.say("Будет сделано:")
	for _, l := range plan {
		e.say("  - %s", l)
	}
	if !o.Yes {
		a, err := e.ask("Продолжить? [y/N] ")
		if err != nil || (a != "y" && a != "Y" && a != "д" && a != "Д" && a != "yes" && a != "да") {
			return fmt.Errorf("отменено, ничего не изменено")
		}
	}
	group, err := agent.AdminGroup(ctx, e.Runner)
	if err != nil {
		return err
	}
	var keyed []string
	for _, u := range users {
		if err := e.user(ctx, u, group); err != nil {
			return fmt.Errorf("пользователь %s: %w", u, err)
		}
		keyed = append(keyed, u)
	}
	for _, pl := range o.Plugins {
		penv := plugin.Env{Runner: e.Runner, FS: e.FS, Projects: e.Projects, User: o.Current}
		if err := builtin.InstallPlugin(ctx, penv, pl, builtin.Say(e.say)); err != nil {
			return fmt.Errorf("плагин %s: %w", pl, err)
		}
	}
	if err := e.firewall(ctx, o.Firewall); err != nil {
		return err
	}
	if err := e.sshKeyOnly(ctx, keyed); err != nil {
		return err
	}
	e.say("")
	e.say("Готово. Не закрывая эту сессию, откройте новую и проверьте вход по ключу: ssh %s@этот-сервер, затем sudo -v.", users[0])
	e.say("Вход под root по SSH не менялся: отключать его или нет, решаете вы.")
	return nil
}

// user создаёт или донастраивает одного пользователя.
func (e Env) user(ctx context.Context, name, group string) error {
	accs, err := e.accounts()
	if err != nil {
		return err
	}
	var acc *Account
	for i := range accs {
		if accs[i].Name == name {
			acc = &accs[i]
		}
	}
	if acc == nil {
		e.say("Создаю пользователя %s.", name)
		if err := e.must(ctx, "useradd", "--create-home", "--shell", "/bin/bash", name); err != nil {
			return err
		}
		acc = &Account{Name: name, Home: "/home/" + name}
		if accs2, err := e.accounts(); err == nil {
			for i := range accs2 {
				if accs2[i].Name == name {
					acc = &accs2[i]
				}
			}
		}
	}
	// пароль нужен для sudo; задаёт его сам человек в системной команде passwd
	res, err := e.run(ctx, "passwd", "-S", name)
	if err != nil {
		return err
	}
	f := strings.Fields(string(res.Stdout))
	if len(f) < 2 || (f[1] != "P" && f[1] != "PS") {
		e.say("У пользователя %s нет пароля (он нужен для sudo). Задайте его:", name)
		r, err := e.Runner.Run(ctx, host.Cmd{Name: "passwd", Args: []string{name}, Interactive: true, Timeout: 10 * time.Minute})
		if err != nil {
			return err
		}
		if r.ExitCode != 0 {
			return fmt.Errorf("пароль не задан")
		}
	}
	if err := e.must(ctx, "usermod", "-aG", group, name); err != nil {
		return err
	}
	return e.key(name, acc.Home)
}

// key спрашивает открытый SSH-ключ и добавляет его в authorized_keys. Без ключа init не продолжается.
func (e Env) key(name, home string) error {
	dir, file := home+"/.ssh", home+"/.ssh/authorized_keys"
	old, _ := e.FS.ReadFile(file)
	have := 0
	for _, l := range strings.Split(string(old), "\n") {
		if keyRe.MatchString(strings.TrimSpace(l)) {
			have++
		}
	}
	prompt := fmt.Sprintf("Открытый SSH-ключ для %s (строка вида ssh-ed25519 AAAA…): ", name)
	if have > 0 {
		prompt = fmt.Sprintf("Открытый SSH-ключ для %s (у него уже ключей: %d; Enter, чтобы оставить как есть): ", name, have)
	}
	var key string
	for try := 0; ; try++ {
		k, err := e.ask(prompt)
		if err != nil && have > 0 {
			return nil // ввода нет (запуск без терминала), ключи уже есть
		}
		if err != nil {
			return fmt.Errorf("ключ не введён: без SSH-ключа вход по паролю отключать нельзя")
		}
		if k == "" && have > 0 {
			return nil
		}
		if keyRe.MatchString(k) {
			key = k
			break
		}
		if try >= 2 {
			return fmt.Errorf("это не открытый SSH-ключ; он лежит на вашем компьютере в ~/.ssh/id_ed25519.pub")
		}
		e.say("Это не похоже на открытый SSH-ключ. Он лежит на вашем компьютере в файле ~/.ssh/id_ed25519.pub.")
	}
	body := strings.Fields(key)[1]
	if strings.Contains(string(old), body) {
		return nil
	}
	if err := e.FS.MkdirAll(dir, fs.FileMode(0o700)); err != nil {
		return err
	}
	text := string(old)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := e.FS.WriteFile(file, []byte(text+key+"\n"), fs.FileMode(0o600)); err != nil {
		return err
	}
	for _, p := range []string{dir, file} {
		if err := e.FS.Chown(p, name, ""); err != nil {
			return err
		}
	}
	return e.FS.Chmod(dir, fs.FileMode(0o700))
}

func (e Env) firewall(ctx context.Context, mode string) error {
	m := &firewall.Manager{Runner: e.Runner}
	if _, err := e.run(ctx, "ufw", "version"); err != nil {
		if mode == firewall.ModeOff {
			return nil // ufw нет, фильтрации и так нет
		}
		e.say("Ставлю ufw...")
		if err := (&pkgmgr.Manager{Runner: e.Runner, FS: e.FS}).Install(ctx, "ufw"); err != nil {
			return fmt.Errorf("не удалось поставить ufw: %w", err)
		}
	}
	e.say("Файервол: режим %s (порт SSH %d).", mode, m.SSHPort(ctx))
	if fe := m.SetMode(ctx, mode); fe != nil {
		return fmt.Errorf("файервол: %s", fe.Message)
	}
	return nil
}

// dropInUsers читает пользователей из прежнего файла lite, чтобы повторный init их не потерял.
func dropInUsers(old string) []string {
	for _, l := range strings.Split(old, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "Match User "); ok {
			return strings.Split(strings.TrimSpace(rest), ",")
		}
	}
	return nil
}

// sshKeyOnly запрещает вход по паролю по SSH перечисленным пользователям (у каждого уже есть ключ).
// Конфигурация проверяется `sshd -t`; при ошибке прежний файл возвращается.
func (e Env) sshKeyOnly(ctx context.Context, users []string) error {
	oldB, oldErr := e.FS.ReadFile(SSHDropIn)
	set := map[string]bool{}
	for _, u := range append(dropInUsers(string(oldB)), users...) {
		if userRe.MatchString(u) {
			set[u] = true
		}
	}
	var all []string
	for u := range set {
		all = append(all, u)
	}
	sort.Strings(all)
	body := dropInMark + "\nMatch User " + strings.Join(all, ",") + "\n    PasswordAuthentication no\n    KbdInteractiveAuthentication no\n"
	if string(oldB) == body {
		return nil
	}
	if main, err := e.FS.ReadFile("/etc/ssh/sshd_config"); err == nil && !strings.Contains(string(main), "sshd_config.d") {
		return fmt.Errorf("в /etc/ssh/sshd_config нет строки Include /etc/ssh/sshd_config.d/*.conf: вход по паролю не изменён")
	}
	if err := e.FS.MkdirAll("/etc/ssh/sshd_config.d", fs.FileMode(0o755)); err != nil {
		return err
	}
	if err := e.FS.WriteFile(SSHDropIn, []byte(body), fs.FileMode(0o644)); err != nil {
		return err
	}
	restore := func() {
		if oldErr == nil {
			_ = e.FS.WriteFile(SSHDropIn, oldB, fs.FileMode(0o644))
		} else {
			_ = e.FS.Remove(SSHDropIn)
		}
	}
	if err := e.must(ctx, "sshd", "-t"); err != nil {
		restore()
		return fmt.Errorf("новая настройка sshd не прошла проверку, возвращена прежняя: %w", err)
	}
	if e.must(ctx, "systemctl", "reload", "ssh") != nil {
		if err := e.must(ctx, "systemctl", "reload", "sshd"); err != nil {
			restore()
			return fmt.Errorf("не удалось перечитать настройки sshd, возвращена прежняя: %w", err)
		}
	}
	e.say("Вход по SSH по паролю отключён для: %s (остался вход по ключу).", strings.Join(users, ", "))
	return nil
}
