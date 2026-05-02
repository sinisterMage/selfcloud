package wasm

import (
	"context"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// Rehydrator walks every function in the store at boot and re-Deploys its
// code from the on-disk blob store, so functions stay invokable across
// server restarts. It is intended to be called once at startup, after both
// the wasm and firecracker runtimes are constructed.
type Rehydrator struct {
	st    *store.Store
	blobs *BlobStore
	wasm  Runtime
	fc    Runtime
}

// NewRehydrator wires the dependencies. Pass fc=nil if firecracker isn't in
// play — wasm-only deployments will still rehydrate.
func NewRehydrator(st *store.Store, blobs *BlobStore, wasmRT, fc Runtime) *Rehydrator {
	return &Rehydrator{st: st, blobs: blobs, wasm: wasmRT, fc: fc}
}

// Run loads every function with a SourceRef and re-Deploys it. Errors are
// logged but not propagated; a missing blob just means the user will get
// the helpful 503 from the trigger router until they re-upload.
func (r *Rehydrator) Run(ctx context.Context) {
	fns, err := r.st.ListFunctions(ctx, "")
	if err != nil {
		log.With("err", err).Warn("rehydrate: list functions failed")
		return
	}
	for i := range fns {
		f := &fns[i]
		if f.SourceRef == "" {
			continue
		}
		data, err := r.blobs.Get(f.SourceRef)
		if err != nil {
			log.With("err", err, "fn", f.Meta.Name).Warn("rehydrate: blob load failed")
			continue
		}
		var rt Runtime
		switch f.Runtime {
		case store.FunctionRuntimeFirecracker:
			rt = r.fc
		default:
			rt = r.wasm
		}
		if rt == nil {
			continue
		}
		if err := rt.Deploy(ctx, f, data); err != nil {
			log.With("err", err, "fn", f.Meta.Name).Warn("rehydrate: redeploy failed")
			continue
		}
		log.With("fn", f.Meta.Name, "runtime", f.Runtime).Debug("rehydrated function")
	}
}
