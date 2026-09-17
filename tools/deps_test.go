package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
)

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want TextContent", res.Content[0])
	}
	return tc.Text
}

func TestWithClientRejectsUnauthenticated(t *testing.T) {
	called := false
	deps := Deps{
		IsAuthenticated: func() bool { return false },
		RefreshIfNeeded: func(context.Context) error { t.Fatal("must not refresh when logged out"); return nil },
		GetClient:       func() (*appie.Client, error) { t.Fatal("must not build a client when logged out"); return nil, nil },
	}
	h := withClient(deps, func(context.Context, *appie.Client, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return nil, nil
	})

	res, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handler returned a transport error: %v", err)
	}
	if called {
		t.Fatal("the inner handler ran without authentication")
	}
	if !res.IsError {
		t.Fatal("result should be flagged as an error")
	}
	if !strings.Contains(resultText(t, res), "not_authenticated") {
		t.Fatalf("result = %q, want the not_authenticated payload", resultText(t, res))
	}
}

func TestWithClientSurfacesRefreshFailure(t *testing.T) {
	called := false
	deps := Deps{
		IsAuthenticated: func() bool { return true },
		RefreshIfNeeded: func(context.Context) error { return errors.New("refresh token rejected") },
		GetClient:       func() (*appie.Client, error) { t.Fatal("must not run after a failed refresh"); return nil, nil },
	}
	h := withClient(deps, func(context.Context, *appie.Client, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return nil, nil
	})

	res, _ := h(context.Background(), mcp.CallToolRequest{})
	if called {
		t.Fatal("the inner handler ran despite a failed refresh")
	}
	if !strings.Contains(resultText(t, res), "refresh token rejected") {
		t.Fatalf("result = %q, want the refresh error", resultText(t, res))
	}
}

func TestWithClientSurfacesClientFailure(t *testing.T) {
	deps := Deps{
		IsAuthenticated: func() bool { return true },
		RefreshIfNeeded: func(context.Context) error { return nil },
		GetClient:       func() (*appie.Client, error) { return nil, errors.New("no config") },
	}
	h := withClient(deps, func(context.Context, *appie.Client, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Fatal("the inner handler ran without a client")
		return nil, nil
	})

	res, _ := h(context.Background(), mcp.CallToolRequest{})
	if !strings.Contains(resultText(t, res), "no config") {
		t.Fatalf("result = %q, want the client error", resultText(t, res))
	}
}

func TestWithClientPassesClientThrough(t *testing.T) {
	want := appie.New()
	var got *appie.Client
	refreshed := false

	deps := Deps{
		IsAuthenticated: func() bool { return true },
		RefreshIfNeeded: func(context.Context) error { refreshed = true; return nil },
		GetClient:       func() (*appie.Client, error) { return want, nil },
	}
	h := withClient(deps, func(_ context.Context, c *appie.Client, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		got = c
		return mcp.NewToolResultText("done"), nil
	})

	res, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !refreshed {
		t.Fatal("tokens should be refreshed before every call")
	}
	if got != want {
		t.Fatal("the handler did not receive the client from GetClient")
	}
	if resultText(t, res) != "done" {
		t.Fatalf("result = %q, want \"done\"", resultText(t, res))
	}
}

func TestJSONResult(t *testing.T) {
	res, err := jsonResult(map[string]int{"a": 1})
	if err != nil {
		t.Fatalf("jsonResult: %v", err)
	}
	if got := resultText(t, res); !strings.Contains(got, `"a": 1`) {
		t.Fatalf("result = %q, want indented JSON", got)
	}
}
