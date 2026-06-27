package keysource

import (
	"context"
	"fmt"
	"sync"

	"filippo.io/age"
)

// Provider releases the caller's own group age key material from the broker
// (POST /keys/get) — once, cached for the process lifetime (seedling tiering:
// release the group key once, then encrypt/decrypt locally with native age).
type Provider struct {
	c   *Client
	ctx context.Context

	mu sync.Mutex
	gk *GroupKey // cached group key material
}

// NewProvider builds a key provider bound to ctx (used for the broker calls).
// The group is whatever the bound context's token resolves to at the broker — the
// active context is the single source of truth, so the encryption key always
// matches the context you operate under.
func NewProvider(ctx context.Context, c *Client) *Provider {
	return &Provider{c: c, ctx: ctx}
}

// Fetch releases (once, cached) the bound context's group key material.
func (p *Provider) Fetch() (*GroupKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gk != nil {
		return p.gk, nil
	}
	gk, err := p.c.get(p.ctx, "")
	if err != nil {
		return nil, err
	}
	p.gk = gk
	return gk, nil
}

// Recipient parses the group's native age X25519 recipient (encrypt side).
func (p *Provider) Recipient() (age.Recipient, *GroupKey, error) {
	gk, err := p.Fetch()
	if err != nil {
		return nil, nil, err
	}
	r, err := age.ParseX25519Recipient(gk.Recipient)
	if err != nil {
		return nil, nil, fmt.Errorf("parse group recipient %q: %w", gk.KekID, err)
	}
	return r, gk, nil
}

// Identity parses the group's native age X25519 identity (decrypt side).
func (p *Provider) Identity() (age.Identity, *GroupKey, error) {
	gk, err := p.Fetch()
	if err != nil {
		return nil, nil, err
	}
	id, err := age.ParseX25519Identity(gk.Identity)
	if err != nil {
		return nil, nil, fmt.Errorf("parse group identity %q: %w", gk.KekID, err)
	}
	return id, gk, nil
}
