package main

import (
	"context"
	"testing"
	"time"
)

// The tool-side refresh hook must go through the in-memory client. When
// another process already refreshed the tokens file, the client adopts those
// tokens instead of refreshing with its stale, rotated-away refresh token.
func TestRefreshClientTokensAdoptsTokensFile(t *testing.T) {
	path := t.TempDir() + "/tokens.json"
	t.Setenv("AH_TOKENS_PATH", path)
	t.Cleanup(func() {
		clientMu.Lock()
		globalClient = nil
		clientMu.Unlock()
	})

	stale := &tokenFile{AccessToken: "a0", RefreshToken: "r0", ExpiresAt: time.Now().Add(10 * time.Second)}
	if err := SaveTokens(path, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := ReloadClient(); err != nil {
		t.Fatal(err)
	}
	fresh := &tokenFile{AccessToken: "a1", RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour)}
	if err := SaveTokens(path, fresh); err != nil {
		t.Fatal(err)
	}

	// A cancelled context makes any network refresh fail, so a nil error
	// proves the fresh file tokens were adopted without calling AH.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RefreshClientTokens(ctx); err != nil {
		t.Fatalf("RefreshClientTokens = %v, want nil (file tokens adopted)", err)
	}

	// Writing the old tokens back must not undo the adoption: older file
	// tokens are ignored, and the client still holds a valid token.
	if err := SaveTokens(path, stale); err != nil {
		t.Fatal(err)
	}
	if err := RefreshClientTokens(ctx); err != nil {
		t.Fatalf("second RefreshClientTokens = %v, want nil", err)
	}
}

func TestRefreshClientTokensReportsFailure(t *testing.T) {
	path := t.TempDir() + "/tokens.json"
	t.Setenv("AH_TOKENS_PATH", path)
	t.Cleanup(func() {
		clientMu.Lock()
		globalClient = nil
		clientMu.Unlock()
	})
	expired := &tokenFile{AccessToken: "a0", RefreshToken: "r0", ExpiresAt: time.Now().Add(-time.Minute)}
	if err := SaveTokens(path, expired); err != nil {
		t.Fatal(err)
	}
	if _, err := ReloadClient(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RefreshClientTokens(ctx); err == nil {
		t.Fatal("RefreshClientTokens with an expired token and no network = nil, want error")
	}
}
