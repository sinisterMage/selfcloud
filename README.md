# selfCloud

A self-hostable, fully-featured cloud platform with a one-line setup. Manage S3-compatible object storage, serverless functions, and containers from a single web dashboard or with Terraform.

## Quick start

```bash
curl -fsSL https://get.selfcloud.dev | sh
```

Once the installer finishes it prints the dashboard URL and a one-time bootstrap token. Open the URL in your browser, paste the token, finish the first-run wizard, and you have a private cloud running on a single machine. Add more machines from the dashboard later when you need them.

## What's in the box

| Capability | Backed by | Notes |
|---|---|---|
| Object storage (S3 API) | [Garage](https://garagehq.deuxfleurs.fr) | Single-node by default, expands to a multi-zone cluster from the dashboard |
| Containers | containerd | OCI images, bridge networking, port publishing, logs, exec |
| Wasm functions | wazero (pure Go, WASI Preview 1) | Sub-10ms cold starts, HTTP and cron triggers, language-agnostic stdin/stdout JSON ABI |
| MicroVM functions | Firecracker | Any-language functions, snapshot/restore for warm starts |
| Web dashboard | React + Vite + Tailwind | Embedded into the Go binary, no separate hosting needed |
| Infra-as-Code | Terraform provider | Same REST API the dashboard uses |

## Architecture at a glance

selfCloud is a single static Go binary (`selfcloud`). One copy per machine. The first machine becomes the control-plane leader; additional machines `selfcloud join` into a Raft cluster. State is stored in an embedded BoltDB-backed Raft FSM. Workloads (containers, functions, S3 buckets) are reconciled from desired state on every node.

```
┌──────────────────────────────────────────────────────────┐
│                       selfcloud                          │
│  ┌────────────┐  ┌──────────────┐  ┌──────────────────┐  │
│  │   Web UI   │  │  REST + gRPC │  │ Terraform / CLI  │  │
│  └─────┬──────┘  └───────┬──────┘  └────────┬─────────┘  │
│        └────────────────┬┴──────────────────┘            │
│                ┌────────▼─────────┐                      │
│                │  Control plane   │  Raft + BoltDB       │
│                └────────┬─────────┘                      │
│   ┌────────────┬────────┼────────┬───────────────┐       │
│   ▼            ▼        ▼        ▼               ▼       │
│ Garage     containerd   wazero   Firecracker  Networking │
└──────────────────────────────────────────────────────────┘
```

## Repository layout

```
cmd/selfcloud/                  single binary entrypoint, Cobra subcommands
internal/api/                   REST + gRPC handlers, OpenAPI spec
internal/auth/                  local users, API tokens, bootstrap token
internal/store/                 Raft FSM over BoltDB, resource CRUD
internal/scheduler/             placement decisions
internal/runtime/container/     containerd v2 client wrapper
internal/runtime/wasm/          wazero function runtime + warm pool
internal/runtime/firecracker/   Firecracker microVM runtime
internal/storage/garage/        Garage process supervisor + admin client
internal/network/               bridge + nftables port publishing
internal/cluster/               gossip, join tokens, add-node flow
internal/installer/             bootstrap (TLS, systemd, first-boot)
web/                            React + Vite + Tailwind dashboard
terraform-provider-selfcloud/   Terraform provider
install.sh                      curl|sh entrypoint
```

## Building from source

Requirements: Go 1.23+, Bun (or Node 20+), make, Linux x86_64 or arm64 (target host).

```bash
make all          # build the dashboard, embed it, build the binary
./bin/selfcloud server --data-dir ./data
```

## Status

This is the v0 implementation that follows the [selfCloud Platform Plan](docs/architecture.md). The phased roadmap (containers → S3 → Wasm functions → Firecracker → Terraform → multi-node) is being delivered in order.

## License

Apache-2.0.
