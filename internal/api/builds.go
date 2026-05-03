package api

import (
	"context"
	"net/http"
	"time"

	"nhooyr.io/websocket"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// handleListBuilds returns recent builds for a function, newest first.
func (s *Server) handleListBuilds(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListBuilds(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

// handleGetBuild returns a single Build by name (e.g. "build-<uid>").
func (s *Server) handleGetBuild(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.GetBuild(r.Context(), r.PathValue("project"), r.PathValue("id"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, build)
}

// handleTriggerBuild starts a manual build for a git-backed function.
func (s *Server) handleTriggerBuild(w http.ResponseWriter, r *http.Request) {
	if s.builder == nil {
		httpError(w, 503, "builder not configured (containerd may be unavailable)")
		return
	}
	fn, err := s.store.GetFunction(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	if fn.Source.Type != "git" || fn.Source.Git == nil {
		httpError(w, 400, "function does not have a git source")
		return
	}
	build, err := s.builder.Trigger(r.Context(), fn, "manual")
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, build)
}

// handleBuildLogsWS streams the build's stdout/stderr to the dashboard
// over a WebSocket. If the build has already completed it replays the
// on-disk log and closes.
func (s *Server) handleBuildLogsWS(w http.ResponseWriter, r *http.Request) {
	if s.builder == nil {
		httpError(w, 503, "builder not configured")
		return
	}
	build, err := s.store.GetBuild(r.Context(), r.PathValue("project"), r.PathValue("id"))
	if mapStoreErr(w, err) {
		return
	}
	conn, err := upgradeWS(w, r)
	if err != nil {
		log.With("err", err).Warn("build logs ws: accept")
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	go pingLoop(r.Context(), conn)

	ch, unsub := s.builder.StreamLogs(build.Meta.UID)
	defer unsub()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Watch for build completion in the background and close the WS so
	// the client sees the final state without waiting for an idle ping.
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				cur, err := s.store.GetBuild(ctx, build.Meta.Project, build.Meta.Name)
				if err == nil && cur != nil {
					if cur.Status == store.PhaseSucceeded || cur.Status == store.PhaseFailed {
						time.Sleep(time.Second)
						cancel()
						return
					}
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Write(wctx, websocket.MessageText, []byte(line))
			wcancel()
			if err != nil {
				return
			}
		}
	}
}
