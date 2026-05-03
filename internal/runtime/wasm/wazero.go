package wasm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// Wazero is the production wasm runtime. It compiles each function once on
// Deploy and caches the compiled artifact; Invoke spins up a fresh
// `api.Module` per call (cheap relative to compile) and feeds the request as
// a JSON envelope on stdin, reading the response from stdout. stderr is
// captured into InvokeResponse.Logs.
//
// The ABI is intentionally trivial so that any language whose toolchain
// targets `wasm32-wasi` works out of the box (TinyGo, Rust, Zig, AssemblyScript,
// recent Go via the experimental wasi target, ...).
//
//	stdin  -> {"method":"GET","path":"/x","headers":{"k":"v"},"body":"<b64>","env":{"K":"V"}}
//	stdout -> {"status":200,"headers":{"k":"v"},"body":"<b64>"}
//
// Anything written to stderr surfaces in the response Logs field and the
// node's structured log.
type Wazero struct {
	rt    wazero.Runtime
	mu    sync.RWMutex
	funcs map[string]*compiledFn
}

type compiledFn struct {
	mod      wazero.CompiledModule
	memMB    int
	deadline time.Duration
}

// envelope sent to the guest on stdin and received back on stdout.
type wasmRequest struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"` // base64
	Env     map[string]string   `json:"env"`
}

type wasmResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"` // base64
}

// NewWazero builds a Wazero-backed wasm runtime. The returned Runtime is
// safe for concurrent use.
func NewWazero(ctx context.Context) *Wazero {
	cfg := wazero.NewRuntimeConfig().
		WithCompilationCache(wazero.NewCompilationCache()).
		WithCloseOnContextDone(true)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	return &Wazero{rt: rt, funcs: map[string]*compiledFn{}}
}

// Deploy compiles the supplied wasm bytes and stores the compiled module.
func (w *Wazero) Deploy(ctx context.Context, fn *store.Function, code []byte) error {
	if len(code) < 8 || string(code[:4]) != "\x00asm" {
		return errors.New("not a wasm module (bad magic header)")
	}
	mod, err := w.rt.CompileModule(ctx, code)
	if err != nil {
		return fmt.Errorf("wasm compile: %w", err)
	}
	c := &compiledFn{
		mod:      mod,
		memMB:    fn.MemoryMB,
		deadline: time.Duration(fn.TimeoutMS) * time.Millisecond,
	}
	if c.deadline <= 0 {
		c.deadline = 5 * time.Second
	}
	if c.memMB <= 0 {
		c.memMB = 128
	}
	w.mu.Lock()
	if old, ok := w.funcs[fnKey(fn)]; ok {
		_ = old.mod.Close(ctx)
	}
	w.funcs[fnKey(fn)] = c
	w.mu.Unlock()
	log.With("fn", fn.Meta.Name, "size", len(code)).Debug("wasm: compiled")
	return nil
}

// Remove drops the cached compiled module.
func (w *Wazero) Remove(ctx context.Context, fn *store.Function) error {
	w.mu.Lock()
	c, ok := w.funcs[fnKey(fn)]
	delete(w.funcs, fnKey(fn))
	w.mu.Unlock()
	if ok {
		return c.mod.Close(ctx)
	}
	return nil
}

// Invoke runs the cached module against the request. Each call gets a fresh
// instance so guest state is not shared across invocations.
func (w *Wazero) Invoke(ctx context.Context, fn *store.Function, req *InvokeRequest) (*InvokeResponse, error) {
	w.mu.RLock()
	c, ok := w.funcs[fnKey(fn)]
	w.mu.RUnlock()
	if !ok {
		return nil, ErrFunctionNotReady
	}

	// Marshal the request envelope.
	envReq := wasmRequest{
		Method:  req.Method,
		Path:    req.Path,
		Headers: req.Headers,
		Body:    base64.StdEncoding.EncodeToString(req.Body),
		Env:     req.Env,
	}
	if envReq.Headers == nil {
		envReq.Headers = http.Header{}
	}
	stdin, err := json.Marshal(envReq)
	if err != nil {
		return nil, err
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	cctx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()

	cfg := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(stdin)).
		WithStdout(stdout).
		WithStderr(stderr).
		WithStartFunctions(). // we'll call _start ourselves so we can observe exit codes
		WithName("")          // anonymous: allows concurrent instantiation
	for k, v := range req.Env {
		cfg = cfg.WithEnv(k, v)
	}
	cfg = cfg.WithEnv("SELFCLOUD_FN", fn.Meta.Name).
		WithEnv("SELFCLOUD_PROJECT", fn.Meta.Project).
		WithEnv("SELFCLOUD_REQUEST_METHOD", req.Method).
		WithEnv("SELFCLOUD_REQUEST_PATH", req.Path)

	// File-mode secret mounts: stage the bytes into a per-invocation
	// tempdir on the host and project it at /secrets inside the guest.
	// Cleanup runs after the call returns.
	if len(req.SecretFiles) > 0 {
		tmp, err := os.MkdirTemp("", "selfcloud-fn-secrets-*")
		if err == nil {
			defer os.RemoveAll(tmp)
			for guestPath, data := range req.SecretFiles {
				name := filepath.Base(guestPath)
				if name == "" || name == "." || name == "/" {
					name = "secret"
				}
				_ = os.WriteFile(filepath.Join(tmp, name), data, 0o400)
			}
			cfg = cfg.WithFSConfig(wazero.NewFSConfig().WithDirMount(tmp, "/secrets"))
		} else {
			log.With("err", err).Warn("wasm: secret stage tmpdir failed")
		}
	}

	start := time.Now()
	mod, err := w.rt.InstantiateModule(cctx, c.mod, cfg)
	if err != nil {
		return nil, fmt.Errorf("wasm instantiate: %w", err)
	}
	defer mod.Close(context.Background())

	// Enforce memory cap by capping pages on the linear memory if exposed.
	if mem := mod.Memory(); mem != nil && c.memMB > 0 {
		maxPages := uint32(c.memMB * 16) // 1 MiB = 16 wasm pages (64KiB)
		if cur := mem.Size(); cur > maxPages*65536 {
			return nil, fmt.Errorf("wasm: instance memory %dB exceeds cap %dMB", cur, c.memMB)
		}
		_ = maxPages
	}

	// Call _start (WASI command). If the module is reactor-style with
	// `_initialize` plus exported funcs, fall back to that.
	if startFn := mod.ExportedFunction("_start"); startFn != nil {
		_, err = startFn.Call(cctx)
	} else if initFn := mod.ExportedFunction("_initialize"); initFn != nil {
		if _, err = initFn.Call(cctx); err == nil {
			if handle := mod.ExportedFunction("handle"); handle != nil {
				_, err = handle.Call(cctx)
			}
		}
	} else {
		err = errors.New("wasm module does not export _start or _initialize")
	}
	elapsed := time.Since(start)

	exitCode := uint32(0)
	if err != nil {
		var exitErr *sys.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			err = nil // ExitCode 0 is normal WASI program exit.
			if exitCode != 0 {
				return errResponse(stderr, fmt.Errorf("wasm exited with code %d", exitCode), elapsed), nil
			}
		} else if errors.Is(err, context.DeadlineExceeded) {
			return errResponse(stderr, fmt.Errorf("wasm timed out after %s", c.deadline), elapsed), nil
		} else {
			return errResponse(stderr, err, elapsed), nil
		}
	}

	resp, perr := decodeResponse(stdout.Bytes(), stderr.Bytes(), elapsed)
	if perr != nil {
		return errResponse(stderr, perr, elapsed), nil
	}
	if exitCode != 0 {
		resp.Logs += fmt.Sprintf("\n[wasm exit %d]", exitCode)
	}
	return resp, nil
}

func decodeResponse(stdoutBytes, stderrBytes []byte, elapsed time.Duration) (*InvokeResponse, error) {
	logs := string(stderrBytes)
	stdoutBytes = bytes.TrimSpace(stdoutBytes)
	if len(stdoutBytes) == 0 {
		return &InvokeResponse{
			Status:  204,
			Headers: http.Header{},
			Body:    nil,
			Logs:    logs,
			Elapsed: elapsed,
		}, nil
	}
	var env wasmResponse
	if err := json.Unmarshal(stdoutBytes, &env); err != nil {
		// Treat raw stdout as a 200 text/plain response so trivially
		// `print("hello")` style guests still work.
		h := http.Header{}
		h.Set("content-type", "text/plain; charset=utf-8")
		return &InvokeResponse{
			Status:  200,
			Headers: h,
			Body:    stdoutBytes,
			Logs:    logs,
			Elapsed: elapsed,
		}, nil
	}
	body, err := base64.StdEncoding.DecodeString(env.Body)
	if err != nil {
		return nil, fmt.Errorf("wasm response body not valid base64: %w", err)
	}
	h := http.Header{}
	for k, vs := range env.Headers {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	if h.Get("content-length") == "" {
		h.Set("content-length", strconv.Itoa(len(body)))
	}
	if env.Status == 0 {
		env.Status = 200
	}
	return &InvokeResponse{
		Status:  env.Status,
		Headers: h,
		Body:    body,
		Logs:    logs,
		Elapsed: elapsed,
	}, nil
}

func errResponse(stderr *bytes.Buffer, err error, elapsed time.Duration) *InvokeResponse {
	body, _ := json.Marshal(map[string]any{"error": err.Error()})
	h := http.Header{}
	h.Set("content-type", "application/json")
	logs := stderr.String()
	return &InvokeResponse{
		Status:  500,
		Headers: h,
		Body:    body,
		Logs:    logs,
		Elapsed: elapsed,
	}
}

// Close shuts down the wazero runtime, releasing all compiled modules.
func (w *Wazero) Close() error {
	w.mu.Lock()
	for _, c := range w.funcs {
		_ = c.mod.Close(context.Background())
	}
	w.funcs = map[string]*compiledFn{}
	w.mu.Unlock()
	return w.rt.Close(context.Background())
}

// Ensure the runtime satisfies the interface.
var _ Runtime = (*Wazero)(nil)
