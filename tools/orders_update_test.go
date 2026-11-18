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

func TestUpdateOrderItemsReportsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"basketItemsUpdate": map[string]any{"__typename": "Basket"}},
			})
			return
		}
		// The slot accepts 1 and silently drops 2.
		json.NewEncoder(w).Encode(map[string]any{
			"id":         777,
			"totalPrice": map[string]any{"priceTotalPayable": 3.5},
			"orderedProducts": []map[string]any{
				{"quantity": 2, "product": map[string]any{"webshopId": 1}},
			},
		})
	}))
	defer srv.Close()

	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	registerUpdateOrderItems(s, Deps{
		IsAuthenticated: func() bool { return true },
		RefreshIfNeeded: func(context.Context) error { return nil },
		GetClient:       func() (*appie.Client, error) { return client, nil },
	})

	var req mcp.CallToolRequest
	req.Params.Arguments = map[string]any{"items": []any{
		map[string]any{"product_id": float64(1), "quantity": float64(2)},
		map[string]any{"product_id": float64(2), "quantity": float64(1)},
	}}
	res, err := s.GetTool("ah_update_order_items").Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	text := res.Content[0].(mcp.TextContent).Text
	if strings.Contains(text, "ah_revert_order") {
		t.Errorf("result must not tell the agent to revert: %s", text)
	}

	var got appie.BasketWriteResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode result %q: %v", text, err)
	}
	if len(got.Landed) != 1 || got.Landed[0] != (appie.BasketLine{ID: 1, Qty: 2}) {
		t.Errorf("landed = %+v, want [{1 2}]", got.Landed)
	}
	if len(got.Rejected) != 1 || got.Rejected[0] != 2 {
		t.Errorf("rejected = %v, want [2]", got.Rejected)
	}
	if got.TotalItems != 1 || got.TotalPrice != 3.5 {
		t.Errorf("totals = %d / %v, want 1 / 3.5", got.TotalItems, got.TotalPrice)
	}
}
