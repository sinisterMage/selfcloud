// Package auth provides selfcloud's authentication primitives: local users,
// API tokens, and the bootstrap token printed by the installer.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/selfcloud/selfcloud/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrTokenExpired       = errors.New("token expired")
)

// HashToken returns the canonical hex-sha256 of a token plaintext, used for
// constant-time lookups in the store without keeping the secret around.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// GenerateToken returns a 32-byte URL-safe random token.
func GenerateToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sct_" + hex.EncodeToString(b[:]), nil
}

// HashPassword applies argon2id with sane defaults and returns a parseable
// string of the form $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>.
func HashPassword(plain string) (string, error) {
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(plain), salt[:], 3, 64*1024, 2, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=2$%s$%s",
		hex.EncodeToString(salt[:]), hex.EncodeToString(hash)), nil
}

// VerifyPassword constant-time compares plain against an argon2id hash.
func VerifyPassword(plain, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	salt, err := hex.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(plain), salt, 3, 64*1024, 2, uint32(len(want)))
	if len(got) != len(want) {
		return false
	}
	var diff byte
	for i := range got {
		diff |= got[i] ^ want[i]
	}
	return diff == 0
}

// Identity is what the API hands handlers after a successful auth check.
type Identity struct {
	UserID  string
	Email   string
	IsAdmin bool
	Token   string // empty for bootstrap auth
}

// Manager bundles auth helpers around the store.
type Manager struct {
	s *store.Store
}

func NewManager(s *store.Store) *Manager { return &Manager{s: s} }

// CreateBootstrapToken generates a one-time token, stores its hash on the
// cluster config, and returns the plaintext for the installer to print.
func (m *Manager) CreateBootstrapToken(ctx context.Context) (string, error) {
	plain, err := GenerateToken()
	if err != nil {
		return "", err
	}
	cfg, err := m.s.GetCluster(ctx)
	if err != nil {
		return "", err
	}
	cfg.BootstrapToken = HashToken(plain)
	if err := m.s.PutCluster(ctx, cfg); err != nil {
		return "", err
	}
	return plain, nil
}

// ConsumeBootstrapToken atomically validates and clears the bootstrap token.
func (m *Manager) ConsumeBootstrapToken(ctx context.Context, plain string) error {
	cfg, err := m.s.GetCluster(ctx)
	if err != nil {
		return err
	}
	if cfg.BootstrapToken == "" {
		return ErrInvalidCredentials
	}
	if HashToken(plain) != cfg.BootstrapToken {
		return ErrInvalidCredentials
	}
	cfg.BootstrapToken = ""
	cfg.Initialized = true
	return m.s.PutCluster(ctx, cfg)
}

// CreateUser creates a user with the given password.
func (m *Manager) CreateUser(ctx context.Context, name, email, password string, admin bool) (*store.User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &store.User{
		Meta:         store.Meta{Project: "system", Name: name},
		Email:        email,
		PasswordHash: hash,
		IsAdmin:      admin,
	}
	if err := m.s.PutUser(ctx, u); err != nil {
		return nil, err
	}
	u.PasswordHash = ""
	return u, nil
}

// CreateToken issues a long-lived API token for a user. The plaintext is
// returned only here; the store keeps a hashed version.
func (m *Manager) CreateToken(ctx context.Context, name, userID string, ttl time.Duration, admin bool) (*store.Token, error) {
	plain, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	t := &store.Token{
		Meta:      store.Meta{Project: "system", Name: name},
		UserID:    userID,
		HashHex:   HashToken(plain),
		IsAdmin:   admin,
		Plaintext: plain,
	}
	if ttl > 0 {
		t.ExpiresAt = time.Now().UTC().Add(ttl)
	}
	if err := m.s.PutToken(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// ResolveToken looks up an identity from a presented bearer token.
func (m *Manager) ResolveToken(ctx context.Context, plain string) (*Identity, error) {
	t, err := m.s.GetTokenByHash(ctx, HashToken(plain))
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt) {
		return nil, ErrTokenExpired
	}
	return &Identity{
		UserID:  t.UserID,
		IsAdmin: t.IsAdmin,
		Token:   t.Meta.Name,
	}, nil
}

// Login authenticates an email + password pair and returns a fresh token.
func (m *Manager) Login(ctx context.Context, email, password string) (*store.Token, *store.User, error) {
	u, err := m.s.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, nil, ErrInvalidCredentials
	}
	if !VerifyPassword(password, u.PasswordHash) {
		return nil, nil, ErrInvalidCredentials
	}
	tok, err := m.CreateToken(ctx, fmt.Sprintf("session-%d", time.Now().Unix()), u.Meta.Name, 30*24*time.Hour, u.IsAdmin)
	if err != nil {
		return nil, nil, err
	}
	u.LastLoginAt = time.Now().UTC()
	_ = m.s.PutUser(ctx, u)
	u.PasswordHash = ""
	return tok, u, nil
}
