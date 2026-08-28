# terraform-provider-versitygw

## Overview

Terraform provider for the [Versity S3 Gateway](https://github.com/versity/versitygw):
manages IAM accounts and buckets through the gateway's admin API.

## Architecture

- `main.go` — provider entry point, sets the registry address and version
- `internal/provider/provider.go` — provider definition, schema, env fallbacks
- `internal/provider/resource_user.go` — `versitygw_user`
- `internal/provider/resource_bucket.go` — `versitygw_bucket`
- `internal/provider/data_source_*.go` — `versitygw_users`, `versitygw_buckets`
- `internal/client/` — SigV4-signed HTTP client for the admin and S3 APIs
- Uses `terraform-plugin-framework` (not SDKv2)

## Upstream facts the code depends on

Measured against versitygw v1.7.0. Each of these is load-bearing; changing the
code without re-checking them breaks things silently.

- **Every admin route is a `PATCH`** (`s3api/admin-router.go`), signed with
  SigV4 for service `s3` — not for a service of its own.
- **Without `--admin-port` the admin routes are mounted on the S3 server**
  (`embedgw/embedgw.go:606`). That is why `admin_endpoint` falls back to
  `endpoint` rather than erroring.
- **Bodies are marshalled by Go field name.** `auth.Account` carries json tags
  only, so `encoding/xml` uses `Access`, `Secret`, `Role`, `UserID`, `GroupID`,
  `ProjectID`. Renaming a field in `internal/client/types.go` produces an
  account with empty values and no error.
- **`list-users` returns secret keys** (`s3api/controllers/admin.go:127`
  returns `auth.ListUserAccountsResult`, whose `Account` has a `Secret` field).
  This is what makes drift detection on `secret_key` possible.
- **There is no admin route to delete a bucket.** Deletion goes to the S3 API,
  which refuses a non-empty bucket.
- **`change-bucket-owner` discards the bucket's ACL and policy.** Documented in
  the resource, not worked around.

## Key Design Decisions

- **Two endpoints, one client.** Accounts and ownership need the admin API;
  bucket deletion needs the S3 API. Splitting into two clients would have made
  every resource hold both.
- **`GetUser`/`GetBucket` filter a full listing.** The gateway has no
  single-object route. Both return `(nil, nil)` for "does not exist" so callers
  distinguish absence from failure without inspecting error codes.
- **Errors are typed.** `client.APIError` carries the gateway's code and status,
  with `IsNotFound`/`IsConflict` covering both APIs' vocabularies —
  `XAdminUserNotFound` and `NoSuchBucket` mean the same thing to a caller.
- **A non-XML error body stays readable.** A proxy answering with HTML must not
  turn into a "malformed XML" complaint that points at the wrong component.
- **The root account is out of scope.** It lives on the command line, never in
  the IAM service, so it appears in no listing and a resource for it would read
  back as missing on every plan.

## Build & Test

```bash
go build -v .
go test -v ./...                           # unit tests, no gateway needed
TF_ACC=1 go test -v -timeout 120m ./...    # acceptance tests, real gateway
```

Acceptance tests need a running gateway; see the comment at the top of
`internal/provider/provider_test.go` for the container invocation.

## Release

Tag with `vX.Y.Z` to trigger GoReleaser via GitHub Actions. Requires the
`GPG_PRIVATE_KEY` and `PASSPHRASE` secrets.
