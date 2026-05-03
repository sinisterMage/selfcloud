package container

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/network"
	"github.com/selfcloud/selfcloud/internal/store"
)

// EventEmitter is the optional bus the reconciler emits lifecycle events
// to. Implementations live in the events package; this avoids importing
// it directly here so the runtime stays lightweight.
type EventEmitter interface {
	Emit(ev store.EventRecord)
}

// SecretResolver is the optional secret reference resolver. When set,
// the reconciler resolves "secret://name" values in container Env and
// SecretMounts before calling rt.Start.
type SecretResolver interface {
	Resolve(ctx context.Context, project string, in map[string]string) (map[string]string, error)
	Reveal(ctx context.Context, project, name string) (string, error)
}

// Reconciler watches the desired-container set in the store and converges
// the local runtime to match it. It tracks per-container in-memory state
// (last attempt time, attempt count, last reconciled spec generation) so
// that retries are bounded and store mutations from the reconciler itself
// don't cause an event-driven hot loop.
type Reconciler struct {
	st      *store.Store
	rt      Runtime
	net     *network.Manager
	bus     EventEmitter
	secrets SecretResolver
	dataDir string

	mu    sync.Mutex
	state map[string]*reconcileState
}

type reconcileState struct {
	lastAttempt    time.Time
	nextAttempt    time.Time
	lastAliveCheck time.Time
	attempts       int
	lastGen        int64
	startedOK      bool
}

// NewReconciler wires the dependencies. Bus and secrets are optional
// and may be nil for environments that don't run them.
func NewReconciler(st *store.Store, rt Runtime, net *network.Manager) *Reconciler {
	return &Reconciler{
		st:    st,
		rt:    rt,
		net:   net,
		state: map[string]*reconcileState{},
	}
}

// WithBus attaches an event bus so lifecycle events are emitted on
// container start/crash/stop.
func (r *Reconciler) WithBus(b EventEmitter) *Reconciler {
	r.bus = b
	return r
}

// WithSecrets attaches a secret resolver so secret:// refs in env and
// SecretMounts are resolved at start time. dataDir is the host directory
// under which the reconciler stages secret-mount files — it must match
// the runtime's bind-source root (see CtrRuntime.SetDataDir).
func (r *Reconciler) WithSecrets(s SecretResolver, dataDir string) *Reconciler {
	r.secrets = s
	r.dataDir = dataDir
	if dd, ok := r.rt.(interface{ SetDataDir(string) }); ok {
		dd.SetDataDir(dataDir)
	}
	return r
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

	// If we already started this exact spec successfully, decide whether
	// the runtime still has it alive. If not, RestartPolicy decides
	// whether to bring it back.
	if st.startedOK && st.lastGen == c.Meta.Generation && c.Status.Phase == store.PhaseRunning {
		if !r.shouldVerifyAlive(c, st, now) {
			return
		}
		alive := r.isRunning(ctx, c)
		st.lastAliveCheck = now
		if alive {
			return
		}
		// The runtime no longer reports the task. Apply RestartPolicy.
		if !shouldRestart(c.RestartPolicy) {
			c.Status.Phase = store.PhaseStopped
			c.Status.Message = "exited (restartPolicy=" + string(c.RestartPolicy) + ")"
			c.Status.UpdatedAt = now.UTC()
			_ = r.st.PutContainer(ctx, c)
			st.startedOK = false
			return
		}
		log.With("name", c.Meta.Name).Info("reconcile: container exited, restarting per policy")
		st.startedOK = false
		// fall through into the start path below
	}

	// Backoff: if we attempted recently and failed, wait until nextAttempt.
	if c.Status.Phase == store.PhaseFailed && now.Before(st.nextAttempt) {
		return
	}

	st.lastAttempt = now

	// Resolve secrets in env vars + secret mounts before handing the
	// container to the runtime. We work on a copy so the persisted
	// resource never holds plaintext.
	cWork := *c
	if r.secrets != nil {
		if resolved, err := r.secrets.Resolve(ctx, c.Meta.Project, c.Env); err == nil {
			cWork.Env = resolved
		} else {
			log.With("err", err, "name", c.Meta.Name).Warn("reconcile: env secret resolve failed")
		}
		// Materialise file-mode secret mounts onto disk so the runtime's
		// container factory can bind-mount them.
		for _, sm := range c.SecretMounts {
			if sm.MountPath == "" {
				continue
			}
			pt, err := r.secrets.Reveal(ctx, c.Meta.Project, sm.Secret)
			if err != nil {
				log.With("err", err, "secret", sm.Secret).Warn("reconcile: secret mount resolve failed")
				continue
			}
			if err := writeSecretFile(r.dataDir, &cWork, sm.MountPath, pt); err != nil {
				log.With("err", err, "secret", sm.Secret).Warn("reconcile: secret mount write failed")
			}
		}
		// Inject env-mode secret mounts.
		if cWork.Env == nil {
			cWork.Env = map[string]string{}
		}
		for _, sm := range c.SecretMounts {
			if sm.EnvName == "" {
				continue
			}
			pt, err := r.secrets.Reveal(ctx, c.Meta.Project, sm.Secret)
			if err != nil {
				log.With("err", err, "secret", sm.Secret).Warn("reconcile: secret env resolve failed")
				continue
			}
			cWork.Env[sm.EnvName] = pt
		}
	}

	out, err := r.rt.Start(ctx, &cWork)
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
		if r.bus != nil {
			r.bus.Emit(store.EventRecord{
				Type:    "container.crash",
				Project: c.Meta.Project,
				Subject: c.Meta.Name,
				Data: map[string]string{
					"name":     c.Meta.Name,
					"image":    c.Image,
					"error":    err.Error(),
					"attempts": fmtInt(st.attempts),
				},
			})
		}
		return
	}

	// Success.
	wasRunning := st.startedOK
	st.attempts = 0
	st.startedOK = true
	st.lastGen = c.Meta.Generation
	if r.bus != nil && !wasRunning {
		r.bus.Emit(store.EventRecord{
			Type:    "container.start",
			Project: c.Meta.Project,
			Subject: c.Meta.Name,
			Data: map[string]string{
				"name":  c.Meta.Name,
				"image": c.Image,
			},
		})
	}

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

func fmtInt(i int) string {
	return strconv.Itoa(i)
}

// shouldVerifyAlive returns true when the reconciler has gone long enough
// without checking the runtime to be worth a probe. We don't probe on
// every event so the hot reconcile loop stays cheap.
func (r *Reconciler) shouldVerifyAlive(_ *store.Container, st *reconcileState, now time.Time) bool {
	return now.Sub(st.lastAliveCheck) > 8*time.Second
}

// isRunning queries the runtime via the optional liveness hook. If the
// runtime doesn't expose one (e.g. the in-memory stub) we conservatively
// answer "yes" so we don't stop-and-restart healthy containers.
func (r *Reconciler) isRunning(ctx context.Context, c *store.Container) bool {
	type alive interface {
		IsRunning(ctx context.Context, c *store.Container) (bool, error)
	}
	if a, ok := r.rt.(alive); ok {
		ok, err := a.IsRunning(ctx, c)
		if err != nil {
			log.With("err", err, "name", c.Meta.Name).Debug("reconcile: liveness check failed")
			return true
		}
		return ok
	}
	return true
}

// shouldRestart maps a RestartPolicy onto a "restart-on-exit" decision.
// We don't distinguish exit codes today; OnFailure currently behaves the
// same as Always. That can be tightened once the runtime exposes the
// exit code.
func shouldRestart(p store.RestartPolicy) bool {
	switch p {
	case store.RestartAlways, store.RestartOnFailure:
		return true
	default:
		return false
	}
}
