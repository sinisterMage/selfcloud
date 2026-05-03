# selfCloud documentation

Welcome. These docs are organised by audience and intent.

## Operating selfCloud

- [Architecture](architecture.md) — the single-binary model, Raft, the
  reconciler, runtime layout.
- [Install](install.md) — single-node, multi-node, behind a proxy,
  air-gapped.
- [Upgrade](upgrade.md) — `selfcloud upgrade`, master.key handling,
  what to back up, rollback.
- [CLI reference](cli.md) — every `selfcloud` subcommand and flag.
- [Troubleshooting](troubleshooting.md) — diagnosing common failures
  via `selfcloud doctor` + the `/readyz` checklist.

## Building things on selfCloud

- [Containers](containers.md) — desired state, ports, volumes, secret
  mounts, `exec` and log streaming.
- [Functions](functions.md) — wasm vs Firecracker, triggers
  (HTTP / cron / event), git push-to-deploy, secrets in env and as
  files.
- [Storage](storage.md) — Garage, the S3 API, access keys, multi-zone
  layout.
- [Events](events.md) — the event taxonomy, EventRules, sinks
  (webhook / invoke / container action), the in-app sidecar.
- [Secrets](secrets.md) — encryption-at-rest, master.key handling,
  rotation.
- [Terraform](terraform.md) — the `selfcloud` provider, examples, the
  `insecure` flag.

## Reference

- [REST API](api.md) — auth, error envelope, pagination, every
  endpoint.
- [Contributing](contributing.md) — repo layout, dev loop, tests,
  release process.
