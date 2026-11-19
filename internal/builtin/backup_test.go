package builtin

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

func backupEnv(t *testing.T, stdin string) (plugin.Env, *host.MemFS, *[]host.Cmd) {
	t.Helper()
	mem := host.NewMemFS()
	var cmds []host.Cmd
	env := plugin.Env{
		FS: mem, Stdin: strings.NewReader(stdin),
		Now: func() time.Time { return time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC) },
		Runner: fn(func(c host.Cmd) (host.Result, error) {
			cmds = append(cmds, c)
			if c.Name == "tar" && c.Args[0] == "-czf" {
				return host.Result{Stdout: []byte("TARDATA-with-secret-value")}, nil
			}
			if c.Name == "tar" {
				return host.Result{}, nil
			}
			return host.Result{ExitCode: 127}, nil // psql, rclone and so on are not installed
		}),
	}
	return env, mem, &cmds
}

func TestBackupRequiresKey(t *testing.T) {
	env, _, _ := backupEnv(t, "")
	if _, err := (Backup{}).Run(context.Background(), env, "run", nil); err == nil || !strings.Contains(err.Error(), "without encryption") {
		t.Fatalf("without a key the backup is not taken: %v", err)
	}
}

func TestBackupEncryptsAndRestores(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	env, mem, cmds := backupEnv(t, "")
	if _, err := (Backup{}).Run(context.Background(), env, "setup", []string{id.Recipient().String()}); err != nil {
		t.Fatal(err)
	}
	if _, err := (Backup{}).Run(context.Background(), env, "run", nil); err != nil {
		t.Fatal(err)
	}
	name := "config-20261119T030000Z.tar.gz.age"
	enc := mem.Files[configDir+"/"+name]
	if len(enc) == 0 || bytes.Contains(enc, []byte("secret-value")) || mem.Perms[configDir+"/"+name] != 0o600 {
		t.Fatalf("the backup is not encrypted or the mode is not 600 (%v)", mem.Perms[configDir+"/"+name])
	}
	var tarArgs string
	for _, c := range *cmds {
		if c.Name == "tar" && c.Args[0] == "-czf" {
			tarArgs = strings.Join(c.Args, " ")
		}
	}
	for _, want := range []string{"--exclude=etc/lite-deploy/exec.age", "var/lib/lite-deploy/secrets", "etc/lite-deploy"} {
		if !strings.Contains(tarArgs, want) {
			t.Errorf("tar has no %q: %s", want, tarArgs)
		}
	}
	// the private key decrypts, a foreign one does not
	r, err := age.Decrypt(bytes.NewReader(enc), id)
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := io.ReadAll(r); string(plain) != "TARDATA-with-secret-value" {
		t.Fatalf("decrypted: %q", plain)
	}
	other, _ := age.GenerateX25519Identity()
	renv := env
	renv.Stdin = strings.NewReader(other.String() + "\n")
	if _, err := (Backup{}).Run(context.Background(), renv, "restore", []string{name, "--identity-stdin"}); err == nil {
		t.Fatal("a foreign key must not decrypt")
	}
	renv.Stdin = strings.NewReader(id.String() + "\n")
	*cmds = nil
	if _, err := (Backup{}).Run(context.Background(), renv, "restore", []string{name, "--identity-stdin"}); err != nil {
		t.Fatal(err)
	}
	last := (*cmds)[len(*cmds)-1]
	if last.Name != "tar" || last.Args[0] != "-xzf" || string(last.Stdin) != "TARDATA-with-secret-value" {
		t.Fatalf("unpacking: %+v", last)
	}
}

func TestBackupRotationAndSetupValidation(t *testing.T) {
	env, mem, _ := backupEnv(t, "")
	if _, err := (Backup{}).Run(context.Background(), env, "setup", []string{"not-a-key"}); err == nil {
		t.Fatal("an invalid key must be rejected")
	}
	id, _ := age.GenerateX25519Identity()
	_, _ = (Backup{}).Run(context.Background(), env, "setup", []string{id.Recipient().String()})
	for d := 1; d <= 16; d++ {
		mem.Files[configDir+"/config-202611"+twoDigits(d)+"T010000Z.tar.gz.age"] = []byte("x")
	}
	if _, err := (Backup{}).Run(context.Background(), env, "run", nil); err != nil {
		t.Fatal(err)
	}
	names, _ := mem.ReadDir(configDir)
	if len(names) != configKeep {
		t.Fatalf("%d backups are left, expected %d", len(names), configKeep)
	}
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestBackupCheckFailsWithSecretsButNoKey(t *testing.T) {
	env, mem, _ := backupEnv(t, "")
	mem.Files["/var/lib/lite-deploy/secrets/shop/api_key"] = []byte("v")
	got := (Backup{}).Check(context.Background(), env)
	if len(got) != 1 || got[0].Level != plugin.LevelFail {
		t.Fatalf("%+v", got)
	}
}

func TestBackblazeFetchValidation(t *testing.T) {
	var calls []string
	env := b2Env("", &calls, 0)
	env.FS.(*host.MemFS).Files[Backblaze.settings()] = []byte(`{"path":"my-backups"}`)
	if _, err := Backblaze.Run(context.Background(), env, "fetch", []string{"../x"}); err == nil {
		t.Fatal("an invalid argument must be rejected")
	}
	if _, err := Backblaze.Run(context.Background(), env, "fetch", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "copy b2:my-backups/config "+backupRoot+"/config") {
		t.Fatalf("%v", calls)
	}
}

func TestPGDumpsAreEncryptedWhenEnabled(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	mem := host.NewMemFS()
	var calls []string
	env := plugin.Env{
		FS: mem, Stdin: strings.NewReader(""),
		Now: func() time.Time { return time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC) },
		Runner: fn(func(c host.Cmd) (host.Result, error) {
			calls = append(calls, c.Name+" "+strings.Join(c.Args, " "))
			if c.Name == "runuser" {
				last := c.Args[len(c.Args)-1]
				switch {
				case c.Args[3] == "pg_dump":
					mem.Files[c.Args[6]] = []byte("DUMP-CONTENT-secret-rows")
				case c.Args[3] == "pg_restore":
					calls = append(calls, "RESTORED:"+string(mem.Files[last]))
				case strings.HasPrefix(last, "select datname"):
					return host.Result{Stdout: []byte("shop\n")}, nil
				case strings.HasPrefix(last, "select 1 from pg_database"):
					return host.Result{Stdout: []byte("1\n")}, nil
				}
			}
			return host.Result{}, nil
		}),
	}
	if _, err := (Backup{}).Run(context.Background(), env, "setup", []string{id.Recipient().String(), "--encrypt-dumps"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (PG{}).Run(context.Background(), env, "backup", nil); err != nil {
		t.Fatal(err)
	}
	enc := PGBackupDir + "/shop-20261119T030000Z.dump.age"
	if b := mem.Files[enc]; len(b) == 0 || strings.Contains(string(b), "secret-rows") {
		t.Fatal("the backup must be encrypted")
	}
	if _, plain := mem.Files[PGBackupDir+"/shop-20261119T030000Z.dump"]; plain {
		t.Fatal("the plain backup must not remain")
	}
	// restoring without a key refuses, with a key it decrypts into a temporary file and removes it
	if _, err := (PG{}).Run(context.Background(), env, "restore", []string{"shop", "shop-20261119T030000Z.dump.age"}); err == nil {
		t.Fatal("without a key the restore must refuse")
	}
	env.Stdin = strings.NewReader(id.String() + "\n")
	if _, err := (PG{}).Run(context.Background(), env, "restore", []string{"shop", "shop-20261119T030000Z.dump.age", "--identity-stdin"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "RESTORED:DUMP-CONTENT-secret-rows") {
		t.Fatalf("pg_restore did not receive the decrypted data:\n%s", strings.Join(calls, "\n"))
	}
	if _, left := mem.Files[PGBackupDir+"/.restore-shop.dump"]; left {
		t.Fatal("the temporary file must be removed")
	}
}

func TestParseIdentitySkipsAgeKeygenComments(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	file := "# created: 2026-11-19T10:00:00Z\n# public key: " + id.Recipient().String() + "\n" + id.String() + "\n"
	got, err := parseIdentity(file)
	if err != nil || got.String() != id.String() {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"", "# only comment\n", "AGE-SECRET-KEY-nope"} {
		if _, err := parseIdentity(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
