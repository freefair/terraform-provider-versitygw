# 04 — `versitygw_bucket_object_lock_configuration`

Counterpart of `aws_s3_bucket_object_lock_configuration`. Depends on plan 03.

## Schema

```hcl
resource "versitygw_bucket_object_lock_configuration" "archive" {
  bucket = versitygw_bucket.archive.name
  rule {
    default_retention {
      mode = "COMPLIANCE"   # or "GOVERNANCE"
      days = 30             # or years
    }
  }
  depends_on = [versitygw_bucket_versioning.archive]
}
```

| Attribute | Notes |
|---|---|
| `bucket` | required, replaces |
| `object_lock_enabled` | optional, computed, default `Enabled` — the only value S3 accepts in this body |
| `rule.default_retention.mode` | `GOVERNANCE` \| `COMPLIANCE` |
| `rule.default_retention.days` / `years` | exactly one |

## API mapping

| Op | Request |
|---|---|
| Create / Update | `PUT /{bucket}?object-lock`, body `ObjectLockConfiguration` XML |
| Read | `GET /{bucket}?object-lock`; `ObjectLockConfigurationNotFoundError` → absent |
| Delete | none in S3; state only. Object lock cannot be turned off once on — say so. |

## Upstream facts (`backend/posix/posix.go` `PutObjectLockConfiguration`)

- When the gateway runs with a versioning directory, the bucket's versioning
  must be `Enabled` or the PUT answers `ErrObjectLockConfigurationNotAllowed`.
  Hence the `depends_on` in the example; document it. The resource should
  turn that error into "enable versioning on this bucket first (see
  versitygw_bucket_versioning)".
- Without a versioning directory the gateway stores the configuration
  anyway (the check is skipped when `p.versioningEnabled()` is false) — lock
  semantics without versions are questionable. **Verify** and document; do
  not block it in the provider, the gateway's behaviour is upstream's call.
- Suspending versioning afterwards is refused by the gateway (plan 03).

## Open question: object lock at bucket creation

On AWS, a bucket must be created with `ObjectLockEnabledForBucket` before
`PutObjectLockConfiguration` is allowed. The admin route
`/:bucket/create` (`s3api/controllers/admin.go` `CreateBucket`) delegates to
the regular S3 `CreateBucket` handler, so the header
`x-amz-bucket-object-lock-enabled: true` **should** pass through — **verify**
by sending it through the admin route and reading `?object-lock` back. If the
gateway requires it, add `object_lock_enabled` (bool, replaces) to
`versitygw_bucket`, exactly as `aws_s3_bucket.object_lock_enabled`, and note
that this also enables versioning at creation. If the gateway does not care,
leave the bucket resource alone.

## Measured (v1.7.0, implementation)

- `PUT ?object-lock` requires `Content-MD5`; the client now sends it on
  every sub-resource PUT.
- Lock on an unversioned bucket → `InvalidBucketState` (HTTP 409, "Versioning
  must be 'Enabled'"). Lock on a bucket versioned after creation works, so
  no `object_lock_enabled` on `versitygw_bucket` — the bucket resource stays
  untouched. (`x-amz-bucket-object-lock-enabled` on the admin create route
  does work and enables versioning too; not exposed, not needed.)
- Removing the rule: PUT without `Rule` → `GET` answers `<Rule></Rule>`,
  normalised to "no rule" in the client.
- **Never send `DELETE ?object-lock`**: it deletes the bucket. Delete is
  state-only.

## Tests

1. bucket + versioning Enabled + lock with `GOVERNANCE`/`days = 1` → read
   back; a `PutObject` then `GetObjectRetention` shows the default applied
   (proves the gateway honours the rule).
2. change to `COMPLIANCE`/`years = 1` → in-place.
3. lock without versioning enabled → `ExpectError` naming the versioning
   resource.
4. import.

## Docs

`docs/resources/bucket_object_lock_configuration.md`; example with the
versioning pair; README table.
