package keysource

import (
	"context"
	"fmt"
	"sync"
)

// Provider adapts the broker /keys/get endpoint to abccrypt.KEKProvider.
//
// It fetches a group KEK at most once per distinct kek_id and caches the
// (version, KEK) for the process lifetime — the seedling key-delivery model:
// release K_G once, unwrap every file's DEK locally (N files = 1 round-trip).
type Provider struct {
	c   *Client
	ctx context.Context

	mu    sync.Mutex
	cache map[string]cachedKEK // kek_id -> (version, kek)
	own   string               // the caller's own kek_id, once discovered
}

type cachedKEK struct {
	version int
	kek     []byte
}

// NewProvider builds a KEK provider bound to ctx (used for the broker calls).
func NewProvider(ctx context.Context, c *Client) *Provider {
	return &Provider{c: c, ctx: ctx, cache: map[string]cachedKEK{}}
}

// OwnKekID resolves the caller's own group kek_id (one /keys/get with no kek_id),
// caching the released KEK so the subsequent WrapKEK is free. Used by encrypt to
// label the abc recipient with the active group's kek_id.
func (p *Provider) OwnKekID() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.own != "" {
		return p.own, nil
	}
	kekID, version, kek, err := p.c.get(p.ctx, "")
	if err != nil {
		return "", err
	}
	p.own = kekID
	p.cache[kekID] = cachedKEK{version: version, kek: kek}
	return kekID, nil
}

// WrapKEK (encrypt side) returns the current KEK + version for kekID.
func (p *Provider) WrapKEK(kekID string) ([]byte, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cache[kekID]; ok {
		return c.kek, c.version, nil
	}
	gotID, version, kek, err := p.c.get(p.ctx, kekID)
	if err != nil {
		return nil, 0, err
	}
	if gotID != kekID {
		return nil, 0, fmt.Errorf("keys broker: released kek_id %q but %q was requested (not a member?)", gotID, kekID)
	}
	p.cache[kekID] = cachedKEK{version: version, kek: kek}
	return kek, version, nil
}

// UnwrapKEK (decrypt side) returns the KEK for a SPECIFIC (kekID, version).
//
// The seedling broker releases only the CURRENT version of a group KEK. If the
// file was wrapped under an older version (i.e. the KEK was rotated since), we
// fail closed with a clear error rather than silently substitute the latest key
// (G3) — which would just fail the AEAD unwrap with an opaque error anyway.
func (p *Provider) UnwrapKEK(kekID string, version int) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cache[kekID]; ok {
		if c.version != version {
			return nil, versionMismatch(kekID, version, c.version)
		}
		return c.kek, nil
	}
	gotID, gotVer, kek, err := p.c.get(p.ctx, kekID)
	if err != nil {
		return nil, err
	}
	if gotID != kekID {
		return nil, fmt.Errorf("keys broker: released kek_id %q but the file names %q (not a member?)", gotID, kekID)
	}
	p.cache[kekID] = cachedKEK{version: gotVer, kek: kek}
	if gotVer != version {
		return nil, versionMismatch(kekID, version, gotVer)
	}
	return kek, nil
}

func versionMismatch(kekID string, want, have int) error {
	return fmt.Errorf(
		"managed decrypt: this file was encrypted with %s version %d, but the broker now holds version %d "+
			"(the group key was rotated). Recover version %d to decrypt this file.",
		kekID, want, have, want)
}
