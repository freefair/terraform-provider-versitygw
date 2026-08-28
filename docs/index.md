---
page_title: "Provider: versitygw"
description: |-
  Manage accounts and buckets on a Versity S3 Gateway.
---

# versitygw Provider

Manages accounts and buckets on a [Versity S3 Gateway](https://github.com/versity/versitygw),
an S3-compatible gateway over a POSIX filesystem, ScoutFS, Azure Blob Storage or
another S3 server.

The gateway keeps account management behind an admin API of its own, so this
provider authenticates as an `admin` or `root` account. A regular `user` can
neither create buckets nor manage accounts.

## Example Usage

```terraform
terraform {
  required_providers {
    versitygw = {
      source  = "freefair/versitygw"
      version = "~> 0.1"
    }
  }
}

# Endpoint and credentials come from VERSITYGW_ENDPOINT, VERSITYGW_ACCESS_KEY
# and VERSITYGW_SECRET_KEY.
provider "versitygw" {}

resource "versitygw_user" "ci" {
  access_key = "build-pipeline"
  secret_key = var.ci_secret
  role       = "user"
}

resource "versitygw_bucket" "artifacts" {
  name  = "build-artifacts"
  owner = versitygw_user.ci.access_key
}
```

## Schema

### Optional

- `endpoint` (String) S3 API endpoint. Falls back to `VERSITYGW_ENDPOINT`.
- `admin_endpoint` (String) Admin API endpoint. Falls back to
  `VERSITYGW_ADMIN_ENDPOINT`, then to `endpoint`.
- `access_key` (String) Access key ID of an `admin` or `root` account. Falls
  back to `VERSITYGW_ACCESS_KEY`.
- `secret_key` (String, Sensitive) Secret key. Falls back to
  `VERSITYGW_SECRET_KEY`.
- `region` (String) Region requests are signed with; must match the gateway's
  `--region`. Falls back to `VERSITYGW_REGION`, then `us-east-1`.
- `insecure` (Boolean) Skip TLS certificate verification. Falls back to
  `VERSITYGW_INSECURE`.

## Which endpoint the admin API lives on

This trips people up, so it is worth stating directly. Leaving `--admin-port`
unset does **not** disable the admin API — the gateway mounts it on the S3
listener instead:

```go
// embedgw/embedgw.go
if len(cfg.AdminPorts) == 0 {
    opts = append(opts, s3api.WithAdminServer())
}
```

So:

- Gateway started **without** `--admin-port`: set only `endpoint`. The provider
  falls back to it for the admin calls, which is correct.
- Gateway started **with** `--admin-port`: the admin routes exist nowhere else,
  and `admin_endpoint` must name that listener. Without it every account
  operation answers `404`.

The second layout is the safer one to run: separating the listeners keeps
`create-user` and `list-users` off the endpoint that serves buckets.

## Secrets and state

Two properties worth knowing before deciding where the state lives:

- `versitygw_user.secret_key` is stored in state, like any Terraform-managed
  credential.
- The gateway's `list-users` route returns each account **with its secret key**,
  which is what lets this provider detect a key changed outside Terraform — and
  what makes the admin endpoint equivalent to the whole key ring.

Encrypt the state, keep the admin endpoint off any network that does not need
it, and treat the credentials this provider uses as the most privileged ones on
the gateway.
