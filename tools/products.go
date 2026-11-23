package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterProductTools registers product-related MCP tools.
func RegisterProductTools(s *server.MCPServer, deps Deps) {
	registerSearchProducts(s, deps)
	registerSearchProductsBulk(s, deps)
	registerSearchProductsFiltered(s, deps)
	registerGetProduct(s, deps)
	registerGetProductsBulk(s, deps)
	registerGetBonusOffers(s, deps)
	registerGetBonusGroupProducts(s, deps)
	registerGetLastChanceItems(s, deps)
	registerSearchStores(s, deps)
}

// --- ah_search_products ---

func registerSearchProducts(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_search_products",
		mcp.WithTitleAnnotation("Albert Heijn: Search Products"),
		mcp.WithDescription(
			"Search for Albert Heijn (Dutch supermarket) products by keyword. "+
				"AH is a Dutch supermarket so prefer Dutch search terms for best results: "+
				"e.g. 'melk' (milk), 'kaas' (cheese), 'brood' (bread), 'kip' (chicken), 'appel' (apple). "+
				"English terms also work but may return fewer results. "+
				"Returns id, title, price, bonus_price, unit, is_bonus, bonus_mechanism, is_available, is_orderable, previously_bought (omitted when false), and image_url when include_images=true. "+
				"Neither is_available nor is_orderable guarantees the item is in stock for the delivery slot. "+
				assortmentNote,
		),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query in Dutch or English, e.g. 'melk', 'cola', 'pindakaas', 'chicken'"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return (default 10, max 30)"),
		),
		mcp.WithBoolean("include_images",
			mcp.Description("Set to true to include image_url in each result (default false)"),
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

		query := req.GetString("query", "")
		if query == "" {
			return errResult("query is required"), nil
		}
		limit, err := argInt(req, "limit", 10)
		if err != nil {
			return errResult(err.Error()), nil
		}
		includeImages := req.GetBool("include_images", false)
		start := time.Now()

		cacheKey := SearchCacheKey(query, limit, includeImages)
		if cached, ok := GlobalCache.Get(cacheKey); ok {
			LogInfo("ah_search_products", "cache_hit query=%q duration=%v", query, time.Since(start))
			return mcp.NewToolResultText(string(cached)), nil
		}

		var products []appie.Product
		if err := withRetry(ctx, "ah_search_products", func() error {
			var e error
			products, e = c.SearchProducts(ctx, query, limit)
			return e
		}); err != nil {
			LogError("ah_search_products", "search failed query=%q err=%v", query, err)
			return errResult(fmt.Sprintf("Search failed: %v", err)), nil
		}

		results := make([]productItem, 0, len(products))
		for _, p := range products {
			results = append(results, newProductItem(p, includeImages))
		}
		annotateItems(ctx, c, results)
		data, err := json.Marshal(results)
		if err != nil {
			return errResult(fmt.Sprintf("marshal result: %v", err)), nil
		}
		// An empty result may be transient, so it is never cached.
		if len(results) > 0 {
			GlobalCache.Set(cacheKey, data, CacheTTLSearch)
		}
		LogInfo("ah_search_products", "query=%q results=%d duration=%v", query, len(results), time.Since(start))
		return mcp.NewToolResultText(string(data)), nil
	})
}

// --- ah_get_bonus_group_products ---

func registerGetBonusGroupProducts(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_bonus_group_products",
		mcp.WithTitleAnnotation("Albert Heijn: Bonus Group Products"),
		mcp.WithDescription(
			"Get all individual products belonging to a specific Albert Heijn bonus promotion group. "+
				"Use this to drill into a deal like '2+1 gratis kaas' or 'Alle yoghurt 25% korting'. "+
				"Get segment_id from the bonus_segment_id field in ah_get_bonus_offers results. "+
				"Returns the same fields as ah_search_products.",
		),
		mcp.WithString("segment_id",
			mcp.Required(),
			mcp.Description("Bonus segment ID from the bonus_segment_id field in ah_get_bonus_offers"),
		),
		mcp.WithBoolean("include_images",
			mcp.Description("Set to true to include image_url in each result (default false)"),
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

		segmentID := req.GetString("segment_id", "")
		if segmentID == "" {
			return errResult("segment_id is required"), nil
		}

		products, err := c.GetBonusGroupProducts(ctx, segmentID)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get bonus group products for %s: %v", segmentID, err)), nil
		}

		includeImages := req.GetBool("include_images", false)
		results := make([]productItem, 0, len(products))
		for _, p := range products {
			results = append(results, newProductItem(p, includeImages))
		}
		annotateItems(ctx, c, results)
		return jsonResult(results)
	})
}

// --- ah_get_bonus_offers ---

func registerGetBonusOffers(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_bonus_offers",
		mcp.WithTitleAnnotation("Albert Heijn: Bonus Offers"),
		mcp.WithDescription(
			"Get current Albert Heijn bonus/promotional offers. "+
				"Use this (not ah_search_products) when the user asks what is on bonus/sale/discount. "+
				"Supports optional keyword filter to find e.g. cheese on bonus: set query='kaas'. "+
				"Group deals (e.g. '2+1 gratis', 'Alle yoghurt 25% korting') have id=0 and a non-empty bonus_segment_id — "+
				"pass that to ah_get_bonus_group_products to see the individual products in the group. "+
				"Returns id, bonus_segment_id, title, original_price, bonus_price, discount_percentage, bonus_mechanism. "+
				"The bonus feed is the Dutch national one: single products outside the Belgian assortment are dropped "+
				"unless include_non_be=true (then in_be_assortment marks them). Group deals (id 0) are not checked.",
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return (default 20)"),
		),
		mcp.WithString("query",
			mcp.Description("Optional keyword filter (Dutch or English) applied client-side, e.g. 'kaas', 'vlees', 'bier'"),
		),
		mcp.WithBoolean("include_non_be",
			mcp.Description("Keep single products outside the Belgian assortment (default false)"),
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

		limit, err := argInt(req, "limit", 20)
		if err != nil {
			return errResult(err.Error()), nil
		}
		query := strings.ToLower(req.GetString("query", ""))

		// GetBonusProducts fetches all categories and fails if any one errors.
		// Fall back to spotlight (featured deals) on error so the tool always
		// returns something useful.
		products, err := c.GetBonusProducts(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Albert Heijn MCP] GetBonusProducts failed (%v), falling back to spotlight\n", err)
			products, err = c.GetSpotlightBonusProducts(ctx)
			if err != nil {
				return errResult(fmt.Sprintf("Failed to get bonus products: %v", err)), nil
			}
		}

		type item struct {
			ID                 int     `json:"id,omitempty"`
			BonusSegmentID     string  `json:"bonus_segment_id,omitempty"`
			Title              string  `json:"title"`
			OriginalPrice      float64 `json:"original_price,omitempty"`
			BonusPrice         float64 `json:"bonus_price"`
			DiscountPercentage float64 `json:"discount_percentage,omitempty"`
			BonusMechanism     string  `json:"bonus_mechanism,omitempty"`
			InBEAssortment     *bool   `json:"in_be_assortment,omitempty"`
		}
		includeNonBE := req.GetBool("include_non_be", false)
		// Client-side keyword filter when query is set.
		matched := products[:0:0]
		for _, p := range products {
			if query == "" || strings.Contains(strings.ToLower(p.Title), query) {
				matched = append(matched, p)
			}
		}
		ids := make([]int, 0, len(matched))
		for _, p := range matched {
			ids = append(ids, p.ID)
		}
		flags := lookupAssortment(ctx, c, ids)
		results := make([]item, 0)
		for _, p := range matched {
			if len(results) >= limit {
				break
			}
			in, known := flags[p.ID]
			if known && !in && !includeNonBE {
				continue
			}
			it := item{
				ID:             p.ID,
				BonusSegmentID: p.BonusSegmentID,
				Title:          p.Title,
				OriginalPrice:  p.Price.Was,
				BonusPrice:     p.Price.Now,
				BonusMechanism: p.BonusMechanism,
			}
			if known {
				it.InBEAssortment = &in
			}
			it.DiscountPercentage = discountPercentage(p.Price.Was, p.Price.Now)
			results = append(results, it)
		}
		return jsonResult(results)
	})
}

// --- ah_get_last_chance_items ---

func registerGetLastChanceItems(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_last_chance_items",
		mcp.WithTitleAnnotation("Albert Heijn: Last-Chance Items"),
		mcp.WithDescription(
			"Get last-chance / vandaag-af / clearance items from an Albert Heijn store. "+
				"Requires a store_id (use ah_search_products to find stores, or provide postal_code to find the nearest store). "+
				"Uses the dedicated bargainItems GraphQL endpoint which returns today-only markdown deals.",
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return (default 20)"),
		),
		mcp.WithNumber("store_id",
			mcp.Description("AH store ID (integer). Required to retrieve bargain items."),
		),
		mcp.WithString("postal_code",
			mcp.Description("Dutch postal code (e.g. '1234AB') to find the nearest store when store_id is not known."),
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

		limit, err := argInt(req, "limit", 20)
		if err != nil {
			return errResult(err.Error()), nil
		}
		storeID, err := argInt(req, "store_id", 0)
		if err != nil {
			return errResult(err.Error()), nil
		}

		// If store_id not provided, try to resolve from postal_code.
		if storeID == 0 {
			postalCode := req.GetString("postal_code", "")
			if postalCode == "" {
				// Try member's address as last resort.
				member, mErr := c.GetMember(ctx)
				if mErr == nil && member.Address.PostalCode != "" {
					postalCode = member.Address.PostalCode
				}
			}
			if postalCode != "" {
				stores, sErr := c.SearchStores(ctx, postalCode)
				if sErr == nil && len(stores) > 0 {
					storeID = stores[0].ID
				}
			}
		}

		if storeID == 0 {
			// NOTE: The bargainItems GraphQL endpoint is store-specific and
			// requires a store ID. Without one we cannot retrieve last-chance
			// items. Ask the user to supply store_id or postal_code.
			return errResult(
				"Cannot retrieve last-chance items without a store. " +
					"Please provide store_id or postal_code. " +
					"Example: {\"store_id\": 1234} or {\"postal_code\": \"1011AB\"}",
			), nil
		}

		bargains, err := c.GetBargains(ctx, storeID)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get bargains for store %d: %v", storeID, err)), nil
		}

		type item struct {
			ID                 int     `json:"id"`
			Title              string  `json:"title"`
			Brand              string  `json:"brand,omitempty"`
			Category           string  `json:"category,omitempty"`
			MarkdownType       string  `json:"markdown_type,omitempty"`
			DiscountPercentage float64 `json:"discount_percentage,omitempty"`
			ExpirationDate     string  `json:"expiration_date,omitempty"`
			Stock              int     `json:"stock,omitempty"`
			PriceWas           string  `json:"price_was,omitempty"`
			PriceNow           string  `json:"price_now"`
		}
		results := make([]item, 0, len(bargains))
		for i, b := range bargains {
			if i >= limit {
				break
			}
			// Parse expiration date for display
			expDate := b.ExpirationDate
			if t, err := time.Parse(time.RFC3339, expDate); err == nil {
				expDate = t.Format("2006-01-02")
			}
			results = append(results, item{
				ID:                 b.Product.ID,
				Title:              b.Product.Title,
				Brand:              b.Product.Brand,
				Category:           b.Category,
				MarkdownType:       b.MarkdownType,
				DiscountPercentage: b.MarkdownPercentage,
				ExpirationDate:     expDate,
				Stock:              b.Stock,
				PriceWas:           b.PriceWas,
				PriceNow:           b.PriceNow,
			})
		}
		return jsonResult(results)
	})
}

// productItem is the product shape returned by the search and bonus-group tools.
// is_available maps availableOnline and is_orderable maps isOrderable. Neither
// is proven to catch items that are sold out for the delivery slot.
type productItem struct {
	ID               int     `json:"id"`
	Title            string  `json:"title"`
	Price            float64 `json:"price"`
	BonusPrice       float64 `json:"bonus_price,omitempty"`
	Unit             string  `json:"unit,omitempty"`
	IsBonus          bool    `json:"is_bonus"`
	BonusMechanism   string  `json:"bonus_mechanism,omitempty"`
	IsAvailable      bool    `json:"is_available"`
	IsOrderable      bool    `json:"is_orderable"`
	PreviouslyBought bool    `json:"previously_bought,omitempty"`
	ImageURL         string  `json:"image_url,omitempty"`
	InBEAssortment   *bool   `json:"in_be_assortment,omitempty"`
}

// newProductItem builds a productItem. Image URLs are long and rarely needed,
// so they are only included when includeImages is set.
func newProductItem(p appie.Product, includeImages bool) productItem {
	it := productItem{
		ID:               p.ID,
		Title:            p.Title,
		Unit:             p.UnitSize,
		IsBonus:          p.IsBonus,
		BonusMechanism:   p.BonusMechanism,
		IsAvailable:      p.IsAvailable,
		IsOrderable:      p.IsOrderable,
		PreviouslyBought: p.IsPreviouslyBought,
	}
	it.Price, it.BonusPrice = splitPrice(p)
	if includeImages && len(p.Images) > 0 {
		it.ImageURL = p.Images[0].URL
	}
	return it
}

// discountPercentage returns the discount from was to now in percent, rounded
// to 2 decimals, or 0 when either price is unknown.
func discountPercentage(was, now float64) float64 {
	if was <= 0 || now <= 0 {
		return 0
	}
	return math.Round((1-now/was)*10000) / 100
}

// splitPrice returns the regular price and, for bonus products, the bonus price.
func splitPrice(p appie.Product) (price, bonusPrice float64) {
	if !p.IsBonus {
		return p.Price.Now, 0
	}
	if p.Price.Was == 0 || p.Price.Was <= p.Price.Now {
		// Deals like "VOOR 1.39" or "2 voor 2.99" come without a lower now
		// price: the bonus price is only in the mechanism text.
		return max(p.Price.Now, p.Price.Was), 0
	}
	return p.Price.Was, p.Price.Now
}

// bulkProduct is one entry of the ah_get_products_bulk result.
type bulkProduct struct {
	ID               int         `json:"id"`
	Title            string      `json:"title"`
	Brand            string      `json:"brand,omitempty"`
	Category         string      `json:"category,omitempty"`
	Price            float64     `json:"price"`
	BonusPrice       float64     `json:"bonus_price,omitempty"`
	UnitSize         string      `json:"unit_size,omitempty"`
	IsBonus          bool        `json:"is_bonus"`
	BonusMechanism   string      `json:"bonus_mechanism,omitempty"`
	NutriScore       string      `json:"nutri_score,omitempty"`
	IsAvailable      bool        `json:"is_available"`
	IsOrderable      bool        `json:"is_orderable"`
	PreviouslyBought bool        `json:"previously_bought,omitempty"`
	PropertyIcons    []string    `json:"property_icons,omitempty"`
	Description      string      `json:"description,omitempty"`
	ZelfToevoegen    string      `json:"zelf_toevoegen,omitempty"`
	NutritionalInfo  interface{} `json:"nutritional_info,omitempty"`
	InBEAssortment   *bool       `json:"in_be_assortment,omitempty"`
	WebOnly          bool        `json:"web_only,omitempty"`
	WebPath          string      `json:"web_path,omitempty"`
	Error            string      `json:"error,omitempty"`
}

func newBulkProduct(p *appie.Product, includeNutri bool) bulkProduct {
	pr := bulkProduct{
		ID:               p.ID,
		Title:            p.Title,
		Brand:            p.Brand,
		Category:         p.Category,
		UnitSize:         p.UnitSize,
		IsBonus:          p.IsBonus,
		BonusMechanism:   p.BonusMechanism,
		NutriScore:       p.NutriScore,
		IsAvailable:      p.IsAvailable,
		IsOrderable:      p.IsOrderable,
		PreviouslyBought: p.IsPreviouslyBought,
		PropertyIcons:    p.PropertyIcons,
		Description:      p.ShortDescription,
		ZelfToevoegen:    p.SelfAdd,
		InBEAssortment:   p.InAssortment,
		WebPath:          p.WebPath,
	}
	pr.Price, pr.BonusPrice = splitPrice(*p)
	if includeNutri && len(p.NutritionalInfo) > 0 {
		pr.NutritionalInfo = p.NutritionalInfo
	}
	return pr
}

// jsonResult marshals v as compact JSON and wraps it in a CallToolResult.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return errResult(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// refreshTokens calls RefreshIfNeeded from the auth package via the Deps closure.
func refreshTokens(ctx context.Context, deps Deps) error {
	return deps.RefreshIfNeeded(ctx)
}

// --- ah_search_products_bulk ---

func registerSearchProductsBulk(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_search_products_bulk",
		mcp.WithTitleAnnotation("Albert Heijn: Bulk Product Search"),
		mcp.WithDescription(
			"Search for multiple products in one tool call. "+
				"Pass a JSON array of search queries; results for all are returned together. "+
				"Use this instead of calling ah_search_products repeatedly — saves tool-call quota. "+
				"Max 10 queries per call. Dutch terms give best results. "+
				assortmentNote,
		),
		mcp.WithArray("queries",
			mcp.Required(),
			mcp.Description(`Search queries, e.g. ["melk", "kaas", "brood", "kip"]`),
			mcp.WithStringItems(),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max results per query (default 5, max 10)"),
		),
		mcp.WithBoolean("include_images",
			mcp.Description("Set to true to include image_url in each result (default false)"),
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

		queries, err := parseStringArray("queries", req.GetArguments()["queries"])
		if err != nil {
			return errResult(err.Error()), nil
		}
		if len(queries) == 0 {
			return errResult("queries array is empty"), nil
		}
		if len(queries) > 10 {
			queries = queries[:10]
		}

		limit, err := argInt(req, "limit", 5)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if limit > 10 {
			limit = 10
		}
		includeImages := req.GetBool("include_images", false)

		start := time.Now()

		type searchResult struct {
			Query   string        `json:"query"`
			Results []productItem `json:"results"`
			Error   string        `json:"error,omitempty"`
		}

		results := make([]searchResult, len(queries))
		for i := range results {
			results[i].Query = queries[i]
		}

		const concurrency = 3
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup

		for i, q := range queries {
			wg.Add(1)
			i, q := i, q
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				// Check cache first.
				cacheKey := SearchCacheKey(q, limit, includeImages)
				if cached, ok := GlobalCache.Get(cacheKey); ok {
					var items []productItem
					if json.Unmarshal(cached, &items) == nil {
						results[i].Results = items
						return
					}
				}

				var products []appie.Product
				if err := withRetry(ctx, "ah_search_products_bulk", func() error {
					var e error
					products, e = c.SearchProducts(ctx, q, limit)
					return e
				}); err != nil {
					results[i].Error = err.Error()
					return
				}

				items := make([]productItem, 0, len(products))
				for _, p := range products {
					items = append(items, newProductItem(p, includeImages))
				}
				annotateItems(ctx, c, items)
				results[i].Results = items

				// Store in cache so single-search calls also benefit. An empty
				// result may be transient, so it is never cached.
				if len(items) == 0 {
					return
				}
				if data, err := json.Marshal(items); err == nil {
					GlobalCache.Set(cacheKey, data, CacheTTLSearch)
				}
			}()
		}
		wg.Wait()

		LogInfo("ah_search_products_bulk", "queries=%d duration=%v", len(queries), time.Since(start))
		return jsonResult(results)
	})
}

// --- ah_get_products_bulk ---

func registerGetProductsBulk(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_products_bulk",
		mcp.WithTitleAnnotation("Albert Heijn: Bulk Product Details"),
		mcp.WithDescription(
			"Get details for multiple products by ID in one tool call. "+
				"Use instead of calling ah_get_product repeatedly — saves tool-call quota. "+
				"Optionally include nutritional info (calories, fat, protein, etc.) for all products. "+
				"Returns the same availability flags and property_icons as ah_get_product, plus description "+
				"and zelf_toevoegen (a meal kit's 'Zelf toevoegen' text: the extras to buy). "+
				"BE-only ids (wiNNNNNN on ah.be, absent from the Dutch REST catalog) resolve through the gql gateway "+
				"with web_only true, web_path, title, unit_size, category and BE price, but no images, nutrition or bonus. "+
				assortmentNote+
				"Max 20 product IDs per call.",
		),
		mcp.WithArray("product_ids",
			mcp.Required(),
			mcp.Description("Product IDs, e.g. [123456, 789012, 345678]"),
			mcp.Items(productIDsSchema),
		),
		mcp.WithBoolean("include_nutritional_info",
			mcp.Description("Set to true to include nutritional values for all products (default false)"),
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

		productIDs, err := parseIntArray("product_ids", req.GetArguments()["product_ids"])
		if err != nil {
			return errResult(err.Error()), nil
		}
		if len(productIDs) == 0 {
			return errResult("product_ids array is empty"), nil
		}
		if len(productIDs) > 20 {
			productIDs = productIDs[:20]
		}

		includeNutri := req.GetBool("include_nutritional_info", false)
		start := time.Now()

		cacheKey := func(pid int) string {
			if includeNutri {
				return BulkProductFullCacheKey(pid)
			}
			return BulkProductCacheKey(pid)
		}

		// misses holds the indexes of results that are not served from cache.
		results := make([]bulkProduct, len(productIDs))
		var misses []int
		for i, pid := range productIDs {
			results[i].ID = pid
			if cached, ok := GlobalCache.Get(cacheKey(pid)); ok {
				var pr bulkProduct
				if json.Unmarshal(cached, &pr) == nil {
					results[i] = pr
					continue
				}
			}
			misses = append(misses, i)
		}

		// Nutrition needs a GraphQL call per product, so only the plain path
		// batches. Ids the batch leaves out stay in misses for the per-id fetch.
		if !includeNutri && len(misses) > 0 {
			ids := make([]int, len(misses))
			for j, i := range misses {
				ids[j] = productIDs[i]
			}
			found := getProductsBatched(ctx, c, ids)
			remaining := misses[:0]
			for _, i := range misses {
				p, ok := found[productIDs[i]]
				if !ok {
					remaining = append(remaining, i)
					continue
				}
				results[i] = newBulkProduct(&p, false)
				if data, err := json.Marshal(results[i]); err == nil {
					GlobalCache.Set(cacheKey(p.ID), data, CacheTTLProduct)
				}
			}
			misses = remaining
		}

		const concurrency = 5
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup

		for _, i := range misses {
			wg.Add(1)
			pid := productIDs[i]
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				var p *appie.Product
				var webOnly bool
				if err := withRetry(ctx, "ah_get_products_bulk", func() error {
					var e error
					p, webOnly, e = getProductOrGQL(ctx, c, pid, includeNutri)
					return e
				}); err != nil {
					results[i].Error = err.Error()
					return
				}

				pr := newBulkProduct(p, includeNutri)
				pr.WebOnly = webOnly
				results[i] = pr

				if data, err := json.Marshal(pr); err == nil {
					GlobalCache.Set(cacheKey(pid), data, CacheTTLProduct)
				}
			}()
		}
		wg.Wait()

		// Cached entries keep their flag; fill it for the rest.
		var unflagged []int
		for _, r := range results {
			if r.Error == "" && r.InBEAssortment == nil {
				unflagged = append(unflagged, r.ID)
			}
		}
		if len(unflagged) > 0 {
			flags := lookupAssortment(ctx, c, unflagged)
			for i := range results {
				if in, ok := flags[results[i].ID]; ok && results[i].InBEAssortment == nil {
					results[i].InBEAssortment = &in
				}
			}
		}

		LogInfo("ah_get_products_bulk", "ids=%d nutri=%v duration=%v", len(productIDs), includeNutri, time.Since(start))
		return jsonResult(results)
	})
}

// productBatchSize caps the ids per GetProductsByIDs call to keep the URL short.
const productBatchSize = 30

// getProductsBatched fetches ids in chunks with GetProductsByIDs and returns the
// products by id. The endpoint silently leaves out unknown ids (such as BE
// web-only products), and a failed chunk is skipped, so callers must fall back
// to per-id fetches for any id not in the map.
func getProductsBatched(ctx context.Context, c *appie.Client, ids []int) map[int]appie.Product {
	found := make(map[int]appie.Product, len(ids))
	for chunk := range slices.Chunk(ids, productBatchSize) {
		var products []appie.Product
		if err := withRetry(ctx, "ah_get_products_bulk", func() error {
			var e error
			products, e = c.GetProductsByIDs(ctx, chunk)
			return e
		}); err != nil {
			LogWarn("ah_get_products_bulk", "batch of %d ids failed, falling back per id: %v", len(chunk), err)
			continue
		}
		for _, p := range products {
			found[p.ID] = p
		}
	}
	return found
}

// --- ah_search_stores ---

func registerSearchStores(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_search_stores",
		mcp.WithTitleAnnotation("Albert Heijn: Search Stores"),
		mcp.WithDescription(
			"Find Albert Heijn stores near a Dutch postal code. "+
				"If no postal_code is given, automatically uses the address from the member profile. "+
				"Returns store id (use this for ah_get_last_chance_items), name, type, and address.",
		),
		mcp.WithString("postal_code",
			mcp.Description("Dutch postal code, e.g. '1234AB'. Optional — falls back to member address."),
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

		postalCode := req.GetString("postal_code", "")

		// Auto-resolve from member profile when not provided.
		if postalCode == "" {
			member, mErr := c.GetMember(ctx)
			if mErr != nil {
				return errResult("No postal_code provided and could not fetch member address: " + mErr.Error()), nil
			}
			postalCode = member.Address.PostalCode
			if postalCode == "" {
				return errResult("No postal_code provided and member profile has no address on file."), nil
			}
		}

		stores, err := c.SearchStores(ctx, postalCode)
		if err != nil {
			return errResult(fmt.Sprintf("Store search failed for %s: %v", postalCode, err)), nil
		}
		if len(stores) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No AH stores found near %s.", postalCode)), nil
		}

		type storeEntry struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Type       string `json:"type,omitempty"`
			Street     string `json:"street,omitempty"`
			City       string `json:"city,omitempty"`
			PostalCode string `json:"postal_code,omitempty"`
		}
		results := make([]storeEntry, 0, len(stores))
		for _, st := range stores {
			results = append(results, storeEntry{
				ID:         st.ID,
				Name:       st.Name,
				Type:       st.StoreType,
				Street:     fmt.Sprintf("%s %s", st.Address.Street, st.Address.HouseNumber),
				City:       st.Address.City,
				PostalCode: st.Address.PostalCode,
			})
		}
		return jsonResult(results)
	})
}

// --- ah_get_product ---

func registerGetProduct(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_product",
		mcp.WithTitleAnnotation("Albert Heijn: Product Details"),
		mcp.WithDescription(
			"Get detailed information about a single Albert Heijn product by ID. "+
				"Returns title, brand, category, description, price, unit size, bonus info, NutriScore, property icons, is_available, is_orderable, previously_bought. "+
				"Set include_nutritional_info=true to also return calories, fat, protein, etc. "+
				"A BE-only id (wiNNNNNN on ah.be) resolves through the gql gateway with web_only true and web_path. "+
				assortmentNote+
				"Get product_id from ah_search_products.",
		),
		mcp.WithNumber("product_id",
			mcp.Required(),
			mcp.Description("Numeric product ID from ah_search_products"),
		),
		mcp.WithBoolean("include_nutritional_info",
			mcp.Description("Set to true to include nutritional values (default false)"),
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
		includeNutri := req.GetBool("include_nutritional_info", false)
		start := time.Now()

		var cacheKey string
		if includeNutri {
			cacheKey = ProductFullCacheKey(productID)
		} else {
			cacheKey = ProductCacheKey(productID)
		}
		if cached, ok := GlobalCache.Get(cacheKey); ok {
			LogInfo("ah_get_product", "cache_hit id=%d duration=%v", productID, time.Since(start))
			return mcp.NewToolResultText(string(cached)), nil
		}

		var p *appie.Product
		var webOnly bool
		if err := withRetry(ctx, "ah_get_product", func() error {
			var e error
			p, webOnly, e = getProductOrGQL(ctx, c, productID, includeNutri)
			return e
		}); err != nil {
			LogError("ah_get_product", "failed id=%d err=%v", productID, err)
			return errResult(fmt.Sprintf("Failed to get product %d: %v", productID, err)), nil
		}

		type result struct {
			ID                   int         `json:"id"`
			Title                string      `json:"title"`
			Brand                string      `json:"brand,omitempty"`
			Category             string      `json:"category,omitempty"`
			ShortDescription     string      `json:"short_description,omitempty"`
			Price                float64     `json:"price"`
			BonusPrice           float64     `json:"bonus_price,omitempty"`
			UnitSize             string      `json:"unit_size,omitempty"`
			UnitPriceDescription string      `json:"unit_price_description,omitempty"`
			IsBonus              bool        `json:"is_bonus"`
			BonusMechanism       string      `json:"bonus_mechanism,omitempty"`
			NutriScore           string      `json:"nutri_score,omitempty"`
			IsAvailable          bool        `json:"is_available"`
			IsOrderable          bool        `json:"is_orderable"`
			PreviouslyBought     bool        `json:"previously_bought,omitempty"`
			PropertyIcons        []string    `json:"property_icons,omitempty"`
			ZelfToevoegen        string      `json:"zelf_toevoegen,omitempty"`
			NutritionalInfo      interface{} `json:"nutritional_info,omitempty"`
			ImageURL             string      `json:"image_url,omitempty"`
			InBEAssortment       *bool       `json:"in_be_assortment,omitempty"`
			WebOnly              bool        `json:"web_only,omitempty"`
			WebPath              string      `json:"web_path,omitempty"`
		}
		r := result{
			ID:                   p.ID,
			Title:                p.Title,
			Brand:                p.Brand,
			Category:             p.Category,
			ShortDescription:     p.ShortDescription,
			IsBonus:              p.IsBonus,
			BonusMechanism:       p.BonusMechanism,
			NutriScore:           p.NutriScore,
			IsAvailable:          p.IsAvailable,
			IsOrderable:          p.IsOrderable,
			PreviouslyBought:     p.IsPreviouslyBought,
			UnitSize:             p.UnitSize,
			UnitPriceDescription: p.UnitPriceDescription,
			PropertyIcons:        p.PropertyIcons,
			ZelfToevoegen:        p.SelfAdd,
			InBEAssortment:       p.InAssortment,
			WebOnly:              webOnly,
			WebPath:              p.WebPath,
		}
		if r.InBEAssortment == nil {
			if in, ok := lookupAssortment(ctx, c, []int{p.ID})[p.ID]; ok {
				r.InBEAssortment = &in
			}
		}
		r.Price, r.BonusPrice = splitPrice(*p)
		if len(p.Images) > 0 {
			r.ImageURL = p.Images[0].URL
		}
		if includeNutri && len(p.NutritionalInfo) > 0 {
			r.NutritionalInfo = p.NutritionalInfo
		}
		data, err := json.Marshal(r)
		if err != nil {
			return errResult(fmt.Sprintf("marshal result: %v", err)), nil
		}
		GlobalCache.Set(cacheKey, data, CacheTTLProduct)
		LogInfo("ah_get_product", "id=%d nutri=%v duration=%v", productID, includeNutri, time.Since(start))
		return mcp.NewToolResultText(string(data)), nil
	})
}

// --- ah_search_products_filtered ---

func registerSearchProductsFiltered(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_search_products_filtered",
		mcp.WithTitleAnnotation("Albert Heijn: Search Products (Filtered)"),
		mcp.WithDescription(
			"Search Albert Heijn products with optional bonus filter. "+
				"Set bonus=true to return only products currently on promotion/sale. "+
				"Dutch search terms give best results: 'melk', 'kaas', 'vlees', etc.",
		),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query in Dutch or English"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results (default 10, max 30)"),
		),
		mcp.WithBoolean("bonus",
			mcp.Description("Set to true to return only products currently on bonus/promotion"),
		),
		mcp.WithBoolean("include_images",
			mcp.Description("Set to true to include image_url in each result (default false)"),
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

		query := req.GetString("query", "")
		if query == "" {
			return errResult("query is required"), nil
		}
		limit, err := argInt(req, "limit", 10)
		if err != nil {
			return errResult(err.Error()), nil
		}
		bonus := req.GetBool("bonus", false)
		includeImages := req.GetBool("include_images", false)

		products, err := c.SearchProductsFiltered(ctx, appie.SearchOptions{
			Query: query,
			Limit: limit,
			Bonus: bonus,
		})
		if err != nil {
			return errResult(fmt.Sprintf("Search failed: %v", err)), nil
		}

		results := make([]productItem, 0, len(products))
		for _, p := range products {
			results = append(results, newProductItem(p, includeImages))
		}
		annotateItems(ctx, c, results)
		return jsonResult(results)
	})
}
