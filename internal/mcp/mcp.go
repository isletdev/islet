// Package mcp exposes Islet to AI agents over the Model Context Protocol
// (Streamable HTTP, stateless JSON-RPC). It is off by default and every
// call carries a scoped API token, so an agent can only do what its token
// allows.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	// Resolve reports the method and path a call will actually reach, for a
	// tool whose target comes from its arguments rather than being fixed.
	//
	// Both gates below — the token's scopes and the caller's role — are checks
	// on a specific route. A tool that can reach more than one route has to be
	// checked against the one it was asked for, or a token scoped to read would
	// pass a check against a path it is not going to use. A tool without this
	// is checked against Method and Path, as before.
	Resolve func(args map[string]any) (method, path string, err error) `json:"-"`
}

// Server holds the tool set.
type Server struct {
	tools []Tool
	allow func(scopes, method, path string) bool
	// refused is called when a gate turns a call away. A token probing for
	// what it cannot do is the shape of an agent that has been pointed
	// somewhere it should not be, or of a leaked credential being tried, and
	// until this existed neither left any trace: the refusal happened before
	// any tool ran, so nothing reached the audit log.
	refused func(ctx context.Context, actor, tool, method, path, why string)
}

// New builds a server from tools and a scope check.
func New(tools []Tool, allow func(scopes, method, path string) bool) *Server {
	return &Server{tools: tools, allow: allow}
}

// OnRefusal registers a callback for calls a gate turned away.
func (s *Server) OnRefusal(f func(ctx context.Context, actor, tool, method, path, why string)) {
	s.refused = f
}

func (s *Server) deny(ctx context.Context, actor, tool, method, path, why string) {
	if s.refused != nil {
		s.refused(ctx, actor, tool, method, path, why)
	}
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
// Handle answers one MCP request. role is the calling user's role: a tool that
// changes the server needs the same role the equivalent HTTP route needs, and
// checking only the token scope would let a viewer act as an admin.
func (s *Server) Handle(ctx context.Context, actor, scopes, role string, body []byte) []byte {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) > 0 && body[0] == '[' {
		var reqs []request
		if err := json.Unmarshal(body, &reqs); err != nil {
			return errResp(nil, -32700, "parse error")
		}
		var out []json.RawMessage
		for _, r := range reqs {
			if b := s.one(ctx, actor, scopes, role, r); b != nil {
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
	return s.one(ctx, actor, scopes, role, r)
}

func errResp(id json.RawMessage, code int, msg string) []byte {
	b, _ := json.Marshal(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
	return b
}

func okResp(id json.RawMessage, result any) []byte {
	b, _ := json.Marshal(response{JSONRPC: "2.0", ID: id, Result: result})
	return b
}

func (s *Server) one(ctx context.Context, actor, scopes, role string, r request) []byte {
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
		out, err := s.CallTool(ctx, actor, scopes, role, p.Name, p.Arguments)
		if err != nil {
			if errors.Is(err, ErrNoSuchTool) {
				return errResp(r.ID, -32602, err.Error())
			}
			return okResp(r.ID, toolResult(err.Error(), true))
		}
		return okResp(r.ID, toolResult(out, false))
	}
	return errResp(r.ID, -32601, "method not found: "+r.Method)
}

// ErrNoSuchTool is returned when the name does not match anything. It is
// distinguished because a protocol error and a tool that failed are different
// things to a client.
var ErrNoSuchTool = errors.New("unknown tool")

// Tools is the set the agent may see, for a caller that renders them itself.
func (s *Server) Tools() []Tool { return s.tools }

// CallTool runs one tool under the same two gates every call passes: the
// token's scopes and the caller's role, both checked against the route the call
// actually resolves to.
//
// It exists so that the JSON-RPC endpoint and the in-panel assistant are one
// implementation rather than two. They serve different callers — one an agent
// over HTTP, the other the model behind the panel's chat — and a second copy of
// this would be a second place for the gates to drift, which is exactly the
// kind of difference nobody notices until it is the difference that mattered.
func (s *Server) CallTool(ctx context.Context, actor, scopes, role, name string, args map[string]any) (string, error) {
	for _, t := range s.tools {
		if t.Name != name {
			continue
		}
		method, path := t.Method, t.Path
		if t.Resolve != nil {
			m, pth, err := t.Resolve(args)
			if err != nil {
				s.deny(ctx, actor, t.Name, "", "", err.Error())
				return "", err
			}
			method, path = m, pth
		}
		if !s.allow(scopes, method, path) {
			why := "this token's scopes do not cover " + method + " " + path + " (needs " + t.Scope + ")"
			s.deny(ctx, actor, t.Name, method, path, why)
			return "", errors.New(why)
		}
		// A tool that changes the server needs the role the equivalent HTTP
		// route needs. Checking the scope alone let a viewer with a wide token
		// of their own run a cron job, which runs as root.
		if method != "GET" && role == "viewer" {
			why := "viewers cannot " + method + " " + path
			s.deny(ctx, actor, t.Name, method, path, why)
			return "", errors.New(why)
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		return t.Call(cctx, actor, args)
	}
	s.deny(ctx, actor, name, "", "", "no such tool")
	return "", fmt.Errorf("%w %s", ErrNoSuchTool, name)
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
