package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/selfcloud/selfcloud/internal/auth"
	"github.com/selfcloud/selfcloud/internal/runtime/wasm"
	"github.com/selfcloud/selfcloud/internal/store"
	"github.com/selfcloud/selfcloud/internal/version"
)

// tokenReader is the random source for webhook tokens; tests can swap
// it out via package-level assignment if they need determinism.
var tokenReader = rand.Reader

// ----- helpers ---------------------------------------------------------

func httpError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg, "status": status})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("empty body")
	}
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(v)
}

func mapStoreErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrAlreadyExists):
		httpError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrConflict):
		httpError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotLeader):
		httpError(w, http.StatusServiceUnavailable, err.Error())
	default:
		httpError(w, http.StatusInternalServerError, err.Error())
	}
	return true
}

// ----- meta + setup ----------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ready": true})
}

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"version":     version.String(),
		"goVersion":   runtime.Version(),
		"goos":        runtime.GOOS,
		"goarch":      runtime.GOARCH,
		"startedAt":   s.startedAt,
		"uptimeSec":   int(time.Since(s.startedAt).Seconds()),
		"description": "selfCloud node",
	})
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetCluster(r.Context())
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"initialized":       cfg.Initialized,
		"multiNode":         cfg.MultiNode,
		"replicationFactor": cfg.ReplicationFactor,
	})
}

type initializeReq struct {
	BootstrapToken string `json:"bootstrapToken"`
	AdminEmail     string `json:"adminEmail"`
	AdminName      string `json:"adminName"`
	AdminPassword  string `json:"adminPassword"`
	MultiNode      bool   `json:"multiNode"`
}

func (s *Server) handleInitialize(w http.ResponseWriter, r *http.Request) {
	var body initializeReq
	if err := decodeJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if err := s.auth.ConsumeBootstrapToken(r.Context(), body.BootstrapToken); err != nil {
		httpError(w, 401, "invalid bootstrap token")
		return
	}
	u, err := s.auth.CreateUser(r.Context(), body.AdminName, body.AdminEmail, body.AdminPassword, true)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	cfg, err := s.store.GetCluster(r.Context())
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	cfg.Initialized = true
	cfg.MultiNode = body.MultiNode
	if cfg.ReplicationFactor == 0 {
		if body.MultiNode {
			cfg.ReplicationFactor = 1 // bumps to 3 once 3 storage nodes present
		} else {
			cfg.ReplicationFactor = 1
		}
	}
	if err := s.store.PutCluster(r.Context(), cfg); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	tok, err := s.auth.CreateToken(r.Context(), "admin-initial", u.Meta.Name, 0, true)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.ensureDefaultProject(r.Context())
	writeJSON(w, 201, map[string]any{
		"user":  u,
		"token": tok.Plaintext,
	})
}

func (s *Server) ensureDefaultProject(ctx context.Context) error {
	c, err := s.store.GetProject(ctx, "default")
	if err == nil && c != nil {
		return nil
	}
	return s.store.PutProject(ctx, &store.Project{
		Meta:        store.Meta{Name: "default"},
		DisplayName: "Default",
	})
}

// ----- auth -------------------------------------------------------------

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body loginReq
	if err := decodeJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	tok, u, err := s.auth.Login(r.Context(), body.Email, body.Password)
	if err != nil {
		httpError(w, 401, "invalid credentials")
		return
	}
	writeJSON(w, 200, map[string]any{"user": u, "token": tok.Plaintext})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	id := IdentityFrom(r.Context())
	if id == nil {
		httpError(w, 401, "no identity")
		return
	}
	u, err := s.store.GetUser(r.Context(), id.UserID)
	if err != nil {
		writeJSON(w, 200, map[string]any{"identity": id})
		return
	}
	writeJSON(w, 200, map[string]any{"identity": id, "user": u})
}

type createTokenReq struct {
	Name string `json:"name"`
	TTL  string `json:"ttl,omitempty"`
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	id := IdentityFrom(r.Context())
	if id == nil {
		httpError(w, 401, "no identity")
		return
	}
	var body createTokenReq
	if err := decodeJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if body.Name == "" {
		httpError(w, 400, "name required")
		return
	}
	var ttl time.Duration
	if body.TTL != "" {
		d, err := time.ParseDuration(body.TTL)
		if err != nil {
			httpError(w, 400, "invalid ttl")
			return
		}
		ttl = d
	}
	tok, err := s.auth.CreateToken(r.Context(), body.Name, id.UserID, ttl, id.IsAdmin)
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 201, tok)
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListTokens(r.Context())
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, tokens)
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.store.DeleteToken(r.Context(), name); mapStoreErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- projects ---------------------------------------------------------

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListProjects(r.Context())
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handlePutProject(w http.ResponseWriter, r *http.Request) {
	var p store.Project
	if err := decodeJSON(r, &p); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if err := s.store.PutProject(r.Context(), &p); mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProject(r.Context(), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteProject(r.Context(), r.PathValue("name")); mapStoreErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- containers -------------------------------------------------------

func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListContainers(r.Context(), r.PathValue("project"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handlePutContainer(w http.ResponseWriter, r *http.Request) {
	var c store.Container
	if err := decodeJSON(r, &c); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	c.Meta.Project = r.PathValue("project")
	if err := s.store.PutContainer(r.Context(), &c); mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 201, c)
}

func (s *Server) handleGetContainer(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetContainer(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) handleDeleteContainer(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetContainer(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	_ = s.containers.Stop(r.Context(), c)
	_ = s.containers.Remove(r.Context(), c)
	if err := s.store.DeleteContainer(r.Context(), r.PathValue("project"), r.PathValue("name")); mapStoreErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStartContainer(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetContainer(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	st, err := s.containers.Start(r.Context(), c)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	c.Status = *st
	if err := s.store.PutContainer(r.Context(), c); mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) handleStopContainer(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetContainer(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	if err := s.containers.Stop(r.Context(), c); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	c.Status.Phase = store.PhaseStopped
	c.Status.UpdatedAt = time.Now().UTC()
	if err := s.store.PutContainer(r.Context(), c); mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetContainer(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	if err := s.containers.Logs(r.Context(), c, false, w); err != nil {
		fmt.Fprintf(w, "\n[error] %s\n", err)
	}
}

// ----- buckets ----------------------------------------------------------

func (s *Server) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListBuckets(r.Context(), r.PathValue("project"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handlePutBucket(w http.ResponseWriter, r *http.Request) {
	var b store.Bucket
	if err := decodeJSON(r, &b); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	b.Meta.Project = r.PathValue("project")
	if s.garageAdm != nil {
		id, err := s.garageAdm.CreateBucket(r.Context(), b.Meta.Name)
		if err == nil {
			b.GarageID = id
		}
	}
	b.Status = store.Status{Phase: store.PhaseRunning, UpdatedAt: time.Now().UTC()}
	if err := s.store.PutBucket(r.Context(), &b); mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 201, b)
}

func (s *Server) handleGetBucket(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.GetBucket(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, b)
}

func (s *Server) handleDeleteBucket(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.GetBucket(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	if s.garageAdm != nil && b.GarageID != "" {
		_ = s.garageAdm.DeleteBucket(r.Context(), b.GarageID)
	}
	if err := s.store.DeleteBucket(r.Context(), b.Meta.Project, b.Meta.Name); mapStoreErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- access keys ------------------------------------------------------

type createAccessKeyReq struct {
	Name        string `json:"name"`
	Bucket      string `json:"bucket,omitempty"`
	Permissions string `json:"permissions"`
}

func (s *Server) handleListAccessKeys(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListAccessKeys(r.Context(), r.PathValue("project"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleCreateAccessKey(w http.ResponseWriter, r *http.Request) {
	var body createAccessKeyReq
	if err := decodeJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if body.Permissions == "" {
		body.Permissions = "read"
	}
	a := &store.AccessKey{
		Meta:        store.Meta{Project: r.PathValue("project"), Name: body.Name},
		BucketName:  body.Bucket,
		Permissions: body.Permissions,
	}
	if s.garageAdm != nil {
		id, secret, err := s.garageAdm.CreateKey(r.Context(), body.Name)
		if err == nil {
			a.AccessKeyID = id
			a.SecretAccessKey = secret
			if body.Bucket != "" {
				if b, err := s.store.GetBucket(r.Context(), r.PathValue("project"), body.Bucket); err == nil && b.GarageID != "" {
					_ = s.garageAdm.AllowKey(r.Context(), id, b.GarageID,
						body.Permissions != "",
						body.Permissions == "write" || body.Permissions == "owner",
						body.Permissions == "owner")
				}
			}
		}
	} else {
		// fallback: synthesise a key locally so the API still works
		raw, _ := auth.GenerateToken()
		a.AccessKeyID = "GK" + raw[len(raw)-12:]
		a.SecretAccessKey = raw
	}
	if err := s.store.PutAccessKey(r.Context(), a); mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 201, a)
}

func (s *Server) handleDeleteAccessKey(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetAccessKey(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	if s.garageAdm != nil && a.AccessKeyID != "" {
		_ = s.garageAdm.DeleteKey(r.Context(), a.AccessKeyID)
	}
	if err := s.store.DeleteAccessKey(r.Context(), a.Meta.Project, a.Meta.Name); mapStoreErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- functions --------------------------------------------------------

func (s *Server) handleListFunctions(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListFunctions(r.Context(), r.PathValue("project"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handlePutFunction(w http.ResponseWriter, r *http.Request) {
	var f store.Function
	if err := decodeJSON(r, &f); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	f.Meta.Project = r.PathValue("project")
	if f.Runtime == "" {
		f.Runtime = store.FunctionRuntimeWasm
	}
	if f.Source.Type == "" {
		if f.Source.Git != nil {
			f.Source.Type = "git"
		} else {
			f.Source.Type = "upload"
		}
	}

	// On creation, mint a unique webhook token for git sources so we
	// don't depend on the user's own URL to be unguessable.
	wasNew := false
	if f.Meta.UID == "" {
		wasNew = true
	} else if cur, err := s.store.GetFunction(r.Context(), f.Meta.Project, f.Meta.Name); err == nil && cur != nil {
		// Preserve existing webhook token / source ref / build pointer
		// so a generic update-from-list call doesn't lose them.
		if f.SourceRef == "" {
			f.SourceRef = cur.SourceRef
		}
		if f.LatestBuild == "" {
			f.LatestBuild = cur.LatestBuild
		}
		if f.Source.Git != nil && cur.Source.Git != nil && f.Source.Git.WebhookToken == "" {
			f.Source.Git.WebhookToken = cur.Source.Git.WebhookToken
		}
	}
	if f.Source.Git != nil && f.Source.Git.WebhookToken == "" {
		t, err := mintWebhookToken()
		if err != nil {
			httpError(w, 500, err.Error())
			return
		}
		f.Source.Git.WebhookToken = t
	}

	if err := s.store.PutFunction(r.Context(), &f); mapStoreErr(w, err) {
		return
	}

	// If this is the first time we're saving a git-backed function and a
	// builder is wired up, kick off an initial build so the user sees
	// build logs without an extra click.
	if wasNew && f.Source.Git != nil && s.builder != nil {
		_, _ = s.builder.Trigger(r.Context(), &f, "create")
	}
	writeJSON(w, 201, f)
}

// mintWebhookToken returns a 16-byte hex token suitable for use as a
// per-function webhook URL segment.
func mintWebhookToken() (string, error) {
	var b [16]byte
	if _, err := tokenReader.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Server) handleGetFunction(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.GetFunction(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, f)
}

func (s *Server) handleDeleteFunction(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.GetFunction(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	if s.wasm != nil {
		_ = s.wasm.Remove(r.Context(), f)
	}
	if s.fcracker != nil {
		_ = s.fcracker.Remove(r.Context(), f)
	}
	if err := s.store.DeleteFunction(r.Context(), f.Meta.Project, f.Meta.Name); mapStoreErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUploadFunctionCode(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.GetFunction(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if s.blobs == nil {
		httpError(w, 503, "blob store not initialised")
		return
	}
	id, err := s.blobs.Put(body)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	f.SourceRef = id
	switch f.Runtime {
	case store.FunctionRuntimeFirecracker:
		if s.fcracker != nil {
			if err := s.fcracker.Deploy(r.Context(), f, body); err != nil {
				httpError(w, 500, err.Error())
				return
			}
		}
	default:
		if s.wasm != nil {
			if err := s.wasm.Deploy(r.Context(), f, body); err != nil {
				httpError(w, 500, err.Error())
				return
			}
		}
	}
	f.Status.Phase = store.PhaseRunning
	f.Status.UpdatedAt = time.Now().UTC()
	if err := s.store.PutFunction(r.Context(), f); mapStoreErr(w, err) {
		return
	}
	writeJSON(w, 200, f)
}

func (s *Server) handleInvokeFunction(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.GetFunction(r.Context(), r.PathValue("project"), r.PathValue("name"))
	if mapStoreErr(w, err) {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	subPath := r.URL.Query().Get("path")
	if subPath == "" {
		subPath = "/"
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
		Path:   req.Path,
		DurMS:  dur.Milliseconds(),
		BodyKB: len(body) / 1024,
	}
	if err != nil {
		rec.Status = 500
		rec.Error = err.Error()
		s.invocations.record(f.Meta.Project+"/"+f.Meta.Name, rec)
		httpError(w, 500, err.Error())
		return
	}
	rec.Status = resp.Status
	if len(resp.Logs) > 0 {
		rec.LogsTail = tailString(resp.Logs, 256)
	}
	s.invocations.record(f.Meta.Project+"/"+f.Meta.Name, rec)
	wasm.CopyResponse(w, resp)
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
