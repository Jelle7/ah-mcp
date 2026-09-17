package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// openBrowser attempts to open url in the default system browser.
// Runs in a goroutine — failure is silently ignored.
func openBrowser(url string) {
	go func() {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		case "darwin":
			cmd = exec.Command("open", url)
		default: // linux and others
			cmd = exec.Command("xdg-open", url)
		}
		_ = cmd.Start()
	}()
}

// oauthFlow tracks an in-progress OAuth flow across tool calls. The callback
// server owns a fixed port, so the flow must outlive the tool call that
// started it — a cancelled call leaves it running and a later ah_login
// resumes waiting on the same flow rather than colliding on the port.
var oauthFlow struct {
	sync.Mutex
	active   bool
	loginURL string
	done     <-chan error
	cancel   func()
}

// clearOAuthFlow marks the flow finished and tears down its callback server.
func clearOAuthFlow() {
	oauthFlow.Lock()
	cancel := oauthFlow.cancel
	oauthFlow.active = false
	oauthFlow.cancel = nil
	oauthFlow.done = nil
	oauthFlow.loginURL = ""
	oauthFlow.Unlock()
	if cancel != nil {
		cancel()
	}
}

// RegisterLoginTool registers the ah_login and ah_logout MCP tools.
func RegisterLoginTool(s *server.MCPServer, deps Deps) {
	registerLogout(s, deps)
	tool := mcp.NewTool("ah_login",
		mcp.WithTitleAnnotation("Albert Heijn: Log In"),
		mcp.WithDescription(
			"Log in to Albert Heijn. "+
				"Local mode: opens your browser automatically and waits — returns success once you log in (single call). "+
				"Remote mode: returns a URL to open manually, then call ah_login again to confirm. "+
				"Already authenticated? Returns your name immediately.",
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleLogin(ctx, deps)
	})
}

func registerLogout(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_logout",
		mcp.WithTitleAnnotation("Albert Heijn: Log Out"),
		mcp.WithDescription(
			"Log out of Albert Heijn by deleting the stored tokens. "+
				"Use this to switch accounts or reset a broken session. "+
				"After logout, call ah_login to authenticate again.",
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Always drop an in-flight login, even when no tokens are stored —
		// otherwise its callback server keeps the port until it times out.
		clearOAuthFlow()

		if !deps.IsAuthenticated() {
			return mcp.NewToolResultText("Already logged out (no active session)."), nil
		}
		if err := os.Remove(deps.TokensPath); err != nil && !os.IsNotExist(err) {
			return errResult(fmt.Sprintf("Failed to remove tokens: %v", err)), nil
		}
		// Reset the in-memory client so the next call gets a fresh unauthenticated state.
		if _, err := deps.ReloadClient(); err != nil {
			LogWarn("ah_logout", "reload client after logout: %v", err)
		}
		LogInfo("ah_logout", "tokens removed")
		return mcp.NewToolResultText("Logged out. Call ah_login to authenticate again."), nil
	})
}

func loginSuccess(ctx context.Context, deps Deps) (*mcp.CallToolResult, error) {
	c, err := deps.ReloadClient()
	if err != nil {
		return errResult(fmt.Sprintf("Login succeeded but could not reload client: %v", err)), nil
	}
	LogInfo("ah_login", "login_success")
	member, err := c.GetMember(ctx)
	if err != nil {
		return mcp.NewToolResultText("Login successful!"), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Login successful! Connected as %s %s.", member.FirstName, member.LastName)), nil
}

func stillWaitingResult(url string) *mcp.CallToolResult {
	return mcp.NewToolResultText(fmt.Sprintf(
		"Still waiting for browser login. Please open this URL if you haven't yet:\n\n%s\n\nThen call ah_login again to confirm.",
		url,
	))
}

func handleLogin(ctx context.Context, deps Deps) (*mcp.CallToolResult, error) {
	// Already authenticated — report immediately.
	if deps.IsAuthenticated() {
		c, err := deps.GetClient()
		if err != nil {
			return mcp.NewToolResultText("Already logged in (could not fetch member name)."), nil
		}
		member, err := c.GetMember(ctx)
		if err != nil {
			return mcp.NewToolResultText("Already logged in (could not fetch member name)."), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Already connected as %s %s.", member.FirstName, member.LastName)), nil
	}

	oauthFlow.Lock()

	// A flow is already running — either from a previous remote-mode call or
	// from a local call whose context was cancelled before the user finished.
	if oauthFlow.active {
		done := oauthFlow.done
		url := oauthFlow.loginURL
		oauthFlow.Unlock()
		return awaitLogin(ctx, deps, done, url, deps.RemoteMode)
	}

	flow, err := deps.StartOAuthFlow()
	if err != nil {
		oauthFlow.Unlock()
		LogError("ah_login", "start oauth flow: %v", err)
		return errResult(fmt.Sprintf("Failed to start OAuth flow: %v", err)), nil
	}

	oauthFlow.active = true
	oauthFlow.loginURL = flow.LoginURL
	oauthFlow.done = flow.Done
	oauthFlow.cancel = flow.Cancel
	oauthFlow.Unlock()

	if deps.RemoteMode {
		// Remote mode: return the URL — the user calls ah_login again after
		// completing the browser flow.
		return mcp.NewToolResultText(fmt.Sprintf(
			"Please open this URL in your browser to log in to Albert Heijn:\n\n%s\n\nCall ah_login again once you have completed the login.",
			flow.LoginURL,
		)), nil
	}

	// Local mode: open the browser, then block until the callback arrives.
	// The agent does not need to call ah_login a second time.
	openBrowser(flow.LoginURL)
	return awaitLogin(ctx, deps, flow.Done, flow.LoginURL, false)
}

// awaitLogin waits for a flow to finish. In poll mode it returns immediately
// with the pending URL; otherwise it blocks until the flow completes or ctx is
// cancelled. A cancelled context deliberately leaves the flow running so the
// callback server keeps the port and the next ah_login can pick it back up.
func awaitLogin(ctx context.Context, deps Deps, done <-chan error, url string, poll bool) (*mcp.CallToolResult, error) {
	if poll {
		select {
		case loginErr := <-done:
			return finishLogin(ctx, deps, loginErr)
		default:
			return stillWaitingResult(url), nil
		}
	}
	select {
	case loginErr := <-done:
		return finishLogin(ctx, deps, loginErr)
	case <-ctx.Done():
		return stillWaitingResult(url), nil
	}
}

func finishLogin(ctx context.Context, deps Deps, loginErr error) (*mcp.CallToolResult, error) {
	clearOAuthFlow()
	if loginErr != nil {
		LogError("ah_login", "login failed: %v", loginErr)
		return errResult(fmt.Sprintf("Login failed: %v", loginErr)), nil
	}
	return loginSuccess(ctx, deps)
}
