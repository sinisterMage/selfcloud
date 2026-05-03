// Package api hosts the HTTPS REST + WebSocket API that everything (the
// dashboard, the CLI, the Terraform provider, joining nodes) talks to.
package api

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/selfcloud/selfcloud/internal/auth"
	"github.com/selfcloud/selfcloud/internal/cluster"
	"github.com/selfcloud/selfcloud/internal/config"
	"github.com/selfcloud/selfcloud/internal/events"
	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/runtime/container"
	"github.com/selfcloud/selfcloud/internal/runtime/firecracker"
	"github.com/selfcloud/selfcloud/internal/runtime/wasm"
	"github.com/selfcloud/selfcloud/internal/secrets"
	"github.com/selfcloud/selfcloud/internal/storage/garage"
	"github.com/selfcloud/selfcloud/internal/store"
)

// Server bundles the HTTP server with all its dependencies. One per node.
type Server struct {
	cfg         *config.Config
	store       *store.Store
	auth        *auth.Manager
	cluster     *cluster.Manager
	containers  container.Runtime
	wasm        wasm.Runtime
	fcracker    firecracker.Runtime
	garage      *garage.Supervisor
	garageAdm   *garage.AdminClient
	blobs       *wasm.BlobStore
	raft        *store.Raft
	bus         *events.Bus
	secrets     *secrets.Manager
	builder     BuilderHook
	httpSrv     *http.Server
	startedAt   time.Time
	invocations *invocationRing
	// s3 is built lazily from the cluster-level internal access key the
	// first time the bucket browser asks for it.
	s3   *minio.Client
	s3Mu sync.Mutex
}

// BuilderHook is the optional Phase 2 git-source builder. We keep the
// type abstract here so the api package doesn't import builder/.
type BuilderHook interface {
	Trigger(ctx context.Context, f *store.Function, trigger string) (*store.Build, error)
	StreamLogs(buildUID string) (<-chan string, func())
}

// Options bundles dependency injection so cmd/selfcloud/server.go can wire
// everything from one place.
type Options struct {
	Config      *config.Config
	Store       *store.Store
	Auth        *auth.Manager
	Cluster     *cluster.Manager
	Containers  container.Runtime
	Wasm        wasm.Runtime
	Firecracker firecracker.Runtime
	Garage      *garage.Supervisor
	GarageAdmin *garage.AdminClient
	Blobs       *wasm.BlobStore
	Raft        *store.Raft
	Bus         *events.Bus
	Secrets     *secrets.Manager
	Builder     BuilderHook
}

// AttachRuleDispatcher registers a *events.RuleDispatcher onto the bus
// using adapters that bridge to this server's runtime façades. Called
// from cmd/selfcloud/server.go once the Server is built.
func (s *Server) AttachRuleDispatcher() {
	if s.bus == nil || s.store == nil {
		return
	}
	disp := events.NewRuleDispatcher(s.store,
		functionInvokerAdapter{s: s},
		containerControlAdapter{s: s},
	)
	s.bus.AddSink(disp)
}

// New builds an API server.
func New(opt Options) *Server {
	return &Server{
		cfg:         opt.Config,
		store:       opt.Store,
		auth:        opt.Auth,
		cluster:     opt.Cluster,
		containers:  opt.Containers,
		wasm:        opt.Wasm,
		fcracker:    opt.Firecracker,
		garage:      opt.Garage,
		garageAdm:   opt.GarageAdmin,
		blobs:       opt.Blobs,
		raft:        opt.Raft,
		bus:         opt.Bus,
		secrets:     opt.Secrets,
		builder:     opt.Builder,
		startedAt:   time.Now().UTC(),
		invocations: newInvocationRing(100),
	}
}

// s3Client returns a memoised minio client signed with the cluster-level
// internal admin key. It returns an error if the key isn't provisioned yet
// (e.g. before the first bucket is created).
func (s *Server) s3Client(ctx context.Context) (*minio.Client, error) {
	s.s3Mu.Lock()
	defer s.s3Mu.Unlock()
	if s.s3 != nil {
		return s.s3, nil
	}
	cfg, err := s.store.GetCluster(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.S3InternalKeyID == "" || cfg.S3InternalKeySecret == "" {
		// Try to provision one now via the garage admin client.
		if s.garageAdm == nil {
			return nil, errors.New("garage admin client unavailable")
		}
		id, secret, err := s.garageAdm.CreateKey(ctx, "selfcloud-internal")
		if err != nil {
			return nil, err
		}
		cfg.S3InternalKeyID = id
		cfg.S3InternalKeySecret = secret
		if err := s.store.PutCluster(ctx, cfg); err != nil {
			return nil, err
		}
	}
	endpoint := s.cfg.S3Addr
	if strings.HasPrefix(endpoint, "0.0.0.0") {
		endpoint = "127.0.0.1" + strings.TrimPrefix(endpoint, "0.0.0.0")
	}
	cl, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.S3InternalKeyID, cfg.S3InternalKeySecret, ""),
		Secure: false,
	})
	if err != nil {
		return nil, err
	}
	s.s3 = cl
	return cl, nil
}

// Run starts listening. Blocks until ctx is cancelled or the server errors.
func (s *Server) Run(ctx context.Context) error {
	mux := s.routes()
	handler := s.recoverer(s.requestLogger(s.cors(mux)))
	srv := &http.Server{
		Addr:              s.cfg.APIAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if !s.cfg.DisableTLS && s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	s.httpSrv = srv

	errCh := make(chan error, 1)
	go func() {
		log.With("addr", s.cfg.APIAddr, "tls", !s.cfg.DisableTLS).Info("api: listening")
		var err error
		if s.cfg.DisableTLS {
			err = srv.ListenAndServe()
		} else {
			err = srv.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// requireAuth is the standard auth middleware. It accepts a Bearer token,
// the bootstrap token (used once during first-run wizard), or local Unix
// socket connections (deferred).
func (s *Server) requireAuth(next http.Handler, adminOnly bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		header := r.Header.Get("authorization")
		switch {
		case strings.HasPrefix(strings.ToLower(header), "bearer "):
			token = strings.TrimSpace(header[len("Bearer "):])
		case r.URL.Query().Get("access_token") != "":
			// Browsers can't set headers when opening a WebSocket; allow
			// the token as a query parameter for WS endpoints.
			token = r.URL.Query().Get("access_token")
		}
		if token == "" {
			httpError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		id, err := s.auth.ResolveToken(r.Context(), token)
		if err != nil {
			httpError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if adminOnly && !id.IsAdmin {
			httpError(w, http.StatusForbidden, "admin only")
			return
		}
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("origin")
		if origin != "" {
			w.Header().Set("access-control-allow-origin", origin)
			w.Header().Set("access-control-allow-credentials", "true")
			w.Header().Set("access-control-allow-headers", "authorization, content-type")
			w.Header().Set("access-control-allow-methods", "GET,POST,PUT,DELETE,PATCH,OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		if !strings.HasPrefix(r.URL.Path, "/healthz") && !strings.HasPrefix(r.URL.Path, "/assets") {
			log.L().Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.status,
				"dur_ms", time.Since(start).Milliseconds(),
			)
		}
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.L().Error("panic", "err", rec, "path", r.URL.Path)
				httpError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

type ctxKey int

const identityKey ctxKey = 1

func withIdentity(ctx context.Context, id *auth.Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// IdentityFrom returns the authenticated identity, if any.
func IdentityFrom(ctx context.Context) *auth.Identity {
	if v, ok := ctx.Value(identityKey).(*auth.Identity); ok {
		return v
	}
	return nil
}
