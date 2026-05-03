package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/selfcloud/selfcloud/internal/store"
)

// SecretRefPrefix is the string prefix that flags a value (in env vars,
// volume mounts, ...) as referencing a Secret instead of a literal value.
const SecretRefPrefix = "secret://"

// Manager is the orchestration layer above the cipher and the store. It
// owns the cluster master key and uses it to seal/open Secret values.
type Manager struct {
	st  *store.Store
	key []byte
}

// New wires a Manager. The supplied key must be 32 bytes (AES-256).
func New(st *store.Store, masterKey []byte) (*Manager, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("master key must be 32 bytes")
	}
	return &Manager{st: st, key: masterKey}, nil
}

// Put encrypts plaintext under the cluster master key and persists a
// Secret resource. If a secret with the same name already exists its
// version is incremented; the caller can detect this via the returned
// Secret.Version.
func (m *Manager) Put(ctx context.Context, project, name, description, plaintext string) (*store.Secret, error) {
	if name == "" {
		return nil, errors.New("secret name required")
	}
	nonce, ct, err := Seal(m.key, []byte(plaintext))
	if err != nil {
		return nil, err
	}
	cur, err := m.st.GetSecret(ctx, project, name)
	version := 1
	if err == nil && cur != nil {
		version = cur.Version + 1
	}
	sec := &store.Secret{
		Meta: store.Meta{
			Project:   project,
			Name:      name,
			UpdatedAt: time.Now().UTC(),
		},
		Description: description,
		KeyID:       MasterKeyVersion,
		Nonce:       nonce,
		Ciphertext:  ct,
		Version:     version,
	}
	if cur != nil {
		sec.Meta.UID = cur.Meta.UID
		sec.Meta.CreatedAt = cur.Meta.CreatedAt
	}
	if err := m.st.PutSecret(ctx, sec); err != nil {
		return nil, err
	}
	// Caller never needs the ciphertext on the returned object; clear it
	// for safety.
	sec.Nonce = nil
	sec.Ciphertext = nil
	return sec, nil
}

// Reveal returns the plaintext value of a secret. Caller must enforce
// authorisation (e.g. admin only) on the API edge.
func (m *Manager) Reveal(ctx context.Context, project, name string) (string, error) {
	sec, err := m.st.GetSecret(ctx, project, name)
	if err != nil {
		return "", err
	}
	if sec.KeyID != MasterKeyVersion {
		return "", fmt.Errorf("secret %q sealed under unknown key %q", name, sec.KeyID)
	}
	pt, err := Open(m.key, sec.Nonce, sec.Ciphertext)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// Resolve replaces any "secret://name" values in m with their plaintext.
// Other values pass through untouched. Returns a new map; never mutates
// the input.
func (m *Manager) Resolve(ctx context.Context, project string, in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return in, nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if !strings.HasPrefix(v, SecretRefPrefix) {
			out[k] = v
			continue
		}
		ref := strings.TrimPrefix(v, SecretRefPrefix)
		// Allow "project/name" override but default to current project.
		p, n := project, ref
		if i := strings.Index(ref, "/"); i > 0 {
			p, n = ref[:i], ref[i+1:]
		}
		pt, err := m.Reveal(ctx, p, n)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", v, err)
		}
		out[k] = pt
	}
	return out, nil
}

// ResolveOne is a convenience wrapper around Resolve for a single value.
func (m *Manager) ResolveOne(ctx context.Context, project, ref string) (string, error) {
	if !strings.HasPrefix(ref, SecretRefPrefix) {
		return ref, nil
	}
	out, err := m.Resolve(ctx, project, map[string]string{"v": ref})
	if err != nil {
		return "", err
	}
	return out["v"], nil
}

// IsRef reports whether v looks like a "secret://..." reference.
func IsRef(v string) bool {
	return strings.HasPrefix(v, SecretRefPrefix)
}
