package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

const (
	backupConf     = "/etc/lite-deploy/backup.json"
	configDir      = backupRoot + "/config"
	configKeep     = 14
	backupSvc      = "/etc/systemd/system/lite-backup.service"
	backupTimerPth = "/etc/systemd/system/lite-backup.timer"
)

var configFileRe = regexp.MustCompile(`^config-(\d{8}T\d{6}Z)\.tar\.gz\.age$`)

// Backup is the `backup` plugin: backups of project settings, secrets and databases; upload to storage locations (backblaze).
// Secrets enter the backup only encrypted with the owner key: the server keeps only the public part.
type Backup struct{}

func (Backup) Name() string       { return "backup" }
func (Backup) Kind() string       { return "builtin" }
func (Backup) Requires() []string { return nil }
func (Backup) Description() string {
	return "backups: project settings and secrets (encrypted), pg databases, upload to storage locations"
}
func (Backup) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "setup", Confirm: true, Args: "AGE_PUBLIC_KEY [--encrypt-dumps]", Help: "set the backup encryption key (age1…); the private part stays with you; --encrypt-dumps also encrypts database dumps"},
		{Name: "run", Root: true, Help: "take backups: settings and secrets (encrypted), pg databases; upload to storage locations"},
		{Name: "list", Root: true, Help: "settings and secrets backups"},
		{Name: "restore", Confirm: true, Args: "FILE --identity-stdin", Help: "restore a settings and secrets backup into / (on a new server); the age private key on stdin"},
		{Name: "schedule", Confirm: true, Args: "on|off", Help: "daily backup with a systemd timer (around 03:00)"},
	}
}

type backupConfig struct {
	Recipient    string `json:"recipient"`
	EncryptDumps bool   `json:"encrypt_dumps,omitempty"`
}

func (t tools) backupConfig() (backupConfig, error) {
	b, err := t.env.FS.ReadFile(backupConf)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			e := plugin.Errorf("precondition_failed", "the backup encryption key is not set: secrets are not backed up without encryption")
			e.Hint = "age-keygen (on your computer), then sudo lite backup setup age1…"
			return backupConfig{}, e
		}
		return backupConfig{}, plugin.Errorf("step_failed", "%s: %v", backupConf, err)
	}
	var c backupConfig
	if err := json.Unmarshal(b, &c); err != nil || c.Recipient == "" {
		return backupConfig{}, plugin.Errorf("step_failed", "file %s is corrupt", backupConf)
	}
	return c, nil
}

// configArchive builds a tar.gz of the settings and secrets. The executor key and the storage keys are not included: they are issued again on a new server.
func (t tools) configArchive() ([]byte, error) {
	args := []string{"-czf", "-", "-C", "/",
		"--exclude=etc/lite-deploy/exec.age", "--exclude=etc/lite-deploy/backblaze",
		"etc/lite-deploy", "var/lib/lite-deploy/state", "var/lib/lite-deploy/secrets"}
	res, err := t.env.Runner.Run(t.ctx, host.Cmd{Name: "tar", Args: args, Timeout: 10 * time.Minute})
	if err != nil {
		return nil, plugin.Errorf("step_failed", "tar: %v", err)
	}
	// code 1: "the file changed while being read"; acceptable for our small files, anything else is an error
	if res.ExitCode != 0 && res.ExitCode != 1 {
		return nil, plugin.Errorf("step_failed", "tar: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return res.Stdout, nil
}

func encryptTo(recipient string, data []byte) ([]byte, error) {
	r, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return nil, plugin.Errorf("usage", "invalid age key: %v", err)
	}
	var out bytes.Buffer
	w, err := age.Encrypt(&out, r)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type configFile struct {
	Name string
	At   time.Time
}

func (t tools) configFiles() []configFile {
	names, err := t.env.FS.ReadDir(configDir)
	if err != nil {
		return nil
	}
	var out []configFile
	for _, n := range names {
		if m := configFileRe.FindStringSubmatch(n); m != nil {
			if at, err := time.Parse(stampLayout, m[1]); err == nil {
				out = append(out, configFile{n, at})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

func (t tools) backupRun() (plugin.Output, error) {
	cfg, err := t.backupConfig()
	if err != nil {
		return plugin.Output{}, err
	}
	plain, err := t.configArchive()
	if err != nil {
		return plugin.Output{}, err
	}
	enc, err := encryptTo(cfg.Recipient, plain)
	if err != nil {
		return plugin.Output{}, err
	}
	if err := t.env.FS.MkdirAll(configDir, 0o700); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", configDir, err)
	}
	name := "config-" + t.now().UTC().Format(stampLayout) + ".tar.gz.age"
	if err := t.env.FS.WriteFile(configDir+"/"+name, enc, 0o600); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", name, err)
	}
	for i, f := range t.configFiles() {
		if i >= configKeep {
			_ = t.env.FS.Remove(configDir + "/" + f.Name)
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Settings and secrets: %s (encrypted)\n", name)
	data := map[string]any{"config": name}
	if t.ok("psql", "--version") {
		out, err := t.pgBackup(nil)
		if err != nil {
			return plugin.Output{}, err
		}
		sb.WriteString(out.Text)
		data["pg"] = out.Data
	}
	var sent []string
	for _, st := range Stores {
		if !st.Configured(t.env) {
			continue
		}
		if _, err := st.Upload(t); err != nil {
			return plugin.Output{}, err
		}
		sent = append(sent, st.PluginName)
		sb.WriteString("Uploaded: " + st.Title + ".\n")
	}
	data["uploaded"] = append([]string{}, sent...)
	return plugin.Output{Text: sb.String(), Data: data}, nil
}

func (t tools) backupRestore(args []string) (plugin.Output, error) {
	var rest []string
	stdin := false
	for _, a := range args {
		if a == "--identity-stdin" {
			stdin = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) != 1 || !stdin || !configFileRe.MatchString(rest[0]) {
		return plugin.Output{}, plugin.Errorf("usage", "usage: echo AGE-SECRET-KEY-… | lite backup restore config-…tar.gz.age --identity-stdin")
	}
	if t.env.Stdin == nil {
		return plugin.Output{}, plugin.Errorf("usage", "the age private key is read from stdin")
	}
	keyText, err := io.ReadAll(io.LimitReader(t.env.Stdin, 4096))
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "read the key: %v", err)
	}
	id, err := parseIdentity(string(keyText))
	if err != nil {
		return plugin.Output{}, plugin.Errorf("usage", "invalid age private key")
	}
	enc, err := t.env.FS.ReadFile(configDir + "/" + rest[0])
	if err != nil {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "file %s is not in %s (download it from the storage: lite backblaze fetch)", rest[0], configDir)
	}
	r, err := age.Decrypt(bytes.NewReader(enc), id)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "could not decrypt the backup with this key")
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "decryption: %v", err)
	}
	res, err := t.env.Runner.Run(t.ctx, host.Cmd{Name: "tar", Args: []string{"-xzf", "-", "-C", "/"}, Stdin: plain, Timeout: 10 * time.Minute})
	if err != nil || res.ExitCode != 0 {
		msg := ""
		if err != nil {
			msg = err.Error()
		} else {
			msg = strings.TrimSpace(string(res.Stderr))
		}
		return plugin.Output{}, plugin.Errorf("step_failed", "tar: %s", msg)
	}
	return textOut("Settings and secrets restored. Next: lite pg restore for the databases, then deploy the projects.\n"), nil
}

func (t tools) backupSchedule(args []string) (plugin.Output, error) {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return plugin.Output{}, plugin.Errorf("usage", "usage: lite backup schedule on|off")
	}
	if args[0] == "off" {
		_ = t.must("systemctl", "disable", "--now", "lite-backup.timer")
		_ = t.env.FS.Remove(backupTimerPth)
		_ = t.env.FS.Remove(backupSvc)
		_ = t.must("systemctl", "daemon-reload")
		return textOut("The daily backup is off.\n"), nil
	}
	if _, err := t.backupConfig(); err != nil {
		return plugin.Output{}, err
	}
	svc := "[Unit]\nDescription=lite-deploy: backups\n\n[Service]\nType=oneshot\nExecStart=/usr/local/bin/lite-deploy backup run\n"
	tm := "[Unit]\nDescription=lite-deploy: daily backups\n\n[Timer]\nOnCalendar=*-*-* 03:00:00\nRandomizedDelaySec=15min\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
	for p, c := range map[string]string{backupSvc: svc, backupTimerPth: tm} {
		if err := t.env.FS.WriteFile(p, []byte(c), 0o644); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", p, err)
		}
	}
	if err := t.must("systemctl", "daemon-reload"); err != nil {
		return plugin.Output{}, err
	}
	if err := t.must("systemctl", "enable", "--now", "lite-backup.timer"); err != nil {
		return plugin.Output{}, err
	}
	return textOut("Backups (settings, secrets, databases, upload) will run daily around 03:00.\n"), nil
}

func (Backup) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	t := tools{ctx, env}
	switch cmd {
	case "restore":
		return t.backupRestore(args)
	case "schedule":
		return t.backupSchedule(args)
	case "setup":
		encDumps := false
		var rest []string
		for _, a := range args {
			if a == "--encrypt-dumps" {
				encDumps = true
				continue
			}
			rest = append(rest, a)
		}
		if len(rest) != 1 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite backup setup age1… [--encrypt-dumps]")
		}
		if _, err := age.ParseX25519Recipient(rest[0]); err != nil {
			return plugin.Output{}, plugin.Errorf("usage", "that is not an age public key (age1…): %v", err)
		}
		b, _ := json.Marshal(backupConfig{Recipient: rest[0], EncryptDumps: encDumps})
		if err := env.FS.MkdirAll("/etc/lite-deploy", 0o755); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "%v", err)
		}
		if err := env.FS.WriteFile(backupConf, append(b, '\n'), 0o644); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", backupConf, err)
		}
		msg := "Backup encryption key saved. Keep the private part off the server: without it backups cannot be decrypted.\n"
		if encDumps {
			msg += "Database dumps will be encrypted too (restore: lite pg restore DATABASE FILE.age --identity-stdin).\n"
		}
		return textOut(msg), nil
	}
	if err := noArgs(args, cmd, "backup"); err != nil {
		return plugin.Output{}, err
	}
	switch cmd {
	case "run":
		return t.backupRun()
	case "list":
		var sb strings.Builder
		list := []string{}
		for _, f := range t.configFiles() {
			fmt.Fprintln(&sb, f.Name)
			list = append(list, f.Name)
		}
		if sb.Len() == 0 {
			sb.WriteString("No backups.\n")
		}
		return plugin.Output{Text: sb.String(), Data: map[string]any{"config": list}}, nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "plugin backup has no command %q", cmd)
}

func (Backup) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	secretsExist := false
	if names, err := env.FS.ReadDir("/var/lib/lite-deploy/secrets"); err == nil && len(names) > 0 {
		secretsExist = true
	}
	if _, err := t.backupConfig(); err != nil {
		if secretsExist {
			return []plugin.Finding{finding("backup", "key", plugin.LevelFail, "the server has project secrets but backups are not configured: losing the server would lose them too", "sudo lite backup setup age1…")}
		}
		return []plugin.Finding{finding("backup", "key", plugin.LevelOK, "settings backups are not configured (no secrets yet)", "")}
	}
	files := t.configFiles()
	switch {
	case len(files) == 0:
		return []plugin.Finding{finding("backup", "config", plugin.LevelWarn, "there are no settings and secrets backups yet", "sudo lite backup run; sudo lite backup schedule on")}
	case t.now().Sub(files[0].At) > pgStale:
		return []plugin.Finding{finding("backup", "config", plugin.LevelFail, "the latest settings and secrets backup is older than 36 hours ("+files[0].At.Format(time.RFC3339)+")", "sudo lite backup schedule on")}
	}
	return []plugin.Finding{finding("backup", "config", plugin.LevelOK, "the settings and secrets backup is fresh ("+files[0].At.Format(time.RFC3339)+")", "")}
}
