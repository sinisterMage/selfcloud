// Package firecracker hosts the microVM-backed functions runtime. Each
// invocation is a Firecracker microVM created from a pre-baked rootfs +
// kernel and snapshot/restored for fast warm starts.
//
// The default implementation here is a stub for portability. The real
// integration shells out to the `firecracker` and `jailer` binaries when
// they are present on the host, mirroring how the containerd subpackage
// uses `ctr`.
package firecracker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/runtime/wasm"
	"github.com/selfcloud/selfcloud/internal/store"
)

// ErrUnsupported indicates Firecracker is not installed on the host.
var ErrUnsupported = errors.New("firecracker not available on this host")

// Runtime mirrors the wasm.Runtime interface so the API and trigger router
// can use them interchangeably.
type Runtime = wasm.Runtime

// Stub returns canned responses identifying itself as the firecracker stub.
type Stub struct {
	mu    sync.RWMutex
	funcs map[string][]byte
}

func NewStub() *Stub { return &Stub{funcs: map[string][]byte{}} }

func k(fn *store.Function) string { return fn.Meta.Project + "/" + fn.Meta.Name }

func (s *Stub) Deploy(_ context.Context, fn *store.Function, code []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(code))
	copy(cp, code)
	s.funcs[k(fn)] = cp
	return nil
}

func (s *Stub) Remove(_ context.Context, fn *store.Function) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.funcs, k(fn))
	return nil
}

func (s *Stub) Invoke(_ context.Context, fn *store.Function, req *wasm.InvokeRequest) (*wasm.InvokeResponse, error) {
	s.mu.RLock()
	code, ok := s.funcs[k(fn)]
	s.mu.RUnlock()
	if !ok {
		return nil, wasm.ErrFunctionNotReady
	}
	start := time.Now()
	body := []byte(fmt.Sprintf(`{"function":%q,"runtime":"firecracker-stub","method":%q,"path":%q,"size":%d}`,
		fn.Meta.Name, req.Method, req.Path, len(code)))
	h := http.Header{}
	h.Set("content-type", "application/json")
	return &wasm.InvokeResponse{Status: 200, Headers: h, Body: body, Logs: "[stub] firecracker", Elapsed: time.Since(start)}, nil
}

func (s *Stub) Close() error { return nil }

// New returns the best available runtime for the given data dir. Falls
// back to Stub when firecracker isn't installed.
func New(dataDir string) Runtime {
	if _, err := exec.LookPath("firecracker"); err != nil {
		log.L().Warn("firecracker runtime: binary not found, using stub")
		return NewStub()
	}
	if r, err := newJailer(dataDir); err == nil {
		log.L().Info("firecracker runtime: jailer-backed")
		return r
	}
	return NewStub()
}
