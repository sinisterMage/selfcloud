package container

import (
	"context"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/network"
	"github.com/selfcloud/selfcloud/internal/store"
)

// Reconciler watches the desired-container set in the store and converges
// the local runtime to match it. On startup it makes one pass to bring
// previously-running containers back online; thereafter it reacts to store
// events.
type Reconciler struct {
	st  *store.Store
	rt  Runtime
	net *network.Manager
}

// NewReconciler wires the dependencies.
func NewReconciler(st *store.Store, rt Runtime, net *network.Manager) *Reconciler {
	return &Reconciler{st: st, rt: rt, net: net}
}

// Run is a blocking goroutine. It performs an initial pass and then
// subscribes to store events.
func (r *Reconciler) Run(ctx context.Context, nodeID string) {
	r.bootstrap(ctx, nodeID)
	events := r.st.Subscribe()
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
			c, err := r.st.GetContainer(ctx, ev.Project, ev.Name)
			if err != nil {
				continue
			}
			r.reconcileOne(ctx, c, nodeID)
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

func (r *Reconciler) reconcileOne(ctx context.Context, c *store.Container, nodeID string) {
	// Skip containers assigned to a different node.
	if c.NodeID != "" && c.NodeID != nodeID {
		return
	}
	if c.Status.Phase == store.PhaseStopped {
		return
	}
	// If we don't think it's running locally, start it. The runtime is
	// responsible for being idempotent (its `Start` should be safe to call
	// repeatedly).
	st, err := r.rt.Start(ctx, c)
	if err != nil {
		log.With("name", c.Meta.Name, "err", err).Warn("reconcile: start failed")
		c.Status = store.ContainerStatus{
			Status: store.Status{Phase: store.PhaseFailed, Message: err.Error(), UpdatedAt: time.Now().UTC()},
		}
		_ = r.st.PutContainer(ctx, c)
		return
	}
	// Wire up port publishing.
	if r.net != nil && st.IPAddress != "" {
		for _, p := range c.Ports {
			if err := r.net.Publish(p.Host, p.Container, p.Protocol, st.IPAddress); err != nil {
				log.With("err", err, "host", p.Host).Warn("reconcile: publish port failed")
			}
		}
	}
	c.Status = *st
	c.NodeID = nodeID
	_ = r.st.PutContainer(ctx, c)
}
