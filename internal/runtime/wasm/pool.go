package wasm

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// DefaultWarmInstances is how many priming invocations the pool runs after
// Deploy to warm the underlying runtime's JIT and module caches.
const DefaultWarmInstances = 2

// WarmPool wraps a Runtime and amortises cold-start cost. After a function
// is deployed it asynchronously runs N priming invocations against the
// underlying runtime, then bounds in-flight concurrency so a runaway client
// can't exhaust host resources. Per-function statistics are exposed via
// Stats() for the dashboard.
type WarmPool struct {
	inner       Runtime
	warmCount   int
	concurrency int

	mu  sync.Mutex
	hot map[string]*pooled
}

type pooled struct {
	last        time.Time
	hits        atomic.Uint64
	warmedAt    time.Time
	concurrency chan struct{}
}

// PoolStats is what the API surfaces to the dashboard.
type PoolStats struct {
	Function string    `json:"function"`
	Hits     uint64    `json:"hits"`
	LastHit  time.Time `json:"lastHit"`
	WarmedAt time.Time `json:"warmedAt"`
}

// NewWarmPool wraps inner with a warm pool using defaults.
func NewWarmPool(inner Runtime) *WarmPool {
	return NewWarmPoolWith(inner, DefaultWarmInstances, 32)
}

// NewWarmPoolWith allows tuning the warmup count and per-function concurrency.
func NewWarmPoolWith(inner Runtime, warmInstances, concurrency int) *WarmPool {
	if warmInstances < 0 {
		warmInstances = 0
	}
	if concurrency <= 0 {
		concurrency = 32
	}
	return &WarmPool{
		inner:       inner,
		warmCount:   warmInstances,
		concurrency: concurrency,
		hot:         map[string]*pooled{},
	}
}

func (p *WarmPool) Deploy(ctx context.Context, fn *store.Function, code []byte) error {
	if err := p.inner.Deploy(ctx, fn, code); err != nil {
		return err
	}
	p.mu.Lock()
	if old, ok := p.hot[fnKey(fn)]; ok {
		// Drain any in-flight permits before replacing.
		close(old.concurrency)
	}
	pp := &pooled{
		last:        time.Now(),
		warmedAt:    time.Time{},
		concurrency: make(chan struct{}, p.concurrency),
	}
	p.hot[fnKey(fn)] = pp
	p.mu.Unlock()

	// Warm the runtime asynchronously: run N no-op invocations to prime
	// the JIT, page caches, and any internal pools the underlying runtime
	// keeps. Errors here are non-fatal (the function may not handle empty
	// stdin gracefully — that's fine, the warmup still touched the code).
	if p.warmCount > 0 {
		fnCopy := *fn
		go func() {
			wctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for i := 0; i < p.warmCount; i++ {
				_, _ = p.inner.Invoke(wctx, &fnCopy, &InvokeRequest{
					Method: "GET",
					Path:   "/__warmup",
					Env:    map[string]string{"SELFCLOUD_WARMUP": "1"},
				})
			}
			p.mu.Lock()
			if cur, ok := p.hot[fnKey(&fnCopy)]; ok {
				cur.warmedAt = time.Now()
			}
			p.mu.Unlock()
			log.With("fn", fnCopy.Meta.Name, "warm", p.warmCount).Debug("wasm: warmed")
		}()
	}
	return nil
}

func (p *WarmPool) Remove(ctx context.Context, fn *store.Function) error {
	p.mu.Lock()
	if old, ok := p.hot[fnKey(fn)]; ok {
		close(old.concurrency)
	}
	delete(p.hot, fnKey(fn))
	p.mu.Unlock()
	return p.inner.Remove(ctx, fn)
}

func (p *WarmPool) Invoke(ctx context.Context, fn *store.Function, req *InvokeRequest) (*InvokeResponse, error) {
	p.mu.Lock()
	pp, ok := p.hot[fnKey(fn)]
	p.mu.Unlock()
	if ok {
		select {
		case pp.concurrency <- struct{}{}:
			defer func() { <-pp.concurrency }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		pp.last = time.Now()
		pp.hits.Add(1)
	}
	return p.inner.Invoke(ctx, fn, req)
}

func (p *WarmPool) Close() error { return p.inner.Close() }

// Stats returns a snapshot of pool statistics.
func (p *WarmPool) Stats() []PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PoolStats, 0, len(p.hot))
	for k, v := range p.hot {
		out = append(out, PoolStats{
			Function: k,
			Hits:     v.hits.Load(),
			LastHit:  v.last,
			WarmedAt: v.warmedAt,
		})
	}
	return out
}
