package builtin

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// fn runs a function as a Runner.
type fn func(host.Cmd) (host.Result, error)

func (f fn) Run(_ context.Context, c host.Cmd) (host.Result, error) { return f(c) }

func TestBindLocalOnly(t *testing.T) {
	for in, want := range map[string]bool{
		"127.0.0.1 ::1": true, "-::1 127.0.0.1": true, "127.0.0.1": true,
		"": false, "0.0.0.0": false, "* -::*": false, "127.0.0.1 10.0.0.5": false,
	} {
		if got := bindLocalOnly(in); got != want {
			t.Errorf("bindLocalOnly(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPGCreateDB(t *testing.T) {
	mem := host.NewMemFS()
	var sql []string
	r := fn(func(c host.Cmd) (host.Result, error) {
		if c.Name == "runuser" {
			sql = append(sql, c.Args[len(c.Args)-1]+string(c.Stdin))
		}
		return host.Result{}, nil // the role and the database do not exist yet: select returns nothing
	})
	env := plugin.Env{Runner: r, FS: mem}
	if _, err := (PG{}).Run(context.Background(), env, "create-db", []string{"shop", "shopapp"}); err != nil {
		t.Fatal(err)
	}
	pass, err := mem.ReadFile(pgSecretDir + "/pg-shopapp")
	if err != nil || len(strings.TrimSpace(string(pass))) != 48 {
		t.Fatalf("the password was not written: %q %v", pass, err)
	}
	all := strings.Join(sql, "\n")
	if !strings.Contains(all, "create role shopapp login password '"+strings.TrimSpace(string(pass))+"'") ||
		!strings.Contains(all, "create database shop owner shopapp") {
		t.Fatalf("unexpected SQL:\n%s", all)
	}
	// the second call uses the same password
	if _, err := (PG{}).Run(context.Background(), env, "create-db", []string{"shop2", "shopapp"}); err != nil {
		t.Fatal(err)
	}
	again, _ := mem.ReadFile(pgSecretDir + "/pg-shopapp")
	if string(again) != string(pass) {
		t.Fatal("the role password changed on a repeated call")
	}
}

func TestPGCreateDBRejectsBadNames(t *testing.T) {
	env := plugin.Env{Runner: fn(func(host.Cmd) (host.Result, error) {
		t.Fatal("the command must not run")
		return host.Result{}, nil
	}), FS: host.NewMemFS()}
	for _, bad := range []string{"Shop", "a;drop", "1x", "a-b", ""} {
		if _, err := (PG{}).Run(context.Background(), env, "create-db", []string{bad}); err == nil {
			t.Errorf("the name %q must be rejected", bad)
		}
	}
	if _, err := (PG{}).Run(context.Background(), env, "drop-db", []string{"a;b"}); err == nil {
		t.Error("drop-db with a bad name must refuse")
	}
}

func TestPodmanPSUnknownProject(t *testing.T) {
	env := plugin.Env{Runner: fn(func(host.Cmd) (host.Result, error) { return host.Result{}, nil }), FS: host.NewMemFS(), Projects: []string{"shop"}}
	if _, err := (Podman{}).Run(context.Background(), env, "ps", []string{"nope"}); err == nil {
		t.Fatal("an unknown project must be rejected")
	}
}

func TestRedisCheckPublicBindFails(t *testing.T) {
	r := fn(func(c host.Cmd) (host.Result, error) {
		if c.Name == "redis-cli" {
			return host.Result{Stdout: []byte("bind\n0.0.0.0\n")}, nil
		}
		if c.Name == "apt-get" {
			return host.Result{}, nil
		}
		return host.Result{}, nil
	})
	fs := (Redis{}).Check(context.Background(), plugin.Env{Runner: r, FS: host.NewMemFS()})
	found := false
	for _, f := range fs {
		if f.Check == "bind" && f.Level == plugin.LevelFail {
			found = true
		}
	}
	if !found {
		t.Fatalf("a bind error was expected: %+v", fs)
	}
}

func TestNormalizeFrom(t *testing.T) {
	for in, want := range map[string]string{"10.0.0.5": "10.0.0.5/32", "10.0.0.7/24": "10.0.0.0/24", "fd00::1": "fd00::1/128"} {
		got, err := normalizeFrom(in)
		if err != nil || got != want {
			t.Errorf("normalizeFrom(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"0.0.0.0", "0.0.0.0/0", "::/0", "any", "", "10.0.0.5; drop", "::"} {
		if _, err := normalizeFrom(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestHBARoundTrip(t *testing.T) {
	base := "local all postgres peer\nhost all all 127.0.0.1/32 scram-sha-256\n"
	rules := []hbaRule{{"shop", "shopapp", "10.0.0.5/32"}, {"crm", "crm", "10.0.0.6/32"}}
	text := hbaJoin(strings.TrimRight(base, "\n"), rules)
	out, got := hbaSplit(text)
	if out != strings.TrimRight(base, "\n") || len(got) != 2 || got[0] != rules[0] || got[1] != rules[1] {
		t.Fatalf("the round trip did not match:\n%s\n%v", text, got)
	}
	if hbaJoin(out, nil) != base || strings.Contains(hbaJoin(out, nil), "lite-deploy") {
		t.Fatal("an empty set of rules must remove the whole block")
	}
}

func TestPGAllowWritesRuleAndOpensFirewall(t *testing.T) {
	mem := host.NewMemFS()
	mem.Files["/etc/postgresql/16/main/pg_hba.conf"] = []byte("local all postgres peer\n")
	var calls []string
	r := fn(func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c.Name+" "+strings.Join(c.Args, " ")+" "+string(c.Stdin))
		if c.Name == "runuser" {
			switch q := c.Args[len(c.Args)-1]; {
			case q == "show ssl":
				return host.Result{Stdout: []byte("on\n")}, nil
			case q == "show hba_file":
				return host.Result{Stdout: []byte("/etc/postgresql/16/main/pg_hba.conf\n")}, nil
			case strings.HasPrefix(q, "select pg_reload_conf"):
				return host.Result{Stdout: []byte("t\n0\n")}, nil
			}
		}
		if c.Name == "ufw" && len(c.Args) > 0 && c.Args[0] == "status" {
			return host.Result{Stdout: []byte("Status: active\n")}, nil
		}
		return host.Result{}, nil
	})
	env := plugin.Env{Runner: r, FS: mem}
	if _, err := (PG{}).Run(context.Background(), env, "allow", []string{"shop", "shopapp", "10.0.0.5"}); err != nil {
		t.Fatal(err)
	}
	got := string(mem.Files["/etc/postgresql/16/main/pg_hba.conf"])
	if !strings.Contains(got, "hostssl shop shopapp 10.0.0.5/32 scram-sha-256") || !strings.HasPrefix(got, "local all postgres peer") {
		t.Fatalf("pg_hba.conf:\n%s", got)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "ufw allow proto tcp from 10.0.0.5/32 to any port 5432") {
		t.Fatalf("the port is not open:\n%s", strings.Join(calls, "\n"))
	}
}

func TestPGAllowRejectsOpenWorld(t *testing.T) {
	env := plugin.Env{Runner: fn(func(host.Cmd) (host.Result, error) {
		t.Fatal("nothing may run")
		return host.Result{}, nil
	}), FS: host.NewMemFS()}
	if _, err := (PG{}).Run(context.Background(), env, "allow", []string{"shop", "shopapp", "0.0.0.0/0"}); err == nil {
		t.Fatal("0.0.0.0/0 must be rejected")
	}
	if _, err := (PG{}).Run(context.Background(), env, "listen", []string{"0.0.0.0"}); err == nil {
		t.Fatal("listen 0.0.0.0 must be rejected")
	}
}

func pgRunner(calls *[]string, dbs string) fn {
	return fn(func(c host.Cmd) (host.Result, error) {
		*calls = append(*calls, c.Name+" "+strings.Join(c.Args, " ")+" "+string(c.Stdin))
		if c.Name == "runuser" && strings.HasPrefix(c.Args[len(c.Args)-1], "select datname") {
			return host.Result{Stdout: []byte(dbs)}, nil
		}
		return host.Result{}, nil
	})
}

func TestPGBackupRotates(t *testing.T) {
	mem := host.NewMemFS()
	for i := 1; i <= 9; i++ {
		mem.Files[fmt.Sprintf("%s/shop-2026110%dT030000Z.dump", PGBackupDir, i)] = []byte("x")
	}
	var calls []string
	now := time.Date(2026, 11, 19, 3, 30, 0, 0, time.UTC)
	env := plugin.Env{Runner: pgRunner(&calls, "shop\n"), FS: mem, Now: func() time.Time { return now }}
	out, err := (PG{}).Run(context.Background(), env, "backup", nil)
	if err != nil {
		t.Fatal(err)
	}
	names, _ := mem.ReadDir(PGBackupDir)
	left := 0
	for _, n := range names {
		if strings.HasPrefix(n, "shop-") {
			left++
		}
	}
	// nine old backups in mem, the fake did not create a new one; after rotation 7 are left
	if left != 7 {
		t.Fatalf("%d backups are left, expected 7: %v", left, names)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "pg_dump -Fc -f "+PGBackupDir+"/shop-20261119T033000Z.dump shop") {
		t.Fatalf("pg_dump was not called:\n%v", calls)
	}
	_ = out
}

func TestPGRestoreValidation(t *testing.T) {
	var calls []string
	mem := host.NewMemFS()
	env := plugin.Env{Runner: pgRunner(&calls, ""), FS: mem}
	for _, args := range [][]string{
		{"shop", "../etc/passwd"}, {"shop", "crm-20261119T030000Z.dump"}, {"shop", "shop-20261119T030000Z.dump"}, {"shop"},
	} {
		if _, err := (PG{}).Run(context.Background(), env, "restore", args); err == nil {
			t.Errorf("restore %v must refuse", args)
		}
	}
	for _, c := range calls {
		if strings.Contains(c, "pg_restore") {
			t.Fatalf("pg_restore must not be called: %s", c)
		}
	}
}

func TestBackupFindings(t *testing.T) {
	mem := host.NewMemFS()
	mem.Files[PGBackupDir+"/shop-20261119T030000Z.dump"] = []byte("x")
	mem.Files[PGBackupDir+"/crm-20261110T030000Z.dump"] = []byte("x")
	var calls []string
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	env := plugin.Env{Runner: pgRunner(&calls, "crm\nshop\nnew\napp_tpl_1\napp_tpl_2\n"), FS: mem, Now: func() time.Time { return now }, Projects: []string{"shop", "crm"}}
	mem.Files[pgSecretDir+"/pg-new"] = []byte("x")
	got := map[string]string{}
	for _, f := range (tools{context.Background(), env}).backupFindings() {
		got[f.Check] = f.Level
	}
	want := map[string]string{"backup-shop": plugin.LevelOK, "backup-crm": plugin.LevelFail, "backup-new": plugin.LevelWarn, "backup-others": plugin.LevelOK}
	if _, noisy := got["backup-app_tpl_1"]; noisy {
		t.Error("databases outside projects must not produce per-line findings")
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, expected %q", k, got[k], v)
		}
	}
}

func TestReadPassword(t *testing.T) {
	if p, err := readPassword(strings.NewReader("abcdefghijklmnop1234\n")); err != nil || p != "abcdefghijklmnop1234" {
		t.Fatalf("%q %v", p, err)
	}
	for _, bad := range []string{"short", "has space in the password!!", "quote'quote'quote'quote", ""} {
		if _, err := readPassword(strings.NewReader(bad)); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if _, err := readPassword(nil); err == nil {
		t.Error("a nil stdin must give an error")
	}
}

func TestCreateDBWithGivenPassword(t *testing.T) {
	mem := host.NewMemFS()
	var calls []string
	env := plugin.Env{Runner: pgRunner(&calls, ""), FS: mem, Stdin: strings.NewReader("S3cretS3cretS3cret\n")}
	if _, err := (PG{}).Run(context.Background(), env, "create-db", []string{"shop", "shopapp", "--password-stdin"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := mem.ReadFile(pgSecretDir + "/pg-shopapp"); strings.TrimSpace(string(b)) != "S3cretS3cretS3cret" {
		t.Fatalf("secret: %q", b)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "password 'S3cretS3cretS3cret'") {
		t.Fatal("the password was not passed into the SQL")
	}
}

func TestPGRestoreAsAnotherDatabase(t *testing.T) {
	mem := host.NewMemFS()
	mem.Files[PGBackupDir+"/shop-20261119T030000Z.dump"] = []byte("x")
	var calls []string
	env := plugin.Env{FS: mem, Runner: fn(func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c.Name+" "+strings.Join(c.Args, " "))
		if c.Name == "runuser" {
			q := c.Args[len(c.Args)-1]
			switch {
			case strings.HasPrefix(q, "select 1 from pg_database"):
				return host.Result{Stdout: []byte("1\n")}, nil
			case strings.HasPrefix(q, "select pg_get_userbyid"):
				return host.Result{Stdout: []byte("restore_owner\n")}, nil
			}
		}
		return host.Result{}, nil
	})}
	if _, err := (PG{}).Run(context.Background(), env, "restore", []string{"shop", "shop-20261119T030000Z.dump", "--as", "shop_test"}); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(calls, "\n")
	if !strings.Contains(all, "pg_restore --clean --if-exists -d shop_test --no-owner --role=restore_owner "+PGBackupDir+"/shop-20261119T030000Z.dump") {
		t.Fatalf("pg_restore was not run into the other database:\n%s", all)
	}
	for _, bad := range [][]string{{"shop", "shop-20261119T030000Z.dump", "--as"}, {"shop", "shop-20261119T030000Z.dump", "--as", "postgres"}, {"shop", "shop-20261119T030000Z.dump", "--as", "a;b"}} {
		if _, err := (PG{}).Run(context.Background(), env, "restore", bad); err == nil {
			t.Errorf("%v must be rejected", bad)
		}
	}
}
