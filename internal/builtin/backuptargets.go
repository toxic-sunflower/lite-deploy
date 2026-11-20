package builtin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// backupTarget says what is backed up for one target (the server itself, or one project), where it is copied, and when.
type backupTarget struct {
	Enabled *bool    `json:"enabled,omitempty"` // default: on
	Items   []string `json:"items,omitempty"`   // default: see defaultItems
	Stores  []string `json:"stores,omitempty"`  // default: every storage that is set up
	At      string   `json:"at,omitempty"`      // HH:MM every day; default: the common time (03:00)
}

func (b backupTarget) on() bool { return b.Enabled == nil || *b.Enabled }

const (
	serverTarget = "server"
	commonAt     = "03:00"
)

var (
	serverItems  = []string{"caddy", "lite"}
	projectItems = []string{"settings", "secrets", "database", "data"}
	atRe         = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	targetNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
)

func defaultItems(target string) []string {
	if target == serverTarget {
		return serverItems
	}
	return []string{"settings", "secrets", "database"}
}

func (c backupConfig) targeted() bool { return c.Server != nil || len(c.Projects) > 0 }

// targetOf returns the setting of a target with the defaults filled in.
func (c backupConfig) targetOf(name string) backupTarget {
	var b backupTarget
	if name == serverTarget {
		if c.Server != nil {
			b = *c.Server
		}
	} else {
		b = c.Projects[name]
	}
	if len(b.Items) == 0 {
		b.Items = defaultItems(name)
	}
	return b
}

// projectNames are the projects managed by lite on this server.
func (t tools) projectNames() []string {
	names, err := t.env.FS.ReadDir("/etc/lite-deploy/projects")
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range names {
		if p, ok := strings.CutSuffix(n, ".json"); ok && targetNameRe.MatchString(p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// archive packs the existing paths (relative to /) into an encrypted file in the config directory and returns its name.
func (t tools) archive(prefix, recipient string, paths []string, excludes ...string) (string, error) {
	var have []string
	for _, p := range paths {
		if _, err := t.env.FS.Stat("/" + p); err == nil {
			have = append(have, p)
		}
	}
	if len(have) == 0 {
		return "", nil
	}
	args := []string{"-czf", "-", "-C", "/"}
	for _, e := range excludes {
		args = append(args, "--exclude="+e)
	}
	args = append(args, have...)
	res, err := t.env.Runner.Run(t.ctx, host.Cmd{Name: "tar", Args: args, Timeout: 30 * time.Minute})
	if err != nil {
		return "", plugin.Errorf("step_failed", "tar: %v", err)
	}
	if res.ExitCode != 0 && res.ExitCode != 1 { // 1: a file changed while it was read
		return "", plugin.Errorf("step_failed", "tar: %s", strings.TrimSpace(string(res.Stderr)))
	}
	enc, err := encryptTo(recipient, res.Stdout)
	if err != nil {
		return "", err
	}
	if err := t.env.FS.MkdirAll(configDir, fs.FileMode(0o700)); err != nil {
		return "", plugin.Errorf("step_failed", "%s: %v", configDir, err)
	}
	name := prefix + "-" + t.now().UTC().Format(stampLayout) + ".tar.gz.age"
	if err := t.env.FS.WriteFile(configDir+"/"+name, enc, fs.FileMode(0o600)); err != nil {
		return "", plugin.Errorf("step_failed", "%s: %v", name, err)
	}
	t.prune(prefix)
	return name, nil
}

// prune keeps the newest backups of one kind and removes the older ones.
func (t tools) prune(prefix string) {
	n := 0
	for _, f := range t.configFiles() {
		if !strings.HasPrefix(f.Name, prefix+"-") || !configFileRe.MatchString(f.Name) || configFileRe.FindStringSubmatch(f.Name)[1] != prefix {
			continue
		}
		if n++; n > configKeep {
			_ = t.env.FS.Remove(configDir + "/" + f.Name)
		}
	}
}

// runTargets takes the backups of the targets that are on. args: nothing (all), --scheduled (those without a time of their own),
// --server, or --project NAME.
func (t tools) runTargets(cfg backupConfig, args []string) (plugin.Output, error) {
	only, scheduled := "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server":
			only = serverTarget
		case "--project":
			if i+1 >= len(args) || !targetNameRe.MatchString(args[i+1]) {
				return plugin.Output{}, plugin.Errorf("usage", "usage: lite backup run [--server | --project NAME | --scheduled]")
			}
			only = args[i+1]
			i++
		case "--scheduled":
			scheduled = true
		default:
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite backup run [--server | --project NAME | --scheduled]")
		}
	}
	names := append([]string{serverTarget}, t.projectNames()...)
	var sb strings.Builder
	var made []string
	for _, name := range names {
		if only != "" && only != name {
			continue
		}
		tg := cfg.targetOf(name)
		if !tg.on() || (scheduled && tg.At != "") {
			continue
		}
		files, err := t.backupTarget(cfg, name, tg)
		if err != nil {
			return plugin.Output{}, err
		}
		for _, f := range files {
			fmt.Fprintf(&sb, "%s: %s\n", name, f)
		}
		made = append(made, files...)
		sent, err := t.uploadTarget(name, tg)
		if err != nil {
			return plugin.Output{}, err
		}
		for _, s := range sent {
			fmt.Fprintf(&sb, "%s: uploaded to %s\n", name, s)
		}
	}
	if only != "" && sb.Len() == 0 {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "nothing to back up for %s: it is turned off or does not exist", only)
	}
	if sb.Len() == 0 {
		sb.WriteString("Nothing to back up: every target is turned off.\n")
	}
	return plugin.Output{Text: sb.String(), Data: map[string]any{"files": made}}, nil
}

// backupTarget takes the backup of one target and returns the names of the files made.
func (t tools) backupTarget(cfg backupConfig, name string, tg backupTarget) ([]string, error) {
	var files []string
	has := func(item string) bool {
		for _, i := range tg.Items {
			if i == item {
				return true
			}
		}
		return false
	}
	if name == serverTarget {
		var paths []string
		if has("caddy") {
			paths = append(paths, "etc/caddy")
		}
		if has("lite") {
			paths = append(paths, "etc/lite-deploy", "var/lib/lite-deploy/state")
		}
		f, err := t.archive("server", cfg.Recipient, paths, "etc/lite-deploy/exec.age", "etc/lite-deploy/backblaze", "var/lib/lite-deploy/secrets")
		if err != nil {
			return nil, err
		}
		if f != "" {
			files = append(files, f)
		}
		return files, nil
	}
	var paths []string
	if has("settings") {
		paths = append(paths, "etc/lite-deploy/projects/"+name+".json", "var/lib/lite-deploy/state/projects/"+name+".json")
	}
	if has("secrets") {
		paths = append(paths, "var/lib/lite-deploy/secrets/"+name)
	}
	if has("data") {
		paths = append(paths, "home/"+name+"/.local/share/lite-deploy/data")
	}
	f, err := t.archive("project-"+name, cfg.Recipient, paths)
	if err != nil {
		return nil, err
	}
	if f != "" {
		files = append(files, f)
	}
	if has("database") && t.ok("psql", "--version") {
		db := strings.ReplaceAll(name, "-", "_")
		if dbs, err := t.userDBs(); err == nil {
			for _, d := range dbs {
				if d == db {
					out, err := t.pgBackup([]string{db})
					if err != nil {
						return nil, err
					}
					files = append(files, "database "+db+" ("+strings.TrimSpace(out.Text)+")")
				}
			}
		}
	}
	return files, nil
}

// uploadTarget copies the files of one target to the storages it uses.
func (t tools) uploadTarget(name string, tg backupTarget) ([]string, error) {
	var includes []string
	if name == serverTarget {
		includes = []string{"config/server-*"}
	} else {
		includes = []string{"config/project-" + name + "-*", "pg/" + strings.ReplaceAll(name, "-", "_") + "-*"}
	}
	var sent []string
	for _, st := range Stores {
		if !st.Configured(t.env) {
			continue
		}
		if len(tg.Stores) > 0 && !contains(tg.Stores, st.PluginName) {
			continue
		}
		if _, err := st.uploadWith(t, includes); err != nil {
			return sent, err
		}
		sent = append(sent, st.Title)
	}
	return sent, nil
}

// setTarget changes the setting of one target. Usage: set-target TARGET [--on|--off] [--items a,b] [--stores x,y|--all-stores]
// [--at HH:MM|--no-time]. TARGET is "server" or a project name.
func (t tools) setTarget(args []string) (plugin.Output, error) {
	const usage = "usage: lite backup set-target TARGET [--on|--off] [--items a,b] [--stores x,y|--all-stores] [--at HH:MM|--no-time]"
	if len(args) < 2 || !targetNameRe.MatchString(args[0]) {
		return plugin.Output{}, plugin.Errorf("usage", usage)
	}
	name := args[0]
	if name != serverTarget && !contains(t.projectNames(), name) {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "there is no project %s", name)
	}
	cfg, err := t.backupConfig()
	if err != nil {
		return plugin.Output{}, err
	}
	var tg backupTarget
	if name == serverTarget {
		if cfg.Server != nil {
			tg = *cfg.Server
		}
	} else {
		tg = cfg.Projects[name]
	}
	valid := projectItems
	if name == serverTarget {
		valid = serverItems
	}
	for i := 1; i < len(args); i++ {
		next := func() (string, bool) {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch args[i] {
		case "--on", "--off":
			on := args[i] == "--on"
			tg.Enabled = &on
		case "--items":
			v, ok := next()
			if !ok {
				return plugin.Output{}, plugin.Errorf("usage", usage)
			}
			items := strings.Split(v, ",")
			for _, it := range items {
				if !contains(valid, it) {
					return plugin.Output{}, plugin.Errorf("usage", "%q is not something to back up for %s (choose from %s)", it, name, strings.Join(valid, ", "))
				}
			}
			tg.Items = items
		case "--stores":
			v, ok := next()
			if !ok {
				return plugin.Output{}, plugin.Errorf("usage", usage)
			}
			stores := strings.Split(v, ",")
			for _, s := range stores {
				if !contains([]string{"backblaze", "s3", "sftp"}, s) {
					return plugin.Output{}, plugin.Errorf("usage", "%q is not a storage (backblaze, s3, sftp)", s)
				}
			}
			tg.Stores = stores
		case "--all-stores":
			tg.Stores = nil
		case "--at":
			v, ok := next()
			if !ok || !atRe.MatchString(v) {
				return plugin.Output{}, plugin.Errorf("usage", "the time is HH:MM, for example 02:30")
			}
			tg.At = v
		case "--no-time":
			tg.At = ""
		default:
			return plugin.Output{}, plugin.Errorf("usage", usage)
		}
	}
	if name == serverTarget {
		cfg.Server = &tg
	} else {
		if cfg.Projects == nil {
			cfg.Projects = map[string]backupTarget{}
		}
		cfg.Projects[name] = tg
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := t.env.FS.WriteFile(backupConf, append(b, '\n'), fs.FileMode(0o644)); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", backupConf, err)
	}
	note := ""
	if t.ok("systemctl", "is-enabled", "--quiet", "lite-backup.timer") {
		if _, err := t.backupSchedule([]string{"on"}); err != nil {
			return plugin.Output{}, err
		}
		note = " The schedule was updated."
	}
	return textOut("The backup setting of " + name + " was saved." + note + "\n"), nil
}

// targetStatus describes every target for the interface: its setting with the defaults filled in and the time of its last backup.
func (t tools) targetStatus(cfg backupConfig) []map[string]any {
	var out []map[string]any
	last := func(prefix string) string {
		for _, f := range t.configFiles() {
			if m := configFileRe.FindStringSubmatch(f.Name); m != nil && m[1] == prefix {
				return f.At.Format(time.RFC3339)
			}
		}
		return ""
	}
	for _, name := range append([]string{serverTarget}, t.projectNames()...) {
		tg := cfg.targetOf(name)
		prefix := "server"
		if name != serverTarget {
			prefix = "project-" + name
		}
		at := tg.At
		stores := tg.Stores
		if stores == nil {
			stores = []string{}
		}
		out = append(out, map[string]any{"name": name, "kind": map[bool]string{true: "server", false: "project"}[name == serverTarget],
			"enabled": tg.on(), "items": tg.Items, "stores": stores, "at": at, "last": last(prefix)})
	}
	return out
}
