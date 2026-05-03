# Functions

Two runtimes, one schema. Pick `wasm` for the fastest cold start and
the smallest blast radius; pick `firecracker` when you need full
microVM isolation or any-language compatibility beyond what
`wasm32-wasi` supports.

## Picking a runtime

| Trait | Wasm (wazero) | Firecracker |
|---|---|---|
| Cold start | sub-10 ms (compile cached) | ~150 ms cold, sub-100 ms warm-restored |
| Languages | anything that targets `wasm32-wasi` (TinyGo, Rust, Zig, AssemblyScript, Go-experimental) | anything (Node, Python, Go, ...) — runs in a Linux microVM |
| Filesystem | sandbox; selfCloud projects file-mode secrets at `/secrets/<name>` via WASI |  full microVM rootfs; selfCloud writes secrets to `/run/selfcloud/secrets/<name>` |
| Memory | per-function cap (default 128 MiB) | per-VM `mem_size_mib` |
| Best for | request handlers, transformations, glue | shelling out, native deps, language runtimes that don't have a wasm story yet |

## Spec reference

`store.Function`:

```json
{
  "meta": { "project": "default", "name": "hello" },
  "runtime": "wasm",
  "handler": "handler.wasm",
  "triggers": [
    { "http": { "path": "/hello", "methods": ["GET","POST"] } },
    { "cron": { "schedule": "*/5 * * * *" } }
  ],
  "env": { "GREETING": "secret://greeting" },
  "secretMounts": [
    { "secret": "stripe-prod", "envName": "STRIPE_KEY" },
    { "secret": "tls-key", "mountPath": "/secrets/tls.key" }
  ],
  "memoryMB": 128,
  "timeoutMs": 5000,
  "source": {
    "type": "git",
    "git": {
      "url": "https://github.com/me/my-fn",
      "ref": "main",
      "subPath": "fn",
      "authSecret": "github-pat"
    }
  }
}
```

## Triggers

| Trigger | URL / firing |
|---|---|
| `http` | `/<method> /fn/<project>/<name>[/<sub>]` (unauthenticated; do auth in your handler) |
| `cron` | runs through `Server.invoke`, so secrets resolve and lifecycle events fire |
| event-driven | configure an `EventRule` whose action is `invoke: { function: <name> }` |

## Wasm ABI

Stdin envelope (json):

```json
{ "method":"GET", "path":"/x", "headers": {"k":["v"]}, "body":"<base64>", "env":{"K":"V"} }
```

Stdout envelope (json):

```json
{ "status":200, "headers": {"k":["v"]}, "body":"<base64>" }
```

Plain stdout (no JSON envelope) is also accepted as a 200 with
`Content-Type: text/plain`.

stderr is captured into the response's `logs` field.

Env vars selfCloud always sets:

- `SELFCLOUD_FN`, `SELFCLOUD_PROJECT`
- `SELFCLOUD_REQUEST_METHOD`, `SELFCLOUD_REQUEST_PATH`

File-mode secret mounts are projected at `/secrets/<basename>` (read-
only) via WASI's preopen filesystem.

## Firecracker ABI

The in-guest agent is `fc-agent` (in `internal/runtime/firecracker/agent`).
It supports two modes per the function manifest:

- `stdio` (default) — spawn the entrypoint per request, write the
  envelope on stdin, read the response from stdout. Same JSON shape as
  wasm.
- `http` — spawn the entrypoint once as a long-running web server bound
  to `127.0.0.1:8080`, then proxy each request through the agent.

File-mode secret mounts are projected at `/run/selfcloud/secrets/<basename>`
inside the guest (and at the requested absolute path, if you set one).

After the first successful cold invocation, the jailer takes a
Firecracker snapshot; the next invocation restores it (sub-100 ms cold)
instead of cold-booting again.

## Templates

`GET /api/v1/runtime/firecracker/templates` lists the rootfs templates
the node has built. Build them on the host with:

```bash
make firecracker-agent
make firecracker-templates       # node-22, python-3.12, go-1.23
```

You can also bake your own; see `scripts/build-rootfs.sh`.

## Git push-to-deploy

When a function's `source.type` is `git`, selfCloud:

1. Clones the repo (using `authSecret` if private).
2. Auto-detects the language (`package.json`, `Cargo.toml`,
   `go.mod`, `requirements.txt`, ...) and runs the matching build
   recipe inside a build sandbox container.
3. Stores the produced artefact in the content-addressed blob store
   (`functions/blobs/<sha256>`).
4. Updates `function.sourceRef` and deploys to the chosen runtime.

The function gets a unique webhook token at create time. Configure
your Git host's webhook to:

```
POST https://<your-selfcloud>/webhooks/git/<webhookToken>
Content-Type: application/json
X-Hub-Signature-256: sha256=<HMAC-SHA256 of body using webhookSecret>
```

A successful webhook triggers a build whose live logs are visible in
the dashboard's function detail view (`/builds/{id}/logs/ws`).

## Limits and timeouts

- Default `memoryMB` is 128.
- Default `timeoutMs` is 5000 (5s). Wasm enforces it via context
  cancellation; Firecracker passes it to the in-guest agent.
- Body size at the HTTP trigger is capped at 8 MiB.
- Function code upload is capped at 64 MiB.
