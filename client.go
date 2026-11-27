package main

import (
	"context"
	"fmt"
	"sync"

	appie "github.com/gwillem/appie-go"
)

var (
	clientMu     sync.RWMutex
	globalClient *appie.Client
)

// GetClient returns the singleton appie client, creating it from the tokens file
// if it has not been initialised yet.
func GetClient() (*appie.Client, error) {
	clientMu.RLock()
	c := globalClient
	clientMu.RUnlock()
	if c != nil {
		return c, nil
	}
	return ReloadClient()
}

// ReloadClient creates a fresh appie client loaded from the tokens file.
// Call this after a successful OAuth login to pick up newly saved tokens.
func ReloadClient() (*appie.Client, error) {
	path := TokensPath()
	// BE: data calls must send the Belgian client id (x-client-name header),
	// otherwise the NL default "appie-ios" is used. The nl_BE locale makes
	// product search return the Belgian webshop list, BE-only products included.
	c, err := appie.NewWithConfig(path, appie.WithClientID(ahClientID), appie.WithLocale("nl_BE"))
	if err != nil {
		return nil, fmt.Errorf("create appie client: %w", err)
	}
	clientMu.Lock()
	globalClient = c
	clientMu.Unlock()
	return c, nil
}

// RefreshClientTokens refreshes the in-memory client's access token if it is
// about to expire. appie-go owns refresh: it saves the rotated tokens to the
// tokens file and picks up tokens another process saved there, so memory and
// disk never disagree.
func RefreshClientTokens(ctx context.Context) error {
	c, err := GetClient()
	if err != nil {
		return err
	}
	return c.RefreshIfNeeded(ctx)
}
