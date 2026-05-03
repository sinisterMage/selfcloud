package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"nhooyr.io/websocket"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// ----- event rules CRUD -------------------------------------------------

func (s *Server) handleListEventRules(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListEventRules(r.Context(), r.PathValue("project"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handlePutEventRule(w http.ResponseWriter, r *http.Request) {
	var rule store.EventRule
	if err := decodeJSON(r, &rule); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	rule.Meta.Project = r.PathValue("project")
	if rule.Meta.Name == "" {
		httpError(w, 400, "name required")
		return
	}
	// Default-enable new rules so users see them firing right away.
	if rule.Meta.Generation == 0 && !rule.Enabled {
		rule.Enabled = true
	}
	if err := s.store.PutEventRule(r.Context(), &rule); mapStoreErr(w, err) {
		return
	}
	// Strip secret on response.
	if rule.Action.Webhook != nil {
		rule.Action.Webhook.Secret = ""
	}
	writeJSON(w, 200, rule)
}

func (s *Server) handleGetEventRule(w http.ResponseWriter, r *http.Request) {
	rule, err := s.store.GetEventRule(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	if rule.Action.Webhook != nil {
		rule.Action.Webhook.Secret = ""
	}
	writeJSON(w, 200, rule)
}

func (s *Server) handleDeleteEventRule(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteEventRule(r.Context(), r.PathValue("project"), r.PathValue("name")); mapStoreErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListDeliveries returns recent webhook delivery attempts for a rule.
func (s *Server) handleListDeliveries(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListDeliveries(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

// handleTestRule synthesises a fake event matching the rule and dispatches
// it through the bus immediately. Useful for sanity-checking webhooks.
func (s *Server) handleTestRule(w http.ResponseWriter, r *http.Request) {
	if s.bus == nil {
		httpError(w, 503, "event bus not configured")
		return
	}
	rule, err := s.store.GetEventRule(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	t := "test.fired"
	if len(rule.Match.Types) > 0 {
		t = rule.Match.Types[0]
	}
	subject := rule.Match.Subject
	if subject == "" {
		subject = rule.Meta.Name
	}
	s.bus.Emit(store.EventRecord{
		Type:    t,
		Project: rule.Meta.Project,
		Subject: subject,
		Data: map[string]string{
			"test":    "true",
			"rule":    rule.Meta.Name,
			"trigger": "manual",
		},
	})
	writeJSON(w, 202, map[string]any{"queued": true})
}

// ----- event log + WebSocket --------------------------------------------

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}
	out, err := s.store.ListEvents(r.Context(), r.PathValue("project"), limit)
	if mapStoreErr(w, err) {
		return
	}
	if t := r.URL.Query().Get("type"); t != "" {
		filtered := make([]store.EventRecord, 0, len(out))
		for _, e := range out {
			if e.Type == t {
				filtered = append(filtered, e)
			}
		}
		out = filtered
	}
	writeJSON(w, 200, out)
}

// handleEventsWS streams live events for a project. It first drains the
// backlog (last 50 events) so the dashboard immediately has context, then
// pushes every new event as it arrives.
func (s *Server) handleEventsWS(w http.ResponseWriter, r *http.Request) {
	if s.bus == nil {
		httpError(w, 503, "event bus not configured")
		return
	}
	project := r.PathValue("project")

	conn, err := upgradeWS(w, r)
	if err != nil {
		log.With("err", err).Warn("events ws: accept")
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	go pingLoop(r.Context(), conn)

	// Backfill: send the last N events so the timeline is populated
	// immediately on open.
	if backlog, err := s.store.ListEvents(r.Context(), project, 50); err == nil {
		for i := len(backlog) - 1; i >= 0; i-- {
			if err := writeEvent(r.Context(), conn, backlog[i]); err != nil {
				return
			}
		}
	}

	// Live tail.
	ch := make(chan store.EventRecord, 64)
	unsub := s.bus.Subscribe(func(ev store.EventRecord) {
		if ev.Project != "" && ev.Project != project {
			return
		}
		select {
		case ch <- ev:
		default:
			// Drop on backpressure rather than block the bus.
		}
	})
	defer unsub()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			if err := writeEvent(ctx, conn, ev); err != nil {
				return
			}
		}
	}
}

func writeEvent(ctx context.Context, conn *websocket.Conn, ev store.EventRecord) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data)
}
