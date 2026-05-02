// Package container talks to a host containerd daemon. It exposes a minimal
// API used by the API server and reconcile loops.
//
// The package is split into a portable interface (this file) and a build-tag
// gated implementation (containerd_linux.go). On non-Linux dev machines the
// build falls back to a stub so the rest of the project still compiles and
// runs.
package container

import (
	"context"
	"errors"
	"io"

	"github.com/selfcloud/selfcloud/internal/store"
)

// Runtime is the abstraction the rest of selfcloud uses.
type Runtime interface {
	Start(ctx context.Context, c *store.Container) (*store.ContainerStatus, error)
	Stop(ctx context.Context, c *store.Container) error
	Remove(ctx context.Context, c *store.Container) error
	Logs(ctx context.Context, c *store.Container, follow bool, w io.Writer) error
	Exec(ctx context.Context, c *store.Container, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error
	List(ctx context.Context) ([]string, error)
	Close() error
}

// ErrUnsupported is returned by the stub runtime on platforms without
// containerd support.
var ErrUnsupported = errors.New("container runtime unsupported on this platform")
