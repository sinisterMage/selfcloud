// Package garage manages an embedded Garage process: writing the config,
// starting/stopping the binary, and translating selfcloud's bucket / access
// key API into the corresponding `garage` CLI calls.
package garage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
)

// Config describes how to launch Garage on this node.
type Config struct {
	DataDir          string // where Garage stores blocks
	MetadataDir      string // metadata, recommended SSD
	Binary           string // path to `garage` binary; "" => "garage" on PATH
	RPCSecret        string // 32-byte hex
	RPCBindAddr      string // host:port for rpc
	S3BindAddr       string // host:port for S3 API
	WebBindAddr      string // host:port for static web hosting
	AdminBindAddr    string // host:port for admin API (used by selfcloud)
	AdminToken       string // bearer for admin API
	ReplicationFactor int   // 1 (single-node) or 3 (cluster)
	Zone             string
	NodeID           string
	SingleNode       bool
}

// Supervisor owns the Garage child process for one selfcloud node.
type Supervisor struct {
	cfg    Config
	cmd    *exec.Cmd
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewSupervisor returns a Supervisor that has not yet been started.
func NewSupervisor(cfg Config) *Supervisor {
	if cfg.Binary == "" {
		cfg.Binary = "garage"
	}
	if cfg.Zone == "" {
		cfg.Zone = "selfcloud"
	}
	return &Supervisor{cfg: cfg}
}

const configTemplate = `metadata_dir = "{{.MetadataDir}}"
data_dir = "{{.DataDir}}"
db_engine = "lmdb"

replication_factor = {{.ReplicationFactor}}

rpc_bind_addr = "{{.RPCBindAddr}}"
rpc_public_addr = "{{.RPCBindAddr}}"
rpc_secret = "{{.RPCSecret}}"

[s3_api]
api_bind_addr = "{{.S3BindAddr}}"
s3_region = "selfcloud"
root_domain = ".s3.selfcloud.local"

[s3_web]
bind_addr = "{{.WebBindAddr}}"
root_domain = ".web.selfcloud.local"
index = "index.html"

[admin]
api_bind_addr = "{{.AdminBindAddr}}"
admin_token = "{{.AdminToken}}"
metrics_token = "{{.AdminToken}}"
`

// Render the config to garage.toml under DataDir/..; returns the path.
func (s *Supervisor) writeConfig() (string, error) {
	dir := filepath.Dir(s.cfg.MetadataDir)
	cfgPath := filepath.Join(dir, "garage.toml")
	tpl, err := template.New("garage").Parse(configTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, s.cfg); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(cfgPath, buf.Bytes(), 0o640); err != nil {
		return "", err
	}
	return cfgPath, nil
}

// Start launches the Garage process. Idempotent.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return nil
	}
	if _, err := exec.LookPath(s.cfg.Binary); err != nil {
		log.L().Warn("garage binary not found; storage features disabled until installed",
			"binary", s.cfg.Binary)
		return ErrGarageMissing
	}
	cfgPath, err := s.writeConfig()
	if err != nil {
		return fmt.Errorf("write garage config: %w", err)
	}
	for _, dir := range []string{s.cfg.DataDir, s.cfg.MetadataDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	args := []string{"-c", cfgPath, "server"}
	if s.cfg.SingleNode {
		args = []string{"-c", cfgPath, "server", "--single-node"}
	}
	cmd := exec.CommandContext(cctx, s.cfg.Binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start garage: %w", err)
	}
	s.cmd = cmd
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	log.L().Info("garage started", "pid", cmd.Process.Pid, "single_node", s.cfg.SingleNode)
	return nil
}

// Stop sends SIGTERM and waits up to 5s for graceful exit.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	cmd := s.cmd
	cancel := s.cancel
	done := s.done
	s.cmd = nil
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()
	if cmd == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
	return nil
}

// Cli runs `garage <args>` with the active config and returns stdout.
func (s *Supervisor) Cli(ctx context.Context, args ...string) ([]byte, error) {
	dir := filepath.Dir(s.cfg.MetadataDir)
	full := append([]string{"-c", filepath.Join(dir, "garage.toml")}, args...)
	cmd := exec.CommandContext(ctx, s.cfg.Binary, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("garage %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// ErrGarageMissing is returned when the `garage` binary cannot be located.
var ErrGarageMissing = errors.New("garage binary not found in PATH")
