package api

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/runtime/firecracker"
)

// handleListFirecrackerTemplates returns the registered rootfs/kernel
// templates from the firecracker runtime, including whether each one is
// available on disk. The dashboard uses this to populate the New Function
// dialog when "firecracker" is selected.
func (s *Server) handleListFirecrackerTemplates(w http.ResponseWriter, _ *http.Request) {
	if s.fcracker == nil {
		writeJSON(w, 200, []firecracker.Template{})
		return
	}
	lister, ok := s.fcracker.(firecracker.TemplateLister)
	if !ok {
		writeJSON(w, 200, []firecracker.Template{})
		return
	}
	tpls := lister.Templates()
	sort.Slice(tpls, func(i, j int) bool { return tpls[i].Name < tpls[j].Name })
	writeJSON(w, 200, tpls)
}

// ---- per-function invocation ring buffer --------------------------------

// invocationRecord captures one invocation for the FunctionDetail panel.
type invocationRecord struct {
	At       time.Time `json:"at"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Status   int       `json:"status"`
	DurMS    int64     `json:"durMs"`
	BodyKB   int       `json:"bodyKb"`
	Error    string    `json:"error,omitempty"`
	LogsTail string    `json:"logsTail,omitempty"`
}

// invocationRing is a fixed-size circular buffer per function key.
type invocationRing struct {
	mu    sync.Mutex
	rings map[string][]invocationRecord
	size  int
}

func newInvocationRing(size int) *invocationRing {
	if size <= 0 {
		size = 100
	}
	return &invocationRing{rings: map[string][]invocationRecord{}, size: size}
}

func (r *invocationRing) record(key string, rec invocationRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	buf := r.rings[key]
	buf = append(buf, rec)
	if len(buf) > r.size {
		buf = buf[len(buf)-r.size:]
	}
	r.rings[key] = buf
}

func (r *invocationRing) list(key string) []invocationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	src := r.rings[key]
	out := make([]invocationRecord, len(src))
	copy(out, src)
	// reverse so most recent is first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *Server) handleListFunctionInvocations(w http.ResponseWriter, r *http.Request) {
	if s.invocations == nil {
		writeJSON(w, 200, []invocationRecord{})
		return
	}
	key := r.PathValue("project") + "/" + r.PathValue("name")
	writeJSON(w, 200, s.invocations.list(key))
}
