//go:build linux

package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
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
	dataDir   string // host directory used as the source root for SecretMount binds
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
// Idempotent: if a container with the same id already exists in containerd
// we return its status instead of erroring.
//
// dataDir, when non-empty, is the local state directory used to materialise
// secret-mount files (see reconcile.go writeSecretFile). The reconciler
// passes its own dataDir through r.dataDir; tests set it to empty.
func (r *CtrRuntime) Start(ctx context.Context, c *store.Container) (*store.ContainerStatus, error) {
	id := ctrTaskID(c)

	// If the container already exists, treat as a no-op success.
	if existing, _, err := r.run(ctx, "containers", "list", "-q"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(existing)), "\n") {
			if strings.TrimSpace(line) == id {
				return &store.ContainerStatus{
					Status: store.Status{
						Phase:     store.PhaseRunning,
						Message:   "already running",
						UpdatedAt: time.Now().UTC(),
					},
					ContainerdID: id,
					Image:        c.Image,
				}, nil
			}
		}
	}

	if _, stderr, err := r.run(ctx, "images", "pull", c.Image); err != nil {
		return nil, fmt.Errorf("pull %s: %w (%s)", c.Image, err, strings.TrimSpace(string(stderr)))
	}
	args := []string{"run", "-d"}
	for k, v := range c.Env {
		args = append(args, "--env", k+"="+v)
	}
	for _, p := range c.Ports {
		args = append(args, "--label", fmt.Sprintf("selfcloud.port.%d=%d/%s", p.Container, p.Host, p.Protocol))
	}
	// Volumes (host bind mounts; S3-backed bucket mounts are intentionally
	// out of scope here — those need an external rclone helper).
	for _, v := range c.Volumes {
		if v.Bucket != "" || v.MountPath == "" {
			continue
		}
		opts := "rbind,rw"
		if v.ReadOnly {
			opts = "rbind,ro"
		}
		// Bind the same path on the host into the container at MountPath.
		// This is the simplest contract that keeps host-side ownership
		// concerns visible to the operator.
		args = append(args, "--mount",
			fmt.Sprintf("type=bind,src=%s,dst=%s,options=%s", v.MountPath, v.MountPath, opts))
	}
	// Secret file mounts. The reconciler already wrote the plaintext to
	// <dataDir>/secret-mounts/<uid>/<basename(MountPath)> (see
	// secrets.go). Bind that file into the container at MountPath, ro.
	if r.dataDir != "" {
		uid := c.Meta.UID
		if uid == "" {
			uid = c.Meta.Project + "-" + c.Meta.Name
		}
		for _, sm := range c.SecretMounts {
			if sm.MountPath == "" {
				continue
			}
			name := filepath.Base(sm.MountPath)
			if name == "/" || name == "" || name == "." {
				name = "secret"
			}
			src := filepath.Join(r.dataDir, "secret-mounts", uid, name)
			args = append(args, "--mount",
				fmt.Sprintf("type=bind,src=%s,dst=%s,options=rbind,ro", src, sm.MountPath))
		}
	}
	// Resource limits.
	if c.Resources.MemoryMB > 0 {
		args = append(args, "--memory-limit", strconv.FormatInt(c.Resources.MemoryMB*1024*1024, 10))
	}
	if c.Resources.CPUMillicores > 0 {
		// containerd's --cpu-quota/--cpu-period model. period=100000us by
		// default; quota = period * (millicores/1000).
		period := int64(100000)
		quota := period * c.Resources.CPUMillicores / 1000
		if quota < 1000 {
			quota = 1000
		}
		args = append(args,
			"--cpu-period", strconv.FormatInt(period, 10),
			"--cpu-quota", strconv.FormatInt(quota, 10),
		)
	}
	args = append(args, c.Image, id)
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
		ContainerdID: id,
		StartedAt:    time.Now().UTC(),
		Image:        c.Image,
	}, nil
}

// SetDataDir sets the host directory under which secret-mount files are
// staged (see internal/runtime/container/secrets.go). The reconciler
// calls this once on startup so bind-mount sources resolve correctly.
func (r *CtrRuntime) SetDataDir(dir string) { r.dataDir = dir }

// IsRunning reports whether containerd has a live task for this container.
// The reconciler uses it to drive RestartPolicy.
func (r *CtrRuntime) IsRunning(ctx context.Context, c *store.Container) (bool, error) {
	out, _, err := r.run(ctx, "tasks", "ls")
	if err != nil {
		return false, err
	}
	id := ctrTaskID(c)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 3 && fields[0] == id && fields[2] == "RUNNING" {
			return true, nil
		}
	}
	return false, nil
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

// Logs streams the task's stdout (and stderr, merged) into w. When
// follow is true, the underlying `ctr tasks attach` runs until the task
// exits or ctx is cancelled. When follow is false, we attach for a
// short window so the caller gets recent output without hanging.
func (r *CtrRuntime) Logs(ctx context.Context, c *store.Container, follow bool, w io.Writer) error {
	full := append([]string{"--address", r.socket, "--namespace", r.namespace},
		"tasks", "attach", ctrTaskID(c))
	if !follow {
		// Bound the attach so non-follow callers don't block forever.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, r.binary, full...)
	cmd.Stdout = w
	cmd.Stderr = w
	err := cmd.Run()
	// Cancellation of a non-follow attach surfaces as a context.* error;
	// that's expected, not a failure.
	if !follow && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return nil
	}
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
