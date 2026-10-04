package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Reverting discards the edits made since reopen, so no reopen or revert text
// may steer the agent into calling ah_revert_order as a routine last step.
func TestReopenRevertTextDoesNotPromptRevert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"orderReopen": map[string]any{"status": "SUCCESS"}},
		})
	}))
	defer srv.Close()

	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	deps := Deps{
		IsAuthenticated: func() bool { return true },
		RefreshIfNeeded: func(context.Context) error { return nil },
		GetClient:       func() (*appie.Client, error) { return client, nil },
	}
	s := server.NewMCPServer("test", "0")
	registerReopenOrder(s, deps)
	registerRevertOrder(s, deps)

	reopen := s.GetTool("ah_reopen_order")
	revert := s.GetTool("ah_revert_order")

	for _, bad := range []string{"MUST call ah_revert_order", "ALWAYS call"} {
		if strings.Contains(reopen.Tool.Description, bad) {
			t.Errorf("reopen description contains %q: %s", bad, reopen.Tool.Description)
		}
		if strings.Contains(revert.Tool.Description, bad) {
			t.Errorf("revert description contains %q: %s", bad, revert.Tool.Description)
		}
	}
	if !strings.Contains(reopen.Tool.Description, "Leave it REOPENED") {
		t.Errorf("reopen description must say to leave the order reopened: %s", reopen.Tool.Description)
	}
	if !strings.Contains(revert.Tool.Description, "discards") {
		t.Errorf("revert description must warn that it discards edits: %s", revert.Tool.Description)
	}
	if strings.Contains(revert.Tool.Annotations.Title, "Resubmit") {
		t.Errorf("revert title still says Resubmit: %s", revert.Tool.Annotations.Title)
	}

	var req mcp.CallToolRequest
	req.Params.Arguments = map[string]any{"order_id": "777"}
	res, err := reopen.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	text := res.Content[0].(mcp.TextContent).Text
	if strings.Contains(text, "ah_revert_order") {
		t.Errorf("reopen result must not tell the agent to revert: %s", text)
	}
	if !strings.Contains(text, "777") || !strings.Contains(text, "REOPENED") {
		t.Errorf("reopen result = %q, want order id and REOPENED", text)
	}
}
