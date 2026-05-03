package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ReplicatedStore is a thin write wrapper that routes mutations through
// Raft on multi-node clusters and falls through to the local Store on
// single-node deployments. Reads continue to go through the embedded
// *Store directly; only the methods callers want replicated are
// overridden here.
//
// Design notes:
//
//   - Single-node fast path: when there is no Raft handle, or the local
//     node is the only voter, we write directly to the local Store. This
//     keeps the cost of Raft (~ms) off single-node deployments.
//   - Leader path: the wrapper mints meta (UID / CreatedAt / UpdatedAt /
//     Generation), marshals the post-meta value, and ships a LogOp via
//     raft.Apply. The FSM on every committing node — including this one
//     — performs the BoltDB put and emits a typed Event.
//   - Follower path: typed writes return ErrNotLeader. The API server's
//     replicate middleware turns that into a 307 redirect to the leader
//     so clients (CLI, dashboard, terraform, S3 SDK) seamlessly retry
//     against the leader without retrying themselves.
//   - Internal subsystems (reconciler status updates, builder progress,
//     log scanner, sidecar) keep using the bare *Store. Those mutations
//     are local-only by design — they describe the local node's view —
//     and don't need cluster-wide consistency.
type ReplicatedStore struct {
	*Store
	raft    *Raft
	timeout time.Duration
}

// NewReplicatedStore builds a wrapper. raft may be nil (degenerates to a
// pass-through over the local Store, useful in tests and on dev boxes
// where Raft fails to start).
func NewReplicatedStore(s *Store, r *Raft) *ReplicatedStore {
	return &ReplicatedStore{Store: s, raft: r, timeout: 5 * time.Second}
}

// shouldReplicate reports whether a mutation should be sent through Raft.
// We replicate only when there is more than one voter; on a one-voter
// cluster every Apply round-trip is wasted work.
func (rs *ReplicatedStore) shouldReplicate() bool {
	if rs.raft == nil {
		return false
	}
	servers, err := rs.raft.Servers()
	if err != nil || len(servers) <= 1 {
		return false
	}
	return true
}

// applyTypedPut is the workhorse used by every typed Put*. It mints meta
// then either calls into the local store directly or ships a LogOp.
func (rs *ReplicatedStore) applyTypedPut(_ context.Context, bucket string, kind Kind, v projectScoped) error {
	m := v.getMeta()
	if m.Project == "" {
		m.Project = "default"
	}
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !rs.shouldReplicate() {
		return rs.Store.putScoped(bucket, kind, v)
	}
	if !rs.raft.IsLeader() {
		return ErrNotLeader
	}
	if m.UID == "" {
		m.UID = newUID()
		m.CreatedAt = time.Now().UTC()
	}
	m.UpdatedAt = time.Now().UTC()
	m.Generation++
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return rs.raft.Apply(LogOp{
		Op:      "put",
		Bucket:  bucket,
		Kind:    kind,
		Project: m.Project,
		Name:    m.Name,
		Key:     string(keyFor(m.Project, m.Name)),
		Value:   raw,
	}, rs.timeout)
}

// applyTypedDelete is the analog for deletes.
func (rs *ReplicatedStore) applyTypedDelete(_ context.Context, bucket string, kind Kind, project, name string) error {
	if !rs.shouldReplicate() {
		// Fall through to whichever typed delete the embedded Store
		// has, by routing on bucket. This mirrors what the typed
		// methods already do (they each call s.del(bucket, ...) +
		// s.emit(...)) so we get free behaviour without copying.
		return rs.Store.localDelete(bucket, kind, project, name)
	}
	if !rs.raft.IsLeader() {
		return ErrNotLeader
	}
	return rs.raft.Apply(LogOp{
		Op:      "delete",
		Bucket:  bucket,
		Kind:    kind,
		Project: project,
		Name:    name,
		Key:     string(keyFor(project, name)),
	}, rs.timeout)
}

// PutContainer replicates a container write.
func (rs *ReplicatedStore) PutContainer(ctx context.Context, c *Container) error {
	return rs.applyTypedPut(ctx, "containers", KindContainer, c)
}

// DeleteContainer replicates a container delete.
func (rs *ReplicatedStore) DeleteContainer(ctx context.Context, project, name string) error {
	return rs.applyTypedDelete(ctx, "containers", KindContainer, project, name)
}

// PutFunction replicates a function write.
func (rs *ReplicatedStore) PutFunction(ctx context.Context, f *Function) error {
	return rs.applyTypedPut(ctx, "functions", KindFunction, f)
}

// DeleteFunction replicates a function delete.
func (rs *ReplicatedStore) DeleteFunction(ctx context.Context, project, name string) error {
	return rs.applyTypedDelete(ctx, "functions", KindFunction, project, name)
}

// PutBucket replicates a bucket write.
func (rs *ReplicatedStore) PutBucket(ctx context.Context, b *Bucket) error {
	return rs.applyTypedPut(ctx, "buckets", KindBucket, b)
}

// DeleteBucket replicates a bucket delete.
func (rs *ReplicatedStore) DeleteBucket(ctx context.Context, project, name string) error {
	return rs.applyTypedDelete(ctx, "buckets", KindBucket, project, name)
}

// PutAccessKey replicates an access-key write.
func (rs *ReplicatedStore) PutAccessKey(ctx context.Context, a *AccessKey) error {
	return rs.applyTypedPut(ctx, "accesskeys", KindAccessKey, a)
}

// DeleteAccessKey replicates an access-key delete.
func (rs *ReplicatedStore) DeleteAccessKey(ctx context.Context, project, name string) error {
	return rs.applyTypedDelete(ctx, "accesskeys", KindAccessKey, project, name)
}

// PutSecret replicates a secret write.
func (rs *ReplicatedStore) PutSecret(ctx context.Context, sec *Secret) error {
	return rs.applyTypedPut(ctx, "secrets", KindSecret, sec)
}

// DeleteSecret replicates a secret delete.
func (rs *ReplicatedStore) DeleteSecret(ctx context.Context, project, name string) error {
	return rs.applyTypedDelete(ctx, "secrets", KindSecret, project, name)
}

// PutEventRule replicates an event-rule write.
func (rs *ReplicatedStore) PutEventRule(ctx context.Context, r *EventRule) error {
	return rs.applyTypedPut(ctx, "eventrules", KindEventRule, r)
}

// DeleteEventRule replicates an event-rule delete.
func (rs *ReplicatedStore) DeleteEventRule(ctx context.Context, project, name string) error {
	return rs.applyTypedDelete(ctx, "eventrules", KindEventRule, project, name)
}
