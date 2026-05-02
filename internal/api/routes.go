package api

import (
	"net/http"
)

// routes wires the URL space. We use stdlib net/http patterns (Go 1.22+)
// to keep deps light.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Health & meta (unauthenticated).
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /api/v1/meta", s.handleMeta)

	// First-run wizard endpoints. They use the bootstrap token in the body.
	mux.HandleFunc("POST /api/v1/setup/initialize", s.handleInitialize)
	mux.HandleFunc("GET /api/v1/setup/status", s.handleSetupStatus)

	// Cluster join is unauthenticated; the join token in the body is the
	// secret. (We can't require an API token because the joining node
	// doesn't have one yet.)
	mux.HandleFunc("POST /api/v1/cluster/join", s.handleClusterJoin)

	// Auth endpoints (unauthenticated).
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)

	// Authenticated API surface.
	auth := http.NewServeMux()

	auth.HandleFunc("GET /api/v1/auth/me", s.handleMe)
	auth.HandleFunc("POST /api/v1/auth/tokens", s.handleCreateToken)
	auth.HandleFunc("GET /api/v1/auth/tokens", s.handleListTokens)
	auth.HandleFunc("DELETE /api/v1/auth/tokens/{name}", s.handleDeleteToken)

	auth.HandleFunc("GET /api/v1/projects", s.handleListProjects)
	auth.HandleFunc("POST /api/v1/projects", s.handlePutProject)
	auth.HandleFunc("GET /api/v1/projects/{name}", s.handleGetProject)
	auth.HandleFunc("DELETE /api/v1/projects/{name}", s.handleDeleteProject)

	auth.HandleFunc("GET /api/v1/projects/{project}/containers", s.handleListContainers)
	auth.HandleFunc("POST /api/v1/projects/{project}/containers", s.handlePutContainer)
	auth.HandleFunc("GET /api/v1/projects/{project}/containers/{name}", s.handleGetContainer)
	auth.HandleFunc("DELETE /api/v1/projects/{project}/containers/{name}", s.handleDeleteContainer)
	auth.HandleFunc("POST /api/v1/projects/{project}/containers/{name}/start", s.handleStartContainer)
	auth.HandleFunc("POST /api/v1/projects/{project}/containers/{name}/stop", s.handleStopContainer)
	auth.HandleFunc("GET /api/v1/projects/{project}/containers/{name}/logs", s.handleContainerLogs)
	auth.HandleFunc("GET /api/v1/projects/{project}/containers/{name}/logs/ws", s.handleContainerLogsWS)
	auth.HandleFunc("GET /api/v1/projects/{project}/containers/{name}/exec", s.handleContainerExecWS)

	auth.HandleFunc("GET /api/v1/projects/{project}/buckets", s.handleListBuckets)
	auth.HandleFunc("POST /api/v1/projects/{project}/buckets", s.handlePutBucket)
	auth.HandleFunc("GET /api/v1/projects/{project}/buckets/{name}", s.handleGetBucket)
	auth.HandleFunc("DELETE /api/v1/projects/{project}/buckets/{name}", s.handleDeleteBucket)

	auth.HandleFunc("GET /api/v1/projects/{project}/access-keys", s.handleListAccessKeys)
	auth.HandleFunc("POST /api/v1/projects/{project}/access-keys", s.handleCreateAccessKey)
	auth.HandleFunc("DELETE /api/v1/projects/{project}/access-keys/{name}", s.handleDeleteAccessKey)

	auth.HandleFunc("GET /api/v1/projects/{project}/functions", s.handleListFunctions)
	auth.HandleFunc("POST /api/v1/projects/{project}/functions", s.handlePutFunction)
	auth.HandleFunc("GET /api/v1/projects/{project}/functions/{name}", s.handleGetFunction)
	auth.HandleFunc("DELETE /api/v1/projects/{project}/functions/{name}", s.handleDeleteFunction)
	auth.HandleFunc("POST /api/v1/projects/{project}/functions/{name}/code", s.handleUploadFunctionCode)
	auth.HandleFunc("POST /api/v1/projects/{project}/functions/{name}/invoke", s.handleInvokeFunction)

	auth.HandleFunc("GET /api/v1/cluster", s.handleGetCluster)
	auth.HandleFunc("GET /api/v1/cluster/nodes", s.handleListNodes)
	auth.HandleFunc("POST /api/v1/cluster/join-tokens", s.handleIssueJoinToken)
	auth.HandleFunc("GET /api/v1/cluster/join-tokens", s.handleListJoinTokens)
	auth.HandleFunc("PUT /api/v1/cluster", s.handlePutCluster)

	mux.Handle("GET /api/v1/", s.requireAuth(auth, false))
	mux.Handle("POST /api/v1/", s.requireAuth(auth, false))
	mux.Handle("PUT /api/v1/", s.requireAuth(auth, false))
	mux.Handle("DELETE /api/v1/", s.requireAuth(auth, false))
	mux.Handle("PATCH /api/v1/", s.requireAuth(auth, false))

	// Function HTTP triggers (unauthenticated by default; functions can
	// gate themselves).
	mux.HandleFunc("/fn/", s.handleFunctionInvoke)

	// S3 reverse proxy is wired up by Phase 3.
	mux.HandleFunc("/s3/", s.handleS3Proxy)

	// Dashboard.
	mux.Handle("/", dashboardHandler())

	return mux
}
