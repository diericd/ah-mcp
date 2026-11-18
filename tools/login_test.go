package tools

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestNotAuthResultIncludesTokenStatus(t *testing.T) {
	res := notAuthResult(Deps{
		TokensPath:  "/x/.tokens.json",
		TokenStatus: func() string { return "no refresh token in /x/.tokens.json" },
	})
	if !res.IsError {
		t.Fatal("want a tool error")
	}
	var got map[string]string
	text := res.Content[0].(mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	if got["error"] != "not_authenticated" || got["token_path"] != "/x/.tokens.json" || got["token_status"] != "no refresh token in /x/.tokens.json" {
		t.Errorf("result = %v", got)
	}
}

func TestNotAuthResultWithoutTokenStatus(t *testing.T) {
	text := notAuthResult(Deps{}).Content[0].(mcp.TextContent).Text
	var got map[string]string
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	if _, ok := got["token_status"]; ok || got["error"] != "not_authenticated" {
		t.Errorf("result = %v", got)
	}
}
