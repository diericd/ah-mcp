package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestFormatClosingTime(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"2026-02-23T22:59:00Z", "2026-02-23T23:59:00+01:00", true},
		{"2026-10-06T22:59:00Z", "2026-10-07T00:59:00+02:00", true},
		{"2026-10-06T23:59:00+02:00", "2026-10-06T23:59:00+02:00", true},
		{"2026-10-06T23:59:00", "2026-10-06T23:59:00+02:00", true}, // no zone: Brussels wall time
		{"dinsdag 23:59", "dinsdag 23:59", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, _, ok := formatClosingTime(tt.raw)
		if got != tt.want || ok != tt.ok {
			t.Errorf("formatClosingTime(%q) = %q, %v; want %q, %v", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

func callOrderToolJSON(t *testing.T, s *server.MCPServer, name string, args map[string]any) map[string]any {
	t.Helper()
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	res, err := s.GetTool(name).Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s tool error: %+v", name, res.Content)
	}
	var out any
	if err := json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("%s: decode: %v", name, err)
	}
	if list, ok := out.([]any); ok {
		return list[0].(map[string]any)
	}
	return out.(map[string]any)
}

func TestOrderToolsReturnClosingTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"orderFulfillments": map[string]any{"result": []map[string]any{{
					"orderId":         777,
					"modifiable":      true,
					"reopenable":      true,
					"isAfterCutOff":   true,
					"closingDateTime": "2026-10-06T22:59:00Z",
				}}}},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"orderId":     777,
			"orderState":  "SUBMITTED",
			"closingTime": "2026-02-23T22:59:00Z",
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
	registerGetOrderHistory(s, deps)
	registerGetOrderDetails(s, deps)

	hist := callOrderToolJSON(t, s, "ah_get_order_history", nil)
	if hist["closing_time"] != "2026-10-07T00:59:00+02:00" || hist["after_cutoff"] != true || hist["reopenable"] != true {
		t.Errorf("history entry lacks cutoff fields: %v", hist)
	}

	det := callOrderToolJSON(t, s, "ah_get_order_details", map[string]any{"order_id": "777"})
	if det["closing_time"] != "2026-02-23T23:59:00+01:00" || det["after_cutoff"] != true {
		t.Errorf("details lack cutoff fields: %v", det)
	}
}
