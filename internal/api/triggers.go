package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/selfcloud/selfcloud/internal/runtime/wasm"
	"github.com/selfcloud/selfcloud/internal/store"
)

func fmtInt(i int) string                { return strconv.Itoa(i) }
func fmtDuration(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }

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
	start := time.Now()
	resp, err := s.invoke(r.Context(), f, req)
	dur := time.Since(start)
	rec := invocationRecord{
		At:     start.UTC(),
		Method: req.Method,
		Path:   subPath,
		DurMS:  dur.Milliseconds(),
		BodyKB: len(body) / 1024,
	}
	if err != nil {
		rec.Status = 500
		rec.Error = err.Error()
		if s.invocations != nil {
			s.invocations.record(project+"/"+name, rec)
		}
		if errors.Is(err, wasm.ErrFunctionNotReady) {
			httpError(w, 503, "function is deploying; try again in a moment")
			return
		}
		httpError(w, 500, err.Error())
		return
	}
	rec.Status = resp.Status
	if len(resp.Logs) > 0 {
		rec.LogsTail = tailString(resp.Logs, 256)
	}
	if s.invocations != nil {
		s.invocations.record(project+"/"+name, rec)
	}
	wasm.CopyResponse(w, resp)
}

// invoke routes to the right runtime for a function. It also takes
// care of resolving secret:// references in f.Env and emitting
// function.invoked / function.error lifecycle events on the bus.
func (s *Server) invoke(ctx context.Context, f *store.Function, req *wasm.InvokeRequest) (*wasm.InvokeResponse, error) {
	// Resolve secret-backed env vars.
	if s.secrets != nil {
		if resolved, err := s.secrets.Resolve(ctx, f.Meta.Project, f.Env); err == nil {
			merged := map[string]string{}
			for k, v := range resolved {
				merged[k] = v
			}
			for k, v := range req.Env {
				merged[k] = v
			}
			req.Env = merged
		}
	}

	start := time.Now()
	var resp *wasm.InvokeResponse
	var err error
	switch f.Runtime {
	case store.FunctionRuntimeFirecracker:
		if s.fcracker == nil {
			err = errors.New("firecracker runtime not available")
		} else {
			resp, err = s.fcracker.Invoke(ctx, f, req)
		}
	default:
		if s.wasm == nil {
			err = errors.New("wasm runtime not available")
		} else {
			resp, err = s.wasm.Invoke(ctx, f, req)
		}
	}
	if s.bus != nil {
		ev := store.EventRecord{
			Project: f.Meta.Project,
			Subject: f.Meta.Name,
			Data: map[string]string{
				"function": f.Meta.Name,
				"runtime":  string(f.Runtime),
				"method":   req.Method,
				"path":     req.Path,
				"durMs":    fmtDuration(time.Since(start)),
			},
		}
		if err != nil {
			ev.Type = "function.error"
			ev.Data["error"] = err.Error()
		} else {
			ev.Type = "function.invoked"
			ev.Data["status"] = fmtInt(resp.Status)
		}
		s.bus.Emit(ev)
	}
	return resp, err
}
