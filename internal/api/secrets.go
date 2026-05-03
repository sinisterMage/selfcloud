package api

import (
	"net/http"
)

// putSecretReq is the JSON body accepted by POST /api/v1/projects/{p}/secrets.
// Value is the plaintext value to encrypt; it's never echoed back.
type putSecretReq struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Value       string `json:"value"`
}

func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListSecrets(r.Context(), r.PathValue("project"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handlePutSecret(w http.ResponseWriter, r *http.Request) {
	if s.secrets == nil {
		httpError(w, 503, "secrets manager not configured")
		return
	}
	var body putSecretReq
	if err := decodeJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if body.Name == "" {
		httpError(w, 400, "name required")
		return
	}
	if body.Value == "" {
		httpError(w, 400, "value required")
		return
	}
	sec, err := s.secrets.Put(r.Context(), r.PathValue("project"), body.Name, body.Description, body.Value)
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 201, sec)
}

func (s *Server) handleGetSecret(w http.ResponseWriter, r *http.Request) {
	sec, err := s.store.GetSecret(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	// Strip ciphertext on read; only the explicit reveal endpoint
	// returns plaintext.
	sec.Nonce = nil
	sec.Ciphertext = nil
	writeJSON(w, 200, sec)
}

func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSecret(r.Context(), r.PathValue("project"), r.PathValue("name")); mapStoreErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRevealSecret returns the plaintext value of a secret. Admin only;
// the response never gets cached (no-store) so a curious browser tab can't
// hold it.
func (s *Server) handleRevealSecret(w http.ResponseWriter, r *http.Request) {
	id := IdentityFrom(r.Context())
	if id == nil || !id.IsAdmin {
		httpError(w, 403, "admin only")
		return
	}
	if s.secrets == nil {
		httpError(w, 503, "secrets manager not configured")
		return
	}
	pt, err := s.secrets.Reveal(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	w.Header().Set("cache-control", "no-store")
	writeJSON(w, 200, map[string]any{
		"name":  r.PathValue("name"),
		"value": pt,
	})
}
