package agent

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Настоящие unix-сокеты: агент, исполнитель и клиент, uid вызывающего определяет ядро (SO_PEERCRED).
func TestRealSockets(t *testing.T) {
	dir := t.TempDir()
	apiSock, execSock := filepath.Join(dir, "api.sock"), filepath.Join(dir, "exec.sock")

	execLn, err := host.ListenUnix(execSock)
	if err != nil {
		t.Skip("unix-сокеты недоступны:", err)
	}
	defer execLn.Close()
	var ran []string
	go func() {
		for {
			c, err := execLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				Exec(c, c, func(args []string) []byte {
					ran = append(ran, strings.Join(args, " "))
					return []byte(`{"ok":true,"api_version":"v1","command":"deploy","data":{"slot":"blue"}}`)
				}, &bytes.Buffer{})
			}()
		}
	}()

	apiLn, err := host.ListenUnix(apiSock)
	if err != nil {
		t.Fatal(err)
	}
	me := os.Getuid()
	var audit bytes.Buffer
	a := &Agent{Version: "t", Audit: &audit,
		Allowed:  func(uid int) (string, bool) { return "me", uid == me },
		ExecDial: func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "unix", execSock) }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, apiLn) }()

	c, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()
	out, err := Call(c, apiSock, "POST", "/v1/deploy", []byte(`{"project":"shop","sha":"`+sha+`"}`))
	if err != nil || !strings.Contains(string(out), `"slot":"blue"`) {
		t.Fatalf("%v %s", err, out)
	}
	if len(ran) != 1 || ran[0] != "deploy shop "+sha+" --json" {
		t.Fatalf("исполнитель получил %v", ran)
	}
	if !strings.Contains(audit.String(), `"user":"me"`) || !strings.Contains(audit.String(), `"uid":`) {
		t.Fatalf("журнал: %s", audit.String())
	}

	// чужой uid: тот же сокет, но Allowed не пускает
	a.Allowed = func(int) (string, bool) { return "", false }
	out, err = Call(c, apiSock, "GET", "/v1/status", nil)
	if err != nil || !strings.Contains(string(out), "permission_denied") {
		t.Fatalf("%v %s", err, out)
	}
	cancel()
	<-done

	if _, err := Call(context.Background(), filepath.Join(dir, "none.sock"), "GET", "/v1/status", nil); err == nil {
		t.Fatal("без агента должна быть ошибка")
	}
}

func TestPeerUIDMatchesProcess(t *testing.T) {
	dir := t.TempDir()
	ln, err := host.ListenUnix(filepath.Join(dir, "s"))
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	got := make(chan int, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- -2
			return
		}
		defer c.Close()
		uid, _ := host.PeerUID(c)
		got <- uid
	}()
	c, err := net.Dial("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if uid := <-got; uid != os.Getuid() {
		t.Fatalf("SO_PEERCRED: %d, ожидалось %d", uid, os.Getuid())
	}
}
