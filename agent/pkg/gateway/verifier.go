// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package gateway

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
)

var (
	// ErrPublicKeyNotFound is returned when sender public key cannot be resolved.
	ErrPublicKeyNotFound = errors.New("sender public key not found")
	// ErrSignatureVerificationFailed is returned when Ed25519 signature is invalid.
	ErrSignatureVerificationFailed = errors.New("signature verification failed")
)

// KeyResolver resolves public keys for sender identities.
type KeyResolver interface {
	ResolvePublicKey(ctx context.Context, senderID string) (ed25519.PublicKey, error)
}

// MemoryKeyResolver is an in-memory key resolver useful for testing and local identities.
type MemoryKeyResolver struct {
	mu   sync.RWMutex
	keys map[string]ed25519.PublicKey
}

// NewMemoryKeyResolver creates an empty MemoryKeyResolver.
func NewMemoryKeyResolver() *MemoryKeyResolver {
	return &MemoryKeyResolver{
		keys: make(map[string]ed25519.PublicKey),
	}
}

// SetKey stores a public key for a given sender ID.
func (r *MemoryKeyResolver) SetKey(senderID string, pubKey ed25519.PublicKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys[senderID] = pubKey
}

// ResolvePublicKey returns the stored public key or ErrPublicKeyNotFound.
func (r *MemoryKeyResolver) ResolvePublicKey(_ context.Context, senderID string) (ed25519.PublicKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, exists := r.keys[senderID]
	if !exists {
		return nil, fmt.Errorf("%w for sender %s", ErrPublicKeyNotFound, senderID)
	}
	return key, nil
}

// IdentityFetcher defines the interface to fetch identity records from the backplane.
type IdentityFetcher interface {
	GetIdentity(ctx context.Context, id string) (*libbp.IdentityRecord, error)
}

// BackplaneKeyResolver resolves public keys by looking up identity records in Valkey.
type BackplaneKeyResolver struct {
	fetcher IdentityFetcher
}

// NewBackplaneKeyResolver creates a KeyResolver backed by the backplane.
func NewBackplaneKeyResolver(fetcher IdentityFetcher) *BackplaneKeyResolver {
	return &BackplaneKeyResolver{fetcher: fetcher}
}

// ResolvePublicKey resolves the Ed25519 public key for a sender via backplane identity record.
func (r *BackplaneKeyResolver) ResolvePublicKey(ctx context.Context, senderID string) (ed25519.PublicKey, error) {
	if r.fetcher == nil {
		return nil, ErrPublicKeyNotFound
	}
	rec, err := r.fetcher.GetIdentity(ctx, senderID)
	if err != nil {
		return nil, fmt.Errorf("%w for sender %s: %v", ErrPublicKeyNotFound, senderID, err)
	}
	if rec == nil || rec.PubKey == "" {
		return nil, fmt.Errorf("%w for sender %s: empty public key", ErrPublicKeyNotFound, senderID)
	}
	pub, err := libbp.DecodePublicKeyBase64(rec.PubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode public key for %s: %w", senderID, err)
	}
	return pub, nil
}

// Verifier validates cryptographic signatures with a thread-safe cache.
type Verifier struct {
	resolver KeyResolver
	cacheMu  sync.RWMutex
	cache    map[string]ed25519.PublicKey
}

// NewVerifier creates a new Verifier with the given KeyResolver.
func NewVerifier(resolver KeyResolver) *Verifier {
	return &Verifier{
		resolver: resolver,
		cache:    make(map[string]ed25519.PublicKey),
	}
}

// Verify checks if the base64 signature over payload is valid for the sender.
func (v *Verifier) Verify(ctx context.Context, senderID string, payload []byte, sigBase64 string) error {
	if sigBase64 == "" {
		return fmt.Errorf("%w: missing signature", ErrSignatureVerificationFailed)
	}

	pubKey, err := v.getOrResolveKey(ctx, senderID)
	if err != nil {
		return err
	}

	if !libbp.VerifyPayload(pubKey, payload, sigBase64) {
		return ErrSignatureVerificationFailed
	}
	return nil
}

func (v *Verifier) getOrResolveKey(ctx context.Context, senderID string) (ed25519.PublicKey, error) {
	v.cacheMu.RLock()
	key, exists := v.cache[senderID]
	v.cacheMu.RUnlock()
	if exists {
		return key, nil
	}

	if v.resolver == nil {
		return nil, ErrPublicKeyNotFound
	}

	resolved, err := v.resolver.ResolvePublicKey(ctx, senderID)
	if err != nil {
		return nil, err
	}

	v.cacheMu.Lock()
	v.cache[senderID] = resolved
	v.cacheMu.Unlock()
	return resolved, nil
}
