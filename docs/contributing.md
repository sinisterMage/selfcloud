# Contributing

## Repository layout

See [architecture.md](architecture.md) for the full tour. The short
version:

```
cmd/selfcloud/                  binary entrypoint, Cobra subcommands
internal/                       implementation packages (api, store, runtime/*, ...)
web/                            React + Vite + Tailwind dashboard
terraform-provider-selfcloud/   provider, separate go.mod
docs/                           you are here
install.sh                      curl|sh entrypoint
.github/workflows/              ci.yml + release.yml
```

## Dev loop

You'll need: Go 1.23+, Bun (or Node 20+), `make`, and (for runtime
work) a Linux box with `containerd`. Tests run on macOS/WSL too —
they exercise the cross-platform stubs.

```bash
# Build the dashboard, embed it, build the binary.
make all

# Or run the binary in dev mode (text logs, no TLS, ./data state).
make dev

# Run the dashboard against a separate dev API.
make dev-web
```

`make dev-web` runs Vite at `http://127.0.0.1:5173/` with
`/api` and `/fn` proxied to `https://127.0.0.1:8443` (the dev server's
TLS error is expected; Vite ignores it).

## Tests

```bash
make test                # go test -race -count=1 ./...
make lint                # go vet + golangci-lint (if installed)
```

Web typecheck:

```bash
cd web && bun run tsc -b
```

Add a test next to the package you change. Existing tests live in
`internal/store/store_test.go` and `internal/auth/auth_test.go` for
reference; the patterns are vanilla `testing` + table-driven cases.

## Conventions

- The `internal/` package boundary is intentional. Public packages we
  want consumers to import (the Terraform provider, third-party SDKs)
  live in their own `go.mod`.
- New REST endpoints go through one of:
  - The handler's existing typed PUT/DELETE on `*store.ReplicatedStore`
    (containers, functions, buckets, secrets, eventrules,
    accesskeys) — these replicate via Raft on multi-node.
  - The bare `*store.Store` for purely local mutations
    (build progress, status updates, heartbeats).
- The reconciler is the only thing that mutates `status.*`. Don't
  write status from a handler.
- Comments explain non-obvious intent; don't narrate what the code
  does.

## Adding a new resource type

1. Add the struct to `internal/store/types.go` with a `Meta` field.
2. Add a `Kind*` constant.
3. Add typed `Put*` / `Get*` / `List*` / `Delete*` to
   `internal/store/store.go` using `putScoped` and `localDelete`.
4. If the resource should replicate cluster-wide, add the typed
   `Put*` / `Delete*` overrides on `*ReplicatedStore` in
   `internal/store/replicated_store.go`. Otherwise the bare Store
   methods are inherited.
5. Add a Kind decoder branch in `internal/store/fsm.go decodeKind`.
6. Add handlers under `internal/api/`.
7. Wire routes in `internal/api/routes.go`.
8. Add a dashboard page (`web/src/pages/...tsx`) and the corresponding
   types in `web/src/lib/types.ts`.
9. Add a Terraform resource if it makes sense for IaC use cases.

## Release process

Releases are tag-driven. Push a `vX.Y.Z` tag and
`.github/workflows/release.yml` will:

1. Build the dashboard.
2. Cross-compile `selfcloud` and `terraform-provider-selfcloud` for
   `linux/amd64` and `linux/arm64`, with version metadata stamped via
   `-ldflags`.
3. Generate `<asset>.sha256` files.
4. Publish a GitHub Release with the binaries + checksums attached.

`install.sh` and `selfcloud upgrade` consume that layout
(`releases/latest/download/selfcloud-linux-<arch>(.sha256)`).

## Style + linting

`.golangci.yml` is the source of truth. The current configuration
excludes `terraform-provider-selfcloud/` because it has its own go.mod;
run `cd terraform-provider-selfcloud && go vet ./...` separately.

## Filing issues

When you open an issue or PR, include:

- `selfcloud doctor` output if it's a runtime issue.
- The platform (kernel, distro, arch).
- For multi-node issues: `GET /api/v1/cluster/nodes` and the leader's
  `journalctl -u selfcloud --since '1h ago'`.
