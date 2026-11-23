package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// runProductTool registers a tool against a fake AH server and runs it once.
func runProductTool(t *testing.T, h http.HandlerFunc, register func(*server.MCPServer, Deps), name string, args map[string]any) string {
	t.Helper()
	return runProductToolBE(t, h, nil, register, name, args)
}

// runProductToolBE is runProductTool with the ids in notInBE outside the
// Belgian assortment.
func runProductToolBE(t *testing.T, h http.HandlerFunc, notInBE map[int]bool, register func(*server.MCPServer, Deps), name string, args map[string]any) string {
	t.Helper()
	srv := httptest.NewServer(withAssortment(h, notInBE))
	t.Cleanup(srv.Close)

	client := appie.New(appie.WithBaseURL(srv.URL), appie.WithTokens("test", "test"))
	s := server.NewMCPServer("test", "0")
	register(s, Deps{
		IsAuthenticated: func() bool { return true },
		RefreshIfNeeded: func(context.Context) error { return nil },
		GetClient:       func() (*appie.Client, error) { return client, nil },
	})

	var req mcp.CallToolRequest
	req.Params.Arguments = args
	res, err := s.GetTool(name).Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	text := res.Content[0].(mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	return text
}

func TestGetProductsBulkBatchesMissesAndFallsBack(t *testing.T) {
	GlobalCache.Invalidate("product_bulk:")
	t.Cleanup(func() { GlobalCache.Invalidate("product_bulk:") })
	cached, _ := json.Marshal(bulkProduct{ID: 4, Title: "Cached"})
	GlobalCache.Set(BulkProductCacheKey(4), cached, CacheTTLProduct)

	var mu sync.Mutex
	var batchIDs []string
	var detailPaths []string
	h := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/mobile-services/product/search/v2/products":
			batchIDs = append(batchIDs, r.URL.Query()["ids"]...)
			// Out of input order, and id 3 (web-only) is left out.
			json.NewEncoder(w).Encode([]map[string]any{
				{"webshopId": 2, "title": "Two", "currentPrice": 2, "availableOnline": true},
				{"webshopId": 1, "title": "One", "currentPrice": 1, "availableOnline": true},
			})
		case strings.HasPrefix(r.URL.Path, "/mobile-services/product/detail/v4/fir/"):
			detailPaths = append(detailPaths, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		case r.URL.Path == "/graphql":
			// The gql gateway resolves BE-only id 3; id 5 exists nowhere.
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "product(id: 3)") {
				json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"product": map[string]any{
					"id": 3, "title": "AH Witlof", "salesUnitSize": "500 g", "webPath": "/producten/product/wi3/ah-witlof",
					"taxonomies": []map[string]any{{"id": 21611}}, "price": map[string]any{"now": map[string]any{"amount": 3.79}},
				}}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": []map[string]any{{"message": "Subgraph errors redacted"}}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}

	text := runProductTool(t, h, registerGetProductsBulk, "ah_get_products_bulk",
		map[string]any{"product_ids": []any{float64(1), float64(2), float64(3), float64(4), float64(5)}})

	var got []bulkProduct
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d results, want 5: %s", len(got), text)
	}
	want := []struct {
		id    int
		title string
	}{{1, "One"}, {2, "Two"}, {3, "AH Witlof"}, {4, "Cached"}, {5, ""}}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Title != w.title {
			t.Errorf("result %d = {%d %q}, want {%d %q}", i, got[i].ID, got[i].Title, w.id, w.title)
		}
	}
	if got[2].Error != "" || !got[2].WebOnly || got[2].WebPath == "" || got[2].Price != 3.79 ||
		got[2].InBEAssortment == nil || !*got[2].InBEAssortment {
		t.Errorf("BE-only id 3 should resolve through gql: %+v", got[2])
	}
	if got[4].Error == "" {
		t.Errorf("unknown id 5 should carry a per-item error: %+v", got[4])
	}
	if got[0].Error != "" || got[1].Error != "" || got[0].WebOnly {
		t.Errorf("batched ids should have no error and not be web-only: %+v", got[:2])
	}
	if got[0].InBEAssortment == nil || !*got[0].InBEAssortment {
		t.Errorf("batched ids should carry in_be_assortment: %+v", got[0])
	}

	slices.Sort(batchIDs)
	if !slices.Equal(batchIDs, []string{"1", "2", "3", "5"}) {
		t.Errorf("batch ids = %v, want only the cache misses [1 2 3 5]", batchIDs)
	}
	slices.Sort(detailPaths)
	if !slices.Equal(detailPaths, []string{"/mobile-services/product/detail/v4/fir/3", "/mobile-services/product/detail/v4/fir/5"}) {
		t.Errorf("detail calls = %v, want only the ids missing from the batch", detailPaths)
	}
	if _, ok := GlobalCache.Get(BulkProductCacheKey(1)); !ok {
		t.Error("batched product 1 was not cached")
	}
}

func TestGetProductsBulkFetchesMissesInOneCall(t *testing.T) {
	GlobalCache.Invalidate("product_bulk:")
	t.Cleanup(func() { GlobalCache.Invalidate("product_bulk:") })

	var mu sync.Mutex
	var sizes []int
	h := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		ids := r.URL.Query()["ids"]
		sizes = append(sizes, len(ids))
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			out = append(out, map[string]any{"webshopId": json.Number(id), "title": "P" + id})
		}
		json.NewEncoder(w).Encode(out)
	}

	ids := make([]any, 20)
	for i := range ids {
		ids[i] = float64(i + 100)
	}
	runProductTool(t, h, registerGetProductsBulk, "ah_get_products_bulk", map[string]any{"product_ids": ids})
	if !slices.Equal(sizes, []int{20}) {
		t.Errorf("batch sizes = %v, want one call of 20", sizes)
	}
}

func searchHandler(t *testing.T, products []map[string]any, calls *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mobile-services/product/search/v2" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		*calls++
		json.NewEncoder(w).Encode(map[string]any{
			"products": products,
			"page":     map[string]any{"totalElements": len(products)},
		})
	}
}

func TestSearchProductsImageURLOptIn(t *testing.T) {
	GlobalCache.Invalidate("search:")
	t.Cleanup(func() { GlobalCache.Invalidate("search:") })
	products := []map[string]any{{
		"webshopId": 1, "title": "Melk", "currentPrice": 1.19,
		"images": []map[string]any{{"url": "https://img/1.jpg"}},
	}}
	var calls int

	text := runProductTool(t, searchHandler(t, products, &calls), registerSearchProducts, "ah_search_products",
		map[string]any{"query": "melk"})
	if strings.Contains(text, "image_url") {
		t.Errorf("image_url should be omitted by default: %s", text)
	}

	text = runProductTool(t, searchHandler(t, products, &calls), registerSearchProducts, "ah_search_products",
		map[string]any{"query": "melk", "include_images": true})
	if !strings.Contains(text, `"image_url":"https://img/1.jpg"`) {
		t.Errorf("image_url missing with include_images=true: %s", text)
	}
	if calls != 2 {
		t.Errorf("search calls = %d, want 2 (image and no-image results cached apart)", calls)
	}
}

func TestSearchProductsDoesNotCacheEmpty(t *testing.T) {
	GlobalCache.Invalidate("search:")
	t.Cleanup(func() { GlobalCache.Invalidate("search:") })
	var calls int
	h := searchHandler(t, nil, &calls)

	runProductTool(t, h, registerSearchProducts, "ah_search_products", map[string]any{"query": "nori"})
	runProductTool(t, h, registerSearchProducts, "ah_search_products", map[string]any{"query": "nori"})
	runProductTool(t, h, registerSearchProductsBulk, "ah_search_products_bulk", map[string]any{"queries": []any{"nori"}})
	runProductTool(t, h, registerSearchProductsBulk, "ah_search_products_bulk", map[string]any{"queries": []any{"nori"}})
	if calls != 4 {
		t.Errorf("search calls = %d, want 4 (empty results must not be cached)", calls)
	}
}

func TestDiscountPercentageRounded(t *testing.T) {
	if got := discountPercentage(3, 2); got != 33.33 {
		t.Errorf("discountPercentage(3, 2) = %v, want 33.33", got)
	}
	if got := discountPercentage(0, 2); got != 0 {
		t.Errorf("discountPercentage(0, 2) = %v, want 0", got)
	}
}

func TestJSONResultIsCompact(t *testing.T) {
	res, _ := jsonResult([]int{1, 2})
	if got := res.Content[0].(mcp.TextContent).Text; got != "[1,2]" {
		t.Errorf("jsonResult = %q, want compact [1,2]", got)
	}
}

func TestGetProductNotServedFromBulkCache(t *testing.T) {
	GlobalCache.Invalidate("product")
	t.Cleanup(func() { GlobalCache.Invalidate("product") })

	var mu sync.Mutex
	var detailCalls int
	h := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/mobile-services/product/search/v2/products":
			json.NewEncoder(w).Encode([]map[string]any{{"webshopId": 1, "title": "One"}})
		case r.URL.Path == "/mobile-services/product/detail/v4/fir/1":
			detailCalls++
			json.NewEncoder(w).Encode(map[string]any{"productId": 1, "productCard": map[string]any{
				"webshopId": 1, "title": "One", "unitPriceDescription": "1 euro per kg",
			}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}

	runProductTool(t, h, registerGetProductsBulk, "ah_get_products_bulk", map[string]any{"product_ids": []any{float64(1)}})
	text := runProductTool(t, h, registerGetProduct, "ah_get_product", map[string]any{"product_id": float64(1)})
	if detailCalls != 1 {
		t.Errorf("detail calls = %d, want ah_get_product to fetch its own shape", detailCalls)
	}
	if !strings.Contains(text, "unit_price_description") {
		t.Errorf("ah_get_product returned the bulk shape: %s", text)
	}
}

func TestGetProductsBulkReturnsKitSelfAdd(t *testing.T) {
	GlobalCache.Invalidate("product_bulk:")
	t.Cleanup(func() { GlobalCache.Invalidate("product_bulk:") })
	h := func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{
			"webshopId": 564214, "title": "AH Gesneden verspakket Koreaanse bowl kip", "availableOnline": true,
			"descriptionHighlights": "<p>Koreaanse bowl.</p><p><ul><li>voor 2-3 personen||Zelf toevoegen: 195 g kipdijfilet, 4 el mayonaise</li></ul></p>",
		}})
	}
	text := runProductTool(t, h, registerGetProductsBulk, "ah_get_products_bulk",
		map[string]any{"product_ids": []any{float64(564214)}})

	var got []bulkProduct
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results: %s", len(got), text)
	}
	if got[0].ZelfToevoegen != "195 g kipdijfilet, 4 el mayonaise" {
		t.Errorf("zelf_toevoegen = %q", got[0].ZelfToevoegen)
	}
	if got[0].Description != "Koreaanse bowl.\nvoor 2-3 personen\nZelf toevoegen: 195 g kipdijfilet, 4 el mayonaise" {
		t.Errorf("description = %q", got[0].Description)
	}
}

var assortmentAliasRe = regexp.MustCompile(`p(\d+): product\(id: \d+\)`)

// withAssortment answers the gql assortment check in front of h: every id is in
// the Belgian assortment except those in notInBE. Other requests go to h.
func withAssortment(h http.HandlerFunc, notInBE map[int]bool) http.HandlerFunc {
	GlobalCache.Invalidate("assortment")
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			h(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "query Assortment") {
			r.Body = io.NopCloser(bytes.NewReader(body))
			h(w, r)
			return
		}
		data := map[string]any{}
		for _, m := range assortmentAliasRe.FindAllStringSubmatch(string(body), -1) {
			id, _ := strconv.Atoi(m[1])
			var tax any = []map[string]any{{"id": 1}}
			if notInBE[id] {
				tax = nil
			}
			data["p"+m[1]] = map[string]any{"taxonomies": tax}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}
}
