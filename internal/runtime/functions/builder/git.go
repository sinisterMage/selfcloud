// Package builder is selfcloud's git-backed function build pipeline.
// Given a Function whose Source.Type is "git", it shallow-clones the
// referenced ref, autodetects (or accepts override) a build pipeline,
// runs it inside a sandboxed containerd container, captures the
// resulting artifact, and deploys it via the wasm/firecracker runtime.
package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	gitlib "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// CloneResult is what (b *Builder).clone returns to the runner.
type CloneResult struct {
	Dir       string // absolute path to the checkout
	CommitSHA string // commit hash that was checked out
}

// clone performs a shallow clone of the configured Git source. PAT auth
// is read from a project-scoped Secret if the spec references one.
//
// Returns the resolved commit sha so it can be recorded on the Build.
func (b *Builder) clone(ctx context.Context, fn *store.Function, into string) (*CloneResult, error) {
	spec := fn.Source.Git
	if spec == nil {
		return nil, fmt.Errorf("function %q has no git source", fn.Meta.Name)
	}
	if spec.URL == "" {
		return nil, fmt.Errorf("git source has no URL")
	}
	if err := os.MkdirAll(into, 0o750); err != nil {
		return nil, err
	}

	opts := &gitlib.CloneOptions{
		URL:      spec.URL,
		Depth:    1,
		Progress: nil, // we capture stderr at the runner level instead
	}
	if spec.Ref != "" {
		// Try as a branch/tag reference first; fall back to fetching the
		// commit sha after a generic clone if Resolve fails.
		opts.ReferenceName = plumbing.NewBranchReferenceName(spec.Ref)
		opts.SingleBranch = true
	}

	if spec.AuthSecret != "" && b.secrets != nil {
		token, err := b.secrets.Reveal(ctx, fn.Meta.Project, spec.AuthSecret)
		if err != nil {
			return nil, fmt.Errorf("resolve auth secret %q: %w", spec.AuthSecret, err)
		}
		// GitHub & GitLab both accept "x-access-token" / "oauth2" as the
		// username with a PAT in the password slot.
		opts.Auth = &http.BasicAuth{
			Username: "x-access-token",
			Password: token,
		}
	}

	repo, err := gitlib.PlainCloneContext(ctx, into, false, opts)
	if err != nil && spec.Ref != "" {
		// Retry as a tag-ref or as a generic clone followed by checkout.
		opts.ReferenceName = ""
		opts.SingleBranch = false
		opts.Depth = 0
		// Wipe and try again so the previous attempt's partial state
		// doesn't trip the second clone.
		_ = os.RemoveAll(into)
		if err := os.MkdirAll(into, 0o750); err != nil {
			return nil, err
		}
		repo, err = gitlib.PlainCloneContext(ctx, into, false, opts)
		if err != nil {
			return nil, fmt.Errorf("clone: %w", err)
		}
		// Now resolve the requested ref against the full clone.
		hash, err := repo.ResolveRevision(plumbing.Revision(spec.Ref))
		if err != nil {
			return nil, fmt.Errorf("resolve ref %q: %w", spec.Ref, err)
		}
		wt, err := repo.Worktree()
		if err != nil {
			return nil, err
		}
		if err := wt.Checkout(&gitlib.CheckoutOptions{Hash: *hash, Force: true}); err != nil {
			return nil, fmt.Errorf("checkout %s: %w", spec.Ref, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("clone: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("head: %w", err)
	}
	sha := head.Hash().String()

	src := into
	if spec.SubPath != "" {
		src = filepath.Join(into, spec.SubPath)
		if _, err := os.Stat(src); err != nil {
			return nil, fmt.Errorf("subPath %q: %w", spec.SubPath, err)
		}
	}
	log.With("fn", fn.Meta.Name, "sha", sha[:8], "dir", src).Debug("builder: cloned")
	return &CloneResult{Dir: src, CommitSHA: sha}, nil
}
