# Troubleshooting

The first move is always:

```bash
sudo selfcloud doctor
```

It prints a tabular status of every component the platform depends on.
The sections below explain what each line means and how to recover.

## Doctor checks

### `os`

selfCloud only supports Linux. Run on a Linux host (kernel ≥ 4) or in a
Linux VM. macOS / WSL aren't supported targets.

### `kernel`

Anything 4.x+ has cgroups v2, vsock, and nftables. If your kernel is
older (e.g. CentOS 7), upgrade it before continuing.

### `containerd`, `ctr`

Container support depends on a working containerd. If `containerd` is
missing:

```bash
# Debian/Ubuntu
sudo apt-get update && sudo apt-get install -y containerd
# Fedora/RHEL
sudo dnf install -y containerd
# Arch
sudo pacman -S --noconfirm containerd
sudo systemctl enable --now containerd
```

If `ctr` is missing but `containerd` is present, your distro packaged
them separately — install the `containerd-tools` / `containerd-bin` /
similar package.

### `nft`, `ip`

`ip` is required (always present on Linux). `nft` (`nftables`) is
needed for port publishing; without it, containers run but their
declared `ports` aren't reachable from the outside. Install
`nftables`.

### `firecracker`

Optional. Only needed if you'll run `runtime: firecracker` functions.

### `master.key`

If missing, the next start will create it. If present but the
fingerprint mismatches `ClusterConfig.SecretFingerprint`, the server
refuses to start to protect existing secrets:

```
master key fingerprint mismatch: cluster expects "abcd1234", on-disk key is "ffff0000".
Existing secrets will fail to decrypt with this key.
If this is intentional, restart with --force-master-key-rotation;
otherwise restore the original master.key file under /var/lib/selfcloud.
```

Restore the original key from backup, or set the rotation flag and
re-create your secrets.

### `bootstrap-token`

Present only on a fresh install. Once the wizard completes, it's
cleared. If your wizard run failed mid-way, the token survives and
you can retry — the wizard is transactional.

### `port:8443` / `port:7000` / `port:3900`

Each port should be **free** (no listener yet) or **in use by
selfcloud**. "in use by something else" means another process is
holding it; find it with `sudo ss -ltnp | grep :8443` and reconcile.

### `api:/healthz`, `api:/readyz`, `api:/meta`

Direct probes against the running selfcloud node:

- `/healthz` failing: process is down → `journalctl -u selfcloud`.
- `/readyz` reports `not ready: <names>` — the named subsystems still
  haven't reported ready. The most common causes:
  - `garage` — garage failed to start (check journal for `[garage]`).
    Storage features will be unavailable until you fix it.
  - `raft` — multi-node cluster has no leader yet. Wait or check
    the followers can reach the leader's `--raft-addr`.

## Common failures

### "wrote /etc/systemd/system/selfcloud.service" but `systemctl status selfcloud` says failed

```bash
sudo journalctl -u selfcloud -e
```

Look for `master key fingerprint mismatch` (see above), `tls bootstrap`
(missing privileges to write under `--data-dir`), or `bind: address
already in use`.

### Dashboard prints "selfCloud is up" but no bootstrap token

This means the cluster is already initialized. Sign in normally; the
bootstrap token is single-use and gets consumed by the first wizard
run.

### Joining a cluster fails with `invalid join token`

Tokens expire (default 24h) and are single-use. Issue a fresh one from
the leader's **Settings → Join tokens → Issue**.

### Function never returns / 503 "function is deploying"

The runtime hasn't finished its first `Deploy` for that function. For
`wasm`, this is the compile step (sub-second). For `firecracker`, the
first invocation cold-boots the VM and takes ~150 ms; subsequent calls
restore from a snapshot. Check the function's `latestBuild` /
deployment logs in the dashboard.

### Container exits and never restarts

Check `restartPolicy`:

- `Never` — by design. Set to `OnFailure` or `Always`.
- `OnFailure`/`Always` — the reconciler probes liveness every ~10s
  via `ctr tasks ls`. If it returns "not running" and the policy
  permits, the reconciler bumps the generation and re-starts.

### S3 PUT returns 403

Make sure the access key's `permissions` is `write` or `owner`, and
that the key is scoped to the bucket you're using (or has cluster-wide
permissions).

### Multi-node followers serve stale reads

By design — Bolt is local on every node and Raft commits writes
asynchronously. The dashboard polls every 5–10s; you'll see a follower
catch up within a tick of the leader's commit.

### Writes hit a follower

Followers issue `307 Temporary Redirect` to the leader. The dashboard
handles this transparently. CLI / Terraform / S3 SDKs typically do
too, but if you're using a low-level HTTP client without redirect
handling, you'll see the 307. Set `--endpoint` to the leader's API
address explicitly.

## Logs

```bash
journalctl -u selfcloud -e          # everything
journalctl -u selfcloud -e | grep '\[firecracker\]'    # microVM stderr
journalctl -u selfcloud -e | grep '\[garage\]'         # garage process
journalctl -u containerd -e         # underlying container daemon
```
