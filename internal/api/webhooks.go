package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// handleGitWebhook is the unauthenticated push-to-deploy entry point.
// Selfcloud stamps a unique token onto each git-backed function at
// creation time; that token forms the URL path segment, so a leak
// of one function's URL doesn't compromise others. If the function has
// a configured WebhookSecret we verify GitHub's x-hub-signature-256
// HMAC over the raw body; otherwise we accept the bearer-token style
// header generic webhooks use.
func (s *Server) handleGitWebhook(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		httpError(w, 400, "read body")
		return
	}
	defer r.Body.Close()

	// Look up the matching function. With a fully-loaded cluster this is
	// O(N) but the function set is small in practice (tens to hundreds);
	// we can index later if needed.
	fn, err := s.findFunctionByWebhookToken(r, token)
	if err != nil {
		// Don't leak whether the token exists vs. matched.
		http.NotFound(w, r)
		return
	}

	// HMAC verification. We accept either:
	//   - x-hub-signature-256 (GitHub style):  "sha256=<hex>"
	//   - authorization: bearer <secret>      (generic)
	if fn.Source.Git != nil && fn.Source.Git.WebhookSecret != "" {
		if !verifyHMAC(r.Header.Get("x-hub-signature-256"), body, fn.Source.Git.WebhookSecret) &&
			!verifyBearer(r.Header.Get("authorization"), fn.Source.Git.WebhookSecret) {
			httpError(w, 401, "bad signature")
			return
		}
	}

	if s.builder == nil {
		httpError(w, 503, "builder not configured")
		return
	}

	// Pull commit sha out of the payload if present (GitHub format) so the
	// build log can show it before the clone resolves.
	var payload struct {
		After string `json:"after"`
		Ref   string `json:"ref"`
	}
	_ = json.Unmarshal(body, &payload)

	build, err := s.builder.Trigger(r.Context(), fn, "webhook")
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	if s.bus != nil {
		s.bus.Emit(store.EventRecord{
			Type:    "git.push",
			Project: fn.Meta.Project,
			Subject: fn.Meta.Name,
			Data: map[string]string{
				"function": fn.Meta.Name,
				"commit":   payload.After,
				"ref":      payload.Ref,
				"build":    build.Meta.UID,
			},
		})
	}
	log.With("fn", fn.Meta.Name, "build", build.Meta.UID, "commit", payload.After).
		Info("webhook: build triggered")
	writeJSON(w, 202, map[string]any{
		"queued":  true,
		"build":   build.Meta.UID,
		"commit":  payload.After,
	})
}

// findFunctionByWebhookToken walks every project's functions looking for
// one whose Source.Git.WebhookToken matches token.
func (s *Server) findFunctionByWebhookToken(r *http.Request, token string) (*store.Function, error) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		fns, err := s.store.ListFunctions(r.Context(), p.Meta.Name)
		if err != nil {
			continue
		}
		for i := range fns {
			f := &fns[i]
			if f.Source.Git != nil && f.Source.Git.WebhookToken == token {
				return f, nil
			}
		}
	}
	return nil, store.ErrNotFound
}

func verifyHMAC(header string, body []byte, secret string) bool {
	if header == "" || secret == "" {
		return false
	}
	parts := strings.SplitN(header, "=", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return false
	}
	want, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

func verifyBearer(header, secret string) bool {
	if header == "" || secret == "" {
		return false
	}
	low := strings.ToLower(header)
	if !strings.HasPrefix(low, "bearer ") {
		return false
	}
	provided := strings.TrimSpace(header[len("Bearer "):])
	return hmac.Equal([]byte(provided), []byte(secret))
}
