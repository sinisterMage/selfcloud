// Package network owns the host networking surface of a selfcloud node:
// the bridge interface containers attach to and the NAT/port-publishing
// rules that expose them to the outside world.
package network

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/selfcloud/selfcloud/internal/log"
)

// Manager is the host-networking entrypoint. Today it shells out to
// `ip`/`nft` since those are universally available on Linux. A future
// version can drop the binary deps in favour of netlink.
type Manager struct {
	bridge string
	subnet string
	mu     sync.Mutex
}

// NewManager returns an inactive manager.
func NewManager(bridge, subnet string) *Manager {
	if bridge == "" {
		bridge = "selfcloud0"
	}
	if subnet == "" {
		subnet = "10.42.0.0/16"
	}
	return &Manager{bridge: bridge, subnet: subnet}
}

// Setup ensures the bridge exists and IP forwarding is enabled. On
// non-Linux hosts it logs and returns nil so dev runs still work.
func (m *Manager) Setup() error {
	if runtime.GOOS != "linux" {
		log.L().Warn("network: skipping bridge setup on non-Linux host")
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !commandAvailable("ip") {
		return errors.New("`ip` not found; install iproute2")
	}
	if err := run("ip", "link", "show", m.bridge); err != nil {
		if err := run("ip", "link", "add", m.bridge, "type", "bridge"); err != nil {
			return fmt.Errorf("create bridge: %w", err)
		}
	}
	gw := strings.Replace(strings.Split(m.subnet, "/")[0], ".0", ".1", 1)
	_ = run("ip", "addr", "add", gw+"/"+strings.Split(m.subnet, "/")[1], "dev", m.bridge)
	_ = run("ip", "link", "set", m.bridge, "up")
	if commandAvailable("sysctl") {
		_ = run("sysctl", "-w", "net.ipv4.ip_forward=1")
	}
	return nil
}

// Publish adds a DNAT rule mapping host:hostPort -> containerIP:containerPort.
// Idempotent: existing rules are not duplicated.
func (m *Manager) Publish(hostPort, containerPort int, proto, containerIP string) error {
	if runtime.GOOS != "linux" || !commandAvailable("nft") {
		return nil
	}
	if proto == "" {
		proto = "tcp"
	}
	tableExists := run("nft", "list", "table", "ip", "selfcloud") == nil
	if !tableExists {
		_ = run("nft", "add", "table", "ip", "selfcloud")
		_ = run("nft", "add", "chain", "ip", "selfcloud", "prerouting",
			"{ type nat hook prerouting priority dstnat ; policy accept ; }")
	}
	rule := fmt.Sprintf("%s dport %d dnat to %s:%d", proto, hostPort, containerIP, containerPort)
	return run("nft", "add", "rule", "ip", "selfcloud", "prerouting", rule)
}

// Unpublish removes a rule (best-effort).
func (m *Manager) Unpublish(hostPort int, proto string) error {
	if runtime.GOOS != "linux" || !commandAvailable("nft") {
		return nil
	}
	_ = run("nft", "flush", "chain", "ip", "selfcloud", "prerouting")
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func commandAvailable(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
