# REST API

selfCloud exposes a JSON REST API that the dashboard, the CLI, and the
Terraform provider all share. WebSocket endpoints under the same tree
serve live container logs, exec, and the event stream.

## Endpoint

`https://<host>:8443/api/v1/...`

The default TLS certificate is self-signed; clients must accept it (or
trust the cert under `<data-dir>/tls/cert.pem`). Use a reverse proxy
(`nginx`, `caddy`, ...) in front of selfCloud for a public hostname
with a real certificate.

## Auth

Bearer tokens. Get one via:

- The first-run wizard's response (`admin-initial`).
- `POST /api/v1/auth/login` with `{email, password}` (returns a 30-day
  session token).
- `POST /api/v1/auth/tokens` (admin only) — long-lived tokens for CI /
  Terraform / scripts.

```
authorization: Bearer sct_<hex>
```

WebSocket endpoints accept `?access_token=<...>` because browsers can't
set headers on `WebSocket(...)`. All other endpoints require the
header.

## Error envelope

```json
{ "error": "human-readable", "status": 503 }
```

Common shapes:

- `400` — request is malformed (json, missing required field).
- `401` — no/invalid token.
- `403` — admin-only endpoint, non-admin token.
- `404` — resource doesn't exist.
- `409` — conflict (e.g. unique constraint).
- `503` — `ErrNotLeader` (write hit a follower; followers issue a 307
  redirect now, but middleware misses fall back to 503), or a runtime
  isn't available yet.

## Liveness and readiness

| Path | Auth | Meaning |
|---|---|---|
| `GET /healthz` | none | process is up |
| `GET /readyz` | none | every required subsystem reports ready |
| `GET /api/v1/meta` | none | version, runtime, uptime |

`/readyz` returns the per-component breakdown `selfcloud doctor`
displays:

```json
{
  "ready": true,
  "components": [
    {"name":"api","required":true,"ready":true,"message":"listening on 0.0.0.0:8443","sinceMs":120},
    {"name":"garage","required":true,"ready":true,"message":"responsive","sinceMs":543},
    {"name":"master-key","required":true,"ready":true,"message":"loaded","sinceMs":1842},
    {"name":"raft","required":true,"ready":true,"message":"leader: 10.0.0.5:7000","sinceMs":50},
    {"name":"reconciler","required":true,"ready":true,"message":"first pass complete","sinceMs":200},
    {"name":"store","required":true,"ready":true,"message":"boltdb opened","sinceMs":2401}
  ]
}
```

## First-run wizard

```
GET  /api/v1/setup/status                    no auth
POST /api/v1/setup/initialize                no auth, bootstrap-token in body
```

`initialize` is transactional: bootstrap-token clearing + `Initialized=true`
only happen after the admin user, default project, and initial admin
token are all created. A failed run can be retried with the same
token.

## Auth surface

```
POST /api/v1/auth/login            no auth
GET  /api/v1/auth/me               bearer
GET  /api/v1/auth/tokens           bearer
POST /api/v1/auth/tokens           bearer (admin)
DELETE /api/v1/auth/tokens/{name}  bearer (admin)
```

## Projects

```
GET    /api/v1/projects
POST   /api/v1/projects
GET    /api/v1/projects/{name}
DELETE /api/v1/projects/{name}
```

`default` cannot be deleted.

## Containers

```
GET    /api/v1/projects/{p}/containers
POST   /api/v1/projects/{p}/containers
GET    /api/v1/projects/{p}/containers/{name}
DELETE /api/v1/projects/{p}/containers/{name}
POST   /api/v1/projects/{p}/containers/{name}/start
POST   /api/v1/projects/{p}/containers/{name}/stop
GET    /api/v1/projects/{p}/containers/{name}/logs
GET    /api/v1/projects/{p}/containers/{name}/logs/ws    websocket
GET    /api/v1/projects/{p}/containers/{name}/exec       websocket
```

## Buckets and access keys

```
GET    /api/v1/projects/{p}/buckets
POST   /api/v1/projects/{p}/buckets
GET    /api/v1/projects/{p}/buckets/{name}
DELETE /api/v1/projects/{p}/buckets/{name}
GET    /api/v1/projects/{p}/buckets/{name}/objects
PUT    /api/v1/projects/{p}/buckets/{name}/objects
GET    /api/v1/projects/{p}/buckets/{name}/object        download
DELETE /api/v1/projects/{p}/buckets/{name}/object

GET    /api/v1/projects/{p}/access-keys
POST   /api/v1/projects/{p}/access-keys
GET    /api/v1/projects/{p}/access-keys/{name}
DELETE /api/v1/projects/{p}/access-keys/{name}
```

S3 traffic itself goes through the reverse proxy at `/s3/...`; sign
with the access key + secret returned by `POST /access-keys`.

## Functions

```
GET    /api/v1/projects/{p}/functions
POST   /api/v1/projects/{p}/functions
GET    /api/v1/projects/{p}/functions/{name}
DELETE /api/v1/projects/{p}/functions/{name}
POST   /api/v1/projects/{p}/functions/{name}/code             multipart octet-stream
POST   /api/v1/projects/{p}/functions/{name}/invoke
GET    /api/v1/projects/{p}/functions/{name}/invocations
GET    /api/v1/projects/{p}/functions/{name}/builds
POST   /api/v1/projects/{p}/functions/{name}/builds           trigger
GET    /api/v1/projects/{p}/functions/{name}/builds/{id}
GET    /api/v1/projects/{p}/functions/{name}/builds/{id}/logs/ws
GET    /api/v1/runtime/firecracker/templates
```

External invocation: `/<method> /fn/<project>/<function>[/<sub>]` —
unauthenticated by default; functions can implement their own auth in
the handler.

## Secrets

```
GET    /api/v1/projects/{p}/secrets
POST   /api/v1/projects/{p}/secrets
GET    /api/v1/projects/{p}/secrets/{name}
DELETE /api/v1/projects/{p}/secrets/{name}
POST   /api/v1/projects/{p}/secrets/{name}/reveal             admin only
```

## Events and rules

```
GET    /api/v1/projects/{p}/event-rules
POST   /api/v1/projects/{p}/event-rules
GET    /api/v1/projects/{p}/event-rules/{name}
DELETE /api/v1/projects/{p}/event-rules/{name}
GET    /api/v1/projects/{p}/event-rules/{name}/deliveries
POST   /api/v1/projects/{p}/event-rules/{name}/test           admin only
GET    /api/v1/projects/{p}/events
GET    /api/v1/projects/{p}/events/ws                         websocket
```

## Cluster

```
GET    /api/v1/cluster
PUT    /api/v1/cluster                  admin only
GET    /api/v1/cluster/nodes
GET    /api/v1/cluster/join-tokens      admin only
POST   /api/v1/cluster/join-tokens      admin only, returns plaintext once
POST   /api/v1/cluster/join             no auth, body carries the token
```

## Git push-to-deploy webhooks

```
POST /webhooks/git/{token}
```

The function's `source.git.webhookToken` is the URL segment;
`webhookSecret` (if set) is verified as an HMAC-SHA256 in the
`x-hub-signature-256` header. GitHub-style and bare bearer-token
shapes are both supported.
