package tools

import (
	"context"
	"fmt"
	"strings"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterBasketTools registers shopping-list MCP tools.
func RegisterBasketTools(s *server.MCPServer, deps Deps) {
	registerGetShoppingList(s, deps)
	registerAddToShoppingList(s, deps)
	registerAddFreeTextToShoppingList(s, deps)
	registerRemoveFromShoppingList(s, deps)
	registerCheckShoppingListItem(s, deps)
	registerClearShoppingList(s, deps)
	registerShoppingListToOrder(s, deps)
	registerGetFavoriteLists(s, deps)
	registerAddToFavoriteList(s, deps)
	registerRemoveFromFavoriteList(s, deps)
}

// --- ah_get_shopping_list ---

func registerGetShoppingList(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_shopping_list",
		mcp.WithTitleAnnotation("Albert Heijn: View Shopping List"),
		mcp.WithDescription("Get the contents of your Albert Heijn shopping list. Returns item names, quantities, and product IDs."),
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

		// Read from the v2 shopping list endpoint — the same place AddToShoppingList
		// writes to. The v3 /lists endpoint returns "Mijn Lijstjes" (saved favorite
		// lists), which is a separate feature in the AH app and unrelated to the
		// main Boodschappenlijst.
		type v2Product struct {
			WebshopID int    `json:"webshopId"`
			Title     string `json:"title"`
		}
		type v2ProductDetails struct {
			Product v2Product `json:"product"`
		}
		type v2Item struct {
			ListItemID     int              `json:"listItemId"`
			Quantity       int              `json:"quantity"`
			Description    string           `json:"description"` // free-text lines have no product
			StrikedThrough bool             `json:"strikedthrough"`
			Position       int              `json:"position"`
			ProductDetails v2ProductDetails `json:"productDetails"`
		}
		type v2Response struct {
			Items []v2Item `json:"items"`
		}
		var raw v2Response
		if err := c.DoRequest(ctx, "GET", "/mobile-services/shoppinglist/v2/items", nil, &raw); err != nil {
			return errResult(fmt.Sprintf("Failed to get shopping list: %v", err)), nil
		}

		if len(raw.Items) == 0 {
			return mcp.NewToolResultText("Your shopping list is empty."), nil
		}

		type entry struct {
			Position  int    `json:"position"`
			ItemID    int    `json:"item_id,omitempty"`
			Name      string `json:"name"`
			ProductID int    `json:"product_id,omitempty"`
			Quantity  int    `json:"quantity"`
			Checked   bool   `json:"checked,omitempty"`
		}
		entries := make([]entry, 0, len(raw.Items))
		for _, item := range raw.Items {
			name := item.ProductDetails.Product.Title
			if name == "" {
				name = item.Description
			}
			pid := item.ProductDetails.Product.WebshopID
			entries = append(entries, entry{
				Position:  item.Position,
				ItemID:    item.ListItemID,
				Name:      name,
				ProductID: pid,
				Quantity:  item.Quantity,
				Checked:   item.StrikedThrough,
			})
		}
		return jsonResult(entries)
	})
}

// --- ah_add_to_shopping_list ---

func registerAddToShoppingList(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_add_to_shopping_list",
		mcp.WithTitleAnnotation("Albert Heijn: Add to Shopping List"),
		mcp.WithDescription(
			"Add one or more products to your Albert Heijn shopping list. "+
				"Pass an array of items, each with product_id (int) and quantity (int). "+
				"Returns confirmation listing the names of successfully added products.",
		),
		mcp.WithArray("items",
			mcp.Required(),
			mcp.Description(`Items to add. Each item needs product_id (int) and quantity (int > 0, default 1). Example: [{"product_id": 123456, "quantity": 2}]`),
			mcp.Items(itemSchema("product_id")),
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

		inputItems, err := parseItems("items", req.GetArguments()["items"], 1)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if len(inputItems) == 0 {
			return errResult("items array is empty"), nil
		}
		for i, it := range inputItems {
			if it.Quantity == 0 {
				return errResult(fmt.Sprintf("items[%d].quantity must be > 0", i)), nil
			}
		}

		listItems := make([]appie.ListItem, 0, len(inputItems))
		for _, it := range inputItems {
			listItems = append(listItems, appie.ListItem{
				ProductID: it.ProductID,
				Quantity:  it.Quantity,
			})
		}

		if err := c.AddToShoppingList(ctx, listItems); err != nil {
			return errResult(fmt.Sprintf("Failed to add items: %v", err)), nil
		}

		// Fetch product names for the confirmation message.
		var pids []int
		for _, it := range inputItems {
			pids = append(pids, it.ProductID)
		}
		products, pErr := c.GetProductsByIDs(ctx, pids)
		nameMap := map[int]string{}
		if pErr == nil {
			for _, p := range products {
				nameMap[p.ID] = p.Title
			}
		}

		names := make([]string, 0, len(inputItems))
		for _, it := range inputItems {
			if n, ok := nameMap[it.ProductID]; ok {
				names = append(names, fmt.Sprintf("%s (x%d)", n, it.Quantity))
			} else {
				names = append(names, fmt.Sprintf("Product %d (x%d)", it.ProductID, it.Quantity))
			}
		}
		return mcp.NewToolResultText(fmt.Sprintf("Added to shopping list:\n- %s", strings.Join(names, "\n- "))), nil
	})
}

// --- ah_add_free_text_to_shopping_list ---

func registerAddFreeTextToShoppingList(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_add_free_text_to_shopping_list",
		mcp.WithTitleAnnotation("Albert Heijn: Add Free-Text to Shopping List"),
		mcp.WithDescription(
			"Add a free-text item to the Albert Heijn shopping list (no product ID needed). "+
				"Use for reminders like 'verse bloemen', 'any good wine', or items not found in search. "+
				"The list is read back after the add; the result says whether the item was verified.",
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Free-text item description, e.g. 'verse bloemen', 'goede rode wijn'"),
		),
		mcp.WithNumber("quantity",
			mcp.Description("Quantity (default 1)"),
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

		name := req.GetString("name", "")
		if name == "" {
			return errResult("name is required"), nil
		}
		quantity, err := argInt(req, "quantity", 1)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if quantity < 1 {
			quantity = 1
		}

		verified, err := c.AddFreeTextToShoppingList(ctx, name, quantity)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to add free-text item '%s': %v", name, err)), nil
		}
		if !verified {
			return mcp.NewToolResultText(fmt.Sprintf("NOT VERIFIED: AH accepted '%s' but the list read-back shows no new or increased free-text line with that name; treat it as not added.", name)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Added '%s' (x%d) to shopping list (verified on read-back).", name, quantity)), nil
	})
}

// --- ah_remove_from_shopping_list ---

func registerRemoveFromShoppingList(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_remove_from_shopping_list",
		mcp.WithTitleAnnotation("Albert Heijn: Remove from Shopping List"),
		mcp.WithDescription(
			"Remove one or more items from the Albert Heijn Boodschappenlijst. "+
				"For product items pass product_ids; for free-text items pass names. "+
				"Get product_ids from ah_get_shopping_list.",
		),
		mcp.WithArray("product_ids",
			mcp.Description("Product IDs to remove, e.g. [123456, 789012]. Use for product items."),
			mcp.Items(productIDsSchema),
		),
		mcp.WithArray("names",
			mcp.Description(`Free-text item names to remove, e.g. ["verse bloemen"]. Use for items without a product ID.`),
			mcp.WithStringItems(),
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

		var productIDs []int
		if raw := req.GetArguments()["product_ids"]; raw != nil {
			if productIDs, err = parseIntArray("product_ids", raw); err != nil {
				return errResult(err.Error()), nil
			}
		}
		var names []string
		if raw := req.GetArguments()["names"]; raw != nil {
			if names, err = parseStringArray("names", raw); err != nil {
				return errResult(err.Error()), nil
			}
		}

		if len(productIDs) == 0 && len(names) == 0 {
			return errResult("provide at least one product_id or name to remove"), nil
		}

		// The v2 delete is a PATCH with quantity 0, but the payload must carry the
		// same fields a normal add sends (description/searchTerm/strikeThrough) or
		// the API returns 400 "Failed to read request". RemoveFromShoppingList
		// fetches the list, matches by product id or title, and builds that shape.
		if err := c.RemoveFromShoppingList(ctx, productIDs, names); err != nil {
			return errResult(fmt.Sprintf("Failed to remove from shopping list: %v", err)), nil
		}

		var removed []string
		for _, pid := range productIDs {
			removed = append(removed, fmt.Sprintf("product %d", pid))
		}
		removed = append(removed, names...)
		return mcp.NewToolResultText(fmt.Sprintf("Removed from shopping list: %s", strings.Join(removed, ", "))), nil
	})
}

// --- ah_check_shopping_list_item ---

func registerCheckShoppingListItem(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_check_shopping_list_item",
		mcp.WithTitleAnnotation("Albert Heijn: Check Shopping List Item"),
		mcp.WithDescription(
			"Mark a shopping list item as checked (picked up) or uncheck it. "+
				"NOTE: The AH v2 Boodschappenlijst does not expose per-item IDs (listItemId is always 0), "+
				"so this tool only works for items in 'Mijn Lijstjes' (favorite lists) that have real string item IDs. "+
				"Checking items in the main shopping list must be done in the AH app directly.",
		),
		mcp.WithString("item_id",
			mcp.Required(),
			mcp.Description("Item ID (string) from a favorite list — NOT from ah_get_shopping_list (those have no usable IDs)"),
		),
		mcp.WithBoolean("checked",
			mcp.Description("true to check the item (default), false to uncheck"),
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

		itemID := req.GetString("item_id", "")
		if itemID == "" {
			return errResult("item_id is required"), nil
		}
		checked := req.GetBool("checked", true)

		if err := c.CheckShoppingListItem(ctx, itemID, checked); err != nil {
			return errResult(fmt.Sprintf("Failed to update item %s: %v", itemID, err)), nil
		}
		state := "checked"
		if !checked {
			state = "unchecked"
		}
		return mcp.NewToolResultText(fmt.Sprintf("Item %s marked as %s.", itemID, state)), nil
	})
}

// --- ah_clear_shopping_list ---

func registerClearShoppingList(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_clear_shopping_list",
		mcp.WithTitleAnnotation("Albert Heijn: Clear Shopping List"),
		mcp.WithDescription(
			"Remove ALL items from the Albert Heijn shopping list. "+
				"Irreversible — requires confirm=\"yes\".",
		),
		mcp.WithString("confirm",
			mcp.Required(),
			mcp.Description("Must be \"yes\" to confirm clearing the list"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(deps), nil
		}
		if req.GetString("confirm", "") != "yes" {
			return errResult("confirm must be \"yes\" to clear the shopping list"), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		n, left, err := c.ClearBoodschappenlijst(ctx)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to clear shopping list: %v", err)), nil
		}
		if n == 0 && left == 0 {
			return mcp.NewToolResultText("Shopping list is already empty."), nil
		}
		if left > 0 {
			return mcp.NewToolResultText(fmt.Sprintf("Shopping list NOT fully cleared: %d item(s) removed, %d still on the list after read-back.", n, left)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Shopping list cleared (%d item(s) removed).", n)), nil
	})
}

// --- ah_shopping_list_to_order ---

func registerShoppingListToOrder(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_shopping_list_to_order",
		mcp.WithTitleAnnotation("Albert Heijn: Move List to Cart"),
		mcp.WithDescription(
			"Add all unchecked product items from the Albert Heijn shopping list to the online order (cart). "+
				"List quantities are added on top of what the cart already holds. "+
				"Free-text items and struck-through items are skipped. "+
				"Returns landed, rejected (silently dropped by the delivery slot) and qty_mismatch lines from an order read-back.",
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

		res, err := c.ShoppingListToOrder(ctx)
		// A failed read-back after the write is not a missing order: the items
		// may already be in it, so the reopen hint would mislead.
		if isNoActiveOrder(err) && !strings.Contains(err.Error(), "items were sent") {
			return errResult(noActiveOrderHint), nil
		}
		if err != nil {
			return errResult(fmt.Sprintf("Failed to move shopping list to order: %v", err)), nil
		}
		if res == nil {
			return mcp.NewToolResultText("No unchecked product items on the shopping list; nothing sent to the order."), nil
		}
		// landed/rejected/qty_mismatch come from reading the order back: AH
		// accepts the mutation even when the delivery slot drops an item.
		return jsonResult(res)
	})
}

// --- ah_get_favorite_lists ---

func registerGetFavoriteLists(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_favorite_lists",
		mcp.WithTitleAnnotation("Albert Heijn: View Favourite Lists"),
		mcp.WithDescription(
			"List all Albert Heijn favorite/saved shopping lists with their names and item counts. "+
				"Use the returned list ID with ah_add_to_favorite_list or ah_remove_from_favorite_list.",
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

		lists, err := c.GetShoppingLists(ctx, 0)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get favorite lists: %v", err)), nil
		}

		type entry struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			ItemCount int    `json:"item_count"`
		}
		results := make([]entry, 0, len(lists))
		for _, l := range lists {
			results = append(results, entry{ID: l.ID, Name: l.Name, ItemCount: l.ItemCount})
		}
		return jsonResult(results)
	})
}

// --- ah_add_to_favorite_list ---

func registerAddToFavoriteList(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_add_to_favorite_list",
		mcp.WithTitleAnnotation("Albert Heijn: Add to Favourite List"),
		mcp.WithDescription(
			"Add products to a named Albert Heijn favorite list. "+
				"Get list_id from ah_get_favorite_lists.",
		),
		mcp.WithString("list_id",
			mcp.Required(),
			mcp.Description("Favorite list ID from ah_get_favorite_lists"),
		),
		mcp.WithArray("items",
			mcp.Required(),
			mcp.Description(`Items to add, quantity defaults to 1: [{"product_id": 123456, "quantity": 1}]`),
			mcp.Items(itemSchema("product_id")),
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

		listID := req.GetString("list_id", "")
		if listID == "" {
			return errResult("list_id is required"), nil
		}

		inputItems, err := parseItems("items", req.GetArguments()["items"], 1)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if len(inputItems) == 0 {
			return errResult("items array is empty"), nil
		}

		listItems := make([]appie.ListItem, 0, len(inputItems))
		for _, it := range inputItems {
			qty := max(it.Quantity, 1)
			listItems = append(listItems, appie.ListItem{ProductID: it.ProductID, Quantity: qty})
		}
		if err := c.AddToFavoriteList(ctx, listID, listItems); err != nil {
			return errResult(fmt.Sprintf("Failed to add to favorite list: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Added %d item(s) to favorite list %s.", len(listItems), listID)), nil
	})
}

// --- ah_remove_from_favorite_list ---

func registerRemoveFromFavoriteList(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_remove_from_favorite_list",
		mcp.WithTitleAnnotation("Albert Heijn: Remove from Favourite List"),
		mcp.WithDescription(
			"Remove products from a named Albert Heijn favorite list. "+
				"Get list_id from ah_get_favorite_lists.",
		),
		mcp.WithString("list_id",
			mcp.Required(),
			mcp.Description("Favorite list ID from ah_get_favorite_lists"),
		),
		mcp.WithArray("product_ids",
			mcp.Required(),
			mcp.Description("Product IDs to remove, e.g. [123456, 789012]"),
			mcp.Items(productIDsSchema),
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

		listID := req.GetString("list_id", "")
		if listID == "" {
			return errResult("list_id is required"), nil
		}

		productIDs, err := parseIntArray("product_ids", req.GetArguments()["product_ids"])
		if err != nil {
			return errResult(err.Error()), nil
		}
		if len(productIDs) == 0 {
			return errResult("product_ids array is empty"), nil
		}

		if err := c.RemoveFromFavoriteList(ctx, listID, productIDs); err != nil {
			return errResult(fmt.Sprintf("Failed to remove from favorite list: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Removed %d product(s) from favorite list %s.", len(productIDs), listID)), nil
	})
}
