package wasm

import (
	"context"
	"net/http"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// CronScheduler invokes cron-triggered functions on schedule. It watches
// the store for function changes and keeps a single robfig/cron entry per
// function.
type CronScheduler struct {
	c       *cron.Cron
	mu      sync.Mutex
	entries map[string]cron.EntryID
	st      *store.Store
	invoke  Invoker
	bus     CronEmitter
}

// Invoker is satisfied by both wasm.Runtime and firecracker.Runtime; the
// scheduler doesn't care which.
type Invoker interface {
	Invoke(ctx context.Context, fn *store.Function, req *InvokeRequest) (*InvokeResponse, error)
}

// CronEmitter is the optional event bus the scheduler emits a `cron`
// event to on every fire, in addition to invoking the function. This
// lets users hang multi-action rules off the same schedule.
type CronEmitter interface {
	Emit(ev store.EventRecord)
}

// NewCronScheduler builds a scheduler attached to st. invoke is called for
// each cron-triggered function on its schedule.
func NewCronScheduler(st *store.Store, invoke Invoker) *CronScheduler {
	return &CronScheduler{
		c:       cron.New(cron.WithSeconds()),
		entries: map[string]cron.EntryID{},
		st:      st,
		invoke:  invoke,
	}
}

// WithBus attaches an event bus so each fire also publishes a `cron`
// event for the rules engine.
func (s *CronScheduler) WithBus(b CronEmitter) *CronScheduler {
	s.bus = b
	return s
}

// SetInvoker swaps the invoker. Used by cmd/selfcloud/server.go to point
// the scheduler at the API server's Invoke method (which resolves secrets
// and picks the right runtime) instead of hitting wasm directly.
func (s *CronScheduler) SetInvoker(inv Invoker) *CronScheduler {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invoke = inv
	return s
}

// Start runs an initial pass and then subscribes to events.
func (s *CronScheduler) Start(ctx context.Context) {
	s.c.Start()
	all, _ := s.st.ListFunctions(ctx, "")
	for i := range all {
		s.sync(&all[i])
	}
	go func() {
		ev := s.st.Subscribe()
		for {
			select {
			case <-ctx.Done():
				s.c.Stop()
				return
			case e, ok := <-ev:
				if !ok {
					return
				}
				if e.Kind != store.KindFunction {
					continue
				}
				if e.Op == "delete" {
					s.removeKey(fnKeyFromEvent(e))
					continue
				}
				if f, err := s.st.GetFunction(ctx, e.Project, e.Name); err == nil {
					s.sync(f)
				}
			}
		}
	}()
}

func fnKeyFromEvent(e store.Event) string { return e.Project + "/" + e.Name }

func (s *CronScheduler) removeKey(k string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.entries[k]; ok {
		s.c.Remove(id)
		delete(s.entries, k)
	}
}

func (s *CronScheduler) sync(f *store.Function) {
	s.removeKey(fnKey(f))
	for _, t := range f.Triggers {
		if t.Cron == nil || t.Cron.Schedule == "" {
			continue
		}
		f := f
		schedule := t.Cron.Schedule
		id, err := s.c.AddFunc(schedule, func() {
			if s.bus != nil {
				s.bus.Emit(store.EventRecord{
					Type:    "cron",
					Project: f.Meta.Project,
					Subject: f.Meta.Name,
					Data: map[string]string{
						"function": f.Meta.Name,
						"schedule": schedule,
					},
				})
			}
			req := &InvokeRequest{
				Method:  http.MethodPost,
				Path:    "/cron",
				Headers: http.Header{},
				Body:    nil,
				Env:     f.Env,
			}
			s.mu.Lock()
			inv := s.invoke
			s.mu.Unlock()
			if inv == nil {
				log.With("fn", f.Meta.Name).Warn("cron: no invoker configured; skipping")
				return
			}
			if _, err := inv.Invoke(context.Background(), f, req); err != nil {
				log.With("err", err, "fn", f.Meta.Name).Warn("cron invoke failed")
			}
		})
		if err != nil {
			log.With("err", err, "fn", f.Meta.Name).Warn("invalid cron schedule")
			continue
		}
		s.mu.Lock()
		s.entries[fnKey(f)] = id
		s.mu.Unlock()
	}
}
