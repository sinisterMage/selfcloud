//go:build linux

package container

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/selfcloud/selfcloud/internal/store"
)

// CtrRuntime is a thin shim that drives a host's containerd via the `ctr`
// CLI. It intentionally avoids importing github.com/containerd/containerd
// directly because that dependency tree is heavy (≈400 MiB of Go modules)
// and forces every developer build to pull it.
//
// For most operations we use the default namespace `selfcloud`. Snapshots,
// CNI networking and sophisticated lifecycle management are left to higher
// level callers; this layer is intentionally thin.
type CtrRuntime struct {
	binary    string // path to `ctr` (resolved by exec.LookPath)
	socket    string // containerd unix socket
	namespace string
}

// NewCtr returns a CtrRuntime if `ctr` is on PATH, otherwise an error.
func NewCtr(socket, namespace string) (*CtrRuntime, error) {
	bin, err := exec.LookPath("ctr")
	if err != nil {
		return nil, fmt.Errorf("ctr not found in PATH: %w", err)
	}
	if namespace == "" {
		namespace = "selfcloud"
	}
	if socket == "" {
		socket = "/run/containerd/containerd.sock"
	}
	return &CtrRuntime{binary: bin, socket: socket, namespace: namespace}, nil
}

func (r *CtrRuntime) run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	full := append([]string{"--address", r.socket, "--namespace", r.namespace}, args...)
	cmd := exec.CommandContext(ctx, r.binary, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Start pulls the image (best effort) and runs the container detached.
func (r *CtrRuntime) Start(ctx context.Context, c *store.Container) (*store.ContainerStatus, error) {
	if _, _, err := r.run(ctx, "images", "pull", c.Image); err != nil {
		return nil, fmt.Errorf("pull %s: %w", c.Image, err)
	}
	args := []string{"run", "-d"}
	for k, v := range c.Env {
		args = append(args, "--env", k+"="+v)
	}
	for _, p := range c.Ports {
		args = append(args, "--label", fmt.Sprintf("selfcloud.port.%d=%d/%s", p.Container, p.Host, p.Protocol))
	}
	args = append(args, c.Image, ctrTaskID(c))
	args = append(args, c.Command...)
	args = append(args, c.Args...)
	if _, stderr, err := r.run(ctx, args...); err != nil {
		return nil, fmt.Errorf("run: %w (%s)", err, strings.TrimSpace(string(stderr)))
	}
	return &store.ContainerStatus{
		Status: store.Status{
			Phase:     store.PhaseRunning,
			Message:   "started",
			UpdatedAt: time.Now().UTC(),
		},
		ContainerdID: ctrTaskID(c),
		StartedAt:    time.Now().UTC(),
		Image:        c.Image,
	}, nil
}

func ctrTaskID(c *store.Container) string {
	return "selfcloud-" + c.Meta.Project + "-" + c.Meta.Name
}

func (r *CtrRuntime) Stop(ctx context.Context, c *store.Container) error {
	_, _, err := r.run(ctx, "tasks", "kill", "--signal", "SIGTERM", ctrTaskID(c))
	return err
}

func (r *CtrRuntime) Remove(ctx context.Context, c *store.Container) error {
	_, _, _ = r.run(ctx, "tasks", "delete", ctrTaskID(c))
	_, _, err := r.run(ctx, "containers", "delete", ctrTaskID(c))
	return err
}

func (r *CtrRuntime) Logs(ctx context.Context, c *store.Container, follow bool, w io.Writer) error {
	args := []string{"tasks", "attach", ctrTaskID(c)}
	if follow {
		args = []string{"tasks", "ls"}
	}
	stdout, _, err := r.run(ctx, args...)
	if err != nil {
		return err
	}
	_, err = w.Write(stdout)
	return err
}

func (r *CtrRuntime) Exec(ctx context.Context, c *store.Container, cmd []string, _ io.Reader, stdout, stderr io.Writer) error {
	args := append([]string{"tasks", "exec", "--exec-id", fmt.Sprintf("exec-%d", time.Now().UnixNano()), ctrTaskID(c)}, cmd...)
	full := append([]string{"--address", r.socket, "--namespace", r.namespace}, args...)
	c2 := exec.CommandContext(ctx, r.binary, full...)
	c2.Stdout = stdout
	c2.Stderr = stderr
	return c2.Run()
}

func (r *CtrRuntime) List(ctx context.Context) ([]string, error) {
	stdout, _, err := r.run(ctx, "containers", "list", "-q")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

func (r *CtrRuntime) Close() error { return nil }
