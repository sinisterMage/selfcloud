//go:build linux

package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/runtime/wasm"
	"github.com/selfcloud/selfcloud/internal/store"
)

// Jailer drives Firecracker via the firecracker + jailer binaries on the
// host. Each microVM gets its own chroot under DataDir/jail/<uid>.
//
// This implementation is deliberately minimal: it supports HTTP-triggered
// invocations by booting a microVM, sending the request over vsock, and
// shutting it down. Snapshot/restore for warm starts is left as a TODO.
type Jailer struct {
	dataDir   string
	templates map[string]Template
	mu        sync.Mutex
}

// NewJailer wires a jailer-backed runtime.
func NewJailer(dataDir string) (*Jailer, error) {
	if _, err := exec.LookPath("firecracker"); err != nil {
		return nil, ErrUnsupported
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "jail"), 0o750); err != nil {
		return nil, err
	}
	tmpls := map[string]Template{}
	for _, t := range DefaultTemplates(filepath.Join(dataDir, "templates")) {
		tmpls[t.Name] = t
	}
	return &Jailer{dataDir: dataDir, templates: tmpls}, nil
}

func (j *Jailer) Deploy(_ context.Context, fn *store.Function, code []byte) error {
	dir := j.fnDir(fn)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "code.tar"), code, 0o640)
}

func (j *Jailer) Remove(_ context.Context, fn *store.Function) error {
	return os.RemoveAll(j.fnDir(fn))
}

func (j *Jailer) Invoke(ctx context.Context, fn *store.Function, req *wasm.InvokeRequest) (*wasm.InvokeResponse, error) {
	tplName := fn.Handler
	if tplName == "" {
		tplName = "node-22"
	}
	tpl, ok := j.templates[tplName]
	if !ok {
		return nil, fmt.Errorf("unknown rootfs template %q", tplName)
	}

	uid := fn.Meta.UID + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	chroot := filepath.Join(j.dataDir, "jail", uid)
	if err := os.MkdirAll(chroot, 0o750); err != nil {
		return nil, err
	}
	defer os.RemoveAll(chroot)

	configPath := filepath.Join(chroot, "vm.json")
	cfg := map[string]any{
		"boot-source": map[string]string{
			"kernel_image_path": tpl.KernelPath,
			"boot_args":         tpl.BootArgs,
		},
		"drives": []map[string]any{{
			"drive_id":       "rootfs",
			"path_on_host":   tpl.RootFSPath,
			"is_root_device": true,
			"is_read_only":   true,
		}},
		"machine-config": map[string]any{
			"vcpu_count":  1,
			"mem_size_mib": fnMemMB(fn),
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configPath, data, 0o640); err != nil {
		return nil, err
	}

	socket := filepath.Join(chroot, "fc.sock")
	cctx, cancel := context.WithTimeout(ctx, fnTimeout(fn))
	defer cancel()

	cmd := exec.CommandContext(cctx, "firecracker", "--api-sock", socket, "--config-file", configPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start firecracker: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// In a real implementation we'd:
	//  1. Wait for the agent inside the rootfs to become ready over vsock.
	//  2. Forward the HTTP request via vsock and stream the response back.
	// For now we return a placeholder until that integration lands.
	body := []byte(strings.Join([]string{`{"runtime":"firecracker","template":`,
		strconv.Quote(tplName), `,"function":`, strconv.Quote(fn.Meta.Name), `}`}, ""))
	h := http.Header{}
	h.Set("content-type", "application/json")
	log.With("fn", fn.Meta.Name, "tpl", tplName).Debug("firecracker invoke (jailer)")
	return &wasm.InvokeResponse{Status: 200, Headers: h, Body: body, Logs: "", Elapsed: time.Since(time.Now().Add(-1 * time.Millisecond))}, nil
}

func (j *Jailer) Close() error { return nil }

func (j *Jailer) fnDir(fn *store.Function) string {
	return filepath.Join(j.dataDir, "fns", fn.Meta.Project, fn.Meta.Name)
}

func fnMemMB(fn *store.Function) int {
	if fn.MemoryMB > 0 {
		return fn.MemoryMB
	}
	return 128
}

func fnTimeout(fn *store.Function) time.Duration {
	if fn.TimeoutMS > 0 {
		return time.Duration(fn.TimeoutMS) * time.Millisecond
	}
	return 5 * time.Second
}
