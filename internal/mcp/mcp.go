// Package mcp exposes Islet to AI agents over the Model Context Protocol
// (Streamable HTTP, stateless JSON-RPC). It is off by default and every
// call carries a scoped API token, so an agent can only do what its token
// allows.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Tool is a callable the agent sees.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// Scope is the API scope required (matched against the token like an HTTP call).
	Scope  string                                                                       `json:"-"`
	Method string                                                                       `json:"-"`
	Path   string                                                                       `json:"-"`
	Call   func(ctx context.Context, actor string, args map[string]any) (string, error) `json:"-"`
}

// Server holds the tool set.
type Server struct {
	tools []Tool
	allow func(scopes, method, path string) bool
}

// New builds a server from tools and a scope check.
func New(tools []Tool, allow func(scopes, method, path string) bool) *Server {
	return &Server{tools: tools, allow: allow}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Handle processes one JSON-RPC message (or batch) and returns the reply
// bytes, or nil for notifications.
func (s *Server) Handle(ctx context.Context, actor, scopes string, body []byte) []byte {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) > 0 && body[0] == '[' {
		var reqs []request
		if err := json.Unmarshal(body, &reqs); err != nil {
			return errResp(nil, -32700, "parse error")
		}
		var out []json.RawMessage
		for _, r := range reqs {
			if b := s.one(ctx, actor, scopes, r); b != nil {
				out = append(out, b)
			}
		}
		if out == nil {
			return nil
		}
		b, _ := json.Marshal(out)
		return b
	}
	var r request
	if err := json.Unmarshal(body, &r); err != nil {
		return errResp(nil, -32700, "parse error")
	}
	return s.one(ctx, actor, scopes, r)
}

func errResp(id json.RawMessage, code int, msg string) []byte {
	b, _ := json.Marshal(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
	return b
}

func okResp(id json.RawMessage, result any) []byte {
	b, _ := json.Marshal(response{JSONRPC: "2.0", ID: id, Result: result})
	return b
}

func (s *Server) one(ctx context.Context, actor, scopes string, r request) []byte {
	if strings.HasPrefix(r.Method, "notifications/") {
		return nil
	}
	switch r.Method {
	case "initialize":
		return okResp(r.ID, map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "islet", "version": "1"},
			"instructions":    "Islet manages one Linux server: containers, deploys, databases, cron jobs, backups and security. Prefer read tools first. Destructive tools need a token with the matching scope; the server refuses anything the token does not cover.",
		})
	case "ping":
		return okResp(r.ID, map[string]any{})
	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema})
		}
		return okResp(r.ID, map[string]any{"tools": list})
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil {
			return errResp(r.ID, -32602, "invalid params")
		}
		for _, t := range s.tools {
			if t.Name != p.Name {
				continue
			}
			if !s.allow(scopes, t.Method, t.Path) {
				return okResp(r.ID, toolResult("this token's scopes do not cover "+t.Name+" (needs "+t.Scope+")", true))
			}
			cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			out, err := t.Call(cctx, actor, p.Arguments)
			if err != nil {
				return okResp(r.ID, toolResult(err.Error(), true))
			}
			return okResp(r.ID, toolResult(out, false))
		}
		return errResp(r.ID, -32602, "unknown tool "+p.Name)
	}
	return errResp(r.ID, -32601, "method not found: "+r.Method)
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}
}

// ---- helpers for tool implementations ----

// Str reads a string argument.
func Str(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

// Int reads a numeric argument with a default.
func Int(args map[string]any, key string, def int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return def
}

// JSON renders a value for the agent.
func JSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// Drain reads a line stream to a string, capped, then waits.
func Drain(rc io.ReadCloser, wait func() error, max int) (string, error) {
	var b strings.Builder
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if b.Len() < max {
			b.WriteString(sc.Text() + "\n")
		}
	}
	rc.Close()
	err := wait()
	if b.Len() >= max {
		b.WriteString("… truncated …\n")
	}
	if err != nil {
		return b.String(), fmt.Errorf("%w\n%s", err, b.String())
	}
	return b.String(), nil
}

// Schema builds an input schema from property definitions.
func Schema(required []string, props map[string]any) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

// P is a shorthand for a schema property.
func P(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }
