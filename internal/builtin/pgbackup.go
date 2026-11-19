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
	// PGBackupDir каталог копий баз; в нём файлы вида БАЗА-20261119T031500Z.dump.
	PGBackupDir = "/var/backups/lite-deploy/pg"
	pgKeep      = 7              // сколько последних копий каждой базы хранить
	pgStale     = 36 * time.Hour // старше этого копия считается устаревшей
	stampLayout = "20060102T150405Z"
)

var backupFileRe = regexp.MustCompile(`^([a-z][a-z0-9_]{0,39})-(\d{8}T\d{6}Z)\.dump(\.age)?$`)

// passwordRe допустимый пароль роли: подставляется в SQL в кавычках, поэтому без кавычек и пробелов.
var passwordRe = regexp.MustCompile(`^[A-Za-z0-9_.~-]{16,128}$`)

// readPassword читает пароль из стандартного ввода (одна строка).
func readPassword(in io.Reader) (string, error) {
	if in == nil {
		return "", plugin.Errorf("usage", "пароль читается из стандартного ввода: echo ПАРОЛЬ | lite pg ... --password-stdin")
	}
	b, err := io.ReadAll(io.LimitReader(in, 1024))
	if err != nil {
		return "", plugin.Errorf("step_failed", "чтение пароля: %v", err)
	}
	p := strings.TrimRight(string(b), "\r\n")
	if !passwordRe.MatchString(p) {
		return "", plugin.Errorf("usage", "пароль: 16–128 знаков из A-Z a-z 0-9 _ . ~ -")
	}
	return p, nil
}

// extractPasswordFlag убирает флаг --password-stdin из аргументов.
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

// backups перечисляет копии, новые первыми внутри базы.
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

// userDBs базы проектов: все, кроме шаблонов и служебной postgres.
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

// pgBackup снимает копию указанной базы (или всех баз) и удаляет копии сверх pgKeep.
func (t tools) pgBackup(args []string) (plugin.Output, error) {
	if len(args) > 1 {
		return plugin.Output{}, plugin.Errorf("usage", "использование: lite pg backup [БАЗА]")
	}
	dbs := args
	if len(args) == 0 {
		var err error
		if dbs, err = t.userDBs(); err != nil {
			return plugin.Output{}, err
		}
	} else if !pgNameRe.MatchString(args[0]) {
		return plugin.Output{}, plugin.Errorf("usage", "неверное имя базы")
	}
	// postgres должен дойти до своего каталога: родителю нужно право входа (x) для остальных, читать его содержимое по-прежнему нельзя
	if err := t.env.FS.MkdirAll(backupRoot, 0o711); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "каталог %s: %v", backupRoot, err)
	}
	if err := t.env.FS.Chmod(backupRoot, 0o711); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "права %s: %v", backupRoot, err)
	}
	if err := t.env.FS.MkdirAll(PGBackupDir, 0o750); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "каталог %s: %v", PGBackupDir, err)
	}
	if err := t.env.FS.Chown(PGBackupDir, "postgres", "postgres"); err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "владелец %s: %v", PGBackupDir, err)
	}
	var sb strings.Builder
	var made []string
	for _, db := range dbs {
		name := fmt.Sprintf("%s-%s.dump", db, t.now().UTC().Format(stampLayout))
		path := PGBackupDir + "/" + name
		res, err := t.run("runuser", "-u", "postgres", "--", "pg_dump", "-Fc", "-f", path, db)
		if err != nil || res.ExitCode != 0 {
			_ = t.env.FS.Remove(path) // неполный файл хуже, чем его отсутствие
			msg := ""
			if err != nil {
				msg = err.Error()
			} else {
				msg = strings.TrimSpace(string(res.Stderr))
			}
			return plugin.Output{}, plugin.Errorf("step_failed", "копия базы %s не снята: %s", db, msg)
		}
		if cfg, cerr := t.backupConfig(); cerr == nil && cfg.EncryptDumps {
			if _, err := t.encryptFile(path, path+".age", cfg.Recipient); err != nil {
				_ = t.env.FS.Remove(path + ".age")
				_ = t.env.FS.Remove(path)
				return plugin.Output{}, plugin.Errorf("step_failed", "шифрование копии базы %s: %v", db, err)
			}
			_ = t.env.FS.Remove(path) // открытая копия не остаётся
			name += ".age"
		}
		made = append(made, name)
		fmt.Fprintf(&sb, "Копия %s\n", name)
	}
	removed := t.rotate()
	if len(removed) > 0 {
		fmt.Fprintf(&sb, "Удалено старых копий: %d\n", len(removed))
	}
	if made == nil {
		made = []string{}
	}
	return plugin.Output{Text: sb.String(), Data: map[string]any{"created": made, "removed": removed}}, nil
}

// rotate оставляет pgKeep последних копий каждой базы.
func (t tools) rotate() []string {
	seen := map[string]int{}
	var removed []string
	for _, b := range t.backups() { // новые первыми
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
		sb.WriteString("Копий нет.\n")
	}
	return plugin.Output{Text: sb.String(), Data: map[string]any{"backups": list}}, nil
}

// pgRestore возвращает данные базы из копии (существующие объекты заменяются).
func (t tools) pgRestore(args []string) (plugin.Output, error) {
	args, identStdin := extractFlag(args, "--identity-stdin")
	if len(args) != 2 || !pgNameRe.MatchString(args[0]) {
		return plugin.Output{}, plugin.Errorf("usage", "использование: lite pg restore БАЗА ФАЙЛ [--identity-stdin] (файлы: lite pg backups; для .age закрытый ключ age со stdin)")
	}
	m := backupFileRe.FindStringSubmatch(args[1])
	if m == nil || m[1] != args[0] {
		return plugin.Output{}, plugin.Errorf("usage", "файл должен быть копией базы %s из lite pg backups (имя без пути)", args[0])
	}
	if _, err := t.env.FS.Stat(PGBackupDir + "/" + args[1]); err != nil {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "файла %s нет", args[1])
	}
	have, err := t.psql(fmt.Sprintf("select 1 from pg_database where datname='%s'", args[0]))
	if err != nil {
		return plugin.Output{}, err
	}
	if strings.TrimSpace(have) == "" {
		return plugin.Output{}, plugin.Errorf("precondition_failed", "базы %s нет: создайте её (lite pg create-db %s)", args[0], args[0])
	}
	src := PGBackupDir + "/" + args[1]
	if m[3] == ".age" {
		if !identStdin {
			return plugin.Output{}, plugin.Errorf("usage", "копия зашифрована: передайте закрытый ключ age со stdin и флагом --identity-stdin")
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
	return textOut("База " + args[0] + " восстановлена из " + args[1] + ".\n"), nil
}

const (
	backupService = "/etc/systemd/system/lite-pg-backup.service"
	backupTimer   = "/etc/systemd/system/lite-pg-backup.timer"
)

// pgSchedule включает или выключает ежедневную копию (таймер systemd).
func (t tools) pgSchedule(args []string) (plugin.Output, error) {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return plugin.Output{}, plugin.Errorf("usage", "использование: lite pg schedule on|off")
	}
	if args[0] == "off" {
		_ = t.must("systemctl", "disable", "--now", "lite-pg-backup.timer")
		_ = t.env.FS.Remove(backupTimer)
		_ = t.env.FS.Remove(backupService)
		_ = t.must("systemctl", "daemon-reload")
		return textOut("Ежедневная копия выключена.\n"), nil
	}
	svc := "[Unit]\nDescription=lite-deploy: копии баз PostgreSQL\n\n[Service]\nType=oneshot\nExecStart=/usr/local/bin/lite-deploy pg backup\n"
	tm := "[Unit]\nDescription=lite-deploy: ежедневная копия баз PostgreSQL\n\n[Timer]\nOnCalendar=*-*-* 03:30:00\nRandomizedDelaySec=15min\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
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
	return textOut("Копии баз будут сниматься ежедневно около 03:30; хранится по " + fmt.Sprint(pgKeep) + " последних.\n"), nil
}

// backupFindings проверки свежести копий.
func (t tools) backupFindings() []plugin.Finding {
	all, err := t.userDBs()
	if err != nil || len(all) == 0 {
		return nil
	}
	// проверяются базы проектов lite и базы, созданные через lite pg create-db; чужие (вручную, тестовые) считаются вместе
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
			out = append(out, finding("pg", "backup-"+db, plugin.LevelWarn, "у базы "+db+" нет копий", "sudo lite pg backup "+db+"; sudo lite pg schedule on"))
		case t.now().Sub(at) > pgStale:
			out = append(out, finding("pg", "backup-"+db, plugin.LevelFail, "последняя копия базы "+db+" старше 36 часов ("+at.Format(time.RFC3339)+")", "sudo lite pg schedule on"))
		default:
			out = append(out, finding("pg", "backup-"+db, plugin.LevelOK, "копия базы "+db+" свежая ("+at.Format(time.RFC3339)+")", ""))
		}
	}
	if others > 0 {
		out = append(out, finding("pg", "backup-others", plugin.LevelOK, fmt.Sprintf("ещё баз вне проектов lite: %d (копии не проверяются; снять: lite pg backup БАЗА)", others), ""))
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

// encryptFile шифрует файл ключом age потоком (большие дампы не читаются в память целиком) и возвращает путь результата.
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

// decryptToTemp расшифровывает копию во временный файл, доступный postgres, закрытый ключ читается из stdin команды.
func (t tools) decryptToTemp(src, db string) (string, error) {
	if t.env.Stdin == nil {
		return "", plugin.Errorf("usage", "закрытый ключ age читается из stdin")
	}
	keyText, err := io.ReadAll(io.LimitReader(t.env.Stdin, 4096))
	if err != nil {
		return "", plugin.Errorf("step_failed", "чтение ключа: %v", err)
	}
	id, err := parseIdentity(string(keyText))
	if err != nil {
		return "", plugin.Errorf("usage", "неверный закрытый ключ age")
	}
	in, err := t.env.FS.Open(src)
	if err != nil {
		return "", plugin.Errorf("step_failed", "%v", err)
	}
	defer func() { _ = in.Close() }()
	dr, err := age.Decrypt(in, id)
	if err != nil {
		return "", plugin.Errorf("precondition_failed", "не удалось расшифровать копию этим ключом")
	}
	tmp := PGBackupDir + "/.restore-" + db + ".dump"
	out, err := t.env.FS.Create(tmp, 0o600)
	if err != nil {
		return "", plugin.Errorf("step_failed", "%v", err)
	}
	if _, err := io.Copy(out, dr); err != nil {
		_ = out.Close()
		_ = t.env.FS.Remove(tmp)
		return "", plugin.Errorf("step_failed", "расшифровка: %v", err)
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

// parseIdentity берёт закрытый ключ из текста: файл age-keygen содержит комментарии (# …), их пропускаем.
func parseIdentity(text string) (*age.X25519Identity, error) {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		return age.ParseX25519Identity(l)
	}
	return nil, errors.New("ключ не найден")
}
