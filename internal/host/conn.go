package host

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
)

// Conn is a long-running process with pipes: its standard input, its standard output and what it wrote to standard error.
// It is used for one SSH connection that carries many requests.
type Conn struct {
	In  io.WriteCloser
	Out io.Reader

	stderr func() string
	kill   func()
	done   chan struct{}
	once   sync.Once
}

// NewConn builds a Conn from pipes (the real one comes from ExecRunner.Start; tests build their own).
func NewConn(in io.WriteCloser, out io.Reader, stderr func() string, kill func()) *Conn {
	if stderr == nil {
		stderr = func() string { return "" }
	}
	if kill == nil {
		kill = func() {}
	}
	return &Conn{In: in, Out: out, stderr: stderr, kill: kill, done: make(chan struct{})}
}

// Stderr is what the process wrote to standard error so far.
func (c *Conn) Stderr() string { return c.stderr() }

// Done is closed when the process has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Finish marks the connection as ended (called when the process exits).
func (c *Conn) Finish() { c.once.Do(func() { close(c.done) }) }

// Close ends the process.
func (c *Conn) Close() {
	_ = c.In.Close()
	c.kill()
	c.Finish()
}

// Starter starts a long-running process.
type Starter interface {
	Start(ctx context.Context, name string, args []string) (*Conn, error)
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Start starts a real process with pipes. The process lives until Close or until ctx ends.
func (ExecRunner) Start(ctx context.Context, name string, args []string) (*Conn, error) {
	if name == "" {
		return nil, errors.New("empty command name")
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var errb lockedBuf
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := NewConn(in, out, errb.String, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	go func() {
		_ = cmd.Wait()
		c.Finish()
	}()
	return c, nil
}
