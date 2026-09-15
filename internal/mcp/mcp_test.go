package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// scopesFor is a stand-in for auth.ScopeAllows: a token named "read" may only
// GET, one named "all" may do anything.
func scopesFor(scopes, method, path string) bool {
	if scopes == "all" {
		return true
	}
	return scopes == "read" && method == "GET"
}

func call(t *testing.T, s *Server, scopes, role, tool string, args map[string]any) (text string, isErr bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	out := s.Handle(context.Background(), "tester", scopes, role, body)
	var resp struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("bad response %s: %v", out, err)
	}
	if resp.Error != nil {
		return resp.Error.Message, true
	}
	if len(resp.Result.Content) == 0 {
		return "", resp.Result.IsError
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

// A tool whose target comes from its arguments must be checked against the
// route it was asked for. Checking a placeholder instead would let a read-only
// token reach a write route: the gate would pass on the placeholder and the
// call would then go somewhere else entirely.
func TestResolvedRouteIsWhatGetsChecked(t *testing.T) {
	var reached string
	generic := Tool{
		Name: "islet_request", Scope: "varies", Method: "GET", Path: "/api/v1/",
		Resolve: func(args map[string]any) (string, string, error) {
			return Str(args, "method"), Str(args, "path"), nil
		},
		Call: func(_ context.Context, _ string, args map[string]any) (string, error) {
			reached = Str(args, "method") + " " + Str(args, "path")
			return "ok", nil
		},
	}
	s := New([]Tool{generic}, scopesFor)

	t.Run("a read token cannot reach a write route", func(t *testing.T) {
		reached = ""
		text, isErr := call(t, s, "read", "admin", "islet_request", map[string]any{"method": "POST", "path": "/api/v1/domains"})
		if !isErr {
			t.Fatal("a read token posting to /api/v1/domains must be refused")
		}
		if reached != "" {
			t.Fatalf("the call ran anyway: %s", reached)
		}
		if !strings.Contains(text, "POST /api/v1/domains") {
			t.Errorf("the refusal should name the route asked for, got %q", text)
		}
	})

	t.Run("a read token may still read", func(t *testing.T) {
		reached = ""
		if _, isErr := call(t, s, "read", "admin", "islet_request", map[string]any{"method": "GET", "path": "/api/v1/domains"}); isErr {
			t.Fatal("a GET with a read token should be allowed")
		}
		if reached != "GET /api/v1/domains" {
			t.Errorf("reached %q", reached)
		}
	})

	t.Run("a wide token may write", func(t *testing.T) {
		reached = ""
		if _, isErr := call(t, s, "all", "admin", "islet_request", map[string]any{"method": "POST", "path": "/api/v1/domains"}); isErr {
			t.Fatal("a wide token should be allowed to write")
		}
		if reached != "POST /api/v1/domains" {
			t.Errorf("reached %q", reached)
		}
	})

	// The role gate has to follow the resolved method too: a viewer holding a
	// token of their own must not write, whatever the token allows.
	t.Run("a viewer cannot write even with a wide token", func(t *testing.T) {
		reached = ""
		text, isErr := call(t, s, "all", "viewer", "islet_request", map[string]any{"method": "POST", "path": "/api/v1/domains"})
		if !isErr {
			t.Fatal("a viewer must not be able to POST")
		}
		if reached != "" {
			t.Fatalf("the call ran anyway: %s", reached)
		}
		if !strings.Contains(text, "viewers cannot") {
			t.Errorf("got %q", text)
		}
	})

	t.Run("a viewer may read", func(t *testing.T) {
		if _, isErr := call(t, s, "all", "viewer", "islet_request", map[string]any{"method": "GET", "path": "/api/v1/apps"}); isErr {
			t.Fatal("a viewer should be able to read")
		}
	})
}

// A tool that cannot work out where it is going must not be called at all:
// there is nothing to check the gates against.
func TestResolveErrorStopsTheCall(t *testing.T) {
	called := false
	s := New([]Tool{{
		Name: "bad", Scope: "read", Method: "GET", Path: "/api/v1/",
		Resolve: func(map[string]any) (string, string, error) {
			return "", "", errBadTarget
		},
		Call: func(context.Context, string, map[string]any) (string, error) { called = true; return "", nil },
	}}, scopesFor)

	text, isErr := call(t, s, "all", "admin", "bad", nil)
	if !isErr || called {
		t.Fatalf("expected a refusal without calling the tool; isErr=%v called=%v", isErr, called)
	}
	if !strings.Contains(text, "no target") {
		t.Errorf("got %q", text)
	}
}

// A tool with a fixed route keeps being checked against that route.
func TestFixedToolStillUsesItsOwnRoute(t *testing.T) {
	s := New([]Tool{{
		Name: "deploy_app", Scope: "write", Method: "POST", Path: "/api/v1/apps",
		Call: func(context.Context, string, map[string]any) (string, error) { return "deployed", nil },
	}}, scopesFor)

	if _, isErr := call(t, s, "read", "admin", "deploy_app", nil); !isErr {
		t.Fatal("a read token must not reach a write tool")
	}
	if text, isErr := call(t, s, "all", "admin", "deploy_app", nil); isErr || text != "deployed" {
		t.Fatalf("a wide token should reach it: %q %v", text, isErr)
	}
}

type badTarget struct{}

func (badTarget) Error() string { return "no target: path is required" }

var errBadTarget = badTarget{}

// A refusal is the event worth recording: it never reaches a handler, so
// without this nothing anywhere writes down that a token tried.
func TestRefusalsAreReported(t *testing.T) {
	type refusal struct{ actor, tool, method, path, why string }
	var got []refusal

	s := New([]Tool{
		{Name: "deploy_app", Scope: "deploy", Method: "POST", Path: "/api/v1/apps",
			Call: func(context.Context, string, map[string]any) (string, error) { return "ok", nil }},
		{Name: "islet_request", Scope: "varies", Method: "GET", Path: "/api/v1/",
			Resolve: func(args map[string]any) (string, string, error) {
				if Str(args, "path") == "" {
					return "", "", errBadTarget
				}
				return Str(args, "method"), Str(args, "path"), nil
			},
			Call: func(context.Context, string, map[string]any) (string, error) { return "ok", nil }},
	}, scopesFor)
	s.OnRefusal(func(_ context.Context, actor, tool, method, path, why string) {
		got = append(got, refusal{actor, tool, method, path, why})
	})

	// Refused by scope.
	call(t, s, "read", "admin", "deploy_app", nil)
	// Refused by role, on a route resolved from the arguments.
	call(t, s, "all", "viewer", "islet_request", map[string]any{"method": "POST", "path": "/api/v1/domains"})
	// Refused because it could not work out where it was going.
	call(t, s, "all", "admin", "islet_request", nil)
	// A tool that does not exist: worth knowing someone asked.
	call(t, s, "all", "admin", "no_such_tool", nil)
	// And one that succeeds, which must not be reported.
	call(t, s, "all", "admin", "deploy_app", nil)

	if len(got) != 4 {
		t.Fatalf("expected four refusals, got %d: %+v", len(got), got)
	}
	if got[0].tool != "deploy_app" || !strings.Contains(got[0].why, "scopes do not cover") {
		t.Errorf("scope refusal not reported properly: %+v", got[0])
	}
	if got[1].path != "/api/v1/domains" || !strings.Contains(got[1].why, "viewers cannot") {
		t.Errorf("role refusal should carry the resolved path: %+v", got[1])
	}
	if !strings.Contains(got[2].why, "no target") {
		t.Errorf("resolve failure not reported: %+v", got[2])
	}
	if got[3].tool != "no_such_tool" {
		t.Errorf("unknown tool not reported: %+v", got[3])
	}
	for _, r := range got {
		if r.actor != "tester" {
			t.Errorf("every refusal should name who made it, got %q", r.actor)
		}
	}
}
