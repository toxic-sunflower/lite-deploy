// Package mcp is a small Model Context Protocol server over standard input and output (newline-delimited JSON-RPC 2.0),
// so that an AI assistant can look at your servers and do the safe things: read the state, deploy, roll back.
// It never confirms dangerous changes with the owner's key: those stay with the person in the interface.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Tool is one thing the assistant can do.
type Tool struct {
	Name        string
	Description string
	// Schema is the JSON Schema of the arguments.
	Schema map[string]any
	// ReadOnly: the tool only reads. The others change something, and the assistant is told so (readOnlyHint, destructiveHint):
	// Claude Desktop then asks the person before each call.
	ReadOnly bool
	// Run does the work and returns the text the assistant sees.
	Run func(ctx context.Context, args map[string]any) (string, error)
}

// Server serves the tools.
type Server struct {
	Name, Version string
	Tools         []Tool
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Serve reads requests until the input ends.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	rd := bufio.NewReaderSize(in, 1<<20)
	var wmu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = out.Write(append(b, '\n'))
	}
	var wg sync.WaitGroup
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 1 {
			var req request
			if json.Unmarshal(line, &req) != nil || req.Method == "" {
				send(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
			} else {
				wg.Add(1)
				go func() {
					defer wg.Done()
					s.handle(ctx, req, send)
				}()
			}
		}
		if err != nil {
			wg.Wait()
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) handle(ctx context.Context, req request, send func(any)) {
	reply := func(result any) {
		if len(req.ID) > 0 {
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		}
	}
	fail := func(code int, msg string) {
		if len(req.ID) > 0 {
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": code, "message": msg}})
		}
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2024-11-05"
		}
		reply(map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": s.Name, "version": s.Version}})
	case "notifications/initialized", "notifications/cancelled": //nolint:misspell // the protocol spells it this way
	case "ping":
		reply(map[string]any{})
	case "tools/list":
		list := []map[string]any{}
		for _, t := range s.Tools {
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema,
				"annotations": map[string]any{"readOnlyHint": t.ReadOnly, "destructiveHint": !t.ReadOnly, "openWorldHint": true}})
		}
		reply(map[string]any{"tools": list})
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &p) != nil {
			fail(-32602, "invalid parameters")
			return
		}
		for _, t := range s.Tools {
			if t.Name != p.Name {
				continue
			}
			text, err := t.Run(ctx, p.Arguments)
			if err != nil {
				reply(map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}})
				return
			}
			reply(map[string]any{"content": []map[string]any{{"type": "text", "text": text}}})
			return
		}
		fail(-32602, fmt.Sprintf("unknown tool %q", p.Name))
	default:
		fail(-32601, "method not found: "+req.Method)
	}
}
