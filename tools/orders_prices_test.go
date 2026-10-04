package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	pricesSummaryPath = "/mobile-services/order/v1/summaries/active"
	pricesDetailsPath = "/mobile-services/order/v1/777/details-grouped-by-taxonomy"
)

// pricesServer serves an active order 777 with three lines (products 1, 2, 3),
// details for 1 and 2 only, and one OPEN and one CLOSED fulfillment.
// detailsOK=false makes the details endpoint fail.
func pricesServer(t *testing.T, detailsOK bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/graphql":
			body, _ := io.ReadAll(r.Body)
			closed := strings.Contains(string(body), "CLOSED")
			id, amount := 888, 42.5
			slot := map[string]any{"date": "2026-10-09", "dateDisplay": "vrijdag 9 oktober", "timeDisplay": "18:00 - 22:00"}
			if closed {
				id, amount = 555, 0
				slot = map[string]any{"date": "2026-08-06", "dateDisplay": "donderdag 6 augustus", "timeDisplay": "18:00 - 22:00"}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"orderFulfillments": map[string]any{"result": []map[string]any{{
					"orderId":              id,
					"statusCode":           map[bool]int{true: 9, false: 2}[closed],
					"statusDescription":    map[bool]string{true: "Geannuleerd", false: "Ingediend"}[closed],
					"transactionCompleted": false,
					"totalPrice":           map[string]any{"totalPrice": map[string]any{"amount": amount}},
					"delivery":             map[string]any{"status": "SUBMITTED", "slot": slot},
				}}}},
			})
		case r.URL.Path == pricesSummaryPath:
			json.NewEncoder(w).Encode(map[string]any{
				"id":    777,
				"state": "REOPENED",
				"totalPrice": map[string]any{
					"priceTotalPayable": 10.5,
					"priceDiscount":     1.25,
				},
				"orderedProducts": []map[string]any{
					{"quantity": 2, "product": map[string]any{"webshopId": 1, "title": "Kaas"}},
					{"quantity": 1, "product": map[string]any{"webshopId": 2, "title": "Melk"}},
					{"quantity": 1, "product": map[string]any{"webshopId": 3, "title": "Brood"}},
				},
			})
		case r.URL.Path == pricesDetailsPath && detailsOK:
			json.NewEncoder(w).Encode(map[string]any{
				"orderId":    777,
				"orderState": "REOPENED",
				"groupedProductsInTaxonomy": []map[string]any{{
					"taxonomyName": "Zuivel",
					"orderedProducts": []map[string]any{
						{"quantity": 2, "product": map[string]any{
							"webshopId": 1, "title": "Kaas", "priceBeforeBonus": 4.0,
							"currentPrice": 3.0, "isBonus": true, "bonusMechanism": "25% korting",
						}},
						{"quantity": 1, "product": map[string]any{
							"webshopId": 2, "title": "Melk", "priceBeforeBonus": 1.5,
						}},
					},
				}},
			})
		case strings.HasSuffix(r.URL.Path, "/details-grouped-by-taxonomy"):
			if !detailsOK && r.URL.Path == pricesDetailsPath {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"orderId": 555, "orderState": "CANCELLED"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pricesTool(t *testing.T, detailsOK bool, register func(*server.MCPServer, Deps)) *server.MCPServer {
	t.Helper()
	srv := pricesServer(t, detailsOK)
	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	register(s, orderTestDeps(client))
	return s
}

func decodeText(t *testing.T, res *mcp.CallToolResult, v any) string {
	t.Helper()
	text := res.Content[0].(mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	if err := json.Unmarshal([]byte(text), v); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	return text
}

func TestGetCartJoinsLinePrices(t *testing.T) {
	s := pricesTool(t, true, registerGetCart)
	var got struct {
		TotalItems    int              `json:"total_items"`
		TotalPrice    float64          `json:"total_price"`
		TotalDiscount float64          `json:"total_discount"`
		Items         []map[string]any `json:"items"`
	}
	text := decodeText(t, callOrderTool(t, s, "ah_get_cart", map[string]any{}), &got)

	if got.TotalItems != 3 || got.TotalPrice != 10.5 || got.TotalDiscount != 1.25 {
		t.Errorf("totals = %d/%v/%v, want 3/10.5/1.25 in %s", got.TotalItems, got.TotalPrice, got.TotalDiscount, text)
	}
	if len(got.Items) != 3 {
		t.Fatalf("items = %d, want 3: %s", len(got.Items), text)
	}
	kaas, melk, brood := got.Items[0], got.Items[1], got.Items[2]
	if kaas["price_before_bonus"] != 4.0 || kaas["current_price"] != 3.0 ||
		kaas["is_bonus"] != true || kaas["bonus_mechanism"] != "25% korting" {
		t.Errorf("bonus line = %v", kaas)
	}
	if melk["price_before_bonus"] != 1.5 || melk["is_bonus"] != false {
		t.Errorf("plain line = %v", melk)
	}
	if _, ok := melk["current_price"]; ok {
		t.Errorf("current_price must be absent when AH sends none: %v", melk)
	}
	// A line missing from the details join keeps its name and quantity only.
	if brood["name"] != "Brood" || brood["quantity"] != 1.0 {
		t.Errorf("unjoined line = %v", brood)
	}
	if _, ok := brood["price_before_bonus"]; ok {
		t.Errorf("unjoined line must not carry a price: %v", brood)
	}
}

func TestGetCartDetailsFailureKeepsLines(t *testing.T) {
	s := pricesTool(t, false, registerGetCart)
	var got struct {
		TotalPrice float64          `json:"total_price"`
		Items      []map[string]any `json:"items"`
	}
	text := decodeText(t, callOrderTool(t, s, "ah_get_cart", map[string]any{}), &got)
	if len(got.Items) != 3 || got.TotalPrice != 10.5 {
		t.Errorf("want 3 lines and total 10.5 without details, got %s", text)
	}
}

func TestGetOrderDetailsTotals(t *testing.T) {
	tests := []struct {
		name         string
		orderID      string
		wantPrice    any
		wantDiscount any
	}{
		{"active order uses summary totals", "777", 10.5, 1.25},
		{"open fulfillment total", "888", 42.5, nil},
		{"closed fulfillment total", "555", 0.0, nil},
		{"unknown order omits totals", "999", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := pricesTool(t, true, registerGetOrderDetails)
			var got map[string]any
			text := decodeText(t, callOrderTool(t, s, "ah_get_order_details", map[string]any{"order_id": tt.orderID}), &got)
			if got["total_price"] != tt.wantPrice {
				t.Errorf("total_price = %v, want %v in %s", got["total_price"], tt.wantPrice, text)
			}
			if got["total_discount"] != tt.wantDiscount {
				t.Errorf("total_discount = %v, want %v in %s", got["total_discount"], tt.wantDiscount, text)
			}
		})
	}
}

func TestOrderListsReturnISODateAndStatus(t *testing.T) {
	tests := []struct {
		tool          string
		register      func(*server.MCPServer, Deps)
		wantDate      string
		wantDisplay   string
		wantCode      float64
		wantCancelled bool
	}{
		{"ah_get_order_history", registerGetOrderHistory, "2026-10-09", "vrijdag 9 oktober", 2, false},
		{"ah_get_past_orders", registerGetPastOrders, "2026-08-06", "donderdag 6 augustus", 9, true},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			s := pricesTool(t, true, tt.register)
			var got []map[string]any
			text := decodeText(t, callOrderTool(t, s, tt.tool, map[string]any{}), &got)
			if len(got) != 1 {
				t.Fatalf("want 1 order, got %s", text)
			}
			o := got[0]
			if o["date"] != tt.wantDate || o["date_display"] != tt.wantDisplay {
				t.Errorf("date = %v / %v, want %s / %s", o["date"], o["date_display"], tt.wantDate, tt.wantDisplay)
			}
			if o["status_code"] != tt.wantCode || o["cancelled"] != tt.wantCancelled || o["delivered"] != false {
				t.Errorf("status fields = %v", o)
			}
		})
	}
}

func TestIsCancelledStatus(t *testing.T) {
	tests := []struct {
		desc, delivery string
		want           bool
	}{
		{"Geannuleerd", "", true},
		{"Annulée", "", true},
		{"", "CANCELLED", true},
		{"Geïncasseerd", "DELIVERED", false},
		{"", "", false},
	}
	for _, tt := range tests {
		if got := isCancelledStatus(tt.desc, tt.delivery); got != tt.want {
			t.Errorf("isCancelledStatus(%q, %q) = %v, want %v", tt.desc, tt.delivery, got, tt.want)
		}
	}
}
