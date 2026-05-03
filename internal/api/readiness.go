package api

import (
	"sort"
	"sync"
	"time"
)

// readiness tracks the liveness of individual subsystems so /readyz can
// give a deterministic answer to "is selfcloud safe to send traffic to?"
//
// Subsystems mark themselves Ready() once their async startup finishes
// (e.g. raft has a leader, garage answered /health, the reconciler did
// at least one pass). /readyz returns 200 only when every required
// component has reported ready; otherwise 503 with the first unfinished
// component named in the response. This is what install.sh waits on
// instead of sleeping for a fixed window.
type readiness struct {
	mu         sync.Mutex
	components map[string]*readyState
	startedAt  time.Time
}

type readyState struct {
	required bool
	ready    bool
	message  string
	updated  time.Time
}

func newReadiness() *readiness {
	return &readiness{
		components: map[string]*readyState{},
		startedAt:  time.Now(),
	}
}

// Require declares a component as required for /readyz to report 200.
// Idempotent; re-declaring an already-tracked component is a no-op.
func (r *readiness) Require(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.components[name]; !ok {
		r.components[name] = &readyState{required: true, updated: time.Now()}
	}
}

// Mark records the current ready state of a named component.
func (r *readiness) Mark(name string, ok bool, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, exists := r.components[name]
	if !exists {
		st = &readyState{required: true}
		r.components[name] = st
	}
	st.ready = ok
	st.message = msg
	st.updated = time.Now()
}

// Snapshot returns a sorted view of every tracked component. Used by
// /readyz to render the JSON body.
func (r *readiness) Snapshot() (overall bool, components []readinessReport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	overall = true
	names := make([]string, 0, len(r.components))
	for n := range r.components {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		st := r.components[n]
		if st.required && !st.ready {
			overall = false
		}
		components = append(components, readinessReport{
			Name:     n,
			Required: st.required,
			Ready:    st.ready,
			Message:  st.message,
			SinceMS:  time.Since(st.updated).Milliseconds(),
		})
	}
	return overall, components
}

type readinessReport struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Ready    bool   `json:"ready"`
	Message  string `json:"message,omitempty"`
	SinceMS  int64  `json:"sinceMs"`
}
