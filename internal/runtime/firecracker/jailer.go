//go:build linux

package firecracker

import (
	"bytes"
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

// Jailer drives Firecracker via the firecracker binary on the host.
//
// First invocation of a function runs cold (kernel + rootfs boot, agent
// vsock handshake). After a successful warm-up, the Jailer asks
// Firecracker to take a memory + state snapshot under
// snapshots/<fnKey>/ and the next invocation restores from it. This
// turns subsequent cold-starts into a sub-100ms restore-and-resume.
//
// Layout under DataDir:
//
//	firecracker/
//	  templates/
//	    kernel/vmlinux
//	    rootfs/<name>.ext4
//	  fns/<project>/<name>/code.tar       <- user upload
//	  snapshots/<project>-<name>/         <- saved warm snapshot
//	    state.bin
//	    mem.bin
//	    code.img                          <- frozen code drive
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
	snapshots map[string]string // function key -> snapshot dir
}

// NewJailer wires a jailer-backed runtime.
func NewJailer(dataDir string) (*Jailer, error) {
	if _, err := exec.LookPath("firecracker"); err != nil {
		return nil, ErrUnsupported
	}
	for _, dir := range []string{
		filepath.Join(dataDir, "jail"),
		filepath.Join(dataDir, "snapshots"),
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
	j := &Jailer{
		dataDir:   dataDir,
		templates: tmpls,
		snapshots: map[string]string{},
	}
	// Re-discover any previously-persisted snapshots so a server restart
	// keeps warm-start eligibility.
	if entries, err := os.ReadDir(filepath.Join(dataDir, "snapshots")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(dataDir, "snapshots", e.Name())
			if _, err := os.Stat(filepath.Join(dir, "state.bin")); err == nil {
				j.snapshots[e.Name()] = dir
			}
		}
	}
	return j, nil
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
// agent over vsock, and tears it down. After the first successful cold
// invocation we take a memory + state snapshot so subsequent calls can
// restore-and-resume in tens of milliseconds.
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
	// Whether to keep the chroot after we return. We flip this to true if
	// we successfully take a snapshot so future restore() calls can find
	// the original vsock UDS path the snapshot recorded.
	keepChroot := false
	defer func() {
		if !keepChroot {
			_ = os.RemoveAll(chroot)
		}
	}()

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
		Method:      req.Method,
		Path:        req.Path,
		Headers:     req.Headers,
		Body:        req.Body,
		Env:         mergeEnv(fn.Env, req.Env),
		SecretFiles: req.SecretFiles,
		Mode:        mode,
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

	resp := &wasm.InvokeResponse{
		Status:  status,
		Headers: h,
		Body:    presp.Body,
		Logs:    presp.Logs,
		Elapsed: elapsed,
	}

	// Best-effort: take a snapshot once per function so the next
	// invocation can warm-start. Failures here only cost us future
	// warm-start eligibility; the current request has already been
	// served successfully.
	if _, has := j.lookupSnapshot(fn); !has {
		if serr := j.snapshot(ctx, fn, apiSock, chroot); serr == nil {
			keepChroot = true
		} else {
			log.With("err", serr, "fn", fn.Meta.Name).Debug("firecracker: snapshot failed")
		}
	}
	return resp, nil
}

func (j *Jailer) Close() error { return nil }

func (j *Jailer) fnDir(fn *store.Function) string {
	return filepath.Join(j.dataDir, "fns", fn.Meta.Project, fn.Meta.Name)
}

// snapshot pauses the running Firecracker VM and writes a state + memory
// pair to <dataDir>/snapshots/<fnKey>/. The chroot path is recorded
// alongside so restore() can reuse the same vsock UDS path the snapshot
// recorded internally.
func (j *Jailer) snapshot(ctx context.Context, fn *store.Function, apiSock, chroot string) error {
	snapDir := filepath.Join(j.dataDir, "snapshots", fnKey(fn))
	if err := os.MkdirAll(snapDir, 0o750); err != nil {
		return err
	}
	statePath := filepath.Join(snapDir, "state.bin")
	memPath := filepath.Join(snapDir, "mem.bin")

	if err := fcAPI(ctx, apiSock, http.MethodPatch, "/vm", map[string]string{"state": "Paused"}); err != nil {
		return fmt.Errorf("pause vm: %w", err)
	}
	if err := fcAPI(ctx, apiSock, http.MethodPut, "/snapshot/create", map[string]any{
		"snapshot_type": "Full",
		"snapshot_path": statePath,
		"mem_file_path": memPath,
	}); err != nil {
		return fmt.Errorf("create snapshot: %w", err)
	}
	meta := map[string]string{"chroot": chroot}
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(snapDir, "meta.json"), mb, 0o640); err != nil {
		return err
	}
	j.mu.Lock()
	j.snapshots[fnKey(fn)] = snapDir
	j.mu.Unlock()
	return nil
}

// restore loads a previously-saved snapshot, resumes the VM, and runs
// the invocation against the still-listening agent. Any failure makes
// Invoke fall back to a cold boot.
func (j *Jailer) restore(ctx context.Context, fn *store.Function, snapDir string, req *wasm.InvokeRequest) (*wasm.InvokeResponse, error) {
	var meta struct {
		Chroot string `json:"chroot"`
	}
	mb, err := os.ReadFile(filepath.Join(snapDir, "meta.json"))
	if err != nil {
		return nil, fmt.Errorf("read snapshot meta: %w", err)
	}
	if err := json.Unmarshal(mb, &meta); err != nil {
		return nil, err
	}
	if meta.Chroot == "" {
		return nil, errors.New("snapshot meta missing chroot")
	}
	if _, err := os.Stat(meta.Chroot); err != nil {
		// Original chroot is gone; cold-boot is required.
		return nil, fmt.Errorf("snapshot chroot missing: %w", err)
	}
	apiSock := filepath.Join(meta.Chroot, "fc-restore.sock")
	_ = os.Remove(apiSock)

	cctx, cancel := context.WithTimeout(ctx, fnTimeout(fn)+10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, "firecracker", "--api-sock", apiSock)
	cmd.Stderr = newPrefixWriter("[firecracker-restore] ")
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
	if err := waitForFile(cctx, apiSock, 3*time.Second); err != nil {
		return nil, fmt.Errorf("api socket never appeared: %w", err)
	}
	if err := fcAPI(cctx, apiSock, http.MethodPut, "/snapshot/load", map[string]any{
		"snapshot_path":         filepath.Join(snapDir, "state.bin"),
		"mem_file_path":         filepath.Join(snapDir, "mem.bin"),
		"enable_diff_snapshots": false,
		"resume_vm":             true,
	}); err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}

	vsockUDS := filepath.Join(meta.Chroot, "v.sock")
	conn, err := dialAgent(cctx, vsockUDS+"_"+strconv.Itoa(protocol.VsockPort), 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to restored agent: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(fnTimeout(fn)))

	mode, _ := fn.Env["MODE"]
	preq := &protocol.Request{
		Method:      req.Method,
		Path:        req.Path,
		Headers:     req.Headers,
		Body:        req.Body,
		Env:         mergeEnv(fn.Env, req.Env),
		SecretFiles: req.SecretFiles,
		Mode:        mode,
	}
	if preq.Env == nil {
		preq.Env = map[string]string{}
	}
	preq.Env["SELFCLOUD_TIMEOUT_MS"] = strconv.Itoa(fnTimeoutMS(fn))

	start := time.Now()
	if err := protocol.WriteFrame(conn, preq); err != nil {
		return nil, err
	}
	var presp protocol.Response
	if err := protocol.ReadFrame(conn, &presp); err != nil {
		return nil, err
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
	return &wasm.InvokeResponse{
		Status:  status,
		Headers: h,
		Body:    presp.Body,
		Logs:    presp.Logs,
		Elapsed: elapsed,
	}, nil
}

func (j *Jailer) lookupSnapshot(fn *store.Function) (string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	s, ok := j.snapshots[fnKey(fn)]
	return s, ok
}

// fcAPI is a tiny client for the Firecracker REST API exposed over its
// per-VM unix socket. Methods we use today: PATCH /vm, PUT /snapshot/*.
func fcAPI(ctx context.Context, sock, method, path string, body any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	cli := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{}
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("firecracker %s %s: %s: %s", method, path, resp.Status, string(b))
	}
	return nil
}

// waitForFile polls until path exists or the context expires.
func waitForFile(ctx context.Context, path string, max time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s", path)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(50 * time.Millisecond)
	}
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
