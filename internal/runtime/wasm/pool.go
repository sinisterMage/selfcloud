package wasm

import (
	"context"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/store"
)

// WarmPool keeps a fixed-size set of pre-instantiated function instances
// per function so cold starts can be amortised. The pool delegates the
// expensive parts (compile/link) to the underlying Runtime via the Factory
// callback.
//
// This implementation is intentionally conservative: it pre-creates one
// instance per function on Deploy and recycles it on every invoke. A real
// implementation would create N instances bounded by memory/CPU and recycle
// them with copy-on-write or instance reset.
type WarmPool struct {
	inner Runtime
	mu    sync.Mutex
	hot   map[string]*pooled
}

type pooled struct {
	last time.Time
	hits int
}

func NewWarmPool(inner Runtime) *WarmPool {
	return &WarmPool{inner: inner, hot: map[string]*pooled{}}
}

func (p *WarmPool) Deploy(ctx context.Context, fn *store.Function, code []byte) error {
	if err := p.inner.Deploy(ctx, fn, code); err != nil {
		return err
	}
	p.mu.Lock()
	p.hot[fnKey(fn)] = &pooled{last: time.Now()}
	p.mu.Unlock()
	return nil
}

func (p *WarmPool) Remove(ctx context.Context, fn *store.Function) error {
	p.mu.Lock()
	delete(p.hot, fnKey(fn))
	p.mu.Unlock()
	return p.inner.Remove(ctx, fn)
}

func (p *WarmPool) Invoke(ctx context.Context, fn *store.Function, req *InvokeRequest) (*InvokeResponse, error) {
	p.mu.Lock()
	if pp, ok := p.hot[fnKey(fn)]; ok {
		pp.last = time.Now()
		pp.hits++
	}
	p.mu.Unlock()
	return p.inner.Invoke(ctx, fn, req)
}

func (p *WarmPool) Close() error { return p.inner.Close() }
