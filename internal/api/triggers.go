package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/selfcloud/selfcloud/internal/runtime/wasm"
	"github.com/selfcloud/selfcloud/internal/store"
)

// handleFunctionInvoke is the public-facing HTTP trigger router. URLs of
// the form `/fn/<project>/<function>[/<rest>]` resolve to a function and
// invoke it via the appropriate runtime.
func (s *Server) handleFunctionInvoke(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/fn/"), "/")
	if len(parts) < 2 {
		httpError(w, 404, "function not found")
		return
	}
	project, name := parts[0], parts[1]
	f, err := s.store.GetFunction(r.Context(), project, name)
	if err != nil {
		httpError(w, 404, "function not found")
		return
	}
	if f.SourceRef == "" {
		httpError(w, 503, "function has no code deployed; upload a .wasm via POST /api/v1/projects/"+project+"/functions/"+name+"/code")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	subPath := "/"
	if len(parts) > 2 {
		subPath = "/" + strings.Join(parts[2:], "/")
	}
	req := &wasm.InvokeRequest{
		Method:  r.Method,
		Path:    subPath,
		Headers: r.Header,
		Body:    body,
		Env:     f.Env,
	}
	resp, err := s.invoke(r.Context(), f, req)
	if err != nil {
		if errors.Is(err, wasm.ErrFunctionNotReady) {
			httpError(w, 503, "function is deploying; try again in a moment")
			return
		}
		httpError(w, 500, err.Error())
		return
	}
	wasm.CopyResponse(w, resp)
}

// invoke routes to the right runtime for a function.
func (s *Server) invoke(ctx context.Context, f *store.Function, req *wasm.InvokeRequest) (*wasm.InvokeResponse, error) {
	switch f.Runtime {
	case store.FunctionRuntimeFirecracker:
		if s.fcracker == nil {
			return nil, errors.New("firecracker runtime not available")
		}
		return s.fcracker.Invoke(ctx, f, req)
	default:
		if s.wasm == nil {
			return nil, errors.New("wasm runtime not available")
		}
		return s.wasm.Invoke(ctx, f, req)
	}
}
