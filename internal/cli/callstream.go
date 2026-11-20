package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/toxic-sunflower/lite-deploy/internal/agent"
)

// cmdCallStream is `call` for many requests over one connection. The interface keeps ONE ssh connection to the server open
// and sends every request through it, so a page never opens a burst of logins (which the server's protection would
// count as an attack). Each request is a line of JSON {"id":1,"method":"GET","path":"/v1/status","body":{...}}, each
// answer a line {"id":1,"response":{...}}. The first line is a greeting so that the other side knows the command exists.
func cmdCallStream(env Env) result {
	const command = "call-stream"
	if env.Stdin == nil || env.Stdout == nil {
		return fail(command, CodeUsage, "usage: lite-deploy call-stream (requests are read from standard input)")
	}
	sock := agent.APISocket
	if env.Getenv != nil && env.Getenv("LITE_DEPLOY_SOCK") != "" {
		sock = env.Getenv("LITE_DEPLOY_SOCK")
	}
	var wmu sync.Mutex
	write := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = env.Stdout.Write(append(b, '\n'))
	}
	write(map[string]any{"stream": "lite-deploy", "version": Version})
	limit := agent.MaxRequest + agent.MaxBody + 1024
	rd := bufio.NewReaderSize(env.Stdin, 64*1024)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for {
		line, err := readLine(rd, limit)
		if len(bytes.TrimSpace(line)) > 0 {
			var req struct {
				ID     int64           `json:"id"`
				Method string          `json:"method"`
				Path   string          `json:"path"`
				Body   json.RawMessage `json:"body"`
				Sig    string          `json:"sig"`
			}
			if json.Unmarshal(line, &req) != nil || (req.Method != "GET" && req.Method != "POST") || !strings.HasPrefix(req.Path, "/v1/") {
				write(map[string]any{"id": req.ID, "error": "invalid request"})
			} else {
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer func() { <-sem; wg.Done() }()
					var body []byte
					if req.Method == "POST" {
						body = req.Body
						// a signed request carries its body as a string, so that the bytes that were signed arrive unchanged
						var text string
						if len(body) > 0 && body[0] == '"' && json.Unmarshal(body, &text) == nil {
							body = []byte(text)
						}
					}
					out, cerr := agent.CallSigned(context.Background(), sock, req.Method, req.Path, body, req.Sig)
					out = bytes.TrimSpace(out)
					if cerr != nil || len(out) == 0 || out[0] != '{' {
						msg := "the agent returned something that is not JSON"
						if cerr != nil {
							msg = cerr.Error()
						}
						write(map[string]any{"id": req.ID, "error": msg})
						return
					}
					write(map[string]any{"id": req.ID, "response": json.RawMessage(out)})
				}()
			}
		}
		if err != nil {
			break
		}
	}
	wg.Wait()
	return result{command: command}
}

// readLine reads one line; a line longer than limit is an error that ends the stream.
func readLine(r *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		part, isPrefix, err := r.ReadLine()
		buf = append(buf, part...)
		if len(buf) > limit {
			return nil, fmt.Errorf("request is too long")
		}
		if err != nil {
			if err == io.EOF && len(buf) > 0 {
				return buf, io.EOF
			}
			return buf, err
		}
		if !isPrefix {
			return buf, nil
		}
	}
}
