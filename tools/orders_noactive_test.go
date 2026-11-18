package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const noActiveOrderBody = `{"message":"Order does not exist: Order should not be null"}`

// noActiveOrderServer answers the active-order summary with AH's "no active
// order" error until a reopen mutation arrives, then with a one-line order.
func noActiveOrderServer(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var (
		mu       sync.Mutex
		reopened string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/graphql" {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "orderReopen") {
				var gql struct {
					Variables struct {
						ID json.Number `json:"id"`
					} `json:"variables"`
				}
				json.Unmarshal(body, &gql)
				reopened = gql.Variables.ID.String()
				json.NewEncoder(w).Encode(map[string]any{
					"data": map[string]any{"orderReopen": map[string]any{"status": "SUCCESS"}},
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"basketItemsUpdate": map[string]any{"__typename": "Basket"}},
			})
			return
		}
		if reopened == "" {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, noActiveOrderBody)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id":         777,
			"state":      "REOPENED",
			"totalPrice": map[string]any{"priceTotalPayable": 3.5},
			"orderedProducts": []map[string]any{
				{"quantity": 2, "product": map[string]any{"webshopId": 1}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, func() string {
		mu.Lock()
		defer mu.Unlock()
		return reopened
	}
}

func orderTestDeps(client *appie.Client) Deps {
	return Deps{
		IsAuthenticated: func() bool { return true },
		RefreshIfNeeded: func(context.Context) error { return nil },
		GetClient:       func() (*appie.Client, error) { return client, nil },
	}
}

func callOrderTool(t *testing.T, s *server.MCPServer, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	res, err := s.GetTool(name).Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("%s handler: %v", name, err)
	}
	return res
}

func TestGetCartNoActiveOrder(t *testing.T) {
	srv, _ := noActiveOrderServer(t)
	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	registerGetCart(s, orderTestDeps(client))

	res := callOrderTool(t, s, "ah_get_cart", map[string]any{})
	text := res.Content[0].(mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("want a NONE state, got tool error: %s", text)
	}
	var got struct {
		State string `json:"state"`
		Items []any  `json:"items"`
		Hint  string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	if got.State != "NONE" || got.Items == nil || len(got.Items) != 0 {
		t.Errorf("state=%q items=%v, want NONE and []", got.State, got.Items)
	}
	if !strings.Contains(got.Hint, "ah_reopen_order") {
		t.Errorf("hint = %q, want it to point at ah_reopen_order", got.Hint)
	}
}

func TestUpdateOrderItemsReopensWhenNoActiveOrder(t *testing.T) {
	srv, reopened := noActiveOrderServer(t)
	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	registerUpdateOrderItems(s, orderTestDeps(client))

	res := callOrderTool(t, s, "ah_update_order_items", map[string]any{
		"order_id": "777",
		"items":    []any{map[string]any{"product_id": float64(1), "quantity": float64(2)}},
	})
	text := res.Content[0].(mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	if got := reopened(); got != "777" {
		t.Fatalf("reopened order = %q, want 777", got)
	}
	var got struct {
		appie.BasketWriteResult
		ReopenedOrderID int `json:"reopened_order_id"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	if got.ReopenedOrderID != 777 {
		t.Errorf("reopened_order_id = %d, want 777 in %s", got.ReopenedOrderID, text)
	}
	if len(got.Landed) != 1 || got.Landed[0] != (appie.BasketLine{ID: 1, Qty: 2}) {
		t.Errorf("landed = %+v, want [{1 2}]", got.Landed)
	}
}

func TestUpdateOrderItemsNoActiveOrderWithoutOrderID(t *testing.T) {
	srv, reopened := noActiveOrderServer(t)
	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	registerUpdateOrderItems(s, orderTestDeps(client))

	res := callOrderTool(t, s, "ah_update_order_items", map[string]any{
		"items": []any{map[string]any{"product_id": float64(1), "quantity": float64(2)}},
	})
	text := res.Content[0].(mcp.TextContent).Text
	if !res.IsError {
		t.Fatalf("want a tool error, got %s", text)
	}
	if reopened() != "" {
		t.Errorf("must not reopen without order_id")
	}
	if !strings.Contains(text, "ah_reopen_order") || !strings.Contains(text, "order_id") {
		t.Errorf("error = %q, want a hint to reopen or pass order_id", text)
	}
}

// otherOrderServer has order 888 active. A reopen of order 777 makes 777
// active unless reopenFails is set. It records every basket mutation.
func otherOrderServer(t *testing.T, reopenFails bool) (srv *httptest.Server, mutations func() []string) {
	t.Helper()
	var (
		mu      sync.Mutex
		active  = 888
		written []string
	)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/graphql" {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "orderReopen") {
				status := "SUCCESS"
				if reopenFails {
					status = "FAILED"
				} else {
					active = 777
				}
				json.NewEncoder(w).Encode(map[string]any{
					"data": map[string]any{"orderReopen": map[string]any{"status": status, "errorMessage": "too late"}},
				})
				return
			}
			// The sold-out check after a write is a read, not a mutation.
			if strings.Contains(string(body), "basketItemsUpdate") {
				written = append(written, r.Header.Get("appie-current-order-id"))
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"basketItemsUpdate": map[string]any{"__typename": "Basket"}},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id":    active,
			"state": "REOPENED",
			"orderedProducts": []map[string]any{
				{"quantity": 2, "product": map[string]any{"webshopId": 1}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), written...)
	}
}

func TestUpdateOrderItemsReopensWhenOtherOrderActive(t *testing.T) {
	srv, mutations := otherOrderServer(t, false)
	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	registerUpdateOrderItems(s, orderTestDeps(client))

	res := callOrderTool(t, s, "ah_update_order_items", map[string]any{
		"order_id": float64(777),
		"items":    []any{map[string]any{"product_id": float64(1), "quantity": float64(2)}},
	})
	text := res.Content[0].(mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	if got := mutations(); len(got) != 1 || got[0] != "777" {
		t.Errorf("mutations went to orders %v, want only 777", got)
	}
	var got struct {
		OrderID         int `json:"order_id"`
		ReopenedOrderID int `json:"reopened_order_id"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	if got.OrderID != 777 || got.ReopenedOrderID != 777 {
		t.Errorf("order_id=%d reopened_order_id=%d, want 777/777 in %s", got.OrderID, got.ReopenedOrderID, text)
	}
}

func TestUpdateOrderItemsOtherOrderActiveReopenFails(t *testing.T) {
	srv, mutations := otherOrderServer(t, true)
	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	registerUpdateOrderItems(s, orderTestDeps(client))

	res := callOrderTool(t, s, "ah_update_order_items", map[string]any{
		"order_id": float64(777),
		"items":    []any{map[string]any{"product_id": float64(1), "quantity": float64(2)}},
	})
	text := res.Content[0].(mcp.TextContent).Text
	if !res.IsError {
		t.Fatalf("want a tool error, got %s", text)
	}
	if got := mutations(); len(got) != 0 {
		t.Errorf("nothing may be written when the reopen fails, got mutations for %v", got)
	}
	if !strings.Contains(text, "777") || !strings.Contains(text, "888") {
		t.Errorf("error = %q, want it to name both orders", text)
	}
}

func TestUpdateOrderItemsRejectsNegativeQuantity(t *testing.T) {
	srv, mutations := otherOrderServer(t, false)
	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	registerUpdateOrderItems(s, orderTestDeps(client))

	res := callOrderTool(t, s, "ah_update_order_items", map[string]any{
		"items": []any{map[string]any{"product_id": float64(1), "quantity": float64(-1)}},
	})
	if !res.IsError {
		t.Fatalf("want a tool error for qty<0, got %s", res.Content[0].(mcp.TextContent).Text)
	}
	if len(mutations()) != 0 {
		t.Error("nothing may be written for qty<0")
	}
}

func TestShoppingListToOrderNoActiveOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mobile-services/shoppinglist/v2/items" {
			json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"quantity": 1, "productDetails": map[string]any{"product": map[string]any{"webshopId": 1}}},
			}})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, noActiveOrderBody)
	}))
	t.Cleanup(srv.Close)
	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	registerShoppingListToOrder(s, orderTestDeps(client))

	res := callOrderTool(t, s, "ah_shopping_list_to_order", map[string]any{})
	text := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, noActiveOrderHint) {
		t.Errorf("result = %q, want the no-active-order hint", text)
	}
}

func TestShoppingListToOrderReadBackFailureIsNotNoActiveOrder(t *testing.T) {
	var (
		mu      sync.Mutex
		written bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/mobile-services/shoppinglist/v2/items":
			json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"quantity": 1, "productDetails": map[string]any{"product": map[string]any{"webshopId": 1}}},
			}})
		case r.URL.Path == "/graphql":
			written = true
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"basketItemsUpdate": map[string]any{"__typename": "Basket"}},
			})
		case written:
			// The reopen lapsed between the write and the read-back.
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, noActiveOrderBody)
		default:
			json.NewEncoder(w).Encode(map[string]any{"id": 777, "state": "REOPENED"})
		}
	}))
	t.Cleanup(srv.Close)
	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	registerShoppingListToOrder(s, orderTestDeps(client))

	res := callOrderTool(t, s, "ah_shopping_list_to_order", map[string]any{})
	text := res.Content[0].(mcp.TextContent).Text
	if !written {
		t.Fatal("the write was not sent")
	}
	if !res.IsError || strings.Contains(text, noActiveOrderHint) || !strings.Contains(text, "items were sent") {
		t.Errorf("result = %q, want the read-back error, not the no-active-order hint", text)
	}
}
