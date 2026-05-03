package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// MasterKeyFile is the relative path of the master key under DataDir.
const MasterKeyFile = "master.key"

// LoadOrCreateMasterKey reads <dataDir>/master.key, generating it on the
// fly the first time the server boots. The file is written with 0600 so
// only the user running selfcloud can read it. The returned fingerprint
// (hex-sha256 of the key) is intended to be stored on the cluster config
// for boot-time validation.
func LoadOrCreateMasterKey(dataDir string) (key []byte, fingerprint string, err error) {
	path := filepath.Join(dataDir, MasterKeyFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		key, err = generate()
		if err != nil {
			return nil, "", err
		}
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return nil, "", err
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			return nil, "", err
		}
		return key, fp(key), nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read master key: %w", err)
	}
	if len(data) != 32 {
		return nil, "", fmt.Errorf("master key file %s has wrong size %d (want 32)", path, len(data))
	}
	// Tighten file mode in case it was relaxed manually.
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(path, 0o600)
	}
	return data, fp(data), nil
}

func generate() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("random key: %w", err)
	}
	return key, nil
}

func fp(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8]) // short prefix is plenty for fingerprinting
}
