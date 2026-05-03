// Package sidecar exposes a per-host unix socket that containers use to
// push application-level events into selfcloud's event bus. Containers
// are expected to bind-mount the socket at /var/run/selfcloud/event.sock
// and POST JSON envelopes to it.
//
// Wire format:
//
//   POST /events
//   {"type": "order.placed", "subject": "order-42", "data": {"...":"..."}}
//
// The Sidecar is a tiny http.Server listening on a unix socket. There is
// no auth: anything inside a container that has the socket mounted is
// trusted, just like Docker's own /var/run/docker.sock.
package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// Emitter is the narrow bus façade.
type Emitter interface {
	Emit(ev store.EventRecord)
}

// Sidecar is the unix-socket HTTP server.
type Sidecar struct {
	bus    Emitter
	socket string
	srv    *http.Server
}

// SocketPath returns the canonical sidecar socket path under dataDir.
func SocketPath(dataDir string) string {
	return filepath.Join(dataDir, "sockets", "event.sock")
}

// New wires a sidecar but does not start it; call Run to listen.
func New(dataDir string, bus Emitter) *Sidecar {
	return &Sidecar{bus: bus, socket: SocketPath(dataDir)}
}

// Run starts listening. It blocks until ctx is cancelled.
func (s *Sidecar) Run(ctx context.Context) error {
	if s.bus == nil {
		return errors.New("sidecar: bus is nil")
	}
	if err := os.MkdirAll(filepath.Dir(s.socket), 0o755); err != nil {
		return err
	}
	// Stale socket from a previous run.
	_ = os.Remove(s.socket)
	ln, err := net.Listen("unix", s.socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.socket, 0o666); err != nil {
		log.With("err", err).Warn("sidecar: chmod socket")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /events", s.handleEvent)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	s.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutCtx)
		_ = os.Remove(s.socket)
	}()
	log.With("socket", s.socket).Info("sidecar: listening")
	if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type sidecarRequest struct {
	Type    string            `json:"type"`
	Project string            `json:"project,omitempty"`
	Subject string            `json:"subject,omitempty"`
	Data    map[string]string `json:"data,omitempty"`
}

func (s *Sidecar) handleEvent(w http.ResponseWriter, r *http.Request) {
	var body sidecarRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Type == "" {
		http.Error(w, "type required", http.StatusBadRequest)
		return
	}
	// Surface the type as `app.event` so EventRules can match it without
	// enumerating every user-defined type. The original is preserved in
	// data["app_type"].
	data := body.Data
	if data == nil {
		data = map[string]string{}
	}
	data["app_type"] = body.Type
	s.bus.Emit(store.EventRecord{
		Type:    "app.event",
		Project: body.Project,
		Subject: body.Subject,
		Data:    data,
	})
	w.WriteHeader(http.StatusAccepted)
}
