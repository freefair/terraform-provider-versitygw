# terraform-provider-versitygw

## Overview

Terraform provider for the [Versity S3 Gateway](https://github.com/versity/versitygw):
manages IAM accounts and buckets through the gateway's admin API.

## Architecture

- `main.go` — provider entry point, sets the registry address and version
- `internal/provider/provider.go` — provider definition, schema, env fallbacks
- `internal/provider/resource_user.go` — `versitygw_user`
- `internal/provider/resource_bucket.go` — `versitygw_bucket`
- `internal/provider/resource_bucket_policy.go` — `versitygw_bucket_policy`
- `internal/provider/resource_bucket_versioning.go` — `versitygw_bucket_versioning`
- `internal/provider/resource_bucket_object_lock_configuration.go` — `versitygw_bucket_object_lock_configuration`
- `internal/provider/resource_bucket_ownership_controls.go` — `versitygw_bucket_ownership_controls`
- `internal/provider/resource_bucket_acl.go` — `versitygw_bucket_acl`
- `internal/provider/data_source_*.go` — `versitygw_users`, `versitygw_buckets`
- `internal/client/` — SigV4-signed HTTP client; `admin.go` for the admin API, `s3.go` for bucket sub-resources on the S3 API
- `docs/plans/` — one plan per missing bucket-level resource; `README.md` there is the roadmap
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
- **Without an IAM backend (`--iam-dir` or another `--iam-*`) the gateway
  runs in single-account mode** and every account route answers
  `501 XAdminMethodNotSupported`. The posix backend and the versioning
  directory must exist before start; the image creates only `/tmp/vgw`.
- **A `DELETE` with a sub-resource the router does not know deletes the
  bucket** (`?versioning`, `?object-lock` — measured, the bucket was gone).
  The client has no delete for those; the resources are state-only on
  destroy. Never add one.
- **`PUT ?object-lock` requires `Content-MD5`**; the client sends it on every
  sub-resource PUT. Object lock needs versioning `Enabled` first
  (`InvalidBucketState`), and suspending versioning with a lock present is
  refused the same way. The versioning directory must exist before start.
- **A fresh bucket is `BucketOwnerEnforced`, which disables ACLs**
  (`PUT ?acl` → `AccessControlListNotSupported`). Ownership controls have a
  real `DELETE`; afterwards the bucket reports none, not the default.
- **Explicit ACL grants resolve every grantee as an account.** A `Group`
  grantee in any spelling, or an unknown access key, answers HTTP 500
  `InternalError`; public access exists only via canned ACLs. The document's
  owner must be the real owner (`InvalidArgument` otherwise) and the owner's
  `FULL_CONTROL` grant is always present on read, duplicated if sent.
- **There is no admin route to delete a bucket.** Deletion goes to the S3 API,
  which refuses a non-empty bucket.
- **`change-bucket-owner` discards the bucket's ACL and policy — and nothing
  else.** `auth.UpdateBucketACLOwner` does a `PutBucketAcl` and a
  `DeleteBucketPolicy`; tags, CORS, website, versioning and object lock stay.
  Documented in the resource, not worked around.
- **Lifecycle, encryption, replication, logging, notification and public
  access block are `ErrNotImplemented` in `s3api/router.go`** and absent from
  `backend.Backend`. Do not plan resources for them; re-check on a version bump.

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
  A bare 404 without a gateway code is never "not found": a proxy or an
  unmounted admin endpoint produces one, and treating it as absence would
  drop resources from state.
- **A non-XML error body stays readable.** A proxy answering with HTML must not
  turn into a "malformed XML" complaint that points at the wrong component.
- **Bucket sub-resources share one client shape** (`s3.go`): path-style
  `PUT/GET/DELETE /<bucket>?<subresource>`, `GET` returns `(nil, nil)` for the
  sub-resource's own not-found code, `DELETE` treats absence as done. New
  sub-resources add typed wrappers there, not new plumbing.
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

CI: `test.yml` pins `versity/versitygw:v1.7.0` — the version the facts above
are measured against. `compat.yml` runs weekly against `:latest` with Terraform
latest and OpenTofu latest. A red compat run means: bump the pin in a deliberate
commit and re-check every fact above. Dependabot covers Go modules and Actions;
it cannot see the image pin.

## Workflow: finishing a task

- **Codex review before the PR.** When a task is complete, run a review through
  the Codex MCP (`mcp__codex__codex`, model `gpt-5.6-sol` — the default in `~/.codex/config.toml`; the short name `sol` is rejected — reasoning effort `high`) on the diff and
  act on its findings before opening the pull request. Tell Codex explicitly
  that it must only review — no edits, no commands beyond reading — and that
  the findings must be its final answer, not a remark in passing; otherwise it
  starts fixing things or buries the result.
- **Self-merge is allowed** when the acceptance tests against a real
  `versitygw` in Docker are green and coverage is around 100 %. Reasonable
  beats absolute: do what is needed, but a solid 90 % is better than a
  contrived 100 %. Anything below that, or a red run, waits for Dennis.
- Work on a feature branch; `main` is behind a merge queue.

## Release

Tag with `vX.Y.Z` to trigger GoReleaser via GitHub Actions. Requires the
`GPG_PRIVATE_KEY` and `PASSPHRASE` secrets.
