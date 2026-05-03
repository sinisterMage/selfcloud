# Secrets

selfCloud keeps secrets encrypted at rest in BoltDB and decrypts them
in-memory only when a workload needs them.

## Cipher

- AES-256-GCM (`internal/secrets/cipher.go`)
- A random 12-byte nonce per record
- Key version prefix `v1`
- Master key lives at `<dataDir>/master.key`, mode `0600`

The first time `selfcloud server` runs, it generates a fresh master key
and stores its fingerprint in `ClusterConfig.SecretFingerprint`. On
every subsequent boot, the on-disk key's fingerprint is compared
against the cluster's; a mismatch refuses to start. Pass
`--force-master-key-rotation` only if the rotation was deliberate
(existing encrypted secrets won't be readable with the new key).

## Storage layout

Each `Secret` is one row keyed by `project/name`:

```json
{
  "meta": { "project": "default", "name": "stripe-prod" },
  "description": "Stripe live secret",
  "keyId": "v1",
  "nonce": "<base64>",
  "ciphertext": "<base64>",
  "version": 3
}
```

`ListSecrets` strips `nonce` + `ciphertext` from list responses; the
plaintext is only returned by the explicit `POST /reveal` (admin-only)
or when a workload references it.

## Referencing secrets

Three places resolve `secret://name` references:

- Container `env` values — substituted by the reconciler at start time.
- Function `env` values — substituted by `Server.invoke` at invoke time.
- `secretMounts` on either — projected as env vars (`envName`) or
  guest-side files (`mountPath`).

Resolution is project-scoped: a function in project A cannot reference
a secret in project B.

## Rotation

The plan-faithful flow:

```bash
# 1. Re-encrypt all secrets with a new master key.
# (No tooling for this yet — open an issue if you need it; the typical
#  approach is: list each secret with reveal, write it back with PUT,
#  then rotate the key.)

# 2. Stop selfcloud.
sudo systemctl stop selfcloud

# 3. Replace master.key and re-create secret records.
sudo openssl rand -out /var/lib/selfcloud/master.key 32
sudo chown selfcloud:selfcloud /var/lib/selfcloud/master.key
sudo chmod 0600 /var/lib/selfcloud/master.key

# 4. Start with the override and immediately re-create your secrets.
sudo systemctl start selfcloud   # will fail unless you also set --force-master-key-rotation
```

To set `--force-master-key-rotation`, edit
`/etc/systemd/system/selfcloud.service` (`ExecStart=...`), add the flag,
`systemctl daemon-reload && systemctl restart selfcloud`. Re-render
later via `selfcloud install` to drop the flag once rotation is done.

## Operational caveats

- **Backups**: never back up `state.db` without `master.key`. They are
  meaningless apart.
- **Multi-node**: `master.key` is per-node. Generate it on each node;
  the cluster's `SecretFingerprint` is set to the leader's. If you
  intend to share the same key across all nodes (so any leader can
  decrypt secrets), copy the file before joining.
- **Reveal**: the reveal endpoint is admin-only. Use it sparingly; it
  defeats the at-rest protection.
