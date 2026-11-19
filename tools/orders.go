package tools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Brussels must resolve on hosts without zoneinfo

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterOrderTools registers order-related MCP tools.
func RegisterOrderTools(s *server.MCPServer, deps Deps) {
	registerGetOrderHistory(s, deps)
	registerGetPastOrders(s, deps)
	registerGetOrderDetails(s, deps)
	registerGetFrequentItems(s, deps)
	registerGetReceipts(s, deps)
	registerGetReceiptDetails(s, deps)
	registerGetCart(s, deps)
	registerGetCartSummary(s, deps)
	registerUpdateCartItem(s, deps)
	registerRemoveFromCart(s, deps)
	registerClearCart(s, deps)
	registerReopenOrder(s, deps)
	registerUpdateOrderItems(s, deps)
	registerRevertOrder(s, deps)
}

// --- ah_get_order_history ---

func registerGetOrderHistory(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_order_history",
		mcp.WithTitleAnnotation("Albert Heijn: Order History"),
		mcp.WithDescription(
			"Get upcoming Albert Heijn online delivery orders (open fulfillments). "+
				"Returns id, date (ISO delivery date), date_display (Dutch text), total_price, "+
				"status (Dutch text), status_code (raw AH code), cancelled, delivered, modifiable flag, "+
				"closing_time (change deadline, RFC3339 Brussels time) and after_cutoff. "+
				"Use the returned id with ah_reopen_order to edit a submitted order before its closing time.",
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of orders to return (default 10)"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		limit, err := argInt(req, "limit", 10)
		if err != nil {
			return errResult(err.Error()), nil
		}

		fulfillments, err := c.GetFulfillments(ctx)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get order history: %v", err)), nil
		}

		type orderEntry struct {
			ID           int     `json:"id"`
			Date         string  `json:"date,omitempty"`
			DateDisplay  string  `json:"date_display,omitempty"`
			TimeWindow   string  `json:"time_window,omitempty"`
			TotalPrice   float64 `json:"total_price"`
			ItemCount    int     `json:"item_count,omitempty"`
			Status       string  `json:"status"`
			StatusCode   int     `json:"status_code"`
			Cancelled    bool    `json:"cancelled"`
			Delivered    bool    `json:"delivered"`
			ShoppingType string  `json:"shopping_type,omitempty"`
			Modifiable   bool    `json:"modifiable"`
			Reopenable   bool    `json:"reopenable"`
			ClosingTime  string  `json:"closing_time,omitempty"`
			AfterCutoff  bool    `json:"after_cutoff"`
		}
		results := make([]orderEntry, 0, len(fulfillments))
		for i, f := range fulfillments {
			if i >= limit {
				break
			}
			closing, _, _ := formatClosingTime(f.ClosingTime)
			results = append(results, orderEntry{
				ID:           f.OrderID,
				Date:         f.Delivery.Slot.Date,
				DateDisplay:  f.Delivery.Slot.DateDisplay,
				TimeWindow:   f.Delivery.Slot.TimeDisplay,
				TotalPrice:   f.TotalPrice,
				Status:       f.StatusDescription,
				StatusCode:   f.StatusCode,
				Cancelled:    isCancelledStatus(f.StatusDescription, f.Delivery.Status),
				ShoppingType: f.ShoppingType,
				Modifiable:   f.Modifiable,
				Reopenable:   f.Reopenable,
				ClosingTime:  closing,
				AfterCutoff:  f.IsAfterCutOff,
			})
		}
		return jsonResult(results)
	})
}

// --- ah_get_past_orders ---

func registerGetPastOrders(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_past_orders",
		mcp.WithTitleAnnotation("Albert Heijn: Past Orders"),
		mcp.WithDescription(
			"Get past/delivered Albert Heijn online delivery orders. "+
				"Returns id, date (ISO delivery date), date_display (Dutch text), total_price, "+
				"status (Dutch text), status_code (raw AH code), cancelled and delivered. "+
				"Use the returned id with ah_get_order_details to see full item lists.",
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of orders to return (default 10)"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		limit, err := argInt(req, "limit", 10)
		if err != nil {
			return errResult(err.Error()), nil
		}

		closed, err := getClosedFulfillments(ctx, c)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get past orders: %v", err)), nil
		}

		type orderEntry struct {
			ID           int     `json:"id"`
			Date         string  `json:"date,omitempty"`
			DateDisplay  string  `json:"date_display,omitempty"`
			TimeWindow   string  `json:"time_window,omitempty"`
			TotalPrice   float64 `json:"total_price"`
			Status       string  `json:"status"`
			StatusCode   int     `json:"status_code"`
			Cancelled    bool    `json:"cancelled"`
			Delivered    bool    `json:"delivered"`
			ShoppingType string  `json:"shopping_type,omitempty"`
		}
		results := make([]orderEntry, 0)
		for i, f := range closed {
			if i >= limit {
				break
			}
			// transactionCompleted is true on open and cancelled orders too, so a
			// closed order counts as delivered unless it was cancelled.
			cancelled := isCancelledStatus(f.StatusDescription, f.Delivery.Status)
			results = append(results, orderEntry{
				ID:           f.OrderID,
				Date:         f.Delivery.Slot.Date,
				DateDisplay:  f.Delivery.Slot.DateDisplay,
				TimeWindow:   f.Delivery.Slot.TimeDisplay,
				TotalPrice:   f.TotalPrice.TotalPrice.Amount,
				Status:       f.StatusDescription,
				StatusCode:   f.StatusCode,
				Cancelled:    cancelled,
				Delivered:    !cancelled,
				ShoppingType: f.ShoppingType,
			})
		}
		if len(results) == 0 {
			return mcp.NewToolResultText("No past orders found."), nil
		}
		return jsonResult(results)
	})
}

const closedFulfillmentsQuery = `query OrderFulfillmentsClosed {
  orderFulfillments(status: CLOSED) {
    result {
      orderId
      statusCode
      statusDescription
      shoppingType
      transactionCompleted
      totalPrice {
        totalPrice { amount }
      }
      delivery {
        status
        slot {
          date
          dateDisplay
          timeDisplay
        }
      }
    }
  }
}`

type closedFulfillment struct {
	OrderID              int    `json:"orderId"`
	StatusCode           int    `json:"statusCode"`
	StatusDescription    string `json:"statusDescription"`
	ShoppingType         string `json:"shoppingType"`
	TransactionCompleted bool   `json:"transactionCompleted"`
	TotalPrice           struct {
		TotalPrice struct {
			Amount float64 `json:"amount"`
		} `json:"totalPrice"`
	} `json:"totalPrice"`
	Delivery struct {
		Status string             `json:"status"`
		Slot   appie.DeliverySlot `json:"slot"`
	} `json:"delivery"`
}

func getClosedFulfillments(ctx context.Context, c *appie.Client) ([]closedFulfillment, error) {
	var resp struct {
		OrderFulfillments struct {
			Result []closedFulfillment `json:"result"`
		} `json:"orderFulfillments"`
	}
	if err := c.DoGraphQL(ctx, closedFulfillmentsQuery, nil, &resp); err != nil {
		return nil, err
	}
	return resp.OrderFulfillments.Result, nil
}

// isCancelledStatus matches the status texts because AH's numeric statusCode
// values are undocumented.
func isCancelledStatus(description, deliveryStatus string) bool {
	return strings.EqualFold(deliveryStatus, "CANCELLED") ||
		strings.Contains(strings.ToLower(description), "annul")
}

// --- ah_get_frequent_items ---

func registerGetFrequentItems(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_frequent_items",
		mcp.WithTitleAnnotation("Albert Heijn: Frequently Ordered Items"),
		mcp.WithDescription(
			"Get frequently ordered products by analysing order history. "+
				"Fetches all fulfillments, expands each order's items, counts per product, "+
				"and returns products ordered at least min_order_count times. "+
				"Returns product_name, product_id, order_count, last_ordered_date.",
		),
		mcp.WithNumber("min_order_count",
			mcp.Description("Minimum number of orders a product must appear in (default 3)"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		minCount, err := argInt(req, "min_order_count", 3)
		if err != nil {
			return errResult(err.Error()), nil
		}

		// Fetch both open and past (CLOSED) fulfillments so we have a full history.
		openFulfillments, err := c.GetFulfillments(ctx)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get open fulfillments: %v", err)), nil
		}

		const closedQuery = `query OrderFulfillmentsClosed {
  orderFulfillments(status: CLOSED) {
    result {
      orderId
      delivery { slot { date } }
    }
  }
}`
		type closedResult struct {
			OrderID  int `json:"orderId"`
			Delivery struct {
				Slot struct {
					Date string `json:"date"`
				} `json:"slot"`
			} `json:"delivery"`
		}
		type closedResp struct {
			OrderFulfillments struct {
				Result []closedResult `json:"result"`
			} `json:"orderFulfillments"`
		}
		var cr closedResp
		_ = c.DoGraphQL(ctx, closedQuery, nil, &cr) // ignore error — CLOSED may not exist

		type minFulfillment struct {
			OrderID   int
			OrderDate string
		}
		var allFulfillments []minFulfillment
		for _, f := range openFulfillments {
			allFulfillments = append(allFulfillments, minFulfillment{OrderID: f.OrderID, OrderDate: f.Delivery.Slot.Date})
		}
		for _, f := range cr.OrderFulfillments.Result {
			allFulfillments = append(allFulfillments, minFulfillment{OrderID: f.OrderID, OrderDate: f.Delivery.Slot.Date})
		}

		type productStats struct {
			Name          string
			Count         int
			LastOrderDate string
		}
		stats := map[int]*productStats{}

		for _, f := range allFulfillments {
			orderDate := f.OrderDate

			order, oErr := c.GetOrderDetails(ctx, f.OrderID)
			if oErr != nil {
				fmt.Fprintf(os.Stderr, "[Albert Heijn MCP] Warning: could not fetch order %d: %v\n", f.OrderID, oErr)
				continue
			}
			seen := map[int]bool{}
			for _, item := range order.Items {
				pid := item.ProductID
				if seen[pid] {
					continue
				}
				seen[pid] = true

				name := ""
				if item.Product != nil {
					name = item.Product.Title
				}
				if name == "" {
					name = strconv.Itoa(pid)
				}

				if stats[pid] == nil {
					stats[pid] = &productStats{Name: name}
				}
				stats[pid].Count++
				// Keep latest date
				if orderDate > stats[pid].LastOrderDate {
					stats[pid].LastOrderDate = orderDate
					stats[pid].Name = name // update name in case it was empty before
				}
			}
		}

		type item struct {
			ProductName     string `json:"product_name"`
			ProductID       int    `json:"product_id"`
			OrderCount      int    `json:"order_count"`
			LastOrderedDate string `json:"last_ordered_date,omitempty"`
		}
		results := []item{}
		for pid, s := range stats {
			if s.Count >= minCount {
				lastDate := s.LastOrderDate
				if t, err := time.Parse("2006-01-02", lastDate); err == nil {
					lastDate = t.Format("2006-01-02")
				}
				results = append(results, item{
					ProductName:     s.Name,
					ProductID:       pid,
					OrderCount:      s.Count,
					LastOrderedDate: lastDate,
				})
			}
		}
		sort.Slice(results, func(i, j int) bool {
			return results[i].OrderCount > results[j].OrderCount
		})
		return jsonResult(results)
	})
}

// --- ah_get_receipts ---

func registerGetReceipts(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_receipts",
		mcp.WithTitleAnnotation("Albert Heijn: Receipts"),
		mcp.WithDescription(
			"List recent Albert Heijn in-store receipts (kassabonnen). "+
				"Returns receipt id, date, and total amount. "+
				"Use ah_get_receipt_details with the id to see individual items.",
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of receipts to return (default 10)"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		limit, err := argInt(req, "limit", 10)
		if err != nil {
			return errResult(err.Error()), nil
		}

		receipts, err := c.GetReceipts(ctx)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get receipts: %v", err)), nil
		}

		type entry struct {
			ID          string  `json:"id"`
			Date        string  `json:"date"`
			TotalAmount float64 `json:"total_amount"`
		}
		results := make([]entry, 0, len(receipts))
		for i, r := range receipts {
			if i >= limit {
				break
			}
			// Reformat ISO datetime to readable date
			date := r.Date
			if t, err := time.Parse(time.RFC3339, date); err == nil {
				date = t.Format("2006-01-02 15:04")
			}
			results = append(results, entry{
				ID:          r.TransactionID,
				Date:        date,
				TotalAmount: r.TotalAmount,
			})
		}
		return jsonResult(results)
	})
}

// --- ah_get_receipt_details ---

func registerGetReceiptDetails(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_receipt_details",
		mcp.WithTitleAnnotation("Albert Heijn: Receipt Details"),
		mcp.WithDescription(
			"Get full details of a single Albert Heijn in-store receipt (kassabon) by its id. "+
				"Returns all purchased items with name, quantity, unit price and line total, "+
				"plus any discounts and payment method. "+
				"Get the id from ah_get_receipts first.",
		),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("Receipt transaction ID from ah_get_receipts"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		id := req.GetString("id", "")
		if id == "" {
			return errResult("id is required"), nil
		}

		receipt, err := c.GetReceipt(ctx, id)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get receipt %s: %v", id, err)), nil
		}

		type itemEntry struct {
			Name      string  `json:"name"`
			Quantity  int     `json:"quantity,omitempty"`
			UnitPrice float64 `json:"unit_price,omitempty"`
			Total     float64 `json:"total"`
		}
		type discountEntry struct {
			Name   string  `json:"name"`
			Amount float64 `json:"amount"`
		}
		type paymentEntry struct {
			Method string  `json:"method"`
			Amount float64 `json:"amount"`
		}
		type result struct {
			ID        string          `json:"id"`
			Items     []itemEntry     `json:"items"`
			Discounts []discountEntry `json:"discounts,omitempty"`
			Payments  []paymentEntry  `json:"payments,omitempty"`
		}

		items := make([]itemEntry, 0, len(receipt.Items))
		for _, it := range receipt.Items {
			items = append(items, itemEntry{
				Name:      it.Description,
				Quantity:  it.Quantity,
				UnitPrice: it.UnitPrice,
				Total:     it.Amount,
			})
		}
		discounts := make([]discountEntry, 0, len(receipt.Discounts))
		for _, d := range receipt.Discounts {
			discounts = append(discounts, discountEntry{Name: d.Name, Amount: d.Amount})
		}
		payments := make([]paymentEntry, 0, len(receipt.Payments))
		for _, p := range receipt.Payments {
			payments = append(payments, paymentEntry{Method: p.Method, Amount: p.Amount})
		}

		return jsonResult(result{
			ID:        receipt.TransactionID,
			Items:     items,
			Discounts: discounts,
			Payments:  payments,
		})
	})
}

// --- ah_get_order_details ---

func registerGetOrderDetails(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_order_details",
		mcp.WithTitleAnnotation("Albert Heijn: Order Details"),
		mcp.WithDescription(
			"Get the full item list for a specific Albert Heijn delivery order by its ID. "+
				"Returns all products with names, quantities, and prices, plus "+
				"closing_time (change deadline, RFC3339 Brussels time) and after_cutoff. "+
				"total_price is the order total (from the active cart or the order lists); "+
				"total_discount is only known for the active order. Both are omitted when unknown. "+
				"Get order_id from ah_get_order_history.",
		),
		mcp.WithNumber("order_id",
			mcp.Required(),
			mcp.Description("Numeric order ID from ah_get_order_history"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		orderID, err := argInt(req, "order_id", 0)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if orderID == 0 {
			return errResult("order_id is required"), nil
		}

		order, err := c.GetOrderDetails(ctx, orderID)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get order details for %d: %v", orderID, err)), nil
		}

		type itemEntry struct {
			ProductID int     `json:"product_id"`
			Name      string  `json:"name,omitempty"`
			Quantity  int     `json:"quantity"`
			Price     float64 `json:"price,omitempty"`
		}
		type orderResult struct {
			ID            string      `json:"id"`
			State         string      `json:"state"`
			ClosingTime   string      `json:"closing_time,omitempty"`
			AfterCutoff   *bool       `json:"after_cutoff,omitempty"`
			Items         []itemEntry `json:"items"`
			TotalPrice    *float64    `json:"total_price,omitempty"`
			TotalDiscount *float64    `json:"total_discount,omitempty"`
		}

		items := make([]itemEntry, 0, len(order.Items))
		for _, it := range order.Items {
			ie := itemEntry{ProductID: it.ProductID, Quantity: it.Quantity}
			if it.Product != nil {
				ie.Name = it.Product.Title
				ie.Price = it.Product.Price.Now
			}
			items = append(items, ie)
		}
		closing, closingAt, ok := formatClosingTime(order.ClosingTime)
		var afterCutoff *bool
		if ok {
			passed := time.Now().After(closingAt)
			afterCutoff = &passed
		}
		total, discount := orderTotals(ctx, c, orderID)
		return jsonResult(orderResult{
			ID:            order.ID,
			State:         order.State,
			ClosingTime:   closing,
			AfterCutoff:   afterCutoff,
			Items:         items,
			TotalPrice:    total,
			TotalDiscount: discount,
		})
	})
}

// brussels is the AH BE store timezone, falling back to UTC if unknown.
var brussels = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// orderTotals looks up the totals that details-grouped-by-taxonomy does not
// return: the active order summary first, then the open and closed order lists.
// Nil means the order was not found.
func orderTotals(ctx context.Context, c *appie.Client, orderID int) (total, discount *float64) {
	if o, err := c.GetOrder(ctx); err == nil && o.ID == strconv.Itoa(orderID) {
		return &o.TotalPrice, &o.TotalDiscount
	}
	if open, err := c.GetFulfillments(ctx); err == nil {
		for _, f := range open {
			if f.OrderID == orderID {
				return &f.TotalPrice, nil
			}
		}
	}
	if closed, err := getClosedFulfillments(ctx, c); err == nil {
		for _, f := range closed {
			if f.OrderID == orderID {
				return &f.TotalPrice.TotalPrice.Amount, nil
			}
		}
	}
	return nil, nil
}

// formatClosingTime renders an AH closing time as RFC3339 in Brussels time.
// The API types it as a plain string, so a value without a zone is read as
// Brussels wall time and an unparseable value is passed through unchanged
// with ok false.
func formatClosingTime(raw string) (formatted string, at time.Time, ok bool) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		t = t.In(brussels)
		return t.Format(time.RFC3339), t, true
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", raw, brussels); err == nil {
		return t.Format(time.RFC3339), t, true
	}
	return raw, time.Time{}, false
}

// --- ah_get_cart ---

func registerGetCart(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_cart",
		mcp.WithTitleAnnotation("Albert Heijn: View Cart"),
		mcp.WithDescription(
			"View the current Albert Heijn online shopping cart (active order). "+
				"Returns the order state, total_items (distinct product lines), total_price, total_discount, and all items "+
				"with name, quantity, price_before_bonus, current_price (bonus price, absent when "+
				"the discount applies per group such as \"2e gratis\"), is_bonus and bonus_mechanism. "+
				"Lines without price fields could not be matched to the order details. "+
				"Lines with web_only true are BE web-only products (wiNNNNNN): the order omits them, the basket "+
				"holds them for delivery, and they count in total_price; their price_before_bonus is the unit price. "+
				"unavailable lists basket lines AH will not deliver, each with product_id, name, quantity and reason: "+
				"sold_out (\"Tijdelijk uitverkocht\", still in items) or store_only (\"Alleen in de winkel\", not in items). "+
				"It is omitted when every line is deliverable or the check failed. "+
				"Use ah_update_cart_item or ah_remove_from_cart to modify items.",
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		order, err := c.GetOrder(ctx)
		if isNoActiveOrder(err) {
			return jsonResult(map[string]any{
				"state": "NONE",
				"items": []any{},
				"hint":  noActiveOrderHint,
			})
		}
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get cart: %v", err)), nil
		}

		type cartItem struct {
			ProductID int    `json:"product_id"`
			Name      string `json:"name,omitempty"`
			Quantity  int    `json:"quantity"`
			*linePrice
			// WebOnly marks a BE web-only line: the order endpoints omit it,
			// only the gql basket lists it, and it counts in total_price.
			WebOnly bool `json:"web_only,omitempty"`
		}
		type cartResult struct {
			ID            string                    `json:"id"`
			State         string                    `json:"state"`
			TotalItems    int                       `json:"total_items"`
			Items         []cartItem                `json:"items"`
			Unavailable   []appie.BasketUnavailable `json:"unavailable,omitempty"`
			TotalPrice    float64                   `json:"total_price"`
			TotalDiscount float64                   `json:"total_discount,omitempty"`
		}
		prices := cartLinePrices(ctx, c, order.ID)
		items := make([]cartItem, 0, len(order.Items))
		for _, it := range order.Items {
			ci := cartItem{ProductID: it.ProductID, Quantity: it.Quantity, linePrice: prices[it.ProductID]}
			if it.Product != nil {
				ci.Name = it.Product.Title
			}
			items = append(items, ci)
		}
		// The order summary lists sold-out lines like any other and omits BE
		// web-only lines; only the gql basket shows both. A failed basket read
		// leaves the cart as the order reports it.
		var unavailable []appie.BasketUnavailable
		basket, err := c.GetBasket(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Albert Heijn MCP] Warning: no basket check for cart: %v\n", err)
		} else {
			unavailable = appie.UnavailableLines(basket)
			for _, b := range appie.WebOnlyLines(basket, order) {
				name := b.Title
				if b.UnitSize != "" {
					name += " " + b.UnitSize
				}
				items = append(items, cartItem{
					ProductID: b.ProductID,
					Name:      name,
					Quantity:  b.Quantity,
					linePrice: &linePrice{PriceBeforeBonus: b.Price},
					WebOnly:   true,
				})
			}
		}
		return jsonResult(cartResult{
			ID:            order.ID,
			State:         order.State,
			TotalItems:    len(items),
			Items:         items,
			Unavailable:   unavailable,
			TotalPrice:    order.TotalPrice,
			TotalDiscount: order.TotalDiscount,
		})
	})
}

const noActiveOrderHint = "No active order. Call ah_reopen_order(order_id) with a modifiable id from ah_get_order_history."

// linePrice holds the per-line prices that only the order details return.
type linePrice struct {
	PriceBeforeBonus float64  `json:"price_before_bonus"`
	CurrentPrice     *float64 `json:"current_price,omitempty"`
	IsBonus          bool     `json:"is_bonus"`
	BonusMechanism   string   `json:"bonus_mechanism,omitempty"`
}

// cartLinePrices fetches the active order's details and indexes their prices
// by product id. The summary behind the cart carries no prices. A failed
// lookup returns no prices instead of failing the cart.
func cartLinePrices(ctx context.Context, c *appie.Client, orderID string) map[int]*linePrice {
	id, err := strconv.Atoi(orderID)
	if err != nil {
		return nil
	}
	details, err := c.GetOrderDetails(ctx, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Albert Heijn MCP] Warning: no cart prices, order %d details: %v\n", id, err)
		return nil
	}
	prices := make(map[int]*linePrice, len(details.Items))
	for _, it := range details.Items {
		if it.Product == nil || prices[it.ProductID] != nil {
			continue
		}
		p := it.Product
		lp := &linePrice{PriceBeforeBonus: p.Price.Now, IsBonus: p.IsBonus, BonusMechanism: p.BonusMechanism}
		// GetOrderDetails sets Was only when AH sent a currentPrice.
		if p.Price.Was != 0 {
			now := p.Price.Now
			lp.PriceBeforeBonus, lp.CurrentPrice = p.Price.Was, &now
		}
		prices[it.ProductID] = lp
	}
	return prices
}

// isNoActiveOrder reports whether err is AH's answer when no order is
// reopened or the reopen has lapsed.
func isNoActiveOrder(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Order should not be null") || strings.Contains(msg, "Order does not exist")
}

// --- ah_get_cart_summary ---

func registerGetCartSummary(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_cart_summary",
		mcp.WithTitleAnnotation("Albert Heijn: Cart Summary"),
		mcp.WithDescription(
			"Get the Albert Heijn shopping cart totals: number of items, "+
				"total price and discount amount. No delivery cost is returned. "+
				"ah_get_cart returns the same totals plus the lines.",
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		summary, err := c.GetOrderSummary(ctx)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get cart summary: %v", err)), nil
		}
		return jsonResult(summary)
	})
}

// --- ah_update_cart_item ---

func registerUpdateCartItem(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_update_cart_item",
		mcp.WithTitleAnnotation("Albert Heijn: Update Cart Item"),
		mcp.WithDescription(
			"Set the quantity of a product in the Albert Heijn shopping cart. "+
				"Use product_id from ah_search_products or ah_get_cart. "+
				"Set quantity=0 to remove the item (or use ah_remove_from_cart).",
		),
		mcp.WithNumber("product_id",
			mcp.Required(),
			mcp.Description("Numeric product ID"),
		),
		mcp.WithNumber("quantity",
			mcp.Required(),
			mcp.Description("New quantity (0 removes the item)"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		productID, err := argInt(req, "product_id", 0)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if productID == 0 {
			return errResult("product_id is required"), nil
		}
		quantity, err := argInt(req, "quantity", -1)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if quantity < 0 {
			return errResult("quantity is required and must be >= 0"), nil
		}

		// GetOrder must be called first so the client caches the active order ID,
		// which is sent as the Appie-Current-Order-Id header on write requests.
		if _, err := c.GetOrder(ctx); err != nil {
			return errResult(fmt.Sprintf("Failed to get active order: %v", err)), nil
		}

		if err := c.UpdateOrderItem(ctx, productID, quantity); err != nil {
			return errResult(fmt.Sprintf("Failed to update cart item %d: %v", productID, err)), nil
		}
		if quantity == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("Product %d removed from cart.", productID)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Product %d quantity set to %d.", productID, quantity)), nil
	})
}

// --- ah_remove_from_cart ---

func registerRemoveFromCart(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_remove_from_cart",
		mcp.WithTitleAnnotation("Albert Heijn: Remove from Cart"),
		mcp.WithDescription(
			"Remove a single product from the Albert Heijn shopping cart. "+
				"Use product_id from ah_search_products or ah_get_cart.",
		),
		mcp.WithNumber("product_id",
			mcp.Required(),
			mcp.Description("Numeric product ID to remove"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		productID, err := argInt(req, "product_id", 0)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if productID == 0 {
			return errResult("product_id is required"), nil
		}

		// GetOrder must be called first to cache the active order ID for the header.
		if _, err := c.GetOrder(ctx); err != nil {
			return errResult(fmt.Sprintf("Failed to get active order: %v", err)), nil
		}

		if err := c.RemoveFromOrder(ctx, productID); err != nil {
			return errResult(fmt.Sprintf("Failed to remove product %d from cart: %v", productID, err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Product %d removed from cart.", productID)), nil
	})
}

// --- ah_clear_cart ---

func registerClearCart(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_clear_cart",
		mcp.WithTitleAnnotation("Albert Heijn: Clear Cart"),
		mcp.WithDescription(
			"Remove ALL items from the Albert Heijn shopping cart. "+
				"Irreversible — requires confirm=\"yes\" to prevent accidental use.",
		),
		mcp.WithString("confirm",
			mcp.Required(),
			mcp.Description(`Must be "yes" to confirm clearing the entire cart`),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if req.GetString("confirm", "") != "yes" {
			return errResult(`confirm must be "yes" to clear the cart`), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		if err := c.ClearOrder(ctx); err != nil {
			return errResult(fmt.Sprintf("Failed to clear cart: %v", err)), nil
		}
		return mcp.NewToolResultText("Shopping cart cleared."), nil
	})
}

// --- ah_reopen_order ---

func registerReopenOrder(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_reopen_order",
		mcp.WithTitleAnnotation("Albert Heijn: Reopen Order for Editing"),
		mcp.WithDescription(
			"Unlock a submitted AH delivery order so its items can be changed (REOPENED state). "+
				"Leave it REOPENED when done; the user reviews and checks out in the AH app. "+
				"While reopened, this client treats it as the active order for the session. "+
				"Do NOT call ah_revert_order to finish: it reverts to the pre-reopen order and discards edits. "+
				"Only works before the order's closing time; returns an error if too late. "+
				"Get order_id from ah_get_order_history (check modifiable=true first).",
		),
		mcp.WithNumber("order_id",
			mcp.Required(),
			mcp.Description("Numeric order ID from ah_get_order_history"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		orderID, err := argInt(req, "order_id", 0)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if orderID == 0 {
			return errResult("order_id is required"), nil
		}

		if err := c.ReopenOrder(ctx, orderID); err != nil {
			return errResult(fmt.Sprintf("Failed to reopen order %d: %v", orderID, err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Order %d is REOPENED. Use ah_update_order_items to edit. Leave it reopened for the user to check out in the app.",
			orderID,
		)), nil
	})
}

// --- ah_update_order_items ---

func registerUpdateOrderItems(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_update_order_items",
		mcp.WithTitleAnnotation("Albert Heijn: Update Order Items"),
		mcp.WithDescription(
			"Add, update, or remove items from the currently active AH order. "+
				"Set quantity=0 to remove an item. "+
				"Use after ah_reopen_order to edit a submitted delivery order. "+
				"Reads the order back and reports, for the sent product IDs only: "+
				"landed [{id,qty}], rejected [id] (sent with qty>0 but missing, e.g. not in the delivery slot assortment; a BE web-only id the basket holds counts as landed), "+
				"qty_mismatch [{id,want,got}], removed [id], "+
				"sold_out [id] (in the order at the sent qty but \"Tijdelijk uitverkocht\", so not delivered; not in landed), "+
				"availability_error when the sold-out check failed, plus order_id, total_items and total_price, "+
				"and reopened_order_id when it reopened an order. "+
				"Empty lists are omitted. "+
				"If order_id is given and it is not the active order (no order is active, or a different one is), "+
				"it reopens order_id first. The write only goes to order_id.",
		),
		mcp.WithNumber("order_id",
			mcp.Description("Optional modifiable order ID from ah_get_order_history. The write only goes to this order; it is reopened if it is not the active one."),
		),
		mcp.WithArray("items",
			mcp.Required(),
			mcp.Description(`Items to add/update/remove. Each: {"product_id": 123456, "quantity": 2} — set quantity=0 to remove.`),
			mcp.Items(itemSchema("product_id", "quantity")),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		// A malformed quantity must not be read as 0, which would remove the item.
		items, err := parseItems("items", req.GetArguments()["items"], -1)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if len(items) == 0 {
			return errResult("items array is empty"), nil
		}
		orderItems := make([]appie.OrderItem, 0, len(items))
		for _, it := range items {
			orderItems = append(orderItems, appie.OrderItem{ProductID: it.ProductID, Quantity: it.Quantity})
		}

		orderID, err := argInt(req, "order_id", 0)
		if err != nil {
			return errResult(err.Error()), nil
		}
		before, err := c.GetOrder(ctx)
		noActive := isNoActiveOrder(err)
		if err != nil && !noActive {
			return errResult(fmt.Sprintf("Failed to get active order: %v", err)), nil
		}
		if noActive && orderID == 0 {
			return errResult(noActiveOrderHint + " Or pass order_id to reopen it automatically."), nil
		}
		reopenedID := 0
		if orderID != 0 && (noActive || before.ID != strconv.Itoa(orderID)) {
			active := "no order"
			if !noActive {
				active = "order " + before.ID
			}
			if err := c.ReopenOrder(ctx, orderID); err != nil {
				return errResult(fmt.Sprintf("Reopening order %d failed, nothing written (active: %s): %v", orderID, active, err)), nil
			}
			if before, err = c.GetOrder(ctx); err != nil {
				return errResult(fmt.Sprintf("Reopened order %d but reading it failed, nothing written: %v", orderID, err)), nil
			}
			if before.ID != strconv.Itoa(orderID) {
				return errResult(fmt.Sprintf("Reopened order %d but the active order is %s, nothing written.", orderID, before.ID)), nil
			}
			reopenedID = orderID
		}

		res, err := c.AddToOrderCheckedFrom(ctx, before, orderItems)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to update order items: %v", err)), nil
		}
		return jsonResult(struct {
			*appie.BasketWriteResult
			ReopenedOrderID int `json:"reopened_order_id,omitempty"`
		}{res, reopenedID})
	})
}

// --- ah_revert_order ---

func registerRevertOrder(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_revert_order",
		mcp.WithTitleAnnotation("Albert Heijn: Abort Reopened Order (discards edits)"),
		mcp.WithDescription(
			"Revert a reopened AH delivery order to its pre-reopen submitted state; this discards all edits made since ah_reopen_order. "+
				"Only call it when the user explicitly asks to abort their changes. "+
				"Do not call it to finish editing: leave the order REOPENED for the user to check out in the app. "+
				"Also clears the client's active order state.",
		),
		mcp.WithNumber("order_id",
			mcp.Required(),
			mcp.Description("Numeric order ID — same value passed to ah_reopen_order"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		orderID, err := argInt(req, "order_id", 0)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if orderID == 0 {
			return errResult("order_id is required"), nil
		}

		if err := c.RevertOrder(ctx, orderID); err != nil {
			return errResult(fmt.Sprintf("Failed to revert order %d: %v", orderID, err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Order %d reverted to its pre-reopen state; edits since reopen were discarded.",
			orderID,
		)), nil
	})
}
