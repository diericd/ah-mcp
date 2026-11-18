package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTokensPath(t *testing.T) {
	exeDir := t.TempDir()
	tests := []struct {
		name, env, want string
	}{
		{"relative resolves against binary dir", "./.tokens.json", filepath.Join(exeDir, ".tokens.json")},
		{"relative without dot", "sub/tokens.json", filepath.Join(exeDir, "sub", "tokens.json")},
		{"absolute kept", "/abs/tokens.json", "/abs/tokens.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveTokensPath(tt.env, exeDir); got != tt.want {
				t.Errorf("resolveTokensPath(%q) = %q, want %q", tt.env, got, tt.want)
			}
		})
	}
}

func TestTokensPathRelativeIgnoresCwd(t *testing.T) {
	t.Setenv("AH_TOKENS_PATH", "./.tokens.json")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(exe), ".tokens.json")
	if got := TokensPath(); got != want {
		t.Errorf("TokensPath() = %q, want %q", got, want)
	}
}

func TestTokenStatus(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name, path, want string
	}{
		{"missing", filepath.Join(dir, "nope.json"), "no token file at"},
		{"unparseable", write("bad.json", "{"), "unreadable token file at"},
		{"no refresh token", write("empty.json", `{"access_token":"a"}`), "no refresh token in"},
		{"expired", write("old.json", `{"refresh_token":"r","expires_at":"2020-01-01T00:00:00Z"}`), "access token expired at"},
		{"ok", write("ok.json", `{"refresh_token":"r"}`), "token file present at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TokenStatus(tt.path)
			if !strings.Contains(got, tt.want) || !strings.Contains(got, tt.path) {
				t.Errorf("TokenStatus(%q) = %q, want %q and the path", tt.path, got, tt.want)
			}
		})
	}
}
