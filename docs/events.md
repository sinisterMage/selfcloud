# Events and rules

selfCloud has one event bus shared across every internal subsystem.
Anything interesting that happens (a container crashed, a function
errored, a build finished, an S3 PUT landed, a cron fired, a log line
matched) becomes an `EventRecord` on the bus. EventRules let you fan
those events out to webhooks, function invocations, or container
actions.

## Event taxonomy

| Type | Source | Subject | Notable data fields |
|---|---|---|---|
| `container.start` | reconciler | container name | `image` |
| `container.crash` | reconciler | container name | `error`, `attempts` |
| `container.log` | log scanner | container name | `line` (the matched line) |
| `function.invoked` | API invoke pipeline | function name | `runtime`, `method`, `path`, `durMs`, `status` |
| `function.error` | API invoke pipeline | function name | `runtime`, `error` |
| `build.start`, `build.success`, `build.failed` | builder | function name | `commitSha`, `duration` |
| `cron` | cron scheduler | function name | `schedule` |
| `s3.put`, `s3.delete` | S3 reverse proxy | `bucket/key` | `bucket`, `key`, `method` |
| user-defined | webhook / sidecar | anything | the body you sent |

The full record:

```json
{
  "uid": "01JBC...",
  "type": "function.error",
  "project": "default",
  "subject": "checkout",
  "at": "2026-05-03T10:42:11.221Z",
  "data": { "runtime": "wasm", "error": "wasm timed out after 5s" }
}
```

Events are persisted into the BoltDB `eventlog` bucket; the dashboard's
**Events** page renders them live via WS.

## EventRule sinks

Three actions, any of which can be the target of one rule:

```json
{
  "match": {
    "types": ["container.crash"],
    "subject": "checkout-*",
    "filter": { "image": "stripe-cli:latest" }
  },
  "action": {
    "webhook": {
      "url": "https://hooks.slack.com/services/...",
      "method": "POST",
      "headers": { "x-extra": "..." },
      "secret": "shared-secret"
    },
    "invoke": {
      "function": "alerter",
      "path": "/crash"
    },
    "container": {
      "container": "alerter-vm",
      "action": "restart"
    }
  },
  "enabled": true
}
```

`subject` is a glob (`*` = anything). For log-pattern rules where the
event type is `container.log`, `subject` is interpreted as a regex.
`filter` matches arbitrary keys in `data`.

Webhook deliveries are persisted with status, response body, attempt
counter, and exponential backoff for retries — visible under the
rule's **Deliveries** tab in the dashboard.

## In-app sidecar

selfCloud listens on a host unix socket
(`<dataDir>/event-sidecar.sock`) that any container or function can
POST to. This is the lowest-friction way for a workload to emit a
custom event:

```bash
curl --unix-socket /var/lib/selfcloud/event-sidecar.sock \
     http://x/emit \
     -d '{"type":"app.signup","subject":"alice@example.com","data":{"plan":"pro"}}'
```

The sidecar verifies the project the caller belongs to via the
container's environment, so a workload can only emit into its own
project.

## Live consumption

```
GET /api/v1/projects/{p}/events/ws        websocket, server pushes JSON envelopes
```

The dashboard, custom debugging tools, and any script that wants to
watch in real time can subscribe.
