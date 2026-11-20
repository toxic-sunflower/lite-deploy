package mcp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInitializeListAndCall(t *testing.T) {
	s := &Server{Name: "lite-deploy", Version: "test", Tools: []Tool{
		{Name: "hello", Description: "says hello", Schema: map[string]any{"type": "object"}, Run: func(_ context.Context, a map[string]any) (string, error) {
			return "hello " + a["who"].(string), nil
		}},
		{Name: "boom", Description: "fails", Schema: map[string]any{"type": "object"}, Run: func(context.Context, map[string]any) (string, error) { return "", errors.New("it failed") }},
	}}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"hello","arguments":{"who":"anna"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"boom","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"unknown"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{`"serverInfo"`, `"name":"hello"`, `hello anna`, `"isError":true`, `it failed`, `unknown tool`, `method not found`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n") != 6 {
		t.Errorf("a notification must get no answer; 6 answers expected:\n%s", got)
	}
}
