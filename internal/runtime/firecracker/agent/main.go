//go:build linux

// Command fc-agent is the in-guest init binary baked into selfcloud's
// Firecracker rootfs templates. It runs as PID 1, prepares the guest
// filesystem, extracts the user's code drive, then loops handling framed
// JSON requests over AF_VSOCK on port protocol.VsockPort.
//
// Build it statically (no CGO) for the rootfs:
//
//	make firecracker-agent
//
// The Makefile target boils down to:
//
//	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
//	    -o bin/fc-agent ./internal/runtime/firecracker/agent
//
// The agent intentionally has no dependency on the rest of selfcloud: it
// only imports the shared protocol package and the AF_VSOCK helper.
package main

import (
	"archive/tar"
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
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mdlayher/vsock"

	"github.com/selfcloud/selfcloud/internal/runtime/firecracker/protocol"
)

// manifest is read from /code/manifest.json (or, if absent, synthesised from
// well-known defaults inside the rootfs template).
type manifest struct {
	// Entrypoint is the argv to spawn for the user process. If empty in
	// "stdio" mode the agent shells out to /code/handler; if empty in
	// "http" mode the agent assumes the rootfs's default web server is
	// already running.
	Entrypoint []string `json:"entrypoint,omitempty"`
	// Env is merged on top of the per-request env when spawning.
	Env map[string]string `json:"env,omitempty"`
	// Mode is "stdio" (default) or "http".
	Mode string `json:"mode,omitempty"`
	// HTTPPort overrides the loopback port for http mode (default 8080).
	HTTPPort int `json:"httpPort,omitempty"`
	// WorkDir overrides the working directory passed to the child.
	WorkDir string `json:"workDir,omitempty"`
}

func main() {
	logf("fc-agent: starting (pid=%d, uid=%d)", os.Getpid(), os.Getuid())

	if os.Getpid() == 1 {
		setupInitDuties()
	}

	if err := mountFilesystems(); err != nil {
		logf("mount: %v (continuing)", err)
	}

	mf, err := mountCodeDrive()
	if err != nil {
		logf("code drive: %v (continuing with empty manifest)", err)
		mf = &manifest{Mode: "stdio"}
	}
	if mf.Mode == "" {
		mf.Mode = "stdio"
	}
	if mf.HTTPPort == 0 {
		mf.HTTPPort = 8080
	}

	listener, err := vsock.Listen(protocol.VsockPort, nil)
	if err != nil {
		logf("vsock listen: %v", err)
		shutdown(1)
	}
	defer listener.Close()
	logf("fc-agent: listening on vsock port %d, mode=%s", protocol.VsockPort, mf.Mode)

	ctx, cancel := signalContext()
	defer cancel()

	// In http mode, eagerly spawn the long-lived child once.
	var childMu sync.Mutex
	var child *exec.Cmd
	if mf.Mode == "http" && len(mf.Entrypoint) > 0 {
		child, err = spawnHTTPChild(ctx, mf)
		if err != nil {
			logf("http child: %v", err)
		}
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logf("accept: %v", err)
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			handleConn(ctx, c, mf, &childMu, &child)
		}(conn)
	}
}

// setupInitDuties handles the bare minimum a Linux PID 1 must do: reap
// zombies and gracefully shut down on SIGTERM.
func setupInitDuties() {
	go func() {
		for {
			var st syscall.WaitStatus
			pid, err := syscall.Wait4(-1, &st, 0, nil)
			if pid <= 0 || err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}
	}()
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}

// mountFilesystems mounts the kernel-required pseudo filesystems. Errors
// are non-fatal because some rootfs templates may have already mounted
// them via /etc/fstab or an init script.
func mountFilesystems() error {
	type m struct{ src, target, fs string }
	mounts := []m{
		{"proc", "/proc", "proc"},
		{"sysfs", "/sys", "sysfs"},
		{"devtmpfs", "/dev", "devtmpfs"},
		{"tmpfs", "/tmp", "tmpfs"},
		{"tmpfs", "/run", "tmpfs"},
	}
	var firstErr error
	for _, mt := range mounts {
		_ = os.MkdirAll(mt.target, 0o755)
		if err := syscall.Mount(mt.src, mt.target, mt.fs, 0, ""); err != nil {
			if firstErr == nil && !errors.Is(err, syscall.EBUSY) {
				firstErr = fmt.Errorf("mount %s: %w", mt.target, err)
			}
		}
	}
	return firstErr
}

// mountCodeDrive mounts /dev/vdb (the code drive), extracts code.tar into
// /code, and reads manifest.json. If the drive isn't present we return a
// default manifest so the agent still serves health-check style invocations.
func mountCodeDrive() (*manifest, error) {
	if err := os.MkdirAll("/code", 0o755); err != nil {
		return nil, err
	}
	dev := "/dev/vdb"
	if _, err := os.Stat(dev); err != nil {
		return nil, err
	}

	// Try ext4 first, fall back to raw tar (treat the drive as a flat tar).
	mounted := false
	if err := syscall.Mount(dev, "/code", "ext4", syscall.MS_RDONLY, ""); err == nil {
		mounted = true
	} else if err2 := syscall.Mount(dev, "/code", "ext2", syscall.MS_RDONLY, ""); err2 == nil {
		mounted = true
	}

	tarPath := "/code/code.tar"
	if !mounted {
		// Raw tar device: copy it to a regular file and extract.
		f, err := os.Open(dev)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		out, err := os.Create("/tmp/code.tar")
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(out, f); err != nil {
			return nil, err
		}
		_ = out.Close()
		tarPath = "/tmp/code.tar"
	}

	// Extract code.tar (best effort: skip if not present, e.g. when the
	// rootfs is itself the code).
	if _, err := os.Stat(tarPath); err == nil {
		if err := extractTar(tarPath, "/srv"); err != nil {
			return nil, fmt.Errorf("extract: %w", err)
		}
	} else {
		// Treat /code as the working dir directly.
		_ = os.Symlink("/code", "/srv")
	}

	// Read manifest.
	mf := &manifest{Mode: "stdio"}
	mfBytes, err := os.ReadFile("/srv/manifest.json")
	if err == nil {
		if err := json.Unmarshal(mfBytes, mf); err != nil {
			return nil, fmt.Errorf("manifest.json: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return mf, nil
}

func extractTar(path, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		// Reject path-traversal attempts.
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
			continue
		}
		target := filepath.Join(dest, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(target, os.FileMode(hdr.Mode)|0o111)
		case tar.TypeReg, tar.TypeRegA:
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			_ = out.Close()
		case tar.TypeSymlink:
			_ = os.Symlink(hdr.Linkname, target)
		}
	}
}

// spawnHTTPChild starts a long-running user process for "http" mode. The
// agent will then proxy each vsock request to it.
func spawnHTTPChild(ctx context.Context, mf *manifest) (*exec.Cmd, error) {
	if len(mf.Entrypoint) == 0 {
		return nil, errors.New("manifest.entrypoint required for http mode")
	}
	cmd := exec.CommandContext(ctx, mf.Entrypoint[0], mf.Entrypoint[1:]...)
	cmd.Dir = orDefault(mf.WorkDir, "/srv")
	cmd.Env = mergeEnv(os.Environ(), mf.Env)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	logf("fc-agent: http child started pid=%d cmd=%v", cmd.Process.Pid, mf.Entrypoint)
	return cmd, nil
}

// handleConn services exactly one framed request per vsock connection.
func handleConn(ctx context.Context, conn net.Conn, mf *manifest, childMu *sync.Mutex, childPtr **exec.Cmd) {
	var req protocol.Request
	if err := protocol.ReadFrame(conn, &req); err != nil {
		writeErr(conn, fmt.Sprintf("read request: %v", err))
		return
	}
	mode := req.Mode
	if mode == "" {
		mode = mf.Mode
	}
	switch mode {
	case "http":
		serveHTTP(ctx, conn, &req, mf, childMu, childPtr)
	default:
		serveStdio(ctx, conn, &req, mf)
	}
}

func serveStdio(ctx context.Context, conn net.Conn, req *protocol.Request, mf *manifest) {
	entry := mf.Entrypoint
	if len(entry) == 0 {
		// Look for /srv/handler as a default convention.
		if _, err := os.Stat("/srv/handler"); err == nil {
			entry = []string{"/srv/handler"}
		} else {
			writeErr(conn, "no entrypoint configured")
			return
		}
	}
	cctx := ctx
	if dl, ok := deadlineFromEnv(req); ok {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, dl)
		defer cancel()
	}
	cmd := exec.CommandContext(cctx, entry[0], entry[1:]...)
	cmd.Dir = orDefault(mf.WorkDir, "/srv")
	cmd.Env = mergeEnv(os.Environ(), mf.Env, req.Env, map[string]string{
		"SELFCLOUD_REQUEST_METHOD": req.Method,
		"SELFCLOUD_REQUEST_PATH":   req.Path,
	})
	stdin := bytes.NewReader(encodeStdinEnvelope(req))
	var stdout, stderr bytes.Buffer
	cmd.Stdin = stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	resp := decodeStdoutEnvelope(stdout.Bytes())
	resp.Logs = stderr.String()
	if err != nil && resp.Status == 0 {
		resp.Status = 500
		resp.Error = err.Error()
	}
	if err := protocol.WriteFrame(conn, resp); err != nil {
		logf("write response: %v", err)
	}
}

func serveHTTP(ctx context.Context, conn net.Conn, req *protocol.Request, mf *manifest, childMu *sync.Mutex, childPtr **exec.Cmd) {
	childMu.Lock()
	if *childPtr == nil {
		c, err := spawnHTTPChild(ctx, mf)
		if err != nil {
			childMu.Unlock()
			writeErr(conn, fmt.Sprintf("spawn http child: %v", err))
			return
		}
		*childPtr = c
	}
	childMu.Unlock()

	url := fmt.Sprintf("http://127.0.0.1:%d%s", mf.HTTPPort, req.Path)
	hreq, err := http.NewRequestWithContext(ctx, req.Method, url, bytes.NewReader(req.Body))
	if err != nil {
		writeErr(conn, fmt.Sprintf("build http request: %v", err))
		return
	}
	for k, vs := range req.Headers {
		for _, v := range vs {
			hreq.Header.Add(k, v)
		}
	}
	// Wait for the child to bind.
	client := &http.Client{Timeout: 30 * time.Second}
	var resp *http.Response
	for i := 0; i < 30; i++ {
		resp, err = client.Do(hreq)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		writeErr(conn, fmt.Sprintf("proxy: %v", err))
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, protocol.MaxMessageSize))
	out := &protocol.Response{
		Status:  resp.StatusCode,
		Headers: resp.Header,
		Body:    body,
	}
	if err := protocol.WriteFrame(conn, out); err != nil {
		logf("write response: %v", err)
	}
}

func encodeStdinEnvelope(req *protocol.Request) []byte {
	env := struct {
		Method  string              `json:"method"`
		Path    string              `json:"path"`
		Headers map[string][]string `json:"headers,omitempty"`
		Body    string              `json:"body,omitempty"` // raw text to be friendly to scripts
		Env     map[string]string   `json:"env,omitempty"`
	}{
		Method:  req.Method,
		Path:    req.Path,
		Headers: req.Headers,
		Body:    string(req.Body),
		Env:     req.Env,
	}
	out, _ := json.Marshal(env)
	out = append(out, '\n')
	return out
}

func decodeStdoutEnvelope(stdout []byte) *protocol.Response {
	stdout = bytes.TrimSpace(stdout)
	if len(stdout) == 0 {
		return &protocol.Response{Status: 204}
	}
	var env struct {
		Status  int                 `json:"status"`
		Headers map[string][]string `json:"headers,omitempty"`
		Body    string              `json:"body,omitempty"`
	}
	if err := json.Unmarshal(stdout, &env); err != nil {
		// Treat raw stdout as a 200 text/plain response.
		return &protocol.Response{
			Status:  200,
			Headers: map[string][]string{"Content-Type": {"text/plain; charset=utf-8"}},
			Body:    stdout,
		}
	}
	if env.Status == 0 {
		env.Status = 200
	}
	return &protocol.Response{
		Status:  env.Status,
		Headers: env.Headers,
		Body:    []byte(env.Body),
	}
}

func writeErr(conn net.Conn, msg string) {
	_ = protocol.WriteFrame(conn, &protocol.Response{
		Status: 500,
		Error:  msg,
	})
}

func deadlineFromEnv(req *protocol.Request) (time.Duration, bool) {
	v, ok := req.Env["SELFCLOUD_TIMEOUT_MS"]
	if !ok {
		return 0, false
	}
	var ms int
	if _, err := fmt.Sscanf(v, "%d", &ms); err != nil || ms <= 0 {
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}

func mergeEnv(base []string, overlays ...map[string]string) []string {
	out := make([]string, 0, len(base))
	out = append(out, base...)
	for _, m := range overlays {
		for k, v := range m {
			out = append(out, k+"="+v)
		}
	}
	return out
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func shutdown(code int) {
	if runtime.GOOS == "linux" {
		syscall.Sync()
		_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
	}
	os.Exit(code)
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[fc-agent] "+format+"\n", args...)
}
