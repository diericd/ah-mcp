package tools

import (
	"context"
	"fmt"

	appie "github.com/gwillem/appie-go"
)

// assortmentNote is the shared tool-description text for in_be_assortment.
const assortmentNote = "in_be_assortment is false for products outside the Belgian assortment " +
	"(the delivery slot rejects them; the search catalog is Dutch) and absent when the check failed. "

// assortmentCacheKey caches one product's assortment flag.
func assortmentCacheKey(id int) string { return fmt.Sprintf("assortment:%d", id) }

// lookupAssortment returns the BE-assortment flag per id, from cache where it
// can. A failed check returns what it has, so callers leave the flag unset.
func lookupAssortment(ctx context.Context, c *appie.Client, ids []int) map[int]bool {
	out := make(map[int]bool, len(ids))
	var misses []int
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if v, ok := GlobalCache.Get(assortmentCacheKey(id)); ok {
			out[id] = string(v) == "1"
			continue
		}
		misses = append(misses, id)
	}
	if len(misses) == 0 {
		return out
	}
	got, err := c.InAssortment(ctx, misses)
	if err != nil {
		LogWarn("assortment", "check of %d ids failed: %v", len(misses), err)
	}
	for id, in := range got {
		out[id] = in
		v := "0"
		if in {
			v = "1"
		}
		GlobalCache.Set(assortmentCacheKey(id), []byte(v), CacheTTLProduct)
	}
	return out
}

// annotateItems sets InBEAssortment on search results.
func annotateItems(ctx context.Context, c *appie.Client, items []productItem) {
	ids := make([]int, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	flags := lookupAssortment(ctx, c, ids)
	for i := range items {
		if in, ok := flags[items[i].ID]; ok {
			items[i].InBEAssortment = &in
		}
	}
}

// getProductOrGQL fetches a product over REST and falls back to the gql
// gateway, which also resolves BE-only (wiNNNNNN) ids that REST 404s. webOnly
// reports that the fallback was used.
func getProductOrGQL(ctx context.Context, c *appie.Client, id int, full bool) (p *appie.Product, webOnly bool, err error) {
	if full {
		p, err = c.GetProductFull(ctx, id)
	} else {
		p, err = c.GetProduct(ctx, id)
	}
	if err == nil {
		return p, false, nil
	}
	g, gerr := c.GetProductGQL(ctx, id)
	if gerr != nil {
		return nil, false, fmt.Errorf("%w; gql fallback: %v", err, gerr)
	}
	return g, true, nil
}
