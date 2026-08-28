# 08 — `versitygw_bucket_ownership_controls`

Counterpart of `aws_s3_bucket_ownership_controls`. Small and self-contained:
it exercises the full PUT/GET/DELETE triple with a tiny body, which makes it
a good first consumer of plan 00's delete path. Its place in the overall
order is set by the README table.

## Schema

```hcl
resource "versitygw_bucket_ownership_controls" "artifacts" {
  bucket = versitygw_bucket.artifacts.name
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}
```

`rule.object_ownership` — `BucketOwnerPreferred` | `ObjectWriter` |
`BucketOwnerEnforced` (`types.ObjectOwnership`, which is what
`backend.Backend.PutBucketOwnershipControls` takes).

## API mapping

| Op | Request |
|---|---|
| Create / Update | `PUT /{bucket}?ownershipControls`, body `OwnershipControls/Rule/ObjectOwnership` |
| Read | `GET /{bucket}?ownershipControls`; `OwnershipControlsNotFoundError` → absent |
| Delete | `DELETE /{bucket}?ownershipControls` |

## Semantics and edge cases

- **Verify** the gateway's default when nothing is set (AWS: buckets created
  after 2023 default to `BucketOwnerEnforced`; the gateway likely reports
  not-found until something is PUT). The resource treats not-found as
  absent either way.
- `BucketOwnerEnforced` disables ACLs on AWS. **Verify** whether the gateway
  rejects `PUT ?acl` in that state (`s3api/controllers` ACL handler); if it
  does, cross-reference from plan 02's docs.
- Owner change does not touch it (plan 00).

## Tests

1. `BucketOwnerEnforced` → read back.
2. → `ObjectWriter` in-place.
3. destroy → `GET` answers not-found (check in `CheckDestroy`).
4. import.

## Docs

`docs/resources/bucket_ownership_controls.md`, example, README table.
