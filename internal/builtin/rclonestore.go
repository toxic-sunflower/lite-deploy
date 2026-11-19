package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

const backupRoot = "/var/backups/lite-deploy"

// RcloneStore место хранения копий вне сервера, работающее через rclone (Backblaze B2, S3, SFTP).
// Все места ведут себя одинаково: setup, upload, list, fetch, schedule, status, проверки; отличается только setup.
type RcloneStore struct {
	PluginName string // имя плагина: backblaze, s3, sftp
	Title      string // для сообщений
	Remote     string // имя remote в rclone.conf
	Describe   string
	SetupArgs  string // подсказка по аргументам setup
	SetupHelp  string
	setupLocal func(t tools, s *RcloneStore, args []string, in string) (conf string, path string, files map[string]string, err error)
}

func (s *RcloneStore) dir() string      { return "/etc/lite-deploy/" + s.PluginName }
func (s *RcloneStore) conf() string     { return s.dir() + "/rclone.conf" }
func (s *RcloneStore) settings() string { return s.dir() + "/settings.json" }
func (s *RcloneStore) lastFile() string {
	return "/var/lib/lite-deploy/" + s.PluginName + "-last-upload"
}
func (s *RcloneStore) unitBase() string { return "lite-" + s.PluginName + "-upload" }

// Name, Kind, Requires, Description реализуют plugin.Plugin.
func (s *RcloneStore) Name() string        { return s.PluginName }
func (s *RcloneStore) Kind() string        { return "builtin" }
func (s *RcloneStore) Requires() []string  { return nil }
func (s *RcloneStore) Description() string { return s.Describe }

// Commands реализует plugin.Plugin.
func (s *RcloneStore) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "install", Help: "поставить rclone", Confirm: true},
		{Name: "setup", Confirm: true, Args: s.SetupArgs, Help: s.SetupHelp},
		{Name: "status", Root: true, Help: "настройка и время последней отправки"},
		{Name: "upload", Root: true, Help: "отправить копии (ничего не удаляя в месте хранения)"},
		{Name: "list", Root: true, Help: "что лежит в месте хранения"},
		{Name: "fetch", Root: true, Args: "[config|pg|all]", Help: "скачать копии в /var/backups/lite-deploy (на новом сервере; по умолчанию config)"},
		{Name: "schedule", Confirm: true, Args: "on|off", Help: "ежедневная отправка по таймеру systemd"},
	}
}

// InstallRclone ставит rclone.
func InstallRclone(ctx context.Context, env plugin.Env, say Say) error {
	say.say("Ставлю rclone...")
	return tools{ctx, env}.install("rclone")
}

type storeConfig struct {
	Path string `json:"path"`
}

func (s *RcloneStore) load(t tools) (storeConfig, error) {
	b, err := t.env.FS.ReadFile(s.settings())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			e := plugin.Errorf("precondition_failed", "%s не настроен", s.Title)
			e.Hint = "lite " + s.PluginName + " setup " + s.SetupArgs
			return storeConfig{}, e
		}
		return storeConfig{}, plugin.Errorf("step_failed", "%s: %v", s.settings(), err)
	}
	var c storeConfig
	if err := json.Unmarshal(b, &c); err != nil || c.Path == "" {
		return storeConfig{}, plugin.Errorf("step_failed", "файл %s повреждён", s.settings())
	}
	return c, nil
}

// Configured истинно, если место хранения настроено.
func (s *RcloneStore) Configured(env plugin.Env) bool {
	_, err := s.load(tools{context.Background(), env})
	return err == nil
}

func (s *RcloneStore) remote(c storeConfig) string { return s.Remote + ":" + c.Path }

func (s *RcloneStore) rclone(t tools, args ...string) (host.Result, error) {
	full := append([]string{"--config", s.conf()}, args...)
	return t.env.Runner.Run(t.ctx, host.Cmd{Name: "rclone", Args: full, Timeout: 3 * time.Hour})
}

func failText(res host.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(res.Stderr))
}

func readAll(in io.Reader) string {
	if in == nil {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(in, 8192))
	return string(b)
}

func (s *RcloneStore) setup(t tools, args []string) (plugin.Output, error) {
	in := readAll(t.env.Stdin)
	conf, path, files, err := s.setupLocal(t, s, args, in)
	if err != nil {
		return plugin.Output{}, err
	}
	if err := t.env.FS.MkdirAll(s.dir(), 0o700); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", s.dir(), err)
	}
	for p, c := range files {
		if err := t.env.FS.WriteFile(p, []byte(c), 0o600); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", p, err)
		}
	}
	if err := t.env.FS.WriteFile(s.conf(), []byte(conf), 0o600); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", s.conf(), err)
	}
	sb, _ := json.Marshal(storeConfig{Path: path})
	if err := t.env.FS.WriteFile(s.settings(), append(sb, '\n'), 0o600); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", s.settings(), err)
	}
	cfg := storeConfig{Path: path}
	res, err := s.rclone(t, "lsf", "--max-depth", "1", s.remote(cfg))
	if err != nil || res.ExitCode != 0 {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "настройки сохранены, но место хранения не отвечает (неверный ключ или адрес): %s", failText(res, err))
	}
	return textOut(s.Title + " настроен: " + s.remote(cfg) + ". Проверка доступа прошла.\n"), nil
}

// Upload отправляет копии: copy, а не sync, чтобы ротация локальных копий не стирала их на месте хранения.
func (s *RcloneStore) Upload(t tools) (plugin.Output, error) {
	cfg, err := s.load(t)
	if err != nil {
		return plugin.Output{}, err
	}
	res, err := s.rclone(t, "copy", backupRoot, s.remote(cfg), "--exclude", "lost+found/**")
	if err != nil || res.ExitCode != 0 {
		return plugin.Output{}, plugin.Errorf("step_failed", "отправка в %s не удалась: %s", s.Title, failText(res, err))
	}
	if err := t.env.FS.MkdirAll("/var/lib/lite-deploy", 0o755); err == nil {
		_ = t.env.FS.WriteFile(s.lastFile(), []byte(t.now().UTC().Format(time.RFC3339)+"\n"), 0o644)
	}
	return textOut("Копии отправлены в " + s.remote(cfg) + ".\n"), nil
}

func (s *RcloneStore) lastTime(t tools) (time.Time, bool) {
	b, err := t.env.FS.ReadFile(s.lastFile())
	if err != nil {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	return at, err == nil
}

func (s *RcloneStore) schedule(t tools, args []string) (plugin.Output, error) {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return plugin.Output{}, plugin.Errorf("usage", "использование: lite %s schedule on|off", s.PluginName)
	}
	svcPath := "/etc/systemd/system/" + s.unitBase() + ".service"
	timerPath := "/etc/systemd/system/" + s.unitBase() + ".timer"
	if args[0] == "off" {
		_ = t.must("systemctl", "disable", "--now", s.unitBase()+".timer")
		_ = t.env.FS.Remove(timerPath)
		_ = t.env.FS.Remove(svcPath)
		_ = t.must("systemctl", "daemon-reload")
		return textOut("Ежедневная отправка выключена.\n"), nil
	}
	if _, err := s.load(t); err != nil {
		return plugin.Output{}, err
	}
	svc := "[Unit]\nDescription=lite-deploy: отправка копий (" + s.Title + ")\n\n[Service]\nType=oneshot\nExecStart=/usr/local/bin/lite-deploy " + s.PluginName + " upload\n"
	tm := "[Unit]\nDescription=lite-deploy: ежедневная отправка копий (" + s.Title + ")\n\n[Timer]\nOnCalendar=*-*-* 04:30:00\nRandomizedDelaySec=15min\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
	for p, c := range map[string]string{svcPath: svc, timerPath: tm} {
		if err := t.env.FS.WriteFile(p, []byte(c), 0o644); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", p, err)
		}
	}
	if err := t.must("systemctl", "daemon-reload"); err != nil {
		return plugin.Output{}, err
	}
	if err := t.must("systemctl", "enable", "--now", s.unitBase()+".timer"); err != nil {
		return plugin.Output{}, err
	}
	return textOut("Копии будут отправляться ежедневно около 04:30 (после снятия копий).\n"), nil
}

func (s *RcloneStore) fetch(t tools, args []string) (plugin.Output, error) {
	what := "config"
	if len(args) > 1 || (len(args) == 1 && args[0] != "config" && args[0] != "pg" && args[0] != "all") {
		return plugin.Output{}, plugin.Errorf("usage", "использование: lite %s fetch [config|pg|all]", s.PluginName)
	}
	if len(args) == 1 {
		what = args[0]
	}
	cfg, err := s.load(t)
	if err != nil {
		return plugin.Output{}, err
	}
	src, dst := s.remote(cfg), backupRoot
	if what != "all" {
		src, dst = s.remote(cfg)+"/"+what, backupRoot+"/"+what
	}
	if err := t.env.FS.MkdirAll(dst, 0o700); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", dst, err)
	}
	res, err := s.rclone(t, "copy", src, dst)
	if err != nil || res.ExitCode != 0 {
		return plugin.Output{}, plugin.Errorf("step_failed", "скачивание не удалось: %s", failText(res, err))
	}
	if what == "pg" || what == "all" {
		_ = t.env.FS.Chown(PGBackupDir, "postgres", "postgres")
	}
	return textOut("Скачано из " + src + " в " + dst + ".\n"), nil
}

// Run реализует plugin.Plugin.
func (s *RcloneStore) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	t := tools{ctx, env}
	switch cmd {
	case "setup":
		return s.setup(t, args)
	case "schedule":
		return s.schedule(t, args)
	case "fetch":
		return s.fetch(t, args)
	}
	if err := noArgs(args, cmd, s.PluginName); err != nil {
		return plugin.Output{}, err
	}
	switch cmd {
	case "install":
		if err := InstallRclone(ctx, env, nil); err != nil {
			return plugin.Output{}, err
		}
		return textOut("rclone установлен.\n"), nil
	case "upload":
		return s.Upload(t)
	case "list":
		cfg, err := s.load(t)
		if err != nil {
			return plugin.Output{}, err
		}
		res, err := s.rclone(t, "lsf", "-R", s.remote(cfg))
		if err != nil || res.ExitCode != 0 {
			return plugin.Output{}, plugin.Errorf("step_failed", "%s не отвечает: %s", s.Title, failText(res, err))
		}
		return textOut(string(res.Stdout)), nil
	case "status":
		cfg, err := s.load(t)
		if err != nil {
			return plugin.Output{}, err
		}
		last := "ещё не отправлялось"
		data := map[string]any{"path": cfg.Path}
		if at, ok := s.lastTime(t); ok {
			last = at.Format(time.RFC3339)
			data["last_upload"] = last
		}
		return plugin.Output{Text: fmt.Sprintf("%s, последняя отправка: %s\n", s.remote(cfg), last), Data: data}, nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "у плагина %s нет команды %q", s.PluginName, cmd)
}

// Check реализует plugin.Plugin.
func (s *RcloneStore) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	if _, err := s.load(t); err != nil {
		if info, serr := env.FS.Stat(s.dir()); serr != nil || !info.IsDir {
			return []plugin.Finding{finding(s.PluginName, "configured", plugin.LevelOK, s.Title+" не настроен (не используется)", "")}
		}
		return []plugin.Finding{finding(s.PluginName, "configured", plugin.LevelFail, err.Error(), "")}
	}
	var out []plugin.Finding
	if info, err := env.FS.Stat(s.conf()); err == nil && info.Mode&0o077 != 0 {
		out = append(out, finding(s.PluginName, "key-perms", plugin.LevelFail, "ключ доступа к "+s.Title+" доступен не только root", "sudo chmod 600 "+s.conf()))
	} else {
		out = append(out, finding(s.PluginName, "key-perms", plugin.LevelOK, "ключ доступа к "+s.Title+" закрыт для остальных", ""))
	}
	switch at, ok := s.lastTime(t); {
	case !ok:
		out = append(out, finding(s.PluginName, "upload", plugin.LevelWarn, "копии ещё ни разу не отправлялись", "sudo lite "+s.PluginName+" upload; sudo lite "+s.PluginName+" schedule on"))
	case t.now().Sub(at) > pgStale:
		out = append(out, finding(s.PluginName, "upload", plugin.LevelFail, "последняя отправка копий была давно ("+at.Format(time.RFC3339)+")", "sudo lite "+s.PluginName+" schedule on"))
	default:
		out = append(out, finding(s.PluginName, "upload", plugin.LevelOK, "копии отправлены "+at.Format(time.RFC3339), ""))
	}
	return out
}

var (
	bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{4,62}$`)
	prefixRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
	keyIDRe  = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)
	secretRe = regexp.MustCompile(`^[A-Za-z0-9/+_.-]{16,128}$`)
	endpoint = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{2,5})?$`)
	hostRe   = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
	userRe   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	pathRe   = regexp.MustCompile(`^/?[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
)

func bucketPath(bucket string, rest []string) (string, error) {
	if !bucketRe.MatchString(bucket) {
		return "", plugin.Errorf("usage", "неверное имя бакета")
	}
	if len(rest) == 0 {
		return bucket, nil
	}
	if len(rest) > 1 || !prefixRe.MatchString(rest[0]) || strings.Contains(rest[0], "..") {
		return "", plugin.Errorf("usage", "неверный префикс")
	}
	return bucket + "/" + rest[0], nil
}

func keyPair(in string) (string, string, error) {
	lines := readLinesFrom(in, 2)
	if len(lines) != 2 || !keyIDRe.MatchString(lines[0]) || !secretRe.MatchString(lines[1]) {
		return "", "", plugin.Errorf("usage", "на stdin нужны две строки: идентификатор ключа и секретный ключ")
	}
	return lines[0], lines[1], nil
}

func readLinesFrom(in string, n int) []string {
	var out []string
	for _, l := range strings.Split(in, "\n") {
		if len(out) == n {
			break
		}
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

// Backblaze: Backblaze B2.
var Backblaze = &RcloneStore{
	PluginName: "backblaze", Title: "Backblaze B2", Remote: "b2",
	Describe:  "копии за пределами сервера: отправка /var/backups/lite-deploy в Backblaze B2",
	SetupArgs: "БАКЕТ [ПРЕФИКС] (ключи из stdin)", SetupHelp: "сохранить ключ и бакет: две строки на stdin, ID ключа и сам ключ",
	setupLocal: func(t tools, s *RcloneStore, args []string, in string) (string, string, map[string]string, error) {
		if len(args) < 1 {
			return "", "", nil, plugin.Errorf("usage", "использование: lite backblaze setup БАКЕТ [ПРЕФИКС] (ID ключа и ключ двумя строками на stdin)")
		}
		path, err := bucketPath(args[0], args[1:])
		if err != nil {
			return "", "", nil, err
		}
		id, key, err := keyPair(in)
		if err != nil {
			return "", "", nil, err
		}
		return fmt.Sprintf("[b2]\ntype = b2\naccount = %s\nkey = %s\n", id, key), path, nil, nil
	},
}

// S3: любое S3-совместимое хранилище (AWS S3, Wasabi, MinIO, Cloudflare R2 и т.д.).
var S3 = &RcloneStore{
	PluginName: "s3", Title: "S3", Remote: "s3",
	Describe:  "копии за пределами сервера: отправка /var/backups/lite-deploy в S3-совместимое хранилище",
	SetupArgs: "АДРЕС_https БАКЕТ [ПРЕФИКС] (ключи из stdin)", SetupHelp: "сохранить адрес, бакет и ключи: две строки на stdin, ключ доступа и секретный ключ",
	setupLocal: func(t tools, s *RcloneStore, args []string, in string) (string, string, map[string]string, error) {
		if len(args) < 2 || !endpoint.MatchString(args[0]) {
			return "", "", nil, plugin.Errorf("usage", "использование: lite s3 setup https://адрес БАКЕТ [ПРЕФИКС] (ключ доступа и секретный ключ двумя строками на stdin)")
		}
		path, err := bucketPath(args[1], args[2:])
		if err != nil {
			return "", "", nil, err
		}
		id, key, err := keyPair(in)
		if err != nil {
			return "", "", nil, err
		}
		return fmt.Sprintf("[s3]\ntype = s3\nprovider = Other\naccess_key_id = %s\nsecret_access_key = %s\nendpoint = %s\n", id, key, args[0]), path, nil, nil
	},
}

// SFTP: сервер по SFTP (проверка ключа хоста обязательна).
var SFTP = &RcloneStore{
	PluginName: "sftp", Title: "SFTP", Remote: "sftp",
	Describe:  "копии за пределами сервера: отправка /var/backups/lite-deploy на сервер по SFTP",
	SetupArgs: "ХОСТ ПОЛЬЗОВАТЕЛЬ КАТАЛОГ (ключи из stdin)", SetupHelp: "stdin: закрытый ключ SSH, затем строка --- и строка known_hosts с ключом хоста (ssh-keyscan -t ed25519 ХОСТ)",
	setupLocal: func(t tools, s *RcloneStore, args []string, in string) (string, string, map[string]string, error) {
		if len(args) != 3 || !hostRe.MatchString(args[0]) || !userRe.MatchString(args[1]) || !pathRe.MatchString(args[2]) || strings.Contains(args[2], "..") {
			return "", "", nil, plugin.Errorf("usage", "использование: lite sftp setup ХОСТ ПОЛЬЗОВАТЕЛЬ КАТАЛОГ (закрытый ключ, строка ---, строка known_hosts на stdin)")
		}
		key, hosts, ok := strings.Cut(in, "\n---\n")
		hosts = strings.TrimSpace(hosts)
		if !ok || !strings.Contains(key, "PRIVATE KEY") || hosts == "" || strings.Count(hosts, "\n") > 3 {
			return "", "", nil, plugin.Errorf("usage", "на stdin нужны закрытый ключ SSH, затем строка --- и строка known_hosts: без проверки ключа хоста соединение не открывается")
		}
		keyFile, khFile := s.dir()+"/id", s.dir()+"/known_hosts"
		conf := fmt.Sprintf("[sftp]\ntype = sftp\nhost = %s\nuser = %s\nkey_file = %s\nknown_hosts_file = %s\n", args[0], args[1], keyFile, khFile)
		files := map[string]string{keyFile: strings.TrimSpace(key) + "\n", khFile: hosts + "\n"}
		return conf, "/" + strings.TrimPrefix(args[2], "/"), files, nil
	},
}

// Stores все места хранения копий вне сервера.
var Stores = []*RcloneStore{Backblaze, S3, SFTP}
