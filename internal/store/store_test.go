package store

import (
	"context"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestProjectCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := &Project{Meta: Meta{Name: "demo"}, DisplayName: "Demo"}
	if err := s.PutProject(ctx, p); err != nil {
		t.Fatalf("put: %v", err)
	}
	if p.Meta.UID == "" {
		t.Fatal("UID not assigned")
	}

	got, err := s.GetProject(ctx, "demo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DisplayName != "Demo" {
		t.Fatalf("display name lost: %q", got.DisplayName)
	}

	list, err := s.ListProjects(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}

	if err := s.DeleteProject(ctx, "demo"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetProject(ctx, "demo"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestContainerCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	c := &Container{
		Meta:  Meta{Name: "web", Project: "default"},
		Image: "nginx:alpine",
	}
	if err := s.PutContainer(ctx, c); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.GetContainer(ctx, "default", "web")
	if err != nil || got.Image != "nginx:alpine" {
		t.Fatalf("get: %v %+v", err, got)
	}
	list, err := s.ListContainers(ctx, "default")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if err := s.DeleteContainer(ctx, "default", "web"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestClusterConfigDefaults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	got, err := s.GetCluster(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ReplicationFactor != 1 {
		t.Fatalf("default replication factor should be 1, got %d", got.ReplicationFactor)
	}
}
