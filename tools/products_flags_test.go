package tools

import (
	"encoding/json"
	"strings"
	"testing"

	appie "github.com/gwillem/appie-go"
)

func TestNewProductItemFlags(t *testing.T) {
	p := appie.Product{
		ID: 627042, Title: "Kit", IsBonus: true, Price: appie.Price{Now: 4, Was: 5},
		IsAvailable: true, IsOrderable: false, IsPreviouslyBought: true,
	}
	got, err := json.Marshal(newProductItem(p, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"is_available":true`, `"is_orderable":false`, `"previously_bought":true`, `"price":5`, `"bonus_price":4`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}

func TestNewBulkProductFlagsAndIcons(t *testing.T) {
	p := &appie.Product{
		ID: 525237, Title: "Kit", Price: appie.Price{Now: 3},
		IsAvailable: true, IsOrderable: true, PropertyIcons: []string{"vegetarisch"},
	}
	got, err := json.Marshal(newBulkProduct(p, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"is_available":true`, `"is_orderable":true`, `"property_icons":["vegetarisch"]`, `"price":3`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(string(got), "previously_bought") {
		t.Errorf("previously_bought should be omitted when false: %s", got)
	}
}

// A "VOOR 1.39"-style deal arrives without a was price: the bonus price is unknown,
// so bonus_price must not repeat the regular price.
func TestBonusDealWithoutWasPrice(t *testing.T) {
	p := appie.Product{ID: 115513, Title: "Peer", IsBonus: true, BonusMechanism: "VOOR 1.39", Price: appie.Price{Now: 1.99}}
	for name, v := range map[string]any{"item": newProductItem(p, false), "bulk": newBulkProduct(&p, false)} {
		got, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(got), `"bonus_price"`) {
			t.Errorf("%s: bonus_price should be omitted in %s", name, got)
		}
		for _, want := range []string{`"price":1.99`, `"bonus_mechanism":"VOOR 1.39"`} {
			if !strings.Contains(string(got), want) {
				t.Errorf("%s: missing %s in %s", name, want, got)
			}
		}
	}
}
