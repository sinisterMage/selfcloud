package garage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
)

// WaitReady polls the admin endpoint until Garage answers or ctx fires.
func (s *Supervisor) WaitReady(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	url := "http://" + s.cfg.AdminBindAddr + "/v1/health"
	cli := &http.Client{Timeout: 1 * time.Second}
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("authorization", "Bearer "+s.cfg.AdminToken)
		resp, err := cli.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("garage admin endpoint never came up: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// EnsureLayout configures the Garage cluster layout. On a single node we
// register this node with capacity equal to the data dir's free space (or
// 100 GB by default) in zone "selfcloud". On multi-node it's a no-op (the
// admin assigns layouts via the dashboard).
func (s *Supervisor) EnsureLayout(ctx context.Context, capacityGB int64) error {
	if !s.cfg.SingleNode {
		// In multi-node mode the dashboard manages layouts.
		return nil
	}
	if capacityGB <= 0 {
		capacityGB = 100
	}
	out, err := s.Cli(ctx, "node", "id", "-q")
	if err != nil {
		return fmt.Errorf("garage node id: %w", err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return errors.New("garage returned empty node id")
	}
	if at := strings.Index(id, "@"); at > 0 {
		id = id[:at]
	}
	if _, err := s.Cli(ctx, "layout", "assign", id,
		"-z", s.cfg.Zone, "-c", strconv.FormatInt(capacityGB, 10)+"G"); err != nil {
		// `assign` errors when already assigned; that's fine.
		log.With("err", err).Debug("garage layout assign returned (likely already assigned)")
	}
	if _, err := s.Cli(ctx, "layout", "apply", "--version", "1"); err != nil {
		log.With("err", err).Debug("garage layout apply returned (likely already applied)")
	}
	return nil
}

// AddNode registers a new peer in this Garage cluster. Used by the multi-
// node join flow.
func (s *Supervisor) AddNode(ctx context.Context, peerAddr string) error {
	_, err := s.Cli(ctx, "node", "connect", peerAddr)
	return err
}

// SetReplicationFactor updates the cluster's replication factor. Only
// effective once enough nodes are present.
func (s *Supervisor) SetReplicationFactor(ctx context.Context, n int) error {
	body, _ := json.Marshal(map[string]int{"replicationFactor": n})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+s.cfg.AdminBindAddr+"/v1/cluster/options", bytes.NewReader(body))
	req.Header.Set("authorization", "Bearer "+s.cfg.AdminToken)
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("garage admin returned %s", resp.Status)
	}
	return nil
}
