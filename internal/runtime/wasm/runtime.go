// Package wasm hosts selfcloud's WebAssembly functions runtime. Functions
// are uploaded as `.wasm` bytes targeting `wasm32-wasi` (WASI Preview 1),
// assigned to triggers, and invoked from the ingress proxy or the cron
// scheduler.
//
// The default runtime is wazero (pure Go, no CGO), which keeps selfcloud a
// single static binary. The ABI is intentionally trivial so any toolchain
// that targets `wasm32-wasi` works out of the box (TinyGo, Rust, Zig,
// AssemblyScript, the experimental Go target, ...): the guest reads a JSON
// request envelope from stdin and writes a JSON response envelope to stdout;
// stderr is captured into the response Logs field.
//
// A no-op `Stub` is also provided for tests and bootstrap.
package wasm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/store"
)

// ErrFunctionNotReady is returned when an invocation is attempted on a
// function that hasn't been deployed to this node yet.
var ErrFunctionNotReady = errors.New("function not ready")

// InvokeRequest is what the trigger router hands the runtime.
type InvokeRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Body    []byte
	Env     map[string]string
}

// InvokeResponse is what the runtime returns.
type InvokeResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
	Logs    string
	Elapsed time.Duration
}

// Runtime is the abstraction the API server and trigger router talk to.
type Runtime interface {
	Deploy(ctx context.Context, fn *store.Function, code []byte) error
	Remove(ctx context.Context, fn *store.Function) error
	Invoke(ctx context.Context, fn *store.Function, req *InvokeRequest) (*InvokeResponse, error)
	Close() error
}

// BlobStore is a tiny content-addressed store on disk used to persist
// uploaded function bytes. Sharing it between Wasm and Firecracker keeps
// uploads idempotent.
type BlobStore struct {
	dir string
}

func NewBlobStore(dir string) (*BlobStore, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &BlobStore{dir: dir}, nil
}

func (b *BlobStore) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	path := filepath.Join(b.dir, id)
	if _, err := os.Stat(path); err == nil {
		return id, nil
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return "", err
	}
	return id, nil
}

func (b *BlobStore) Get(id string) ([]byte, error) {
	return os.ReadFile(filepath.Join(b.dir, id))
}

func (b *BlobStore) Delete(id string) error {
	err := os.Remove(filepath.Join(b.dir, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Stub is the default runtime: it caches the bytes per function and returns
// a deterministic JSON response that proves the round-trip works.
type Stub struct {
	mu    sync.RWMutex
	funcs map[string][]byte
}

func NewStub() *Stub { return &Stub{funcs: map[string][]byte{}} }

func fnKey(fn *store.Function) string {
	return fn.Meta.Project + "/" + fn.Meta.Name
}

func (s *Stub) Deploy(_ context.Context, fn *store.Function, code []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(code))
	copy(cp, code)
	s.funcs[fnKey(fn)] = cp
	return nil
}

func (s *Stub) Remove(_ context.Context, fn *store.Function) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.funcs, fnKey(fn))
	return nil
}

func (s *Stub) Invoke(_ context.Context, fn *store.Function, req *InvokeRequest) (*InvokeResponse, error) {
	s.mu.RLock()
	code, ok := s.funcs[fnKey(fn)]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrFunctionNotReady
	}
	start := time.Now()
	body := []byte(fmt.Sprintf(`{"function":%q,"runtime":"wasm-stub","method":%q,"path":%q,"size":%d}`,
		fn.Meta.Name, req.Method, req.Path, len(code)))
	headers := http.Header{}
	headers.Set("content-type", "application/json")
	return &InvokeResponse{
		Status:  200,
		Headers: headers,
		Body:    body,
		Logs:    "[stub] invoked",
		Elapsed: time.Since(start),
	}, nil
}

func (s *Stub) Close() error { return nil }

// CopyResponse writes a runtime invoke response to a regular HTTP
// ResponseWriter. Used by the trigger router.
func CopyResponse(w http.ResponseWriter, r *InvokeResponse) {
	for k, v := range r.Headers {
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}
	if r.Status == 0 {
		r.Status = 200
	}
	w.WriteHeader(r.Status)
	_, _ = io.Copy(w, bytes.NewReader(r.Body))
}
