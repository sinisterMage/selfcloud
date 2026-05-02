package api

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

func (s *Server) handleGetCluster(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetCluster(r.Context())
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	cfg.BootstrapToken = ""
	cfg.GarageRPCSecret = ""
	cfg.GarageAdminToken = ""
	cfg.JoinTokens = nil
	writeJSON(w, 200, cfg)
}

func (s *Server) handlePutCluster(w http.ResponseWriter, r *http.Request) {
	id := IdentityFrom(r.Context())
	if id == nil || !id.IsAdmin {
		httpError(w, 403, "admin only")
		return
	}
	var body store.ClusterConfig
	if err := decodeJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	cur, err := s.store.GetCluster(r.Context())
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	cur.MultiNode = body.MultiNode
	if body.ReplicationFactor > 0 {
		cur.ReplicationFactor = body.ReplicationFactor
	}
	if err := s.store.PutCluster(r.Context(), cur); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, cur)
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListNodes(r.Context())
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

type issueJoinReq struct {
	TTL string `json:"ttl,omitempty"`
}

func (s *Server) handleIssueJoinToken(w http.ResponseWriter, r *http.Request) {
	id := IdentityFrom(r.Context())
	if id == nil || !id.IsAdmin {
		httpError(w, 403, "admin only")
		return
	}
	var body issueJoinReq
	_ = decodeJSON(r, &body)
	ttl := 24 * time.Hour
	if body.TTL != "" {
		if d, err := time.ParseDuration(body.TTL); err == nil {
			ttl = d
		}
	}
	plain, tok, err := s.cluster.Issue(r.Context(), id.UserID, ttl)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{
		"id":        tok.ID,
		"token":     plain,
		"expiresAt": tok.ExpiresAt,
		"command":   "curl -fsSL https://get.selfcloud.dev | sh -s -- --join " + r.Host + " --token " + plain,
	})
}

func (s *Server) handleListJoinTokens(w http.ResponseWriter, r *http.Request) {
	out, err := s.cluster.List(r.Context())
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

// handleS3Proxy reverse-proxies S3 traffic to the local Garage instance. We
// translate the path-style URL `/s3/<bucket>/<key>` into Garage's listener
// at the configured S3 address.
func (s *Server) handleS3Proxy(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil {
		httpError(w, 503, "garage not configured")
		return
	}
	target, err := url.Parse("http://" + s.cfg.S3Addr)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/s3")
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	r.Host = target.Host
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.With("err", err).Warn("s3 proxy error")
		httpError(w, http.StatusBadGateway, "garage unreachable")
	}
	proxy.ServeHTTP(w, r)
}
