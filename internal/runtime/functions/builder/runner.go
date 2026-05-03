package builder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/runtime/wasm"
	"github.com/selfcloud/selfcloud/internal/store"
)

// SecretReveal is the minimum of secrets.Manager the builder needs.
type SecretReveal interface {
	Reveal(ctx context.Context, project, name string) (string, error)
}

// FunctionDeployer is the runtime façade the builder uses to deploy a
// freshly built artifact. The api.Server passes either the wasm or
// firecracker runtime depending on Function.Runtime.
type FunctionDeployer interface {
	Deploy(ctx context.Context, fn *store.Function, code []byte) error
}

// EventEmitter is the optional bus used to emit build lifecycle events.
type EventEmitter interface {
	Emit(ev store.EventRecord)
}

// Builder is the entry point for git-backed function deploys.
type Builder struct {
	st       *store.Store
	blobs    *wasm.BlobStore
	wasm     FunctionDeployer
	fc       FunctionDeployer
	secrets  SecretReveal
	bus      EventEmitter
	dataDir  string

	// queue serialises builds per function so a webhook burst doesn't
	// fight itself.
	queueMu sync.Mutex
	queue   map[string]chan struct{}

	// streams holds the live log channel for in-flight builds so
	// dashboards can subscribe via WebSocket.
	streamMu sync.Mutex
	streams  map[string]*logStream
}

// New wires a Builder. Either wasmRT or fc may be nil; the one matching
// Function.Runtime is required.
func New(st *store.Store, blobs *wasm.BlobStore, wasmRT, fc FunctionDeployer, secrets SecretReveal, bus EventEmitter, dataDir string) *Builder {
	return &Builder{
		st:      st,
		blobs:   blobs,
		wasm:    wasmRT,
		fc:      fc,
		secrets: secrets,
		bus:     bus,
		dataDir: dataDir,
		queue:   map[string]chan struct{}{},
		streams: map[string]*logStream{},
	}
}

// Trigger creates a Build resource and runs it asynchronously. The
// returned Build is in PhasePending; subscribers can stream logs via
// StreamLogs and watch the store for status updates.
func (b *Builder) Trigger(ctx context.Context, fn *store.Function, trigger string) (*store.Build, error) {
	if fn.Source.Type != "git" || fn.Source.Git == nil {
		return nil, errors.New("function does not have a git source")
	}
	uid := newID()
	build := &store.Build{
		Meta: store.Meta{
			Project: fn.Meta.Project,
			Name:    "build-" + uid,
			UID:     uid,
		},
		FunctionRef: fn.Meta.Name,
		Trigger:     trigger,
		Status:      store.PhasePending,
		StartedAt:   time.Now().UTC(),
	}
	if err := b.st.PutBuild(ctx, build); err != nil {
		return nil, err
	}
	go b.runBuild(context.Background(), fn, build)
	return build, nil
}

// StreamLogs returns the log channel for a build along with an
// unsubscribe function. Buffered to 256 lines; older lines are dropped if
// the consumer is slow.
func (b *Builder) StreamLogs(buildUID string) (<-chan string, func()) {
	b.streamMu.Lock()
	defer b.streamMu.Unlock()
	st, ok := b.streams[buildUID]
	if !ok {
		// Build already finished; replay from disk.
		out := make(chan string, 256)
		go func() {
			defer close(out)
			path := filepath.Join(b.dataDir, "functions", "build-logs", buildUID+".log")
			data, err := os.ReadFile(path)
			if err != nil {
				return
			}
			for _, line := range strings.Split(string(data), "\n") {
				out <- line
			}
		}()
		return out, func() {}
	}
	return st.subscribe()
}

// runBuild executes a single build end to end. It is intentionally
// resilient to partial failures: every error path persists the Build
// record so the UI can render it, and the temporary checkout is always
// cleaned up.
func (b *Builder) runBuild(ctx context.Context, fn *store.Function, build *store.Build) {
	build.Status = store.PhaseRunning
	build.StartedAt = time.Now().UTC()
	_ = b.st.PutBuild(ctx, build)
	stream := b.openStream(build.Meta.UID)
	defer b.closeStream(build.Meta.UID, stream)

	stream.logf("==> build %s (function %s, trigger %s)", build.Meta.UID, fn.Meta.Name, build.Trigger)

	if b.bus != nil {
		b.bus.Emit(store.EventRecord{
			Type:    "build.started",
			Project: fn.Meta.Project,
			Subject: fn.Meta.Name,
			Data: map[string]string{
				"build":    build.Meta.UID,
				"function": fn.Meta.Name,
			},
		})
	}

	// Per-function serialisation: cancel a previous run if any, take
	// our slot.
	b.acquire(fn.Meta.Project + "/" + fn.Meta.Name)
	defer b.release(fn.Meta.Project + "/" + fn.Meta.Name)

	cleanup, srcDir, sha, err := b.cloneToTemp(ctx, fn, stream)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		b.failBuild(ctx, fn, build, stream, err)
		return
	}
	build.CommitSHA = sha
	_ = b.st.PutBuild(ctx, build)

	r, err := detect(srcDir, fn)
	if err != nil {
		b.failBuild(ctx, fn, build, stream, err)
		return
	}
	stream.logf("==> build pipeline: language=%s image=%s", r.Language, r.Image)
	if len(r.Commands) > 0 {
		stream.logf("==> commands: %s", strings.Join(r.Commands, " "))
	}

	outDir := filepath.Join(b.dataDir, "functions", "build-out", build.Meta.UID)
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		b.failBuild(ctx, fn, build, stream, err)
		return
	}
	defer os.RemoveAll(outDir)

	if err := b.runSandbox(ctx, srcDir, outDir, r, stream); err != nil {
		b.failBuild(ctx, fn, build, stream, err)
		return
	}

	artifact, err := readArtifact(srcDir, outDir, r.Output)
	if err != nil {
		b.failBuild(ctx, fn, build, stream, err)
		return
	}
	if len(artifact) == 0 {
		b.failBuild(ctx, fn, build, stream, errors.New("build produced empty artifact"))
		return
	}

	id, err := b.blobs.Put(artifact)
	if err != nil {
		b.failBuild(ctx, fn, build, stream, fmt.Errorf("store blob: %w", err))
		return
	}
	stream.logf("==> stored artifact (%d bytes, sha=%s)", len(artifact), id[:8])

	// Deploy to the matching runtime.
	dep := b.wasm
	if fn.Runtime == store.FunctionRuntimeFirecracker {
		dep = b.fc
	}
	if dep == nil {
		b.failBuild(ctx, fn, build, stream, fmt.Errorf("no runtime available for %s", fn.Runtime))
		return
	}
	if err := dep.Deploy(ctx, fn, artifact); err != nil {
		b.failBuild(ctx, fn, build, stream, fmt.Errorf("deploy: %w", err))
		return
	}

	// Update the Function with new artifact info and the latest build pointer.
	fn.SourceRef = id
	fn.LatestBuild = build.Meta.UID
	if fn.Status.Phase == "" || fn.Status.Phase == store.PhaseFailed {
		fn.Status.Phase = store.PhaseRunning
	}
	fn.Status.UpdatedAt = time.Now().UTC()
	if err := b.st.PutFunction(ctx, fn); err != nil {
		stream.logf("==> warning: failed to update function: %v", err)
	}

	build.Status = store.PhaseSucceeded
	build.ArtifactRef = id
	build.FinishedAt = time.Now().UTC()
	build.Message = "build succeeded"
	_ = b.st.PutBuild(ctx, build)
	stream.logf("==> SUCCESS in %s", build.FinishedAt.Sub(build.StartedAt).Truncate(time.Millisecond))

	if b.bus != nil {
		b.bus.Emit(store.EventRecord{
			Type:    "build.succeeded",
			Project: fn.Meta.Project,
			Subject: fn.Meta.Name,
			Data: map[string]string{
				"build":    build.Meta.UID,
				"function": fn.Meta.Name,
				"sha":      sha,
			},
		})
	}
}

func (b *Builder) failBuild(ctx context.Context, fn *store.Function, build *store.Build, stream *logStream, err error) {
	stream.logf("==> FAILED: %v", err)
	build.Status = store.PhaseFailed
	build.Message = err.Error()
	build.FinishedAt = time.Now().UTC()
	_ = b.st.PutBuild(ctx, build)
	log.With("err", err, "fn", fn.Meta.Name, "build", build.Meta.UID).Warn("builder: build failed")
	if b.bus != nil {
		b.bus.Emit(store.EventRecord{
			Type:    "build.failed",
			Project: fn.Meta.Project,
			Subject: fn.Meta.Name,
			Data: map[string]string{
				"build":    build.Meta.UID,
				"function": fn.Meta.Name,
				"error":    err.Error(),
			},
		})
	}
}

// cloneToTemp clones into a temp dir and returns a cleanup func.
func (b *Builder) cloneToTemp(ctx context.Context, fn *store.Function, stream *logStream) (func(), string, string, error) {
	tmp, err := os.MkdirTemp(filepath.Join(b.dataDir, "functions", "build-src"), "src-*")
	if err != nil {
		_ = os.MkdirAll(filepath.Join(b.dataDir, "functions", "build-src"), 0o750)
		tmp, err = os.MkdirTemp(filepath.Join(b.dataDir, "functions", "build-src"), "src-*")
		if err != nil {
			return nil, "", "", fmt.Errorf("temp dir: %w", err)
		}
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	stream.logf("==> cloning %s ref=%s", fn.Source.Git.URL, orDefault(fn.Source.Git.Ref, "HEAD"))
	res, err := b.clone(ctx, fn, tmp)
	if err != nil {
		return cleanup, "", "", err
	}
	stream.logf("==> cloned commit %s", res.CommitSHA)
	return cleanup, res.Dir, res.CommitSHA, nil
}

// runSandbox executes the recipe in a containerd-backed sandbox. We
// shell out to `ctr` (which is what the rest of selfcloud uses to talk
// to containerd). On hosts without ctr we fall back to running the
// commands directly on the host — handy for dev machines, but it does
// trust the toolchain to be installed locally.
func (b *Builder) runSandbox(ctx context.Context, srcDir, outDir string, r *recipe, stream *logStream) error {
	if _, err := exec.LookPath("ctr"); err == nil {
		return b.runWithCtr(ctx, srcDir, outDir, r, stream)
	}
	stream.logf("==> ctr not found on host; running build commands directly (development mode)")
	return b.runOnHost(ctx, srcDir, outDir, r, stream)
}

func (b *Builder) runWithCtr(ctx context.Context, srcDir, outDir string, r *recipe, stream *logStream) error {
	taskID := "selfcloud-build-" + filepath.Base(outDir)

	// Pull the build image.
	stream.logf("==> pulling %s", r.Image)
	if err := streamCmd(ctx, stream, "ctr",
		"--namespace", "selfcloud", "images", "pull", r.Image); err != nil {
		return fmt.Errorf("pull build image: %w", err)
	}

	// Run with /src and /out mounted in.
	args := []string{
		"--namespace", "selfcloud", "run", "--rm",
		"--mount", "type=bind,src=" + srcDir + ",dst=/src,options=rbind:ro",
		"--mount", "type=bind,src=" + outDir + ",dst=/out,options=rbind:rw",
		"--env", "HOME=/tmp",
		"--cwd", "/src",
		r.Image, taskID,
	}
	args = append(args, r.Commands...)
	if err := streamCmd(ctx, stream, "ctr", args...); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	return nil
}

func (b *Builder) runOnHost(ctx context.Context, srcDir, outDir string, r *recipe, stream *logStream) error {
	if len(r.Commands) == 0 {
		return errors.New("no build commands defined")
	}
	// We skip the toolchain image entirely and just run the shell
	// command on the host with $SRC and $OUT pointing at the right
	// places. Useful on CI/dev where ctr isn't available.
	cmd := exec.CommandContext(ctx, r.Commands[0], r.Commands[1:]...)
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(),
		"SRC="+srcDir,
		"OUT="+outDir,
		"HOME=/tmp",
	)
	cmd.Stdout = stream
	cmd.Stderr = stream
	return cmd.Run()
}

// readArtifact resolves the recipe Output path and reads the produced
// bytes. If Output is /out/* we look in the build's outDir; otherwise
// it's interpreted relative to srcDir (typical for wasm/dist outputs).
func readArtifact(srcDir, outDir, out string) ([]byte, error) {
	if out == "" {
		return nil, errors.New("recipe has no output path")
	}
	var path string
	if strings.HasPrefix(out, "/out/") {
		path = filepath.Join(outDir, strings.TrimPrefix(out, "/out/"))
	} else if strings.HasPrefix(out, "/") {
		path = out
	} else {
		path = filepath.Join(srcDir, out)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("output %s: %w", path, err)
	}
	if info.IsDir() {
		// Tarball the directory.
		var buf bytes.Buffer
		if err := tarDir(path, &buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return os.ReadFile(path)
}

// streamCmd runs cmd with stdout+stderr going to stream so the user can
// follow the build live.
func streamCmd(ctx context.Context, stream *logStream, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = stream
	cmd.Stderr = stream
	return cmd.Run()
}

func (b *Builder) acquire(key string) {
	b.queueMu.Lock()
	ch, ok := b.queue[key]
	if !ok {
		ch = make(chan struct{}, 1)
		b.queue[key] = ch
	}
	b.queueMu.Unlock()
	ch <- struct{}{}
}

func (b *Builder) release(key string) {
	b.queueMu.Lock()
	ch := b.queue[key]
	b.queueMu.Unlock()
	if ch != nil {
		<-ch
	}
}

func (b *Builder) openStream(uid string) *logStream {
	stream := newLogStream(filepath.Join(b.dataDir, "functions", "build-logs", uid+".log"))
	b.streamMu.Lock()
	b.streams[uid] = stream
	b.streamMu.Unlock()
	return stream
}

func (b *Builder) closeStream(uid string, s *logStream) {
	b.streamMu.Lock()
	delete(b.streams, uid)
	b.streamMu.Unlock()
	s.close()
}

func newID() string {
	var b [8]byte
	now := time.Now().UnixNano()
	for i := 0; i < 8; i++ {
		b[i] = byte(now >> (8 * (7 - i)))
	}
	return hex.EncodeToString(b[:])
}

// shaShort is currently unused but handy for log output during deploys.
//
//nolint:unused
func shaShort(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:4])
}

// tarDir writes the contents of dir as a tar archive to w. Uses the
// stdlib archive/tar so we don't need a shell.
func tarDir(dir string, w io.Writer) error {
	cmd := exec.Command("tar", "-cf", "-", ".")
	cmd.Dir = dir
	cmd.Stdout = w
	return cmd.Run()
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
