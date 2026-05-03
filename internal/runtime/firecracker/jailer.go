//go:build linux

package firecracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/runtime/firecracker/protocol"
	"github.com/selfcloud/selfcloud/internal/runtime/wasm"
	"github.com/selfcloud/selfcloud/internal/store"
)

// Jailer drives Firecracker via the firecracker binary on the host. Each
// invocation spawns its own VM; per-function snapshots can be layered on
// later via (j *Jailer).snapshot/restore.
//
// Layout under DataDir:
//
//	firecracker/
//	  templates/
//	    kernel/vmlinux
//	    rootfs/<name>.ext4
//	  fns/<project>/<name>/code.tar       <- user upload
//	  jail/<uid>/                         <- per-invocation chroot
//	    vm.json
//	    fc.sock                           <- firecracker API socket
//	    v.sock                            <- vsock device endpoint (host)
//	    v.sock_5252                       <- agent listener (auto-created)
//	    code.img                          <- raw drive (just code.tar bytes)
type Jailer struct {
	dataDir   string
	templates map[string]Template
	mu        sync.Mutex
	// snapshots maps function key -> path to a saved snapshot. Populated by
	// snapshot()/restore() (currently TODO).
	snapshots map[string]string
}

// NewJailer wires a jailer-backed runtime.
func NewJailer(dataDir string) (*Jailer, error) {
	if _, err := exec.LookPath("firecracker"); err != nil {
		return nil, ErrUnsupported
	}
	for _, dir := range []string{
		filepath.Join(dataDir, "jail"),
		filepath.Join(dataDir, "templates", "kernel"),
		filepath.Join(dataDir, "templates", "rootfs"),
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	tmpls := map[string]Template{}
	for _, t := range DefaultTemplates(filepath.Join(dataDir, "templates")) {
		tmpls[t.Name] = t
	}
	return &Jailer{
		dataDir:   dataDir,
		templates: tmpls,
		snapshots: map[string]string{},
	}, nil
}

// Templates returns the registered rootfs templates.
func (j *Jailer) Templates() []Template {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Template, 0, len(j.templates))
	for _, t := range j.templates {
		out = append(out, t)
	}
	return out
}

func (j *Jailer) Deploy(_ context.Context, fn *store.Function, code []byte) error {
	dir := j.fnDir(fn)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "code.tar"), code, 0o640)
}

func (j *Jailer) Remove(_ context.Context, fn *store.Function) error {
	j.mu.Lock()
	delete(j.snapshots, fnKey(fn))
	j.mu.Unlock()
	return os.RemoveAll(j.fnDir(fn))
}

// Invoke spins up a microVM, ships the code drive, talks to the in-guest
// agent over vsock, and tears it down. Snapshot/restore is a TODO.
func (j *Jailer) Invoke(ctx context.Context, fn *store.Function, req *wasm.InvokeRequest) (*wasm.InvokeResponse, error) {
	tplName := fn.Handler
	if tplName == "" {
		tplName = "node-22"
	}
	tpl, ok := j.templates[tplName]
	if !ok {
		return nil, fmt.Errorf("unknown rootfs template %q", tplName)
	}
	if _, err := os.Stat(tpl.KernelPath); err != nil {
		return nil, fmt.Errorf("kernel not found at %s (run `make firecracker-templates`)", tpl.KernelPath)
	}
	if _, err := os.Stat(tpl.RootFSPath); err != nil {
		return nil, fmt.Errorf("rootfs not found at %s (run `make firecracker-templates`)", tpl.RootFSPath)
	}

	// Future warm-start hook: try to restore from an existing snapshot.
	if snap, ok := j.lookupSnapshot(fn); ok {
		if resp, err := j.restore(ctx, fn, snap, req); err == nil {
			return resp, nil
		}
		// fall through to cold start on restore failure
	}

	uid := fn.Meta.UID + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	chroot := filepath.Join(j.dataDir, "jail", uid)
	if err := os.MkdirAll(chroot, 0o750); err != nil {
		return nil, err
	}
	defer os.RemoveAll(chroot)

	codePath := filepath.Join(j.fnDir(fn), "code.tar")
	codeImg := filepath.Join(chroot, "code.img")
	if _, err := os.Stat(codePath); err == nil {
		// Pad to 512-byte sectors so the kernel block layer is happy.
		if err := copyAndPad(codePath, codeImg, 512); err != nil {
			return nil, fmt.Errorf("stage code drive: %w", err)
		}
	} else {
		// Empty drive (still attached so /dev/vdb exists in the guest).
		if err := os.WriteFile(codeImg, make([]byte, 4096), 0o640); err != nil {
			return nil, err
		}
	}

	apiSock := filepath.Join(chroot, "fc.sock")
	vsockUDS := filepath.Join(chroot, "v.sock")
	configPath := filepath.Join(chroot, "vm.json")

	cfg := buildVMConfig(tpl, codeImg, vsockUDS, fnMemMB(fn), 1)
	cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(configPath, cfgBytes, 0o640); err != nil {
		return nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, fnTimeout(fn)+10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, "firecracker",
		"--api-sock", apiSock,
		"--config-file", configPath,
	)
	cmd.Stderr = newPrefixWriter("[firecracker] ")
	cmd.Stdout = cmd.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start firecracker: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan struct{})
			go func() { _, _ = cmd.Process.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			}
		}
	}()

	// Poll-connect to the in-guest agent. Firecracker creates the host
	// socket file (vsockUDS) once it brings up the vsock device; the agent
	// inside the guest then exposes per-port endpoints at <uds>_<port>.
	conn, err := dialAgent(cctx, vsockUDS+"_"+strconv.Itoa(protocol.VsockPort), 8*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to agent: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(fnTimeout(fn)))

	// Build and send the request envelope.
	mode, _ := fn.Env["MODE"]
	preq := &protocol.Request{
		Method:  req.Method,
		Path:    req.Path,
		Headers: req.Headers,
		Body:    req.Body,
		Env:     mergeEnv(fn.Env, req.Env),
		Mode:    mode,
	}
	if preq.Env == nil {
		preq.Env = map[string]string{}
	}
	preq.Env["SELFCLOUD_TIMEOUT_MS"] = strconv.Itoa(fnTimeoutMS(fn))

	start := time.Now()
	if err := protocol.WriteFrame(conn, preq); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}
	var presp protocol.Response
	if err := protocol.ReadFrame(conn, &presp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	elapsed := time.Since(start)

	h := http.Header{}
	for k, vs := range presp.Headers {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	if h.Get("content-type") == "" {
		h.Set("content-type", "application/octet-stream")
	}
	status := presp.Status
	if status == 0 {
		status = 200
	}
	if presp.Error != "" {
		log.With("fn", fn.Meta.Name, "err", presp.Error).Warn("firecracker: agent reported error")
	}

	return &wasm.InvokeResponse{
		Status:  status,
		Headers: h,
		Body:    presp.Body,
		Logs:    presp.Logs,
		Elapsed: elapsed,
	}, nil
}

func (j *Jailer) Close() error { return nil }

func (j *Jailer) fnDir(fn *store.Function) string {
	return filepath.Join(j.dataDir, "fns", fn.Meta.Project, fn.Meta.Name)
}

// snapshot creates a Firecracker snapshot for fn. NOT IMPLEMENTED yet —
// hook is here so Invoke can later try restore() before falling back to a
// cold boot.
//
//nolint:unused // wired for future warm-start work
func (j *Jailer) snapshot(_ context.Context, _ *store.Function) error {
	return errors.New("snapshot: not implemented")
}

// restore loads a previously-saved snapshot and runs the invocation
// against it. NOT IMPLEMENTED yet.
func (j *Jailer) restore(_ context.Context, _ *store.Function, _ string, _ *wasm.InvokeRequest) (*wasm.InvokeResponse, error) {
	return nil, errors.New("restore: not implemented")
}

func (j *Jailer) lookupSnapshot(fn *store.Function) (string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	s, ok := j.snapshots[fnKey(fn)]
	return s, ok
}

func fnKey(fn *store.Function) string { return fn.Meta.Project + "/" + fn.Meta.Name }

func fnMemMB(fn *store.Function) int {
	if fn.MemoryMB > 0 {
		return fn.MemoryMB
	}
	return 128
}

func fnTimeout(fn *store.Function) time.Duration {
	return time.Duration(fnTimeoutMS(fn)) * time.Millisecond
}

func fnTimeoutMS(fn *store.Function) int {
	if fn.TimeoutMS > 0 {
		return fn.TimeoutMS
	}
	return 5000
}

func mergeEnv(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// dialAgent waits for the vsock UDS endpoint to appear and connects.
// Firecracker exposes per-port endpoints by appending `_<port>` to the
// configured UDS path.
func dialAgent(ctx context.Context, path string, max time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(max)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for %s", path)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c, err := net.Dial("unix", path)
		if err == nil {
			// Firecracker requires us to send "CONNECT <port>\n" as a
			// header before the stream is wired through to the guest.
			if _, werr := c.Write([]byte(fmt.Sprintf("CONNECT %d\n", protocol.VsockPort))); werr == nil {
				// Read the OK\n reply.
				buf := make([]byte, 32)
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				if n, _ := c.Read(buf); n >= 2 && string(buf[:2]) == "OK" {
					_ = c.SetReadDeadline(time.Time{})
					return c, nil
				}
			}
			_ = c.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// buildVMConfig produces the JSON Firecracker reads at startup. Note that
// vsock + drives + machine-config are all configured here; networking is
// optional and gated separately.
func buildVMConfig(tpl Template, codeImg, vsockUDS string, memMB, vcpus int) map[string]any {
	return map[string]any{
		"boot-source": map[string]string{
			"kernel_image_path": tpl.KernelPath,
			"boot_args":         tpl.BootArgs,
		},
		"drives": []map[string]any{
			{
				"drive_id":       "rootfs",
				"path_on_host":   tpl.RootFSPath,
				"is_root_device": true,
				"is_read_only":   true,
			},
			{
				"drive_id":       "code",
				"path_on_host":   codeImg,
				"is_root_device": false,
				"is_read_only":   true,
			},
		},
		"machine-config": map[string]any{
			"vcpu_count":   vcpus,
			"mem_size_mib": memMB,
		},
		"vsock": map[string]any{
			"vsock_id":  "selfcloud-vsock",
			"guest_cid": 3,
			"uds_path":  vsockUDS,
		},
	}
}

// copyAndPad copies src to dst and pads to a multiple of `block` bytes so
// the in-guest kernel can attach it as a block device cleanly.
func copyAndPad(src, dst string, block int) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	n, err := io.Copy(out, in)
	if err != nil {
		return err
	}
	if rem := int(n) % block; rem != 0 {
		pad := make([]byte, block-rem)
		if _, err := out.Write(pad); err != nil {
			return err
		}
	}
	return nil
}

// prefixWriter prepends a prefix to each newline so firecracker logs are
// distinguishable from the rest of selfcloud's output.
type prefixWriter struct {
	prefix string
	buf    []byte
}

func newPrefixWriter(prefix string) *prefixWriter { return &prefixWriter{prefix: prefix} }

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := -1
		for j, c := range p.buf {
			if c == '\n' {
				i = j
				break
			}
		}
		if i < 0 {
			return len(b), nil
		}
		line := string(p.buf[:i])
		p.buf = p.buf[i+1:]
		log.L().Debug(p.prefix + line)
	}
}
