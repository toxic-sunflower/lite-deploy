package host

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerNoShell(t *testing.T) {
	// аргументы не проходят через оболочку: «;» и «$(…)» остаются обычным текстом
	res, err := ExecRunner{}.Run(context.Background(), Cmd{Name: "echo", Args: []string{"a; id", "$(id)"}})
	if err != nil || res.ExitCode != 0 || string(res.Stdout) != "a; id $(id)\n" {
		t.Fatalf("%q %v", res.Stdout, err)
	}
}

func TestExecRunnerExitCodeStdinTimeout(t *testing.T) {
	r := ExecRunner{}
	if res, err := r.Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "exit 3"}}); err != nil || res.ExitCode != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	if res, _ := r.Run(context.Background(), Cmd{Name: "cat", Stdin: []byte("hello")}); string(res.Stdout) != "hello" {
		t.Fatalf("stdin: %q", res.Stdout)
	}
	start := time.Now()
	_, err := r.Run(context.Background(), Cmd{Name: "sleep", Args: []string{"5"}, Timeout: 100 * time.Millisecond})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("таймаут не сработал: %v", err)
	}
	if _, err := r.Run(context.Background(), Cmd{}); err == nil {
		t.Fatal("пустая команда")
	}
	if _, err := r.Run(context.Background(), Cmd{Name: "/nonexistent-binary"}); err == nil {
		t.Fatal("несуществующая команда")
	}
}

func TestFakeRunner(t *testing.T) {
	f := &FakeRunner{}
	if _, err := f.Run(context.Background(), Cmd{Name: "x"}); err == nil {
		t.Fatal("без обработчика ожидали ошибку")
	}
	f.Handler = func(c Cmd) (Result, error) { return Result{Stdout: []byte(c.Name)}, nil }
	res, _ := f.Run(context.Background(), Cmd{Name: "y", Args: []string{"1"}})
	if string(res.Stdout) != "y" || len(f.Calls) != 2 || f.Calls[1].Args[0] != "1" {
		t.Fatalf("%+v %+v", res, f.Calls)
	}
}

func TestMemFS(t *testing.T) {
	m := NewMemFS()
	m.Files["/a/b/c.json"] = []byte("1")
	m.Files["/a/d.json"] = []byte("2")
	m.Files["/ab/x"] = []byte("3")
	names, err := m.ReadDir("/a")
	if err != nil || strings.Join(names, ",") != "b,d.json" {
		t.Fatalf("%v %v", names, err)
	}
	if _, err := m.ReadDir("/nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%v", err)
	}
	if _, err := m.ReadFile("/nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%v", err)
	}
	if b, _ := m.ReadFile("/a/d.json"); string(b) != "2" {
		t.Fatal("ReadFile")
	}
}

func TestOSFS(t *testing.T) {
	dir := t.TempDir()
	if _, err := (OSFS{}).ReadFile(dir + "/none"); err == nil {
		t.Fatal("ожидали ошибку")
	}
	if names, err := (OSFS{}).ReadDir(dir); err != nil || len(names) != 0 {
		t.Fatalf("%v %v", names, err)
	}
}
