# CLI reference

The `selfcloud` binary ships every operator and end-user command in
one place. Subcommands fall into three groups:

- **server / install / join** — run a node, render a unit, add this
  machine to a cluster.
- **doctor / upgrade** — preflight checks and self-upgrade.
- **ergonomic shortcuts + ctl** — talk to the REST API.

Global flags (registered on root):

| Flag | Default | Notes |
|---|---|---|
| `--endpoint` | `$SELFCLOUD_ENDPOINT` or `https://127.0.0.1:8443` | API to talk to |
| `--token` | `$SELFCLOUD_TOKEN` | bearer token |

Most subcommands accept `--project` (defaults to `$SELFCLOUD_PROJECT`
or `default`).

---

## `server`

Run the control-plane process. This is the command systemd's
`ExecStart=` invokes.

```
selfcloud server \
  --data-dir /var/lib/selfcloud \
  --api-addr 0.0.0.0:8443 \
  [--raft-addr <host:port>] \
  [--join-addr <leader:port>] \
  [--no-tls] \
  [--dev] \
  [--force-master-key-rotation]
```

Notable behaviour:

- On a fresh node, mints a one-time bootstrap token and prints it after
  the API binds.
- Reads `<data-dir>/join.json` (created by `selfcloud join`) for cluster
  handshake materials and applies them to local state on first boot.
- Refuses to start if `master.key` doesn't match
  `ClusterConfig.SecretFingerprint` unless `--force-master-key-rotation`
  is set.

## `install`

Render the systemd unit. This is the canonical way to lay down
`/etc/systemd/system/selfcloud.service`; `install.sh` calls it.

```
selfcloud install \
  --binary /usr/local/bin/selfcloud \
  --data-dir /var/lib/selfcloud \
  --api-addr 0.0.0.0:8443 \
  --user selfcloud --group selfcloud \
  [--raft-addr host:port] [--join-addr host:port] \
  [--render-only=false]
```

`--render-only=true` (the default) prints the unit to stdout; pass
`--render-only=false` to write `/etc/systemd/system/selfcloud.service`.

## `join`

Perform the cluster handshake. The script sub-shell `install.sh`
invokes this when called with `--join`.

```
selfcloud join \
  --leader leader-host:8443 \
  --token <plain-join-token> \
  [--node-id node-xxxx] \
  [--advertise-addr <host>] \
  [--data-dir /var/lib/selfcloud]
```

Persists `join.json` under `--data-dir`. The next `server` start uses
it.

## `doctor`

Self-check. Used by `install.sh` (preflight) and operators
(post-install / post-upgrade verification).

```
selfcloud doctor [--preflight] [--data-dir /var/lib/selfcloud] \
                 [--endpoint https://127.0.0.1:8443]
```

Inspects: kernel, containerd, ctr, nft, ip, firecracker, master.key
presence, well-known ports, and (unless `--preflight`) `/healthz`,
`/readyz`, `/api/v1/meta`. Exits non-zero if any required check
fails.

## `upgrade`

Self-upgrade. Downloads the requested release, verifies its
`.sha256`, swaps the binary, restarts systemd, runs `doctor`.

```
selfcloud upgrade [--version vX.Y.Z|latest] [--no-restart] [--dry-run]
```

## Ergonomic shortcuts

Wrappers around the same REST API the dashboard uses. Output is JSON.

| Command | What it does |
|---|---|
| `selfcloud projects ls` | list projects |
| `selfcloud projects create <name>` | create a project |
| `selfcloud projects rm <name>` | delete a project |
| `selfcloud containers ls` | list containers in `--project` |
| `selfcloud containers logs <name>` | dump recent logs |
| `selfcloud containers start <name>` | start a container |
| `selfcloud containers stop <name>` | stop a container |
| `selfcloud containers rm <name>` | delete a container |
| `selfcloud fn ls` | list functions |
| `selfcloud fn invoke <name> [--path /sub]` | invoke a function |
| `selfcloud fn rm <name>` | delete a function |
| `selfcloud secrets ls` | list secrets (without ciphertext) |
| `selfcloud secrets put <name> --value ...` | create / update |
| `selfcloud secrets rm <name>` | delete a secret |
| `selfcloud nodes ls` | list cluster nodes |
| `selfcloud token ls` | list API tokens |
| `selfcloud token issue <name> [--ttl 720h]` | mint a new token |
| `selfcloud token rm <name>` | revoke a token |

## `ctl`

The lower-level plumbing tool. Speaks raw HTTP against the API.

```
selfcloud ctl get  /api/v1/projects/default/containers
selfcloud ctl apply /api/v1/projects/default/containers -f my.json
selfcloud ctl delete /api/v1/projects/default/containers/my-app
```

Use `ctl` when there isn't an ergonomic wrapper for what you want, or
when you're scripting against the API and want exact control over the
URL.
