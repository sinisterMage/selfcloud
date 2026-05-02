package container

import (
	"github.com/selfcloud/selfcloud/internal/log"
)

// New picks the best available runtime for the current host. It first tries
// to drive a real containerd via the `ctr` binary; if that fails (no
// containerd, non-Linux dev box, missing privileges) it falls back to the
// in-memory stub so the rest of the platform keeps working.
func New(socket string) Runtime {
	if r, err := newPlatform(socket); err == nil {
		log.With("socket", socket).Info("container runtime: containerd via ctr")
		return r
	}
	log.L().Warn("container runtime: containerd not available, using in-memory stub")
	return NewStub()
}
