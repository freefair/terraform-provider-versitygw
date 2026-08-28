# 03 — `versitygw_bucket_versioning`

Counterpart of `aws_s3_bucket_versioning`. Pairs with plan 04 (object lock
requires it).

## Schema

```hcl
resource "versitygw_bucket_versioning" "artifacts" {
  bucket = versitygw_bucket.artifacts.name
  versioning_configuration {
    status = "Enabled"   # or "Suspended"
  }
}
```

| Attribute | Notes |
|---|---|
| `bucket` | required, replaces |
| `versioning_configuration.status` | required; `Enabled` \| `Suspended` |

No `mfa_delete` — the gateway has no MFA.

## API mapping

| Op | Request |
|---|---|
| Create / Update | `PUT /{bucket}?versioning`, body `<VersioningConfiguration><Status>…</Status></VersioningConfiguration>` |
| Read | `GET /{bucket}?versioning` → `Status` element; an empty body / missing `Status` means "never configured" |
| Delete | none in S3. Remove from state only — S3 has no "Disabled" once versioning was turned on, only `Suspended`. Description says so, same as AWS. |

## Upstream facts (`backend/posix/posix.go` `PutBucketVersioning`)

- **posix needs a versioning directory.** Without `--versioning-dir` /
  `VGW_VERSIONING_DIR` the PUT answers `ErrVersioningNotConfigured`. Map it to
  a diagnostic that names the flag; the error message alone does not.
  `compat.yml` already sets `VGW_VERSIONING_DIR`; `test.yml` gets it with this
  plan.
- **Suspending is refused while object lock is enabled**
  (`ErrSuspendedVersioningNotAllowed`). Surface verbatim; that is the right
  answer.
- Read on a bucket that never had versioning configured: **verify** whether
  the gateway answers an empty `VersioningConfiguration` (AWS behaviour) or an
  error. Either way the resource treats it as absent.
- scoutfs backend: same flag (`cmd/internal/gwcli/scoutfs.go`). Azure and S3
  backends: **verify** whether `PutBucketVersioning` is implemented there or
  answers `NotImplemented`; document per backend.

## Tests

1. `Enabled` → read back `Enabled`; a `PutObject` twice on the same key
   yields two versions (`ListObjectVersions` in the check — proves the
   gateway actually versions, not just stores the flag).
2. `Suspended` → in-place update.
3. import.
4. Against a gateway without `VGW_VERSIONING_DIR` (same skip mechanism as
   plan 02's disabled-ACL test) → the diagnostic names the flag.

## Docs

`docs/resources/bucket_versioning.md` with the "cannot be disabled, only
suspended" note and the posix flag requirement; example; README table.
Update the acceptance-test comment in `provider_test.go` and the README's
`docker run` line to pass `VGW_VERSIONING_DIR`.
