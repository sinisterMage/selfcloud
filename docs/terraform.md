# Terraform provider

selfCloud's Terraform provider is a thin wrapper around the same REST
API the dashboard and CLI use. Source lives in
`terraform-provider-selfcloud/`.

## Install

```hcl
terraform {
  required_providers {
    selfcloud = {
      source  = "selfcloud/selfcloud"
      version = "~> 0.7"
    }
  }
}

provider "selfcloud" {
  endpoint = "https://your-selfcloud:8443"
  token    = var.selfcloud_token   # or env: SELFCLOUD_TOKEN
  insecure = true                  # default; selfcloud ships self-signed
}
```

Environment fallbacks: `SELFCLOUD_ENDPOINT`, `SELFCLOUD_TOKEN`.

## Resources

| Resource | Maps to |
|---|---|
| `selfcloud_project` | `POST /api/v1/projects` |
| `selfcloud_container` | `POST /api/v1/projects/{p}/containers` (+ `/start` on create) |
| `selfcloud_bucket` | `POST /api/v1/projects/{p}/buckets` |
| `selfcloud_access_key` | `POST /api/v1/projects/{p}/access-keys` |
| `selfcloud_function` | `POST /api/v1/projects/{p}/functions` (+ `/code` upload) |

## Data sources

- `data.selfcloud_nodes` — `GET /api/v1/cluster/nodes`

## Example

```hcl
resource "selfcloud_project" "demo" {
  name = "demo"
}

resource "selfcloud_bucket" "uploads" {
  project = selfcloud_project.demo.name
  name    = "uploads"
}

resource "selfcloud_access_key" "uploader" {
  project     = selfcloud_project.demo.name
  name        = "uploader"
  bucket      = selfcloud_bucket.uploads.name
  permissions = "write"
}

resource "selfcloud_container" "hello" {
  project = selfcloud_project.demo.name
  name    = "hello"
  image   = "nginxdemos/hello:plain-text"
  ports   = [{ host = 8081, container = 80, protocol = "tcp" }]
  restart_policy = "Always"
}

resource "selfcloud_function" "echo" {
  project    = selfcloud_project.demo.name
  name       = "echo"
  runtime    = "wasm"
  http_path  = "/echo"
  memory_mb  = 128
  timeout_ms = 5000
  code_file  = "${path.module}/echo.wasm"
}

data "selfcloud_nodes" "all" {}
```

## TLS

The provider reuses one configured `http.Client` for **all** requests
(JSON ones and the binary `code` upload), so the `insecure` flag
applies uniformly. If you've put selfCloud behind a real certificate
(via a reverse proxy), set `insecure = false`.

## Notes

- `selfcloud_access_key.Read` calls `GET .../access-keys/{name}`,
  returning everything except the secret (which is only available
  once at create time). Plan changes that try to "see" the secret
  will report drift; use `lifecycle.ignore_changes = [secret_access_key]`
  if you want the secret as an output without re-reading it.
- The provider does not currently expose secrets, event rules, or
  cluster configuration. Use the REST API or the dashboard for those
  for now.
