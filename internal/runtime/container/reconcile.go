package container

import (
	"context"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/network"
	"github.com/selfcloud/selfcloud/internal/store"
)

// Reconciler watches the desired-container set in the store and converges
// the local runtime to match it. It tracks per-container in-memory state
// (last attempt time, attempt count, last reconciled spec generation) so
// that retries are bounded and store mutations from the reconciler itself
// don't cause an event-driven hot loop.
type Reconciler struct {
	st  *store.Store
	rt  Runtime
	net *network.Manager

	mu    sync.Mutex
	state map[string]*reconcileState
}

type reconcileState struct {
	lastAttempt time.Time
	nextAttempt time.Time
	attempts    int
	lastGen     int64
	startedOK   bool
}

// NewReconciler wires the dependencies.
func NewReconciler(st *store.Store, rt Runtime, net *network.Manager) *Reconciler {
	return &Reconciler{
		st:    st,
		rt:    rt,
		net:   net,
		state: map[string]*reconcileState{},
	}
}

// Run is a blocking goroutine. It performs an initial pass, subscribes to
// store events, and ticks every 10s so that backed-off retries get another
// chance.
func (r *Reconciler) Run(ctx context.Context, nodeID string) {
	r.bootstrap(ctx, nodeID)
	events := r.st.Subscribe()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Kind != store.KindContainer {
				continue
			}
			if ev.Op == "delete" {
				r.forget(ev.Project + "/" + ev.Name)
				continue
			}
			c, err := r.st.GetContainer(ctx, ev.Project, ev.Name)
			if err != nil {
				continue
			}
			r.reconcileOne(ctx, c, nodeID)
		case <-tick.C:
			r.bootstrap(ctx, nodeID)
		}
	}
}

func (r *Reconciler) bootstrap(ctx context.Context, nodeID string) {
	cs, err := r.st.ListContainers(ctx, "")
	if err != nil {
		log.With("err", err).Warn("reconcile: list failed")
		return
	}
	for i := range cs {
		r.reconcileOne(ctx, &cs[i], nodeID)
	}
}

func (r *Reconciler) keyFor(c *store.Container) string {
	return c.Meta.Project + "/" + c.Meta.Name
}

func (r *Reconciler) get(key string) *reconcileState {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.state[key]
	if !ok {
		s = &reconcileState{}
		r.state[key] = s
	}
	return s
}

func (r *Reconciler) forget(key string) {
	r.mu.Lock()
	delete(r.state, key)
	r.mu.Unlock()
}

// reconcileOne is the only place that decides whether to call rt.Start. It
// is intentionally conservative: skip if the container is in a terminal
// "do not act" state, skip if a recent attempt already happened, and never
// loop on its own writes.
func (r *Reconciler) reconcileOne(ctx context.Context, c *store.Container, nodeID string) {
	// Skip containers assigned to a different node.
	if c.NodeID != "" && c.NodeID != nodeID {
		return
	}
	// Stopped is an intentional terminal state — leave it alone until the
	// user explicitly starts it.
	if c.Status.Phase == store.PhaseStopped {
		return
	}

	key := r.keyFor(c)
	st := r.get(key)

	now := time.Now()

	// If we already started this exact spec successfully, do nothing.
	// Generation increments on every PutContainer so a real change
	// (image, ports, env) will bump it and force a fresh start.
	if st.startedOK && st.lastGen == c.Meta.Generation && c.Status.Phase == store.PhaseRunning {
		return
	}

	// Backoff: if we attempted recently and failed, wait until nextAttempt.
	if c.Status.Phase == store.PhaseFailed && now.Before(st.nextAttempt) {
		return
	}

	st.lastAttempt = now
	out, err := r.rt.Start(ctx, c)
	if err != nil {
		st.attempts++
		st.startedOK = false
		// Exponential backoff: 5s, 10s, 20s, 40s, ... capped at 5min.
		backoff := time.Duration(5) * time.Second
		for i := 1; i < st.attempts && backoff < 5*time.Minute; i++ {
			backoff *= 2
		}
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
		st.nextAttempt = now.Add(backoff)
		log.With("name", c.Meta.Name, "err", err, "attempt", st.attempts, "retry_in", backoff).
			Warn("reconcile: start failed")
		// Only persist a status change to the store if the message would
		// actually change — otherwise we'd cause an event we then react to.
		if c.Status.Phase != store.PhaseFailed || c.Status.Message != err.Error() {
			c.Status = store.ContainerStatus{
				Status: store.Status{
					Phase:     store.PhaseFailed,
					Message:   err.Error(),
					UpdatedAt: now.UTC(),
				},
			}
			_ = r.st.PutContainer(ctx, c)
		}
		return
	}

	// Success.
	st.attempts = 0
	st.startedOK = true
	st.lastGen = c.Meta.Generation

	// Wire up port publishing.
	if r.net != nil && out.IPAddress != "" {
		for _, p := range c.Ports {
			if err := r.net.Publish(p.Host, p.Container, p.Protocol, out.IPAddress); err != nil {
				log.With("err", err, "host", p.Host).Warn("reconcile: publish port failed")
			}
		}
	}

	// Persist observed status only if it actually changed; otherwise we'd
	// emit an event and re-enter reconcileOne.
	if c.Status.Phase != out.Phase || c.NodeID != nodeID {
		c.Status = *out
		c.NodeID = nodeID
		_ = r.st.PutContainer(ctx, c)
	}
}
