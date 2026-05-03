# Upgrade

selfCloud upgrades are a single command:

```bash
sudo selfcloud upgrade
```

It downloads the latest release binary, verifies its `.sha256`,
replaces `/usr/local/bin/selfcloud`, restarts the systemd unit, and
runs `selfcloud doctor` against the freshly-restarted node. Failure at
any step exits non-zero so configuration management stays honest.

## Pinned versions

```bash
sudo selfcloud upgrade --version v0.7.0
```

Use `--dry-run` to fetch + verify without writing the binary. Use
`--no-restart` if you have a custom service supervisor.

## Multi-node upgrade order

1. Issue join tokens / make sure quorum is healthy (`selfcloud nodes ls`).
2. Upgrade the followers first, one at a time. After each:
   `selfcloud doctor --endpoint https://<that-node>:8443`.
3. Upgrade the leader last. Raft will elect a new leader during
   restart; clients will retry against the new one transparently.

## What lives on disk

Under `--data-dir` (default `/var/lib/selfcloud`):

```
master.key                    AES-256 master key (0600)
bootstrap-token               first-run token, rotated by the wizard
state.db                      BoltDB (projects, containers, ..., events)
raft/                         Raft log + snapshots + stable store
tls/cert.pem, tls/key.pem     self-signed TLS for the API
garage/                       Garage's data + metadata
functions/blobs/              uploaded function code (sha256 keyed)
functions/build-logs/         per-build streaming logs
firecracker/templates/        kernel + rootfs ext4
firecracker/snapshots/        warm-start snapshots, per function
secret-mounts/                staged secret files for container binds
join.json                     present only on followers (cluster handshake)
```

## What to back up

Take periodic copies of:

- `master.key` — without it, `state.db` secrets can't be decrypted.
- `state.db` — the source of truth for all desired state.
- `raft/` — only required on the leader; followers re-replicate.

You can stop the service, copy them with `cp -a`, and start again. The
copy time is small (`state.db` is typically tens of MB).

## Master key rotation

The fingerprint of `master.key` is stored in `ClusterConfig.SecretFingerprint`.
On startup, selfCloud refuses to run if the on-disk file's fingerprint
doesn't match — old encrypted secrets would silently fail to decrypt.
If you intentionally rotated the key, restart with:

```bash
selfcloud server --force-master-key-rotation ...
```

Existing `Secret` records will become unreadable; re-create them.

## Rollback

If an upgrade goes badly:

```bash
sudo selfcloud upgrade --version <older-vX.Y.Z>
```

State on disk is forward-compatible across patch releases; major
release upgrades note migration steps in their release notes.
