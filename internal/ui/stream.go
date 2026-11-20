package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// streamConn is one open ssh connection to a server that carries all requests (`lite-deploy call-stream` on the other side).
type streamConn struct {
	conn    *host.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	next    int64
	pending map[int64]chan []byte
	dead    bool
}

var errNoStream = errors.New("the server does not support one connection for all requests")

// openStream starts the connection and waits for the greeting of call-stream. If the server is older and does not know the
// command, errNoStream is returned and the caller falls back to one ssh per request. A failed login is returned as is.
func (s *Server) openStream(ctx context.Context, target string) (*streamConn, string, error) {
	args := append([]string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}, s.SSHOptions...)
	args = append(args, target, "lite-deploy", "call-stream")
	conn, err := s.Streamer.Start(ctx, "ssh", args)
	if err != nil {
		return nil, err.Error(), err
	}
	greet := make(chan []byte, 1)
	c := &streamConn{conn: conn, pending: map[int64]chan []byte{}}
	rd := bufio.NewReaderSize(conn.Out, 64*1024)
	go func() {
		first := true
		for {
			line, err := rd.ReadBytes('\n')
			if len(line) > 0 {
				if first {
					first = false
					greet <- line
				} else {
					c.deliver(line)
				}
			}
			if err != nil {
				break
			}
		}
		if first {
			greet <- nil
		}
		c.fail()
		conn.Finish()
	}()
	select {
	case line := <-greet:
		var g struct {
			Stream string `json:"stream"`
		}
		if line != nil && json.Unmarshal(line, &g) == nil && g.Stream == "lite-deploy" {
			return c, "", nil
		}
	case <-time.After(15 * time.Second):
	}
	// no greeting: the login failed or the command is unknown; stderr tells which
	time.Sleep(50 * time.Millisecond)
	msg := conn.Stderr()
	conn.Close()
	if sshFailure(msg) {
		return nil, msg, errors.New(msg)
	}
	return nil, msg, errNoStream
}

func (c *streamConn) deliver(line []byte) {
	var r struct {
		ID       int64           `json:"id"`
		Response json.RawMessage `json:"response"`
		Error    string          `json:"error"`
	}
	if json.Unmarshal(line, &r) != nil {
		return
	}
	c.mu.Lock()
	ch := c.pending[r.ID]
	delete(c.pending, r.ID)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if r.Error != "" {
		ch <- errBody("internal", r.Error)
		return
	}
	ch <- []byte(r.Response)
}

// fail answers every waiting request with an error: the connection is gone.
func (c *streamConn) fail() {
	c.mu.Lock()
	c.dead = true
	pend := c.pending
	c.pending = map[int64]chan []byte{}
	c.mu.Unlock()
	for _, ch := range pend {
		ch <- errBody("connection_lost", "the connection to the server was lost; reload the page to connect again")
	}
}

func (c *streamConn) alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.dead
}

// do sends one request and waits for its answer.
func (c *streamConn) do(ctx context.Context, method, path string, body []byte, sig string) ([]byte, bool) {
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		return nil, false
	}
	c.next++
	id := c.next
	ch := make(chan []byte, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	req := map[string]any{"id": id, "method": method, "path": path}
	if sig != "" {
		// a signed request carries its body as a string, so the signed bytes reach the agent unchanged
		req["body"], req["sig"] = string(body), sig
	} else if len(body) > 0 {
		req["body"] = json.RawMessage(body)
	}
	b, _ := json.Marshal(req)
	c.wmu.Lock()
	_, err := io.WriteString(c.conn.In, string(b)+"\n")
	c.wmu.Unlock()
	if err != nil {
		c.fail()
		return nil, false
	}
	select {
	case out := <-ch:
		return out, true
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return errBody("internal", "the request was canceled"), true
	}
}

// viaStream sends the request through the shared connection. handled=false means: use one ssh per request instead.
func (s *Server) viaStream(ctx context.Context, target string, st *sshState, method, path string, body []byte, sig string) ([]byte, bool) {
	st.open.Lock()
	if st.noStream {
		st.open.Unlock()
		return nil, false
	}
	if time.Now().Before(st.failUntil) {
		msg := st.failMsg
		st.open.Unlock()
		return errBody("ssh_failed", msg), true
	}
	c := st.conn
	if c == nil || !c.alive() {
		nc, msg, err := s.openStream(ctx, target)
		switch {
		case err == nil:
			st.conn, c = nc, nc
		case errors.Is(err, errNoStream):
			st.noStream = true
			st.open.Unlock()
			return nil, false
		default:
			st.conn = nil
			st.failUntil = time.Now().Add(time.Minute)
			st.failMsg = friendlySSH(msg, target)
			out := errBody("ssh_failed", st.failMsg)
			st.open.Unlock()
			return out, true
		}
	}
	st.open.Unlock()
	return c.do(ctx, method, path, body, sig)
}

func friendlySSH(msg, target string) string {
	return msg + "\nlite-ui stops trying for a minute so that your address is not blocked by the server after repeated failed logins. Check in a terminal that `ssh " + target + "` logs in without asking anything (a key with a passphrase must be loaded first: ssh-add), then reload the page."
}
