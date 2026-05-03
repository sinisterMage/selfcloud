package api

import (
	"context"
	"net/http"
	"time"

	"github.com/selfcloud/selfcloud/internal/runtime/wasm"
	"github.com/selfcloud/selfcloud/internal/store"
)

// functionInvokerAdapter implements events.FunctionInvoker by reusing
// Server.invoke. We expose a thin shim so the events package doesn't
// need to know about wasm.InvokeRequest.
type functionInvokerAdapter struct{ s *Server }

func (a functionInvokerAdapter) InvokeForEvent(ctx context.Context, project, name, path string, body []byte) error {
	f, err := a.s.store.GetFunction(ctx, project, name)
	if err != nil {
		return err
	}
	req := &wasm.InvokeRequest{
		Method: http.MethodPost,
		Path:   path,
		Headers: http.Header{
			"content-type":      []string{"application/json"},
			"x-selfcloud-event": []string{"true"},
		},
		Body: body,
		Env:  f.Env,
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = a.s.invoke(cctx, f, req)
	return err
}

// containerControlAdapter implements events.ContainerControl using the
// runtime + store the API server already has.
type containerControlAdapter struct{ s *Server }

func (a containerControlAdapter) StartByName(ctx context.Context, project, name string) error {
	c, err := a.s.store.GetContainer(ctx, project, name)
	if err != nil {
		return err
	}
	st, err := a.s.containers.Start(ctx, c)
	if err != nil {
		return err
	}
	c.Status = *st
	return a.s.store.PutContainer(ctx, c)
}

func (a containerControlAdapter) StopByName(ctx context.Context, project, name string) error {
	c, err := a.s.store.GetContainer(ctx, project, name)
	if err != nil {
		return err
	}
	if err := a.s.containers.Stop(ctx, c); err != nil {
		return err
	}
	c.Status.Phase = store.PhaseStopped
	c.Status.UpdatedAt = time.Now().UTC()
	return a.s.store.PutContainer(ctx, c)
}

func (a containerControlAdapter) RestartByName(ctx context.Context, project, name string) error {
	if err := a.StopByName(ctx, project, name); err != nil {
		return err
	}
	// Tiny pause so containerd has finished tearing down before we
	// recreate.
	time.Sleep(500 * time.Millisecond)
	return a.StartByName(ctx, project, name)
}
