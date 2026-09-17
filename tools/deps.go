package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// OAuthFlow is a login flow in progress, handed back by Deps.StartOAuthFlow.
type OAuthFlow struct {
	// LoginURL is the URL the user must open in their browser.
	LoginURL string
	// Done receives nil on success, or an error on failure/timeout/cancel.
	Done <-chan error
	// Cancel tears down the temporary callback server. Safe to call twice.
	Cancel func()
}

// Deps holds the dependencies injected into every tool handler.
type Deps struct {
	// TokensPath is the path to the tokens.json file.
	TokensPath string
	// RemoteMode disables automatic browser opening during login.
	// Set this when the server runs on a machine without a display (remote/cloud).
	RemoteMode bool
	// GetClient returns the authenticated appie client.
	GetClient func() (*appie.Client, error)
	// ReloadClient recreates the client from the tokens file.
	ReloadClient func() (*appie.Client, error)
	// IsAuthenticated checks whether valid tokens are on disk.
	IsAuthenticated func() bool
	// StartOAuthFlow starts the temporary callback server and returns the flow.
	StartOAuthFlow func() (*OAuthFlow, error)
	// RefreshIfNeeded refreshes the access token if it is close to expiry and
	// reloads the appie client when it does.
	RefreshIfNeeded func(ctx context.Context) error
	// ServerVersion is the build version of the MCP server binary.
	ServerVersion string
	// AppieVersion is the version of the appie-go library in use.
	AppieVersion string
}

// clientHandler is a tool handler that runs against an authenticated client.
type clientHandler func(ctx context.Context, c *appie.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error)

// withClient adapts a clientHandler into an MCP tool handler, performing the
// auth check, token refresh and client lookup that every AH tool needs.
func withClient(deps Deps, fn clientHandler) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(), nil
		}
		if err := deps.RefreshIfNeeded(ctx); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}
		return fn(ctx, c, req)
	}
}

func notAuthResult() *mcp.CallToolResult {
	return mcp.NewToolResultError(`{"error":"not_authenticated","message":"Not logged in. Call ah_login first."}`)
}

func errResult(msg string) *mcp.CallToolResult {
	return mcp.NewToolResultError(msg)
}

// jsonResult marshals v and wraps it in a CallToolResult.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// toInt converts a decoded JSON value to int, supporting float64, int and
// numeric strings. Anything else yields 0.
func toInt(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	case json.Number:
		n, err := val.Int64()
		if err != nil {
			return 0
		}
		return int(n)
	default:
		return 0
	}
}

// clamp constrains n to [lo, hi].
func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// MCP clients disagree on how array arguments are encoded: some send a real
// JSON array, others (Claude Desktop, Claude Code) send the array serialised
// into a string. The parse* helpers below accept both.

// parseIntArray reads an argument as a list of positive integers.
func parseIntArray(raw any, name string) ([]int, error) {
	var out []int
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		var parsed []int
		if err := json.Unmarshal([]byte(v), &parsed); err != nil {
			return nil, fmt.Errorf("%s must be a JSON array of integers: %w", name, err)
		}
		out = parsed
	case []any:
		for _, r := range v {
			out = append(out, toInt(r))
		}
	default:
		return nil, fmt.Errorf("%s must be a JSON array of integers", name)
	}

	filtered := out[:0]
	for _, n := range out {
		if n > 0 {
			filtered = append(filtered, n)
		}
	}
	return filtered, nil
}

// parseStringArray reads an argument as a list of non-empty strings.
func parseStringArray(raw any, name string) ([]string, error) {
	var out []string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		var parsed []string
		if err := json.Unmarshal([]byte(v), &parsed); err != nil {
			return nil, fmt.Errorf("%s must be a JSON array of strings: %w", name, err)
		}
		out = parsed
	case []any:
		for _, r := range v {
			if s, ok := r.(string); ok {
				out = append(out, s)
			}
		}
	default:
		return nil, fmt.Errorf("%s must be a JSON array of strings", name)
	}

	// Empty entries must be dropped: a blank name would otherwise match every
	// unlabelled list item and remove far more than the caller asked for.
	filtered := out[:0]
	for _, s := range out {
		if strings.TrimSpace(s) != "" {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}

// lineItem is a product/quantity pair as accepted by the item-taking tools.
type lineItem struct {
	ProductID int `json:"product_id"`
	Quantity  int `json:"quantity"`
}

// parseLineItems reads an argument as a list of {product_id, quantity} objects.
// Items with a non-positive product_id are dropped. When defaultQty is > 0 it
// replaces a missing or zero quantity; otherwise a zero quantity is preserved
// (callers such as ah_update_order_items use it to mean "remove").
func parseLineItems(raw any, name string, defaultQty int) ([]lineItem, error) {
	var out []lineItem
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, fmt.Errorf("%s must be a JSON array of {product_id, quantity} objects: %w", name, err)
		}
	case []any:
		for _, r := range v {
			m, ok := r.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, lineItem{
				ProductID: toInt(m["product_id"]),
				Quantity:  toInt(m["quantity"]),
			})
		}
	default:
		return nil, fmt.Errorf("%s must be a JSON array of {product_id, quantity} objects", name)
	}

	filtered := out[:0]
	for _, it := range out {
		if it.ProductID <= 0 {
			continue
		}
		if it.Quantity <= 0 && defaultQty > 0 {
			it.Quantity = defaultQty
		}
		filtered = append(filtered, it)
	}
	return filtered, nil
}

// toListItems converts parsed line items into the appie library's type.
func toListItems(items []lineItem) []appie.ListItem {
	out := make([]appie.ListItem, 0, len(items))
	for _, it := range items {
		out = append(out, appie.ListItem{ProductID: it.ProductID, Quantity: it.Quantity})
	}
	return out
}
