package container

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/store"
)

// Stub is an in-memory runtime used in tests and on dev machines that don't
// have containerd available. It implements the same interface as the real
// runtime so reconcile loops behave identically.
type Stub struct {
	mu     sync.Mutex
	state  map[string]*store.ContainerStatus
	logs   map[string][]byte
	nextIP byte
}

// NewStub returns a fresh in-memory runtime.
func NewStub() *Stub {
	return &Stub{
		state:  map[string]*store.ContainerStatus{},
		logs:   map[string][]byte{},
		nextIP: 2,
	}
}

func key(c *store.Container) string {
	return c.Meta.Project + "/" + c.Meta.Name
}

func (s *Stub) Start(_ context.Context, c *store.Container) (*store.ContainerStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &store.ContainerStatus{
		Status: store.Status{
			Phase:     store.PhaseRunning,
			Message:   "started by stub runtime",
			UpdatedAt: time.Now().UTC(),
		},
		ContainerdID: "stub-" + c.Meta.UID,
		StartedAt:    time.Now().UTC(),
		IPAddress:    fmt.Sprintf("10.42.0.%d", s.nextIP),
		Image:        c.Image,
	}
	s.nextIP++
	s.state[key(c)] = st
	s.logs[key(c)] = []byte(fmt.Sprintf("[stub] container %s started with image %s\n", c.Meta.Name, c.Image))
	return st, nil
}

func (s *Stub) Stop(_ context.Context, c *store.Container) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.state[key(c)]; ok {
		st.Phase = store.PhaseStopped
		st.UpdatedAt = time.Now().UTC()
	}
	return nil
}

func (s *Stub) Remove(_ context.Context, c *store.Container) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, key(c))
	delete(s.logs, key(c))
	return nil
}

func (s *Stub) Logs(_ context.Context, c *store.Container, _ bool, w io.Writer) error {
	s.mu.Lock()
	data := s.logs[key(c)]
	s.mu.Unlock()
	_, err := w.Write(data)
	return err
}

func (s *Stub) Exec(_ context.Context, c *store.Container, cmd []string, _ io.Reader, stdout, _ io.Writer) error {
	_, _ = fmt.Fprintf(stdout, "[stub] exec %v in %s\n", cmd, c.Meta.Name)
	return nil
}

func (s *Stub) List(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.state))
	for k := range s.state {
		out = append(out, k)
	}
	return out, nil
}

func (s *Stub) Close() error { return nil }
