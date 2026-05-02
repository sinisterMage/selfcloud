// Package scheduler decides which node should host a given workload. On a
// single-node cluster it always returns the local node; in multi-node mode
// it spreads work over compute-capable nodes using a simple least-loaded
// strategy.
package scheduler

import (
	"context"
	"errors"
	"sort"

	"github.com/selfcloud/selfcloud/internal/store"
)

// ErrNoCapacity is returned when no node satisfies the placement request.
var ErrNoCapacity = errors.New("no node has capacity for this workload")

// Scheduler is intentionally tiny. We keep it stateless and ask the store
// for the current set of nodes on every Place() call; the cost is negligible
// compared to the workload action itself.
type Scheduler struct {
	store    *store.Store
	selfID   string
	required store.NodeRole
}

// New returns a scheduler that prefers nodes carrying the given role.
func New(s *store.Store, selfID string, required store.NodeRole) *Scheduler {
	return &Scheduler{store: s, selfID: selfID, required: required}
}

// Place returns a node ID for placement. Always returns the local node when
// it is the only one capable of hosting the workload.
func (sc *Scheduler) Place(ctx context.Context, c *store.Container) (string, error) {
	nodes, err := sc.store.ListNodes(ctx)
	if err != nil {
		return "", err
	}
	candidates := []store.Node{}
	for _, n := range nodes {
		if !hasRole(n, sc.required) {
			continue
		}
		if n.Status.Phase != "" && n.Status.Phase != store.PhaseRunning {
			continue
		}
		candidates = append(candidates, n)
	}
	if len(candidates) == 0 {
		if sc.selfID != "" {
			return sc.selfID, nil
		}
		return "", ErrNoCapacity
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Meta.UpdatedAt.Before(candidates[j].Meta.UpdatedAt)
	})
	return candidates[0].Meta.Name, nil
}

func hasRole(n store.Node, r store.NodeRole) bool {
	for _, x := range n.Roles {
		if x == r {
			return true
		}
	}
	return false
}
