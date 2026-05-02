// Package config holds the runtime configuration for a selfcloud node.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is loaded once at startup from CLI flags + env vars.
// Files on disk (TLS, BoltDB, Garage state, ...) all live under DataDir.
type Config struct {
	NodeID         string
	DataDir        string
	BindAddr       string
	AdvertiseAddr  string
	APIAddr        string
	RaftAddr       string
	GossipAddr     string
	GarageRPCAddr  string
	S3Addr         string
	IngressAddr    string
	Dev            bool
	LogLevel       string
	TLSCertFile    string
	TLSKeyFile     string
	DisableTLS     bool
	JoinAddr       string
	JoinToken      string
	Bootstrap      bool
	ContainerdSock string
	UIDistDir      string
}

// Default returns sensible defaults for a single-node deployment.
func Default() *Config {
	return &Config{
		DataDir:        "/var/lib/selfcloud",
		BindAddr:       "0.0.0.0",
		AdvertiseAddr:  "",
		APIAddr:        "0.0.0.0:8443",
		RaftAddr:       "127.0.0.1:7000",
		GossipAddr:     "127.0.0.1:7001",
		GarageRPCAddr:  "0.0.0.0:3901",
		S3Addr:         "0.0.0.0:3900",
		IngressAddr:    "0.0.0.0:8080",
		LogLevel:       "info",
		ContainerdSock: "/run/containerd/containerd.sock",
	}
}

// Path returns DataDir + path joined.
func (c *Config) Path(parts ...string) string {
	all := append([]string{c.DataDir}, parts...)
	return filepath.Join(all...)
}

// Validate checks that the config is internally consistent and that the data
// directory is usable.
func (c *Config) Validate() error {
	if c.DataDir == "" {
		return fmt.Errorf("data-dir must be set")
	}
	if !strings.Contains(c.APIAddr, ":") {
		return fmt.Errorf("api-addr must be host:port, got %q", c.APIAddr)
	}
	if err := os.MkdirAll(c.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	for _, sub := range []string{"raft", "tls", "garage", "containers", "functions", "tmp"} {
		if err := os.MkdirAll(filepath.Join(c.DataDir, sub), 0o750); err != nil {
			return fmt.Errorf("create %s dir: %w", sub, err)
		}
	}
	return nil
}
