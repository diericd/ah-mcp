package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Tool params are declared as numbers and arrays, but clients still send
// numbers as strings, sometimes wrapped in an extra layer of JSON quotes, and
// arrays as JSON-encoded strings. The helpers below accept all of these and
// reject anything else with an error instead of falling back to a default.

// itemSchema is the JSON schema for one {product_id, quantity} item.
func itemSchema(required ...string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"product_id": map[string]any{"type": "integer"},
			"quantity":   map[string]any{"type": "integer", "minimum": 0},
		},
		"required": required,
	}
}

// productIDsSchema is the JSON schema for an array item holding a product ID.
var productIDsSchema = map[string]any{"type": "integer"}

// unquote trims whitespace and one layer of surrounding double quotes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// toInt converts a JSON number, an int or a numeric string to int.
func toInt(v any) (int, error) {
	switch val := v.(type) {
	case int:
		return val, nil
	case int64:
		return int(val), nil
	case float64:
		if val == math.Trunc(val) && !math.IsInf(val, 0) {
			return int(val), nil
		}
	case string:
		if n, err := strconv.Atoi(unquote(val)); err == nil {
			return n, nil
		}
		return 0, fmt.Errorf("must be an integer, got %q", val)
	}
	return 0, fmt.Errorf("must be an integer, got %v", v)
}

// argInt reads integer argument key. A missing, null or empty argument
// returns def.
func argInt(req mcp.CallToolRequest, key string, def int) (int, error) {
	v, ok := req.GetArguments()[key]
	if !ok || v == nil {
		return def, nil
	}
	if s, isStr := v.(string); isStr && unquote(s) == "" {
		return def, nil
	}
	n, err := toInt(v)
	if err != nil {
		return 0, fmt.Errorf("%s %w", key, err)
	}
	return n, nil
}

// parseArray accepts a native array or a JSON-encoded one, also when the JSON
// itself was encoded as a string once more.
func parseArray(key string, raw any) ([]any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, fmt.Errorf("%s is required", key)
	case []any:
		return v, nil
	case string:
		var arr []any
		if err := json.Unmarshal([]byte(v), &arr); err == nil {
			return arr, nil
		}
		var inner string
		if err := json.Unmarshal([]byte(v), &inner); err == nil {
			if err := json.Unmarshal([]byte(inner), &arr); err == nil {
				return arr, nil
			}
		}
		return nil, fmt.Errorf("%s must be a JSON array, got %q", key, v)
	}
	return nil, fmt.Errorf("%s must be a JSON array, got %v", key, raw)
}

// parseIntArray parses an array of positive integer IDs.
func parseIntArray(key string, raw any) ([]int, error) {
	arr, err := parseArray(key, raw)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(arr))
	for i, r := range arr {
		n, err := toInt(r)
		if err != nil {
			return nil, fmt.Errorf("%s[%d] %w", key, i, err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("%s[%d] must be > 0, got %d", key, i, n)
		}
		ids = append(ids, n)
	}
	return ids, nil
}

// parseStringArray parses an array of strings, dropping empty and non-string
// entries.
func parseStringArray(key string, raw any) ([]string, error) {
	arr, err := parseArray(key, raw)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(arr))
	for _, r := range arr {
		if s, ok := r.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// itemArg is one {product_id, quantity} entry of an items param.
type itemArg struct {
	ProductID int
	Quantity  int
}

// parseItems parses an array of {product_id, quantity} objects. A missing
// quantity becomes defQty, or is an error when defQty < 0.
func parseItems(key string, raw any, defQty int) ([]itemArg, error) {
	arr, err := parseArray(key, raw)
	if err != nil {
		return nil, err
	}
	items := make([]itemArg, 0, len(arr))
	for i, r := range arr {
		m, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(`%s[%d] must be an object like {"product_id": 123456, "quantity": 1}`, key, i)
		}
		pid, err := toInt(m["product_id"])
		if err != nil {
			return nil, fmt.Errorf("%s[%d].product_id %w", key, i, err)
		}
		if pid <= 0 {
			return nil, fmt.Errorf("%s[%d].product_id must be > 0, got %d", key, i, pid)
		}
		qty := defQty
		if q, present := m["quantity"]; present && q != nil {
			if qty, err = toInt(q); err != nil {
				return nil, fmt.Errorf("%s[%d].quantity %w", key, i, err)
			}
			if qty < 0 {
				return nil, fmt.Errorf("%s[%d].quantity must be >= 0, got %d", key, i, qty)
			}
		} else if defQty < 0 {
			return nil, fmt.Errorf("%s[%d].quantity is required", key, i)
		}
		items = append(items, itemArg{ProductID: pid, Quantity: qty})
	}
	return items, nil
}
