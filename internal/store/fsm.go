package store

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/raft"
)

// FSM wraps Store as a Raft finite state machine. Every write the cluster
// agrees on flows through Apply, so single-node and multi-node share the
// same write path: the leader's ReplicatedStore.Put*/Delete* mints UIDs
// and timestamps, ships the post-meta bytes via Raft, and the FSM on
// every committing node (including the leader) writes the bucket + emits
// a typed Event. Snapshots dump the BoltDB to bytes; restores replace
// the underlying database.
type FSM struct {
	mu    sync.Mutex
	store *Store
}

// NewFSM creates an FSM.
func NewFSM(s *Store) *FSM { return &FSM{store: s} }

// LogOp encodes a single mutation in the Raft log. Replicas use it to
// apply the same change locally. Kind lets the FSM emit a typed Event so
// reconcilers across the cluster see the change in the same shape they
// would on a local-only write.
type LogOp struct {
	Op      string          `json:"op"`     // "put" | "delete"
	Bucket  string          `json:"bucket"` // BoltDB bucket: containers, buckets, ...
	Kind    Kind            `json:"kind,omitempty"`
	Project string          `json:"project,omitempty"`
	Name    string          `json:"name,omitempty"`
	Key     string          `json:"key"`
	Value   json.RawMessage `json:"value,omitempty"`
}

// Apply executes a single committed log entry against the local store.
func (f *FSM) Apply(l *raft.Log) interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	var op LogOp
	if err := json.Unmarshal(l.Data, &op); err != nil {
		return fmt.Errorf("decode log: %w", err)
	}
	return f.apply(op)
}

func (f *FSM) apply(op LogOp) error {
	switch op.Op {
	case "put":
		if err := f.store.put(op.Bucket, []byte(op.Key), json.RawMessage(op.Value)); err != nil {
			return err
		}
		// Best-effort typed decode so subscribers (reconcilers, the bus)
		// see the same Event shape they'd see for a local-only write.
		var typed any
		if op.Kind != "" {
			if v, derr := decodeKind(op.Kind, op.Value); derr == nil {
				typed = v
			}
		}
		f.store.emit(Event{Kind: op.Kind, Op: "put", Project: op.Project, Name: op.Name, Value: typed})
		return nil
	case "delete":
		if err := f.store.del(op.Bucket, []byte(op.Key)); err != nil {
			return err
		}
		f.store.emit(Event{Kind: op.Kind, Op: "delete", Project: op.Project, Name: op.Name})
		return nil
	default:
		return fmt.Errorf("unknown op %q", op.Op)
	}
}

// decodeKind reconstructs a typed value from raw JSON for a known Kind.
// Used by FSM.Apply so subscribers see Event.Value with the same Go type
// they get on a local write.
func decodeKind(k Kind, raw json.RawMessage) (any, error) {
	switch k {
	case KindProject:
		var v Project
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindContainer:
		var v Container
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindFunction:
		var v Function
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindBucket:
		var v Bucket
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindAccessKey:
		var v AccessKey
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindSecret:
		var v Secret
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindEventRule:
		var v EventRule
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindCluster:
		var v ClusterConfig
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindNode:
		var v Node
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindUser:
		var v User
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindToken:
		var v Token
		err := json.Unmarshal(raw, &v)
		return &v, err
	case KindBuild:
		var v Build
		err := json.Unmarshal(raw, &v)
		return &v, err
	}
	return nil, fmt.Errorf("unknown kind %q", k)
}

// Snapshot returns a snapshot of the current state.
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dump, err := dumpBolt(f.store)
	if err != nil {
		return nil, err
	}
	return &snapshotBuffer{data: dump, created: time.Now().UTC()}, nil
}

// Restore replaces the database content from a snapshot stream.
func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return restoreBolt(f.store, data)
}

type snapshotBuffer struct {
	data    []byte
	created time.Time
}

func (s *snapshotBuffer) Persist(sink raft.SnapshotSink) error {
	if _, err := sink.Write(s.data); err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *snapshotBuffer) Release() {}
