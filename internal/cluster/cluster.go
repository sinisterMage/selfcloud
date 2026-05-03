// Package cluster manages multi-node membership: generating join tokens
// on the leader and giving the API a clean handle to add or remove
// voters from Raft. Liveness is tracked via the per-node heartbeat goroutine
// in cmd/selfcloud (it refreshes Node.LastSeenAt every 15s); gossip is
// not currently implemented and not required given the heartbeat model.
package cluster

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

var (
	ErrInvalidToken = errors.New("invalid join token")
	ErrTokenUsed    = errors.New("join token already used")
	ErrTokenExpired = errors.New("join token expired")
)

// Manager owns join-token bookkeeping; the actual Raft membership change is
// performed by the API server using the *store.Raft handle once a token has
// been validated here.
type Manager struct {
	mu    sync.Mutex
	store *store.Store
}

// NewManager returns a Manager backed by the cluster config singleton.
func NewManager(s *store.Store) *Manager { return &Manager{store: s} }

func hashHex(plain string) string {
	s := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(s[:])
}

// Issue creates a new join token and persists its hash in the cluster
// config. The plaintext is returned only once, here.
func (m *Manager) Issue(ctx context.Context, issuedBy string, ttl time.Duration) (string, *store.JoinToken, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, err
	}
	plain := "scjoin_" + hex.EncodeToString(raw[:])
	tok := store.JoinToken{
		ID:        hex.EncodeToString(raw[:8]),
		HashHex:   hashHex(plain),
		IssuedBy:  issuedBy,
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, err := m.store.GetCluster(ctx)
	if err != nil {
		return "", nil, err
	}
	cfg.JoinTokens = append(cfg.JoinTokens, tok)
	if err := m.store.PutCluster(ctx, cfg); err != nil {
		return "", nil, err
	}
	log.With("id", tok.ID, "ttl", ttl).Info("cluster: issued join token")
	return plain, &tok, nil
}

// Consume validates a token and atomically marks it used.
func (m *Manager) Consume(ctx context.Context, plain, consumedBy string) error {
	hash := hashHex(plain)
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, err := m.store.GetCluster(ctx)
	if err != nil {
		return err
	}
	for i := range cfg.JoinTokens {
		t := &cfg.JoinTokens[i]
		if t.HashHex != hash {
			continue
		}
		if t.ConsumedBy != "" {
			return ErrTokenUsed
		}
		if time.Now().After(t.ExpiresAt) {
			return ErrTokenExpired
		}
		t.ConsumedBy = consumedBy
		return m.store.PutCluster(ctx, cfg)
	}
	return ErrInvalidToken
}

// List returns issued tokens with hash material redacted.
func (m *Manager) List(ctx context.Context) ([]store.JoinToken, error) {
	cfg, err := m.store.GetCluster(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.JoinToken, 0, len(cfg.JoinTokens))
	for _, t := range cfg.JoinTokens {
		t.HashHex = ""
		out = append(out, t)
	}
	return out, nil
}

// JoinCommand returns the one-line shell command an operator pastes onto a
// new machine. It assumes the public installer host is `get.selfcloud.dev`
// but the leader URL can override.
func JoinCommand(leaderAddr, plainToken string) string {
	return fmt.Sprintf(`curl -fsSL https://get.selfcloud.dev | sh -s -- --join %s --token %s`, leaderAddr, plainToken)
}
