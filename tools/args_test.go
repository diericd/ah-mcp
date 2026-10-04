package tools

import (
	"context"
	"strings"
	"testing"

	appie "github.com/gwillem/appie-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestToInt(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    int
		wantErr string
	}{
		{name: "float64", in: float64(793946154), want: 793946154},
		{name: "int", in: 7, want: 7},
		{name: "int64", in: int64(7), want: 7},
		{name: "string", in: "793946154", want: 793946154},
		{name: "string with whitespace", in: " 12 \n", want: 12},
		{name: "quoted string", in: `"793946154"`, want: 793946154},
		{name: "quoted string with whitespace", in: ` " 5 " `, want: 5},
		{name: "negative string", in: "-3", want: -3},
		{name: "double quoted string", in: `""793946154""`, wantErr: `must be an integer, got "\"\"793946154\"\""`},
		{name: "fraction", in: 1.5, wantErr: "must be an integer, got 1.5"},
		{name: "word", in: "abc", wantErr: `must be an integer, got "abc"`},
		{name: "empty string", in: "", wantErr: `must be an integer, got ""`},
		{name: "bool", in: true, wantErr: "must be an integer, got true"},
		{name: "nil", in: nil, wantErr: "must be an integer, got <nil>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toInt(tt.in)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("toInt(%#v) error = %v, want %q", tt.in, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("toInt(%#v) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("toInt(%#v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestArgInt(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		want    int
		wantErr string
	}{
		{name: "missing uses default", args: map[string]any{}, want: 10},
		{name: "null uses default", args: map[string]any{"limit": nil}, want: 10},
		{name: "empty string uses default", args: map[string]any{"limit": " "}, want: 10},
		{name: "empty quoted string uses default", args: map[string]any{"limit": `""`}, want: 10},
		{name: "number", args: map[string]any{"limit": float64(20)}, want: 20},
		{name: "string", args: map[string]any{"limit": "20"}, want: 20},
		{name: "quoted string", args: map[string]any{"limit": `"20"`}, want: 20},
		{name: "garbage names the key", args: map[string]any{"limit": "twenty"}, wantErr: `limit must be an integer, got "twenty"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req mcp.CallToolRequest
			req.Params.Arguments = tt.args
			got, err := argInt(req, "limit", 10)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("argInt error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("argInt unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("argInt = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseIntArray(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    []int
		wantErr string
	}{
		{name: "native", in: []any{float64(1), "2", `"3"`}, want: []int{1, 2, 3}},
		{name: "json string", in: `[1, "2"]`, want: []int{1, 2}},
		{name: "double encoded json string", in: `"[1,2]"`, want: []int{1, 2}},
		{name: "bad item names the index", in: []any{float64(1), "wi387595"}, wantErr: `product_ids[1] must be an integer, got "wi387595"`},
		{name: "non-positive id", in: []any{float64(0)}, wantErr: "product_ids[0] must be > 0, got 0"},
		{name: "not an array", in: "123", wantErr: `product_ids must be a JSON array, got "123"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIntArray("product_ids", tt.in)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestParseItems(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		defQty  int
		want    []itemArg
		wantErr string
	}{
		{
			name: "native with string numbers",
			in:   []any{map[string]any{"product_id": "1", "quantity": float64(2)}},
			want: []itemArg{{ProductID: 1, Quantity: 2}},
		},
		{
			name: "json string",
			in:   `[{"product_id": 1, "quantity": "0"}]`,
			want: []itemArg{{ProductID: 1, Quantity: 0}},
		},
		{
			name:   "missing quantity uses default",
			in:     []any{map[string]any{"product_id": float64(1)}},
			defQty: 1,
			want:   []itemArg{{ProductID: 1, Quantity: 1}},
		},
		{
			name:    "missing quantity is an error when required",
			in:      []any{map[string]any{"product_id": float64(1)}},
			defQty:  -1,
			wantErr: "items[0].quantity is required",
		},
		{
			name:    "negative quantity",
			in:      []any{map[string]any{"product_id": float64(1), "quantity": float64(-1)}},
			wantErr: "items[0].quantity must be >= 0, got -1",
		},
		{
			name:    "bad quantity is not treated as removal",
			in:      []any{map[string]any{"product_id": float64(1), "quantity": "two"}},
			wantErr: `items[0].quantity must be an integer, got "two"`,
		},
		{
			name:    "missing product id",
			in:      []any{map[string]any{"quantity": float64(1)}},
			wantErr: "items[0].product_id must be an integer, got <nil>",
		},
		{
			name:    "item not an object",
			in:      []any{float64(1)},
			wantErr: `items[0] must be an object like {"product_id": 123456, "quantity": 1}`,
		},
		{name: "missing", in: nil, wantErr: "items is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseItems("items", tt.in, tt.defQty)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}

// A malformed order_id used to fall back to 0 and report "order_id is required".
func TestReopenOrderBadOrderID(t *testing.T) {
	s := server.NewMCPServer("test", "0")
	registerReopenOrder(s, Deps{
		IsAuthenticated: func() bool { return true },
		RefreshIfNeeded: func(context.Context) error { return nil },
		GetClient:       func() (*appie.Client, error) { return appie.New(), nil },
	})
	var req mcp.CallToolRequest
	req.Params.Arguments = map[string]any{"order_id": `""793946154""`}
	res, err := s.GetTool("ah_reopen_order").Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	text := res.Content[0].(mcp.TextContent).Text
	if !res.IsError || !strings.Contains(text, "order_id must be an integer") {
		t.Errorf("want integer error, got %q", text)
	}
}

// Numeric, boolean and array params must not be declared as strings, or
// clients send them quoted.
func TestToolParamTypes(t *testing.T) {
	s := server.NewMCPServer("test", "0")
	deps := Deps{}
	RegisterBasketTools(s, deps)
	RegisterOrderTools(s, deps)
	RegisterProductTools(s, deps)

	want := map[string]string{
		"limit": "number", "min_order_count": "number", "order_id": "number",
		"product_id": "number", "quantity": "number", "store_id": "number",
		"include_images": "boolean", "include_nutritional_info": "boolean",
		"bonus": "boolean", "checked": "boolean",
		"items": "array", "product_ids": "array", "names": "array", "queries": "array",
	}
	checked := 0
	for name, tool := range s.ListTools() {
		for param, schema := range tool.Tool.InputSchema.Properties {
			w, ok := want[param]
			if !ok {
				continue
			}
			checked++
			if typ, _ := schema.(map[string]any)["type"].(string); typ != w {
				t.Errorf("%s.%s type = %q, want %q", name, param, typ, w)
			}
		}
	}
	if checked < 20 {
		t.Errorf("checked only %d params, tool registration changed?", checked)
	}
}
