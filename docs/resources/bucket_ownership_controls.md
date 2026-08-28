---
page_title: "versitygw_bucket_ownership_controls Resource - versitygw"
description: |-
  Object ownership of a bucket — what decides whether ACLs are allowed.
---

# versitygw_bucket_ownership_controls (Resource)

Object ownership of a bucket, shaped like `aws_s3_bucket_ownership_controls`.

## Example Usage

```terraform
resource "versitygw_bucket" "uploads" {
  name  = "uploads"
  owner = versitygw_user.app.access_key
}

resource "versitygw_bucket_ownership_controls" "uploads" {
  bucket = versitygw_bucket.uploads.name
  rule {
    object_ownership = "ObjectWriter"
  }
}
```

## Schema

### Required

- `bucket` (String) Name of the bucket. Immutable — changing it forces
  replacement.
- `rule` (Block) with `object_ownership` (String): `BucketOwnerEnforced`,
  `BucketOwnerPreferred` or `ObjectWriter`.

## A fresh bucket is `BucketOwnerEnforced` — and that disables ACLs

The gateway follows current S3 defaults: a new bucket reports
`BucketOwnerEnforced` without anyone having set it, and in that state every
`PUT ?acl` answers `AccessControlListNotSupported`. To use
`versitygw_bucket_acl`, set `ObjectWriter` or `BucketOwnerPreferred` here first
and give the ACL resource a `depends_on` on this one.

## Destroy deletes the controls

Unlike versioning and object lock, the S3 API has a delete for ownership
controls and the gateway honours it. Afterwards the bucket reports no
controls at all (`OwnershipControlsNotFoundError`) rather than falling back
to the default — ACL behaviour in that state is the gateway's call.

## Import

```shell
terraform import versitygw_bucket_ownership_controls.uploads uploads
```
