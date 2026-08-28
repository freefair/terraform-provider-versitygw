# 00 — Shared S3 sub-resource client

Prerequisite for every other plan. No user-visible resource; this is the
plumbing that lets the provider speak the bucket sub-resource part of the S3
API with the same client it already uses for the admin API.

## What exists

- `internal/client/client.go` — `do()` signs any request with SigV4 for
  service `s3`. The admin API and the S3 API use the same signature scheme,
  so nothing about signing changes.
- `internal/client/admin.go` — `DeleteBucket` already goes to the S3
  endpoint (`c.cfg.Endpoint + "/" + url.PathEscape(name)`). That is the pattern to generalise.
- `internal/client/types.go` — `APIError` with `IsNotFound` / `IsConflict`.
- `internal/provider/helpers.go` — `clientFrom`, `asAPIError`, `isNotFound`.

## Changes

### `internal/client/s3.go` (new)

```go
// s3URL builds a bucket sub-resource URL on the S3 endpoint, e.g.
// https://s3.example.com/my-bucket?policy
func (c *Client) s3URL(bucket, subresource string) string

// putBucketSubresource, getBucketSubresource, deleteBucketSubresource wrap do()
// for PUT/GET/DELETE ?<subresource>. GET returns (nil, nil) when the gateway
// answers with the sub-resource's own "not found" code, so callers keep the
// (nil, nil) == absent convention GetUser/GetBucket already use.
```

Typed wrappers per feature live in the same file and are added by each plan
(`PutBucketPolicy`, `GetBucketVersioning`, …). Bodies are marshalled with
`encoding/xml` against small structs mirroring the S3 wire format; the policy
body is raw JSON.

Path-style addressing (`/bucket?policy`) — the gateway's `--virtual-domain`
mode is not assumed, and the existing `DeleteBucket` is path-style too.

### `internal/client/types.go`

Extend `IsNotFound` with the sub-resource codes the gateway uses
(`s3err/s3err.go`):

`NoSuchBucketPolicy`, `NoSuchCORSConfiguration`,
`NoSuchWebsiteConfiguration`, `NoSuchTagSet`,
`ObjectLockConfigurationNotFoundError`, `OwnershipControlsNotFoundError`.

Keep the existing 404 fallback. Add `IsNotImplemented()` for code
`NotImplemented` — a gateway started with `--disable-acl`, or an older
gateway, answers with it, and the resource should say "this gateway does
not support X" rather than "unexpected error".

### Resource pattern (documented once, reused by every plan)

- `bucket` — Required, `RequiresReplace`, same validators as
  `versitygw_bucket.name` (`validators.go`).
- No synthetic `id`; `ImportStatePassthroughID` on `bucket`, tests use
  `ImportStateVerifyIdentifierAttribute: "bucket"` (see `provider_test.go`).
- `Read`: `(nil, nil)` from the client → `RemoveResource`.
- `Create` on a bucket that already carries the configuration: S3 semantics
  are "PUT replaces", so no conflict error — the AWS provider behaves the
  same. Document it in each resource description.
- Configure the resource to reference `versitygw_bucket.<x>.name` for
  `bucket` so destroy order is right (bucket deletion needs the sub-resources
  gone first only in the sense of state; the gateway removes them with the
  bucket).

### Owner change resets ACL and policy — and nothing else

`auth.UpdateBucketACLOwner` (`auth/acl.go`) does exactly two things: `PutBucketAcl`
with a fresh default ACL for the new owner and `DeleteBucketPolicy`. Tags,
CORS, website, versioning, object lock and ownership controls are separate
attributes on the bucket and are untouched. So:

- `versitygw_bucket_policy` and `versitygw_bucket_acl` show drift after an
  owner change and re-apply on the next run. Document it in both resources;
  do not try to re-apply inside the bucket's `Update` — that would hide the
  reset instead of surfacing it.
- The bucket resource description already warns about this; the warning gets
  cross-links to the two resources once they exist.

### Test infrastructure

- `provider_test.go`: a helper that renders `versitygw_user` +
  `versitygw_bucket` fixtures so each sub-resource test adds only its own
  block.
- CI service already sets `VGW_VERSIONING_DIR` in `compat.yml`; add the same
  to `test.yml` when plan 03 lands.

## Verification

- Unit: `client_test.go` gets URL-building and error-mapping cases
  (`?policy` on the S3 endpoint even when `admin_endpoint` differs; each
  new not-found code → `IsNotFound() == true`).
- Acceptance: nothing user-visible yet; plan 01 is the first consumer and
  proves the plumbing end to end.
