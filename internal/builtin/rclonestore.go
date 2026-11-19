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

// RcloneStore is a storage location for backups outside the server, working through rclone (Backblaze B2, S3, SFTP).
// All locations behave the same way: setup, upload, list, fetch, schedule, status, checks; only setup differs.
type RcloneStore struct {
	PluginName string // the plugin name: backblaze, s3, sftp
	Title      string // for messages
	Remote     string // the remote name in rclone.conf
	Describe   string
	SetupArgs  string // a hint about the setup arguments
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

// Name, Kind, Requires, Description implement plugin.Plugin.
func (s *RcloneStore) Name() string        { return s.PluginName }
func (s *RcloneStore) Kind() string        { return "builtin" }
func (s *RcloneStore) Requires() []string  { return nil }
func (s *RcloneStore) Description() string { return s.Describe }

// Commands implements plugin.Plugin.
func (s *RcloneStore) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "install", Help: "install rclone", Confirm: true},
		{Name: "setup", Confirm: true, Args: s.SetupArgs, Help: s.SetupHelp},
		{Name: "status", Root: true, Help: "configuration and the time of the last upload"},
		{Name: "upload", Root: true, Help: "upload backups (never deleting anything at the storage)"},
		{Name: "list", Root: true, Help: "what is in the storage"},
		{Name: "fetch", Root: true, Args: "[config|pg|all]", Help: "download backups into /var/backups/lite-deploy (on a new server; config by default)"},
		{Name: "schedule", Confirm: true, Args: "on|off", Help: "daily upload with a systemd timer"},
	}
}

// InstallRclone installs rclone.
func InstallRclone(ctx context.Context, env plugin.Env, say Say) error {
	say.say("Installing rclone...")
	return tools{ctx, env}.install("rclone")
}

type storeConfig struct {
	Path string `json:"path"`
}

func (s *RcloneStore) load(t tools) (storeConfig, error) {
	b, err := t.env.FS.ReadFile(s.settings())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			e := plugin.Errorf("precondition_failed", "%s is not configured", s.Title)
			e.Hint = "lite " + s.PluginName + " setup " + s.SetupArgs
			return storeConfig{}, e
		}
		return storeConfig{}, plugin.Errorf("step_failed", "%s: %v", s.settings(), err)
	}
	var c storeConfig
	if err := json.Unmarshal(b, &c); err != nil || c.Path == "" {
		return storeConfig{}, plugin.Errorf("step_failed", "file %s is corrupt", s.settings())
	}
	return c, nil
}

// Configured is true if the storage location is configured.
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
		return plugin.Output{}, plugin.Errorf("precondition_failed", "settings saved, but the storage does not respond (wrong key or address): %s", failText(res, err))
	}
	return textOut(s.Title + " configured: " + s.remote(cfg) + ". Access check passed.\n"), nil
}

// Upload sends the backups: copy, not sync, so that rotating local backups does not erase them at the storage.
func (s *RcloneStore) Upload(t tools) (plugin.Output, error) {
	cfg, err := s.load(t)
	if err != nil {
		return plugin.Output{}, err
	}
	res, err := s.rclone(t, "copy", backupRoot, s.remote(cfg), "--exclude", "lost+found/**")
	if err != nil || res.ExitCode != 0 {
		return plugin.Output{}, plugin.Errorf("step_failed", "upload to %s failed: %s", s.Title, failText(res, err))
	}
	if err := t.env.FS.MkdirAll("/var/lib/lite-deploy", 0o755); err == nil {
		_ = t.env.FS.WriteFile(s.lastFile(), []byte(t.now().UTC().Format(time.RFC3339)+"\n"), 0o644)
	}
	return textOut("Backups uploaded to " + s.remote(cfg) + ".\n"), nil
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
		return plugin.Output{}, plugin.Errorf("usage", "usage: lite %s schedule on|off", s.PluginName)
	}
	svcPath := "/etc/systemd/system/" + s.unitBase() + ".service"
	timerPath := "/etc/systemd/system/" + s.unitBase() + ".timer"
	if args[0] == "off" {
		_ = t.must("systemctl", "disable", "--now", s.unitBase()+".timer")
		_ = t.env.FS.Remove(timerPath)
		_ = t.env.FS.Remove(svcPath)
		_ = t.must("systemctl", "daemon-reload")
		return textOut("The daily upload is off.\n"), nil
	}
	if _, err := s.load(t); err != nil {
		return plugin.Output{}, err
	}
	svc := "[Unit]\nDescription=lite-deploy: backup upload (" + s.Title + ")\n\n[Service]\nType=oneshot\nExecStart=/usr/local/bin/lite-deploy " + s.PluginName + " upload\n"
	tm := "[Unit]\nDescription=lite-deploy: daily backup upload (" + s.Title + ")\n\n[Timer]\nOnCalendar=*-*-* 04:30:00\nRandomizedDelaySec=15min\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
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
	return textOut("Backups will be uploaded daily around 04:30 (after the backups are taken).\n"), nil
}

func (s *RcloneStore) fetch(t tools, args []string) (plugin.Output, error) {
	what := "config"
	if len(args) > 1 || (len(args) == 1 && args[0] != "config" && args[0] != "pg" && args[0] != "all") {
		return plugin.Output{}, plugin.Errorf("usage", "usage: lite %s fetch [config|pg|all]", s.PluginName)
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
		return plugin.Output{}, plugin.Errorf("step_failed", "download failed: %s", failText(res, err))
	}
	if what == "pg" || what == "all" {
		_ = t.env.FS.Chown(PGBackupDir, "postgres", "postgres")
	}
	return textOut("Downloaded from " + src + " in " + dst + ".\n"), nil
}

// Run implements plugin.Plugin.
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
		return textOut("rclone is installed.\n"), nil
	case "upload":
		return s.Upload(t)
	case "list":
		cfg, err := s.load(t)
		if err != nil {
			return plugin.Output{}, err
		}
		res, err := s.rclone(t, "lsf", "-R", s.remote(cfg))
		if err != nil || res.ExitCode != 0 {
			return plugin.Output{}, plugin.Errorf("step_failed", "%s is not responding: %s", s.Title, failText(res, err))
		}
		return textOut(string(res.Stdout)), nil
	case "status":
		cfg, err := s.load(t)
		if err != nil {
			return plugin.Output{}, err
		}
		last := "not uploaded yet"
		data := map[string]any{"path": cfg.Path}
		if at, ok := s.lastTime(t); ok {
			last = at.Format(time.RFC3339)
			data["last_upload"] = last
		}
		return plugin.Output{Text: fmt.Sprintf("%s, last upload: %s\n", s.remote(cfg), last), Data: data}, nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "plugin %s has no command %q", s.PluginName, cmd)
}

// Check implements plugin.Plugin.
func (s *RcloneStore) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	if _, err := s.load(t); err != nil {
		if info, serr := env.FS.Stat(s.dir()); serr != nil || !info.IsDir {
			return []plugin.Finding{finding(s.PluginName, "configured", plugin.LevelOK, s.Title+" is not configured (not in use)", "")}
		}
		return []plugin.Finding{finding(s.PluginName, "configured", plugin.LevelFail, err.Error(), "")}
	}
	var out []plugin.Finding
	if info, err := env.FS.Stat(s.conf()); err == nil && info.Mode&0o077 != 0 {
		out = append(out, finding(s.PluginName, "key-perms", plugin.LevelFail, "access key for "+s.Title+" is readable by more than root", "sudo chmod 600 "+s.conf()))
	} else {
		out = append(out, finding(s.PluginName, "key-perms", plugin.LevelOK, "access key for "+s.Title+" is closed to others", ""))
	}
	switch at, ok := s.lastTime(t); {
	case !ok:
		out = append(out, finding(s.PluginName, "upload", plugin.LevelWarn, "backups have never been uploaded", "sudo lite "+s.PluginName+" upload; sudo lite "+s.PluginName+" schedule on"))
	case t.now().Sub(at) > pgStale:
		out = append(out, finding(s.PluginName, "upload", plugin.LevelFail, "the last backup upload was long ago ("+at.Format(time.RFC3339)+")", "sudo lite "+s.PluginName+" schedule on"))
	default:
		out = append(out, finding(s.PluginName, "upload", plugin.LevelOK, "backups uploaded "+at.Format(time.RFC3339), ""))
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
		return "", plugin.Errorf("usage", "invalid bucket name")
	}
	if len(rest) == 0 {
		return bucket, nil
	}
	if len(rest) > 1 || !prefixRe.MatchString(rest[0]) || strings.Contains(rest[0], "..") {
		return "", plugin.Errorf("usage", "invalid prefix")
	}
	return bucket + "/" + rest[0], nil
}

func keyPair(in string) (string, string, error) {
	lines := readLinesFrom(in, 2)
	if len(lines) != 2 || !keyIDRe.MatchString(lines[0]) || !secretRe.MatchString(lines[1]) {
		return "", "", plugin.Errorf("usage", "stdin needs two lines: the key identifier and the secret key")
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
	Describe:  "backups outside the server: upload /var/backups/lite-deploy to Backblaze B2",
	SetupArgs: "BUCKET [PREFIX] (keys on stdin)", SetupHelp: "save the key and bucket: two lines on stdin, the key ID and the key itself",
	setupLocal: func(t tools, s *RcloneStore, args []string, in string) (string, string, map[string]string, error) {
		if len(args) < 1 {
			return "", "", nil, plugin.Errorf("usage", "usage: lite backblaze setup BUCKET [PREFIX] (key ID and key on two lines of stdin)")
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

// S3: any S3-compatible storage (AWS S3, Wasabi, MinIO, Cloudflare R2 and so on).
var S3 = &RcloneStore{
	PluginName: "s3", Title: "S3", Remote: "s3",
	Describe:  "backups outside the server: upload /var/backups/lite-deploy to S3-compatible storage",
	SetupArgs: "ADDRESS_https BUCKET [PREFIX] (keys on stdin)", SetupHelp: "save the address, bucket and keys: two lines on stdin, the access key and the secret key",
	setupLocal: func(t tools, s *RcloneStore, args []string, in string) (string, string, map[string]string, error) {
		if len(args) < 2 || !endpoint.MatchString(args[0]) {
			return "", "", nil, plugin.Errorf("usage", "usage: lite s3 setup https://address BUCKET [PREFIX] (access key and secret key on two lines of stdin)")
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

// SFTP: a server over SFTP (checking the host key is mandatory).
var SFTP = &RcloneStore{
	PluginName: "sftp", Title: "SFTP", Remote: "sftp",
	Describe:  "backups outside the server: upload /var/backups/lite-deploy to a server over SFTP",
	SetupArgs: "HOST USER DIRECTORY (keys on stdin)", SetupHelp: "stdin: an SSH private key, then a --- line and a known_hosts line with the host key (ssh-keyscan -t ed25519 HOST)",
	setupLocal: func(t tools, s *RcloneStore, args []string, in string) (string, string, map[string]string, error) {
		if len(args) != 3 || !hostRe.MatchString(args[0]) || !userRe.MatchString(args[1]) || !pathRe.MatchString(args[2]) || strings.Contains(args[2], "..") {
			return "", "", nil, plugin.Errorf("usage", "usage: lite sftp setup HOST USER DIRECTORY (private key, a --- line and a known_hosts line on stdin)")
		}
		key, hosts, ok := strings.Cut(in, "\n---\n")
		hosts = strings.TrimSpace(hosts)
		if !ok || !strings.Contains(key, "PRIVATE KEY") || hosts == "" || strings.Count(hosts, "\n") > 3 {
			return "", "", nil, plugin.Errorf("usage", "stdin needs an SSH private key, then a --- line and a known_hosts line: without checking the host key the connection is not opened")
		}
		keyFile, khFile := s.dir()+"/id", s.dir()+"/known_hosts"
		conf := fmt.Sprintf("[sftp]\ntype = sftp\nhost = %s\nuser = %s\nkey_file = %s\nknown_hosts_file = %s\n", args[0], args[1], keyFile, khFile)
		files := map[string]string{keyFile: strings.TrimSpace(key) + "\n", khFile: hosts + "\n"}
		return conf, "/" + strings.TrimPrefix(args[2], "/"), files, nil
	},
}

// Stores are all storage locations for backups outside the server.
var Stores = []*RcloneStore{Backblaze, S3, SFTP}
