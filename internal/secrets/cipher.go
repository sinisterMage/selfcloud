// Package secrets provides selfcloud's project-scoped secret management.
// Secrets are encrypted at rest with AES-256-GCM using a per-cluster
// master key. The master key lives on disk under <dataDir>/master.key
// (mode 0600); a fingerprint of the key is also stored in the cluster
// config so we can detect a swapped key on next boot.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// MasterKeyVersion is bumped if we ever change the wrapping format. It's
// recorded as Secret.KeyID so a future rotation pass knows which secrets
// were sealed under which key.
const MasterKeyVersion = "v1"

// Seal encrypts plaintext under key (32 bytes) using AES-256-GCM. The
// returned nonce is 12 bytes and must be stored alongside the ciphertext.
func Seal(key, plaintext []byte) (nonce, ciphertext []byte, err error) {
	if len(key) != 32 {
		return nil, nil, errors.New("master key must be 32 bytes (AES-256)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("gcm: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("nonce: %w", err)
	}
	return nonce, gcm.Seal(nil, nonce, plaintext, nil), nil
}

// Open is the inverse of Seal. It returns ErrTampered if authentication
// fails (e.g. wrong master key, corrupted ciphertext).
func Open(key, nonce, ciphertext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("master key must be 32 bytes (AES-256)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("bad nonce length")
	}
	pt, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrTampered
	}
	return pt, nil
}

// ErrTampered indicates AES-GCM authentication failed during Open.
var ErrTampered = errors.New("secret authentication failed (wrong key or tampered ciphertext)")
