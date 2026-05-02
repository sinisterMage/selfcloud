package auth

import (
	"context"
	"testing"

	"github.com/selfcloud/selfcloud/internal/store"
)

func TestPasswordHashAndVerify(t *testing.T) {
	h, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("hunter2", h) {
		t.Fatal("verify should succeed for correct password")
	}
	if VerifyPassword("wrong", h) {
		t.Fatal("verify should fail for wrong password")
	}
}

func TestTokenLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mgr := NewManager(s)
	ctx := context.Background()
	u, err := mgr.CreateUser(ctx, "admin", "admin@example.com", "hunter2", true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := mgr.CreateToken(ctx, "test", u.Meta.Name, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Plaintext == "" {
		t.Fatal("plaintext should be returned at creation")
	}
	id, err := mgr.ResolveToken(ctx, tok.Plaintext)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !id.IsAdmin {
		t.Fatal("admin flag lost")
	}
	if _, err := mgr.ResolveToken(ctx, "garbage"); err == nil {
		t.Fatal("garbage should not resolve")
	}
}

func TestBootstrapToken(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mgr := NewManager(s)
	ctx := context.Background()

	plain, err := mgr.CreateBootstrapToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.ConsumeBootstrapToken(ctx, plain); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if err := mgr.ConsumeBootstrapToken(ctx, plain); err == nil {
		t.Fatal("second consume should fail")
	}
}
