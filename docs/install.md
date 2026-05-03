# Install

## One-liner

```bash
curl -fsSL https://get.selfcloud.dev | sh
```

The installer:

1. Verifies the host (architecture, kernel, systemd availability).
2. Installs `containerd` from the system package manager if it isn't
   already.
3. Creates the service user `selfcloud` and the data directory
   `/var/lib/selfcloud`.
4. Downloads `selfcloud-linux-<arch>` from the latest GitHub Release
   and verifies its `.sha256` checksum.
5. Renders the systemd unit via `selfcloud install` (one source of
   truth — no `sed` post-processing) and enables `selfcloud.service`.
6. Polls `/readyz` until the node is up, then prints the dashboard URL
   and the one-time bootstrap token.

Supported architectures: `linux/amd64` and `linux/arm64`. macOS and
Windows are not supported as targets.

## Flags

| Flag | Default | Notes |
|---|---|---|
| `--version <vX.Y.Z>` | `latest` | pin a specific release |
| `--bin-dir <path>` | `/usr/local/bin` | where `selfcloud` lives |
| `--data-dir <path>` | `/var/lib/selfcloud` | state, TLS, master.key, BoltDB |
| `--api-addr <host:port>` | `0.0.0.0:8443` | dashboard listener |
| `--user <name>` | `selfcloud` | service `User=` and `Group=` |
| `--no-systemd` | off | skip writing the systemd unit |
| `--join <addr>` | — | after install, join an existing cluster |
| `--token <token>` | — | join token for `--join` |
| `--skip-containerd` | off | leave containerd alone |
| `--dry-run` | off | print actions without executing |

Environment fallbacks:

- `SELFCLOUD_VERSION` overrides `--version`.
- `SELFCLOUD_RELEASE_HOST` overrides the GitHub host (useful for
  self-hosted releases or air-gapped mirrors).

## Single-node walkthrough

```bash
curl -fsSL https://get.selfcloud.dev | sh
# ...installer prints...
# ==========================================================
# selfCloud is up.
# Dashboard:  https://10.0.0.5:8443/
# Bootstrap token: sct_…
# ==========================================================
```

Open the URL, paste the bootstrap token, finish the wizard, sign in.
That's it.

## Multi-node walkthrough

On the first machine:

```bash
curl -fsSL https://get.selfcloud.dev | sh
# Sign in to the dashboard, finish the wizard with "Multi-node" enabled.
# Settings -> Cluster mode -> Multi-node, replicationFactor=3.
# Settings -> Join tokens -> Issue (24h). Copy the printed command.
```

On each additional machine:

```bash
curl -fsSL https://get.selfcloud.dev | sh -s -- \
  --join leader-host:8443 \
  --token <copied-from-leader>
```

The new node:

- Persists `join.json` under `--data-dir`, including the cluster's
  Garage RPC + admin secrets.
- Restarts as a Raft voter (the leader's `cluster/join` API added it).
- Brings up its local Garage configured to peer with the cluster's.
- The leader's dashboard now lists it under **Nodes**.

## Behind a corporate proxy

Set `HTTPS_PROXY` / `HTTP_PROXY` before running the installer.
selfCloud itself respects them; `install.sh` uses `curl`, which honors
them too.

## Air-gapped install

```bash
# On a machine with internet access:
curl -fsSL https://github.com/selfcloud/selfcloud/releases/latest/download/selfcloud-linux-amd64 -o selfcloud
curl -fsSL https://github.com/selfcloud/selfcloud/releases/latest/download/selfcloud-linux-amd64.sha256 -o selfcloud.sha256
sha256sum -c selfcloud.sha256

# Copy `selfcloud` and the install script to the air-gapped host, then:
sudo mv selfcloud /usr/local/bin/selfcloud
sudo selfcloud doctor --preflight
sudo /usr/local/bin/selfcloud install --user selfcloud --group selfcloud --render-only=false
sudo systemctl daemon-reload && sudo systemctl enable --now selfcloud
```

## Verifying the install

```bash
selfcloud doctor                    # local + endpoint checks
curl -k https://127.0.0.1:8443/readyz | jq
journalctl -u selfcloud -e          # full server log
```

`/readyz` reports each subsystem's status (store, master-key, api,
garage, reconciler, raft) so you can pinpoint the bit that hasn't come
up yet.

## Customising the systemd unit

The unit lives at `/etc/systemd/system/selfcloud.service`. Re-render
with new flags via:

```bash
sudo selfcloud install \
  --user selfcloud --group selfcloud \
  --data-dir /var/lib/selfcloud \
  --api-addr 0.0.0.0:8443 \
  --raft-addr 10.0.0.5:7000 \
  --render-only=false
sudo systemctl daemon-reload && sudo systemctl restart selfcloud
```

Never `sed`-edit the unit; `install.sh` deliberately doesn't, so future
flags carry through cleanly.
