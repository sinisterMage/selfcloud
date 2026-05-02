package store

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

// RaftConfig configures the Raft node.
type RaftConfig struct {
	NodeID    string
	BindAddr  string
	DataDir   string
	Bootstrap bool
}

// Raft wraps hashicorp/raft so the rest of the codebase doesn't have to
// import it directly.
type Raft struct {
	r   *raft.Raft
	fsm *FSM
}

// NewRaft brings up a Raft node and wires it to the given FSM.
func NewRaft(cfg RaftConfig, fsm *FSM) (*Raft, error) {
	if cfg.NodeID == "" {
		return nil, fmt.Errorf("raft node id is required")
	}
	conf := raft.DefaultConfig()
	conf.LocalID = raft.ServerID(cfg.NodeID)
	conf.SnapshotThreshold = 1024
	conf.LogLevel = "WARN"

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, err
	}
	logStore, err := raftboltdb.NewBoltStore(filepath.Join(cfg.DataDir, "raft-log.db"))
	if err != nil {
		return nil, fmt.Errorf("raft log store: %w", err)
	}
	stableStore, err := raftboltdb.NewBoltStore(filepath.Join(cfg.DataDir, "raft-stable.db"))
	if err != nil {
		return nil, fmt.Errorf("raft stable store: %w", err)
	}
	snaps, err := raft.NewFileSnapshotStore(cfg.DataDir, 3, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("raft snapshots: %w", err)
	}

	advAddr, err := net.ResolveTCPAddr("tcp", cfg.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve raft bind: %w", err)
	}
	// hashicorp/raft refuses to start when the resolved advertise address
	// is loopback or unspecified. For a single-node cluster we don't need
	// the address to be reachable from the outside, so swap in a dummy.
	if advAddr.IP == nil || advAddr.IP.IsUnspecified() || advAddr.IP.IsLoopback() {
		advAddr = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: advAddr.Port}
	}
	transport, err := raft.NewTCPTransportWithConfig(cfg.BindAddr, advAddr, &raft.NetworkTransportConfig{
		ServerAddressProvider: nil,
		MaxPool:               3,
		Timeout:               10 * time.Second,
		Logger:                nil,
	})
	if err != nil {
		return nil, fmt.Errorf("raft transport: %w", err)
	}

	r, err := raft.NewRaft(conf, fsm, logStore, stableStore, snaps, transport)
	if err != nil {
		return nil, fmt.Errorf("raft.NewRaft: %w", err)
	}

	if cfg.Bootstrap {
		hasState, err := raft.HasExistingState(logStore, stableStore, snaps)
		if err != nil {
			return nil, err
		}
		if !hasState {
			boot := raft.Configuration{
				Servers: []raft.Server{{
					ID:      conf.LocalID,
					Address: transport.LocalAddr(),
				}},
			}
			if f := r.BootstrapCluster(boot); f.Error() != nil {
				return nil, fmt.Errorf("bootstrap raft: %w", f.Error())
			}
		}
	}

	return &Raft{r: r, fsm: fsm}, nil
}

// IsLeader reports whether this node is the cluster leader.
func (r *Raft) IsLeader() bool {
	return r.r.State() == raft.Leader
}

// LeaderAddr returns the current leader's transport address (or empty).
func (r *Raft) LeaderAddr() string {
	addr, _ := r.r.LeaderWithID()
	return string(addr)
}

// Apply submits a log entry. Errors out fast if not leader.
func (r *Raft) Apply(op LogOp, timeout time.Duration) error {
	if r.r.State() != raft.Leader {
		return ErrNotLeader
	}
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	f := r.r.Apply(data, timeout)
	if err := f.Error(); err != nil {
		return err
	}
	if e, ok := f.Response().(error); ok && e != nil {
		return e
	}
	return nil
}

// AddVoter promotes a new node to a voting member.
func (r *Raft) AddVoter(id, addr string) error {
	f := r.r.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 10*time.Second)
	return f.Error()
}

// RemoveServer removes a node from the cluster.
func (r *Raft) RemoveServer(id string) error {
	f := r.r.RemoveServer(raft.ServerID(id), 0, 10*time.Second)
	return f.Error()
}

// Servers returns the current Raft membership.
func (r *Raft) Servers() ([]raft.Server, error) {
	f := r.r.GetConfiguration()
	if err := f.Error(); err != nil {
		return nil, err
	}
	return f.Configuration().Servers, nil
}

// Shutdown gracefully stops the Raft node.
func (r *Raft) Shutdown() error {
	return r.r.Shutdown().Error()
}
