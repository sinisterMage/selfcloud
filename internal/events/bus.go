// Package events is the in-process event system that powers selfcloud's
// "when X happens, do Y" rules. The Bus accepts EventRecords from
// emitters (lifecycle hooks, S3 proxy, log scanner, in-guest sidecar,
// cron) and fans them out to:
//
//  1. a bounded persistent log (per-project) so the dashboard can show a
//     timeline,
//  2. live subscribers (the WebSocket endpoint on /events/ws), and
//  3. the dispatcher, which evaluates EventRules and runs their sinks.
//
// Emit is non-blocking: if the internal queue overflows we drop and
// increment a counter rather than back-pressure the reconciler. Sinks
// are registered after construction so api.Server can supply
// dispatch-only dependencies (function runtime, container runtime) once
// they exist.
package events

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// Sink is anything that wants to act on an event. The bus runs every
// registered sink in its own goroutine on each emit, so a slow sink
// can't block another.
type Sink interface {
	// Name is used for logs only; it doesn't need to be unique.
	Name() string
	Handle(ctx context.Context, ev store.EventRecord)
}

// SubscriberFn is the live-subscriber callback handed to Subscribe. It
// is called from a single bus-owned goroutine so the callback should
// drop quickly (e.g. push to a buffered channel) and not block.
type SubscriberFn func(ev store.EventRecord)

// Bus is the central pubsub for the events feature.
type Bus struct {
	st      *store.Store
	queue   chan store.EventRecord
	stopped atomic.Bool
	dropped atomic.Uint64

	mu    sync.RWMutex
	sinks []Sink
	subs  map[int]SubscriberFn
	nextID int
}

// New creates a Bus and starts its worker. Cancel ctx to stop.
func New(ctx context.Context, st *store.Store) *Bus {
	b := &Bus{
		st:    st,
		queue: make(chan store.EventRecord, 1024),
		subs:  map[int]SubscriberFn{},
	}
	go b.run(ctx)
	go b.trimLoop(ctx)
	return b
}

// AddSink registers a sink. Safe for concurrent use; sinks added later
// only see events emitted after registration.
func (b *Bus) AddSink(s Sink) {
	b.mu.Lock()
	b.sinks = append(b.sinks, s)
	b.mu.Unlock()
}

// Subscribe returns an unsubscribe func.
func (b *Bus) Subscribe(fn SubscriberFn) func() {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs[id] = fn
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}

// Emit pushes an event onto the queue. Never blocks; if the queue is
// full the event is dropped and counted.
func (b *Bus) Emit(ev store.EventRecord) {
	if b.stopped.Load() {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	if ev.Project == "" {
		ev.Project = "default"
	}
	select {
	case b.queue <- ev:
	default:
		b.dropped.Add(1)
	}
}

// Dropped returns the number of events dropped due to queue overflow.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }

func (b *Bus) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			b.stopped.Store(true)
			return
		case ev, ok := <-b.queue:
			if !ok {
				return
			}
			b.dispatch(ctx, ev)
		}
	}
}

func (b *Bus) dispatch(ctx context.Context, ev store.EventRecord) {
	// Persist first so the dashboard timeline reflects the event even if
	// a sink takes a while.
	if err := b.st.AppendEvent(ctx, &ev); err != nil {
		log.With("err", err, "type", ev.Type).Warn("events: append failed")
	}

	b.mu.RLock()
	subs := make([]SubscriberFn, 0, len(b.subs))
	for _, fn := range b.subs {
		subs = append(subs, fn)
	}
	sinks := make([]Sink, len(b.sinks))
	copy(sinks, b.sinks)
	b.mu.RUnlock()

	for _, fn := range subs {
		// Subscribers are expected to be cheap (push to a chan); call
		// inline so we don't fan out goroutines per subscriber.
		safeCallSub(fn, ev)
	}
	for _, s := range sinks {
		s := s
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.With("sink", s.Name(), "panic", r).Error("events: sink panic")
				}
			}()
			s.Handle(ctx, ev)
		}()
	}
}

func safeCallSub(fn SubscriberFn, ev store.EventRecord) {
	defer func() {
		if r := recover(); r != nil {
			log.With("panic", r).Error("events: subscriber panic")
		}
	}()
	fn(ev)
}

// trimLoop trims old events out of every project's log every minute so
// the bucket doesn't grow forever. 10k events per project is plenty for
// a UI timeline.
func (b *Bus) trimLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			projects, err := b.st.ListProjects(ctx)
			if err != nil {
				continue
			}
			for _, p := range projects {
				_ = b.st.TrimEvents(ctx, p.Meta.Name, 10000)
			}
		}
	}
}
