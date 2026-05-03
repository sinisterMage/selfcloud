package api

import (
	"net/http"
	"time"

	"github.com/selfcloud/selfcloud/internal/store"
	"github.com/selfcloud/selfcloud/internal/version"
)

type joinReq struct {
	Token         string `json:"token"`
	NodeID        string `json:"nodeId"`
	AdvertiseAddr string `json:"advertiseAddr"`
	RaftAddr      string `json:"raftAddr"`
	APIAddr       string `json:"apiAddr"`
	Roles         []string `json:"roles"`
	CapacityGB    int64  `json:"capacityGB"`
}

// joinResp is the minimal view a joining node needs. We deliberately do
// NOT return the full ClusterConfig here because it carries the bootstrap
// token hash, S3 internal key, and other cluster-wide secrets unrelated
// to bringing the new node online. Garage's RPC secret + admin token are
// included only because the joining node's local Garage instance needs
// them to peer with the cluster.
type joinResp struct {
	NodeID            string       `json:"nodeId"`
	MultiNode         bool         `json:"multiNode"`
	ReplicationFactor int          `json:"replicationFactor"`
	Peers             []store.Node `json:"peers"`
	GarageRPC         string       `json:"garageRpcSecret,omitempty"`
	GarageAdmin       string       `json:"garageAdminToken,omitempty"`
	Version           string       `json:"version"`
}

// handleClusterJoin is the unauthenticated endpoint that joining nodes hit.
// We validate the join token, add them to the Raft voter set, and return
// enough cluster state for them to bring their local Garage online.
func (s *Server) handleClusterJoin(w http.ResponseWriter, r *http.Request) {
	var body joinReq
	if err := decodeJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if body.NodeID == "" || body.RaftAddr == "" {
		httpError(w, 400, "nodeId and raftAddr are required")
		return
	}
	if err := s.cluster.Consume(r.Context(), body.Token, body.NodeID); err != nil {
		httpError(w, 401, err.Error())
		return
	}
	if s.raft != nil {
		if err := s.raft.AddVoter(body.NodeID, body.RaftAddr); err != nil {
			httpError(w, 500, "raft add voter: "+err.Error())
			return
		}
	}
	roles := body.Roles
	if len(roles) == 0 {
		roles = []string{"control", "compute", "storage"}
	}
	storeRoles := make([]store.NodeRole, 0, len(roles))
	for _, r := range roles {
		storeRoles = append(storeRoles, store.NodeRole(r))
	}
	node := &store.Node{
		Meta:        store.Meta{Project: "system", Name: body.NodeID},
		Address:     body.AdvertiseAddr,
		APIAddress:  body.APIAddr,
		RaftAddress: body.RaftAddr,
		Roles:       storeRoles,
		CapacityGB:  body.CapacityGB,
		Version:     version.String(),
		Status:      store.Status{Phase: store.PhaseRunning, UpdatedAt: time.Now().UTC()},
		LastSeenAt:  time.Now().UTC(),
	}
	if err := s.store.PutNode(r.Context(), node); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	cfg, err := s.store.GetCluster(r.Context())
	if err != nil {
		httpError(w, 500, "load cluster: "+err.Error())
		return
	}
	cfg.MultiNode = true
	if err := s.store.PutCluster(r.Context(), cfg); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	peers, _ := s.store.ListNodes(r.Context())
	writeJSON(w, 200, joinResp{
		NodeID:            body.NodeID,
		MultiNode:         cfg.MultiNode,
		ReplicationFactor: cfg.ReplicationFactor,
		Peers:             peers,
		GarageRPC:         cfg.GarageRPCSecret,
		GarageAdmin:       cfg.GarageAdminToken,
		Version:           version.String(),
	})
}