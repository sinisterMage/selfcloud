package container

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/selfcloud/selfcloud/internal/store"
)

// writeSecretFile writes a resolved secret value to disk under
// <dataDir>/secret-mounts/<container-uid>/<basename(mountPath)> with
// 0600 permissions. CtrRuntime bind-mounts that file into the container
// at the requested MountPath at start time (see ctr.go), so the file is
// the actual source of truth the workload reads.
func writeSecretFile(dataDir string, c *store.Container, mountPath, plaintext string) error {
	if dataDir == "" {
		dataDir = "/var/lib/selfcloud"
	}
	uid := c.Meta.UID
	if uid == "" {
		uid = c.Meta.Project + "-" + c.Meta.Name
	}
	dir := filepath.Join(dataDir, "secret-mounts", uid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	name := filepath.Base(mountPath)
	if name == "/" || name == "" || name == "." {
		name = "secret"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(plaintext), 0o600); err != nil {
		return fmt.Errorf("write secret file %s: %w", path, err)
	}
	return nil
}
