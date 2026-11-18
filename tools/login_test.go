package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	appie "github.com/gwillem/appie-go"
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

func TestExtractLoginCode(t *testing.T) {
	const c = "b5dc5d0b-db11-4f0a-a8f5-3ab02fe141c3"
	cases := map[string]string{
		c:                              c,
		"  " + c + "\n":                c,
		"appie://login-exit?code=" + c: c,
		"appie://login-exit?code=" + c + "&state=x": c,
		"appie%3A%2F%2Flogin-exit%3Fcode%3D" + c:    c,
		"":                                          "",
		"appie://login-exit?state=x":                "",
	}
	for in, want := range cases {
		if got := extractLoginCode(in); got != want {
			t.Errorf("extractLoginCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleLoginWithCodeExchangesIt(t *testing.T) {
	var got string
	deps := Deps{
		IsAuthenticated: func() bool { return true }, // a code re-logs in even with tokens
		ExchangeCode: func(_ context.Context, code string) error {
			got = code
			return nil
		},
		ReloadClient: func() (*appie.Client, error) { return nil, errors.New("no client in test") },
	}
	res, err := handleLogin(context.Background(), deps, "appie://login-exit?code=abc-123")
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc-123" {
		t.Errorf("exchanged code = %q, want abc-123", got)
	}
	// ReloadClient fails in this test, so the result reports that, but the exchange ran.
	if !res.IsError {
		t.Errorf("want the reload error surfaced, got %+v", res.Content)
	}
}

func TestHandleLoginRejectsInputWithoutCode(t *testing.T) {
	called := false
	deps := Deps{ExchangeCode: func(context.Context, string) error { called = true; return nil }}
	res, _ := handleLogin(context.Background(), deps, "appie://login-exit?state=x")
	if !res.IsError || called {
		t.Errorf("want an error and no exchange; called=%v res=%+v", called, res.Content)
	}
}

func TestHandleLoginReportsExchangeFailure(t *testing.T) {
	deps := Deps{ExchangeCode: func(context.Context, string) error { return errors.New("AH API error 400") }}
	res, _ := handleLogin(context.Background(), deps, "abc")
	text := res.Content[0].(mcp.TextContent).Text
	if !res.IsError || !strings.Contains(text, "AH API error 400") {
		t.Errorf("result = %q", text)
	}
}
