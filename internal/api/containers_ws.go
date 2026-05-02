package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"nhooyr.io/websocket"

	"github.com/selfcloud/selfcloud/internal/log"
)

// handleContainerLogsWS streams container logs over a WebSocket. URL:
// `/api/v1/projects/{project}/containers/{name}/logs/ws`.
func (s *Server) handleContainerLogsWS(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetContainer(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	conn, err := upgradeWS(w, r)
	if err != nil {
		log.With("err", err).Warn("ws upgrade failed")
		return
	}
	defer conn.Close(websocket.StatusInternalError, "")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go pingLoop(ctx, conn)

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_ = s.containers.Logs(ctx, c, true, pw)
	}()
	if err := pump(ctx, conn, pr); err != nil {
		log.With("err", err).Debug("logs ws closed")
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

// handleContainerExecWS opens an exec session over a WebSocket. The first
// message from the client is a JSON header with the command to run; from
// there bytes flow in both directions.
func (s *Server) handleContainerExecWS(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetContainer(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	conn, err := upgradeWS(w, r)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go pingLoop(ctx, conn)

	_, raw, err := conn.Read(ctx)
	if err != nil {
		return
	}
	var hdr struct {
		Cmd []string `json:"cmd"`
	}
	if err := json.Unmarshal(raw, &hdr); err != nil || len(hdr.Cmd) == 0 {
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"error":"first message must be {\"cmd\":[...]}"}`))
		return
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go func() {
		defer stdinW.Close()
		_ = readPump(ctx, conn, stdinW)
	}()
	go func() {
		defer stdoutR.Close()
		_ = pump(ctx, conn, stdoutR)
	}()

	if err := s.containers.Exec(ctx, c, hdr.Cmd, stdinR, stdoutW, stdoutW); err != nil {
		_, _ = stdoutW.Write([]byte("\n[error] " + err.Error() + "\n"))
	}
	_ = stdoutW.Close()
	conn.Close(websocket.StatusNormalClosure, "")
}
