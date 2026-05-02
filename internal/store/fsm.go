package store

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/raft"
)

// FSM wraps Store as a Raft finite state machine. On a single-node cluster
// the FSM still goes through Apply so that callers can pretend a multi-node
// future is already here. Snapshots dump the BoltDB to bytes; restores
// replace the underlying database.
type FSM struct {
	mu    sync.Mutex
	store *Store
}

// NewFSM creates an FSM.
func NewFSM(s *Store) *FSM { return &FSM{store: s} }

// LogOp encodes a single mutation in the Raft log. Replicas use it to apply
// the same change locally.
type LogOp struct {
	Op     string          `json:"op"`     // "put" | "delete"
	Bucket string          `json:"bucket"` // "containers", "buckets", ...
	Key    string          `json:"key"`
	Value  json.RawMessage `json:"value,omitempty"`
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
		return f.store.put(op.Bucket, []byte(op.Key), json.RawMessage(op.Value))
	case "delete":
		return f.store.del(op.Bucket, []byte(op.Key))
	default:
		return fmt.Errorf("unknown op %q", op.Op)
	}
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
