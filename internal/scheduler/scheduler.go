// Package scheduler decides which node should host a given workload. On a
// single-node cluster it always returns the local node; in multi-node mode
// it spreads work over capable nodes by picking the candidate that has
// gone the longest without a fresh assignment (heartbeat-update time as a
// proxy for load — cheap, stateless, and good enough for v0).
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

// Place returns a node ID for a container. Always returns the local node
// when it is the only one capable of hosting the workload.
func (sc *Scheduler) Place(ctx context.Context, _ *store.Container) (string, error) {
	return sc.PlaceWorkload(ctx, sc.required)
}

// PlaceFunction returns a node ID for a serverless function. Functions
// always need the compute role.
func (sc *Scheduler) PlaceFunction(ctx context.Context, _ *store.Function) (string, error) {
	return sc.PlaceWorkload(ctx, store.NodeRoleCompute)
}

// PlaceWorkload picks the best candidate from the cluster for a workload
// requiring `required`. Used directly by the API handlers when they want
// to assign a node without constructing a typed workload first.
func (sc *Scheduler) PlaceWorkload(ctx context.Context, required store.NodeRole) (string, error) {
	nodes, err := sc.store.ListNodes(ctx)
	if err != nil {
		return "", err
	}
	candidates := []store.Node{}
	for _, n := range nodes {
		if !hasRole(n, required) {
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
