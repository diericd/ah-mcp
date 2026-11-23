package tools

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// bonusHandler serves one NATIONAL bonus category with three single products
// (601527 is Dutch-only) and one group deal.
func bonusHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/bonuspage/v3/metadata"):
			json.NewEncoder(w).Encode(map[string]any{"periods": []any{map[string]any{"tabs": []any{map[string]any{
				"urlMetadataList": []any{map[string]any{"bonusType": "NATIONAL", "description": "Groente"}},
			}}}}})
		case strings.HasSuffix(r.URL.Path, "/bonuspage/v2/section"):
			prod := func(id int, title string) map[string]any {
				return map[string]any{"product": map[string]any{"webshopId": id, "title": title, "currentPrice": 1.0, "priceBeforeBonus": 2.0, "isBonus": true}}
			}
			json.NewEncoder(w).Encode(map[string]any{"bonusGroupOrProducts": []any{
				prod(2606, "AH Goudse oud plakken"),
				prod(601527, "AH Aftelkalender"),
				prod(140100, "AH Goudse oud geraspt"),
				map[string]any{"bonusGroup": map[string]any{"id": "816408", "segmentDescription": "Alle kaas", "discountDescription": "2e halve prijs"}},
			}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}
}

func TestBonusOffersDropNonBEProducts(t *testing.T) {
	GlobalCache.Invalidate("bonus")
	notInBE := map[int]bool{601527: true}

	var got []struct {
		ID             int    `json:"id"`
		Title          string `json:"title"`
		InBEAssortment *bool  `json:"in_be_assortment"`
	}
	text := runProductToolBE(t, bonusHandler(t), notInBE, registerGetBonusOffers, "ah_get_bonus_offers", map[string]any{})
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	var titles []string
	for _, g := range got {
		titles = append(titles, g.Title)
		if g.ID != 0 && (g.InBEAssortment == nil || !*g.InBEAssortment) {
			t.Errorf("kept product %d should be flagged in_be_assortment: %s", g.ID, text)
		}
		if g.ID == 0 && g.InBEAssortment != nil {
			t.Errorf("a group deal is not checked: %s", text)
		}
	}
	if want := "AH Goudse oud plakken|AH Goudse oud geraspt|Alle kaas"; strings.Join(titles, "|") != want {
		t.Errorf("titles = %v, want %s (Dutch-only aftelkalender dropped)", titles, want)
	}

	text = runProductToolBE(t, bonusHandler(t), notInBE, registerGetBonusOffers, "ah_get_bonus_offers", map[string]any{"include_non_be": true})
	if !strings.Contains(text, `"id":601527,`) || !strings.Contains(text, `"in_be_assortment":false`) {
		t.Errorf("include_non_be should keep 601527 flagged false: %s", text)
	}
}

func TestSearchProductsFlagsBEAssortment(t *testing.T) {
	GlobalCache.Invalidate("search")
	h := func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"page": map[string]any{"totalElements": 2},
			"products": []any{
				map[string]any{"webshopId": 238834, "title": "AH Witlof"},
				map[string]any{"webshopId": 186750, "title": "AH Biologisch Witlof"},
			},
		})
	}
	text := runProductToolBE(t, h, map[int]bool{238834: true}, registerSearchProducts, "ah_search_products", map[string]any{"query": "witlof-be-flag"})
	var got []struct {
		ID             int   `json:"id"`
		InBEAssortment *bool `json:"in_be_assortment"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil || len(got) != 2 {
		t.Fatalf("decode %q: %v", text, err)
	}
	if got[0].InBEAssortment == nil || *got[0].InBEAssortment {
		t.Errorf("238834 should be flagged false: %s", text)
	}
	if got[1].InBEAssortment == nil || !*got[1].InBEAssortment {
		t.Errorf("186750 should be flagged true: %s", text)
	}
}
