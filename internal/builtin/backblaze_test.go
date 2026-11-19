package builtin

import (
	"context"
	"filippo.io/age"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

func b2Env(stdin string, calls *[]string, exit int) plugin.Env {
	return plugin.Env{
		FS:    host.NewMemFS(),
		Stdin: strings.NewReader(stdin),
		Now:   func() time.Time { return time.Date(2026, 11, 19, 5, 0, 0, 0, time.UTC) },
		Runner: fn(func(c host.Cmd) (host.Result, error) {
			*calls = append(*calls, c.Name+" "+strings.Join(c.Args, " "))
			return host.Result{ExitCode: exit, Stderr: []byte("denied")}, nil
		}),
	}
}

func TestBackblazeSetupWritesPrivateConfig(t *testing.T) {
	var calls []string
	env := b2Env("0012345678abcdef\nK001abcdefghijklmnopqrstuvwxyz\n", &calls, 0)
	if _, err := Backblaze.Run(context.Background(), env, "setup", []string{"my-backups", "lite/prod"}); err != nil {
		t.Fatal(err)
	}
	mem := env.FS.(*host.MemFS)
	if mem.Perms[Backblaze.conf()] != 0o600 || !strings.Contains(string(mem.Files[Backblaze.conf()]), "account = 0012345678abcdef") {
		t.Fatalf("конфигурация: %v %s", mem.Perms[Backblaze.conf()], mem.Files[Backblaze.conf()])
	}
	if strings.Contains(strings.Join(calls, "\n"), "K001") {
		t.Fatal("ключ попал в аргументы команды")
	}
	if !strings.Contains(strings.Join(calls, "\n"), "lsf --max-depth 1 b2:my-backups/lite/prod") {
		t.Fatalf("доступ к бакету не проверен: %v", calls)
	}
}

func TestBackblazeSetupRejectsBadInput(t *testing.T) {
	for _, c := range []struct {
		stdin string
		args  []string
	}{
		{"id\n", []string{"my-backups"}},
		{"0012345678abcdef\nK001abcdefghijklmnopqrstuvwxyz\n", []string{"Bad_Bucket"}},
		{"0012345678abcdef\nK001abcdefghijklmnopqrstuvwxyz\n", []string{"my-backups", "../x"}},
		{"bad id\nK001abcdefghijklmnopqrstuvwxyz\n", []string{"my-backups"}},
	} {
		var calls []string
		if _, err := Backblaze.Run(context.Background(), b2Env(c.stdin, &calls, 0), "setup", c.args); err == nil {
			t.Errorf("%q %v должен отказать", c.stdin, c.args)
		}
	}
}

func TestBackblazeUploadUsesCopyAndRecordsTime(t *testing.T) {
	var calls []string
	env := b2Env("", &calls, 0)
	mem := env.FS.(*host.MemFS)
	mem.Files[Backblaze.settings()] = []byte(`{"path":"my-backups/lite"}`)
	if _, err := Backblaze.Run(context.Background(), env, "upload", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "copy "+backupRoot+" b2:my-backups/lite") {
		t.Fatalf("вызовы: %v", calls)
	}
	if strings.Contains(strings.Join(calls, "\n"), "sync") {
		t.Fatal("sync может стереть копии в бакете")
	}
	if _, ok := Backblaze.lastTime(tools{context.Background(), env}); !ok {
		t.Fatal("время отправки не записано")
	}
}

func TestBackblazeUploadFailureDoesNotRecordTime(t *testing.T) {
	var calls []string
	env := b2Env("", &calls, 1)
	mem := env.FS.(*host.MemFS)
	mem.Files[Backblaze.settings()] = []byte(`{"path":"my-backups"}`)
	if _, err := Backblaze.Run(context.Background(), env, "upload", nil); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if _, ok := Backblaze.lastTime(tools{context.Background(), env}); ok {
		t.Fatal("неудачная отправка не должна обновлять время")
	}
}

func TestBackblazeCheckFlagsOpenKeyAndStaleUpload(t *testing.T) {
	var calls []string
	env := b2Env("", &calls, 0)
	mem := env.FS.(*host.MemFS)
	mem.Dirs[Backblaze.dir()] = 0o700
	mem.Files[Backblaze.settings()] = []byte(`{"path":"my-backups"}`)
	mem.Files[Backblaze.conf()] = []byte("x")
	mem.Perms[Backblaze.conf()] = 0o644
	mem.Files[Backblaze.lastFile()] = []byte("2026-11-10T04:30:00Z\n")
	got := map[string]string{}
	for _, f := range Backblaze.Check(context.Background(), env) {
		got[f.Check] = f.Level
	}
	if got["key-perms"] != plugin.LevelFail || got["upload"] != plugin.LevelFail {
		t.Fatalf("%v", got)
	}
}

func TestS3SetupWritesEndpointAndKeys(t *testing.T) {
	var calls []string
	env := b2Env("AKIAEXAMPLE12345\nsecretsecretsecretsecret1234\n", &calls, 0)
	if _, err := S3.Run(context.Background(), env, "setup", []string{"https://s3.example.com", "my-backups", "lite"}); err != nil {
		t.Fatal(err)
	}
	mem := env.FS.(*host.MemFS)
	conf := string(mem.Files[S3.conf()])
	if mem.Perms[S3.conf()] != 0o600 || !strings.Contains(conf, "endpoint = https://s3.example.com") || !strings.Contains(conf, "access_key_id = AKIAEXAMPLE12345") {
		t.Fatalf("%v %s", mem.Perms[S3.conf()], conf)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "lsf --max-depth 1 s3:my-backups/lite") || strings.Contains(strings.Join(calls, "\n"), "secretsecret") {
		t.Fatalf("%v", calls)
	}
	for _, bad := range [][]string{{"http://s3.example.com", "my-backups"}, {"https://s3.example.com"}, {"https://x.example.com;rm", "my-backups"}} {
		if _, err := S3.Run(context.Background(), b2Env("AKIAEXAMPLE12345\nsecretsecretsecretsecret1234\n", &calls, 0), "setup", bad); err == nil {
			t.Errorf("%v должен быть отклонён", bad)
		}
	}
}

func TestSFTPRequiresHostKeyAndWritesPrivateFiles(t *testing.T) {
	var calls []string
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----"
	env := b2Env(key+"\n---\nbackup.example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA\n", &calls, 0)
	if _, err := SFTP.Run(context.Background(), env, "setup", []string{"backup.example.com", "lite", "backups/prod"}); err != nil {
		t.Fatal(err)
	}
	mem := env.FS.(*host.MemFS)
	if mem.Perms[SFTP.dir()+"/id"] != 0o600 || !strings.Contains(string(mem.Files[SFTP.conf()]), "known_hosts_file = "+SFTP.dir()+"/known_hosts") {
		t.Fatalf("%v %s", mem.Perms[SFTP.dir()+"/id"], mem.Files[SFTP.conf()])
	}
	if !strings.Contains(strings.Join(calls, "\n"), "lsf --max-depth 1 sftp:/backups/prod") {
		t.Fatalf("%v", calls)
	}
	for _, in := range []string{key + "\n", key + "\n---\n", "no key\n---\nhost ssh-ed25519 AAAA\n"} {
		if _, err := SFTP.Run(context.Background(), b2Env(in, &calls, 0), "setup", []string{"backup.example.com", "lite", "backups"}); err == nil {
			t.Errorf("stdin %.20q должен быть отклонён (нет ключа хоста или ключа)", in)
		}
	}
	if _, err := SFTP.Run(context.Background(), b2Env(key+"\n---\nh ssh-ed25519 AAAA\n", &calls, 0), "setup", []string{"h", "lite", "../x"}); err == nil {
		t.Error("путь с .. должен быть отклонён")
	}
}

func TestBackupRunUploadsToEveryConfiguredStore(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	env, mem, cmds := backupEnv(t, "")
	inner := env.Runner
	env.Runner = fn(func(c host.Cmd) (host.Result, error) {
		if c.Name == "rclone" {
			*cmds = append(*cmds, c)
			return host.Result{}, nil
		}
		return inner.Run(context.Background(), c)
	})
	_, _ = (Backup{}).Run(context.Background(), env, "setup", []string{id.Recipient().String()})
	mem.Files[Backblaze.settings()] = []byte(`{"path":"b1"}`)
	mem.Files[SFTP.settings()] = []byte(`{"path":"/srv/b"}`)
	if _, err := (Backup{}).Run(context.Background(), env, "run", nil); err != nil {
		t.Fatal(err)
	}
	var remotes []string
	for _, c := range *cmds {
		if c.Name == "rclone" {
			remotes = append(remotes, strings.Join(c.Args, " "))
		}
	}
	all := strings.Join(remotes, "\n")
	if !strings.Contains(all, "b2:b1") || !strings.Contains(all, "sftp:/srv/b") || strings.Contains(all, "s3:") {
		t.Fatalf("отправка только в настроенные места:\n%s", all)
	}
}
