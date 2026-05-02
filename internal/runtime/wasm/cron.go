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
}

// Invoker is satisfied by both wasm.Runtime and firecracker.Runtime; the
// scheduler doesn't care which.
type Invoker interface {
	Invoke(ctx context.Context, fn *store.Function, req *InvokeRequest) (*InvokeResponse, error)
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
		id, err := s.c.AddFunc(t.Cron.Schedule, func() {
			req := &InvokeRequest{
				Method:  http.MethodPost,
				Path:    "/cron",
				Headers: http.Header{},
				Body:    nil,
				Env:     f.Env,
			}
			if _, err := s.invoke.Invoke(context.Background(), f, req); err != nil {
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
