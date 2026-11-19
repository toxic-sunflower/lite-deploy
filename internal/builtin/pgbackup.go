package builtin

import (
	"errors"
	"filippo.io/age"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

const (
	// PGBackupDir is the directory of database backups; it holds files like DATABASE-20261119T031500Z.dump.
	PGBackupDir = "/var/backups/lite-deploy/pg"
	pgKeep      = 7              // how many latest backups of each database to keep
	pgStale     = 36 * time.Hour // a backup older than this counts as stale
	stampLayout = "20060102T150405Z"
)

var backupFileRe = regexp.MustCompile(`^([a-z][a-z0-9_]{0,39})-(\d{8}T\d{6}Z)\.dump(\.age)?$`)

// passwordRe is an acceptable role password: it is put into SQL in quotes, so no quotes and no spaces.
var passwordRe = regexp.MustCompile(`^[A-Za-z0-9_.~-]{16,128}$`)

// readPassword reads a password from standard input (one line).
func readPassword(in io.Reader) (string, error) {
	if in == nil {
		return "", plugin.Errorf("usage", "the password is read from standard input: echo PASSWORD | lite pg ... --password-stdin")
	}
	b, err := io.ReadAll(io.LimitReader(in, 1024))
	if err != nil {
		return "", plugin.Errorf("step_failed", "read the password: %v", err)
	}
	p := strings.TrimRight(string(b), "\r\n")
	if !passwordRe.MatchString(p) {
		return "", plugin.Errorf("usage", "password: 16–128 characters from A-Z a-z 0-9 _ . ~ -")
	}
	return p, nil
}

// extractPasswordFlag removes the --password-stdin flag from the arguments.
func extractPasswordFlag(args []string) ([]string, bool) {
	var rest []string
	found := false
	for _, a := range args {
		if a == "--password-stdin" {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, found
}

type backupFile struct {
	Name, DB string
	At       time.Time
}

// backups lists backups, newest first within a database.
func (t tools) backups() []backupFile {
	names, err := t.env.FS.ReadDir(PGBackupDir)
	if err != nil {
		return nil
	}
	var out []backupFile
	for _, n := range names {
		m := backupFileRe.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		at, err := time.Parse(stampLayout, m[2])
		if err != nil {
			continue
		}
		out = append(out, backupFile{Name: n, DB: m[1], At: at})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DB != out[j].DB {
			return out[i].DB < out[j].DB
		}
		return out[i].At.After(out[j].At)
	})
	return out
}

func (t tools) now() time.Time {
	if t.env.Now != nil {
		return t.env.Now()
	}
	return time.Now()
}

// userDBs are the project databases: all except templates and the system postgres.
func (t tools) userDBs() ([]string, error) {
	out, err := t.psql("select datname from pg_database where not datistemplate and datname <> 'postgres' order by 1")
	if err != nil {
		return nil, err
	}
	var list []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if pgNameRe.MatchString(l) {
			list = append(list, l)
		}
	}
	return list, nil
}

// pgBackup takes a backup of the given database (or all databases) and deletes backups beyond pgKeep.
func (t tools) pgBackup(args []string) (plugin.Output, error) {
	if len(args) > 1 {
		return plugin.Output{}, plugin.Errorf("usage", "usage: lite pg backup [DATABASE]")
	}
	dbs := args
	if len(args) == 0 {
		var err error
		if dbs, err = t.userDBs(); err != nil {
			return plugin.Output{}, err
		}
	} else if !pgNameRe.MatchString(args[0]) {
		return plugin.Output{}, plugin.Errorf("usage", "invalid database name")
	}
	// postgres must be able to reach its directory: the parent needs the enter right (x) for others, reading its content is still not allowed
	if err := t.env.FS.MkdirAll(backupRoot, 0o711); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "directory %s: %v", backupRoot, err)
	}
	if err := t.env.FS.Chmod(backupRoot, 0o711); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "permissions %s: %v", backupRoot, err)
	}
	if err := t.env.FS.MkdirAll(PGBackupDir, 0o750); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "directory %s: %v", PGBackupDir, err)
	}
	if err := t.env.FS.Chown(PGBackupDir, "postgres", "postgres"); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "owner %s: %v", PGBackupDir, err)
	}
	var sb strings.Builder
	var made []string
	for _, db := range dbs {
		name := fmt.Sprintf("%s-%s.dump", db, t.now().UTC().Format(stampLayout))
		path := PGBackupDir + "/" + name
		res, err := t.run("runuser", "-u", "postgres", "--", "pg_dump", "-Fc", "-f", path, db)
		if err != nil || res.ExitCode != 0 {
			_ = t.env.FS.Remove(path) // an incomplete file is worse than no file
			msg := ""
			if err != nil {
				msg = err.Error()
			} else {
				msg = strings.TrimSpace(string(res.Stderr))
			}
			return plugin.Output{}, plugin.Errorf("step_failed", "the backup of database %s was not taken: %s", db, msg)
		}
		if cfg, cerr := t.backupConfig(); cerr == nil && cfg.EncryptDumps {
			if _, err := t.encryptFile(path, path+".age", cfg.Recipient); err != nil {
				_ = t.env.FS.Remove(path + ".age")
				_ = t.env.FS.Remove(path)
				return plugin.Output{}, plugin.Errorf("step_failed", "encrypting the backup of database %s: %v", db, err)
			}
			_ = t.env.FS.Remove(path) // the plain backup does not remain
			name += ".age"
		}
		made = append(made, name)
		fmt.Fprintf(&sb, "Backup %s\n", name)
	}
	removed := t.rotate()
	if len(removed) > 0 {
		fmt.Fprintf(&sb, "Old backups removed: %d\n", len(removed))
	}
	if made == nil {
		made = []string{}
	}
	return plugin.Output{Text: sb.String(), Data: map[string]any{"created": made, "removed": removed}}, nil
}

// rotate keeps the pgKeep latest backups of each database.
func (t tools) rotate() []string {
	seen := map[string]int{}
	var removed []string
	for _, b := range t.backups() { // newest first
		seen[b.DB]++
		if seen[b.DB] > pgKeep {
			if t.env.FS.Remove(PGBackupDir+"/"+b.Name) == nil {
				removed = append(removed, b.Name)
			}
		}
	}
	return removed
}

func (t tools) pgBackupsList() (plugin.Output, error) {
	var sb strings.Builder
	list := []map[string]string{}
	for _, b := range t.backups() {
		fmt.Fprintf(&sb, "%s\t%s\n", b.DB, b.Name)
		list = append(list, map[string]string{"database": b.DB, "file": b.Name, "at": b.At.Format(time.RFC3339)})
	}
	if sb.Len() == 0 {
		sb.WriteString("No backups.\n")
	}
	return plugin.Output{Text: sb.String(), Data: map[string]any{"backups": list}}, nil
}

// pgRestore restores database data from a backup (existing objects are replaced).
func (t tools) pgRestore(args []string) (plugin.Output, error) {
	args, identStdin := extractFlag(args, "--identity-stdin")
	if len(args) != 2 || !pgNameRe.MatchString(args[0]) {
		return plugin.Output{}, plugin.Errorf("usage", "usage: lite pg restore DATABASE FILE [--identity-stdin] (files: lite pg backups; for .age give the age private key on stdin)")
	}
	m := backupFileRe.FindStringSubmatch(args[1])
	if m == nil || m[1] != args[0] {
		return plugin.Output{}, plugin.Errorf("usage", "the file must be a backup of database %s from lite pg backups (a name without a path)", args[0])
	}
	if _, err := t.env.FS.Stat(PGBackupDir + "/" + args[1]); err != nil {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "file %s does not exist", args[1])
	}
	have, err := t.psql(fmt.Sprintf("select 1 from pg_database where datname='%s'", args[0]))
	if err != nil {
		return plugin.Output{}, err
	}
	if strings.TrimSpace(have) == "" {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "database %s does not exist: create it first (lite pg create-db %s)", args[0], args[0])
	}
	src := PGBackupDir + "/" + args[1]
	if m[3] == ".age" {
		if !identStdin {
			return plugin.Output{}, plugin.Errorf("usage", "the backup is encrypted: give the age private key on stdin with the --identity-stdin flag")
		}
		tmp, err := t.decryptToTemp(src, args[0])
		if err != nil {
			return plugin.Output{}, err
		}
		defer func() { _ = t.env.FS.Remove(tmp) }()
		src = tmp
	}
	if err := t.must("runuser", "-u", "postgres", "--", "pg_restore", "--clean", "--if-exists", "-d", args[0], src); err != nil {
		return plugin.Output{}, err
	}
	return textOut("Database " + args[0] + " restored from " + args[1] + ".\n"), nil
}

const (
	backupService = "/etc/systemd/system/lite-pg-backup.service"
	backupTimer   = "/etc/systemd/system/lite-pg-backup.timer"
)

// pgSchedule turns the daily backup on or off (a systemd timer).
func (t tools) pgSchedule(args []string) (plugin.Output, error) {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return plugin.Output{}, plugin.Errorf("usage", "usage: lite pg schedule on|off")
	}
	if args[0] == "off" {
		_ = t.must("systemctl", "disable", "--now", "lite-pg-backup.timer")
		_ = t.env.FS.Remove(backupTimer)
		_ = t.env.FS.Remove(backupService)
		_ = t.must("systemctl", "daemon-reload")
		return textOut("The daily backup is off.\n"), nil
	}
	svc := "[Unit]\nDescription=lite-deploy: PostgreSQL backups\n\n[Service]\nType=oneshot\nExecStart=/usr/local/bin/lite-deploy pg backup\n"
	tm := "[Unit]\nDescription=lite-deploy: daily PostgreSQL backup\n\n[Timer]\nOnCalendar=*-*-* 03:30:00\nRandomizedDelaySec=15min\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
	if err := t.env.FS.WriteFile(backupService, []byte(svc), 0o644); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", backupService, err)
	}
	if err := t.env.FS.WriteFile(backupTimer, []byte(tm), 0o644); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%s: %v", backupTimer, err)
	}
	if err := t.must("systemctl", "daemon-reload"); err != nil {
		return plugin.Output{}, err
	}
	if err := t.must("systemctl", "enable", "--now", "lite-pg-backup.timer"); err != nil {
		return plugin.Output{}, err
	}
	return textOut("Database backups will run daily around 03:30; the latest ones are kept: " + fmt.Sprint(pgKeep) + " latest.\n"), nil
}

// backupFindings are the backup freshness checks.
func (t tools) backupFindings() []plugin.Finding {
	all, err := t.userDBs()
	if err != nil || len(all) == 0 {
		return nil
	}
	// the databases of lite projects and databases created through lite pg create-db are checked; foreign ones (by hand, test) are counted together
	ours := map[string]bool{}
	for _, p := range t.env.Projects {
		ours[strings.ReplaceAll(p, "-", "_")] = true
	}
	if names, err := t.env.FS.ReadDir(pgSecretDir); err == nil {
		for _, n := range names {
			if r, ok := strings.CutPrefix(n, "pg-"); ok {
				ours[r] = true
			}
		}
	}
	var dbs []string
	others := 0
	for _, db := range all {
		if ours[db] {
			dbs = append(dbs, db)
		} else {
			others++
		}
	}
	newest := map[string]time.Time{}
	for _, b := range t.backups() {
		if b.At.After(newest[b.DB]) {
			newest[b.DB] = b.At
		}
	}
	var out []plugin.Finding
	for _, db := range dbs {
		at, ok := newest[db]
		switch {
		case !ok:
			out = append(out, finding("pg", "backup-"+db, plugin.LevelWarn, "database "+db+" has no backups", "sudo lite pg backup "+db+"; sudo lite pg schedule on"))
		case t.now().Sub(at) > pgStale:
			out = append(out, finding("pg", "backup-"+db, plugin.LevelFail, "the latest backup of database "+db+" is older than 36 hours ("+at.Format(time.RFC3339)+")", "sudo lite pg schedule on"))
		default:
			out = append(out, finding("pg", "backup-"+db, plugin.LevelOK, "backup of database "+db+" is fresh ("+at.Format(time.RFC3339)+")", ""))
		}
	}
	if others > 0 {
		out = append(out, finding("pg", "backup-others", plugin.LevelOK, fmt.Sprintf("more databases outside lite projects: %d (backups are not checked; take one: lite pg backup DATABASE)", others), ""))
	}
	return out
}

func extractFlag(args []string, flag string) ([]string, bool) {
	var rest []string
	found := false
	for _, a := range args {
		if a == flag {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, found
}

// encryptFile encrypts a file with an age key as a stream (large dumps are not read into memory whole) and returns the path of the result.
func (t tools) encryptFile(src, dst, recipient string) (string, error) {
	r, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return "", err
	}
	in, err := t.env.FS.Open(src)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	out, err := t.env.FS.Create(dst, 0o600)
	if err != nil {
		return "", err
	}
	w, err := age.Encrypt(out, r)
	if err != nil {
		_ = out.Close()
		return "", err
	}
	if _, err := io.Copy(w, in); err != nil {
		_ = out.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		_ = out.Close()
		return "", err
	}
	return dst, out.Close()
}

// decryptToTemp decrypts a backup into a temporary file readable by postgres; the private key is read from the command stdin.
func (t tools) decryptToTemp(src, db string) (string, error) {
	if t.env.Stdin == nil {
		return "", plugin.Errorf("usage", "the age private key is read from stdin")
	}
	keyText, err := io.ReadAll(io.LimitReader(t.env.Stdin, 4096))
	if err != nil {
		return "", plugin.Errorf("step_failed", "read the key: %v", err)
	}
	id, err := parseIdentity(string(keyText))
	if err != nil {
		return "", plugin.Errorf("usage", "invalid age private key")
	}
	in, err := t.env.FS.Open(src)
	if err != nil {
		return "", plugin.Errorf("step_failed", "%v", err)
	}
	defer func() { _ = in.Close() }()
	dr, err := age.Decrypt(in, id)
	if err != nil {
		return "", plugin.Errorf("precondition_failed", "could not decrypt the backup with this key")
	}
	tmp := PGBackupDir + "/.restore-" + db + ".dump"
	out, err := t.env.FS.Create(tmp, 0o600)
	if err != nil {
		return "", plugin.Errorf("step_failed", "%v", err)
	}
	if _, err := io.Copy(out, dr); err != nil {
		_ = out.Close()
		_ = t.env.FS.Remove(tmp)
		return "", plugin.Errorf("step_failed", "decryption: %v", err)
	}
	if err := out.Close(); err != nil {
		return "", plugin.Errorf("step_failed", "%v", err)
	}
	if err := t.env.FS.Chown(tmp, "postgres", "postgres"); err != nil {
		_ = t.env.FS.Remove(tmp)
		return "", plugin.Errorf("step_failed", "%v", err)
	}
	return tmp, nil
}

// parseIdentity takes the private key from text: an age-keygen file contains comments (# …), which are skipped.
func parseIdentity(text string) (*age.X25519Identity, error) {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		return age.ParseX25519Identity(l)
	}
	return nil, errors.New("key not found")
}
