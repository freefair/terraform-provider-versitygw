---
page_title: "versitygw_bucket_versioning Resource - versitygw"
description: |-
  Versioning state of a bucket.
---

# versitygw_bucket_versioning (Resource)

Versioning state of a bucket, shaped like `aws_s3_bucket_versioning`.

## Example Usage

```terraform
resource "versitygw_bucket" "archive" {
  name  = "archive"
  owner = versitygw_user.archive.access_key
}

resource "versitygw_bucket_versioning" "archive" {
  bucket = versitygw_bucket.archive.name
  versioning_configuration {
    status = "Enabled"
  }
}
```

## Schema

### Required

- `bucket` (String) Name of the bucket. Immutable — changing it forces
  replacement.
- `versioning_configuration` (Block) with `status` (String): `Enabled` or
  `Suspended`.

## The gateway needs a versioning directory

On the posix and scoutfs backends versioning exists only when the gateway was
started with `--versioning-dir` (`VGW_VERSIONING_DIR`), and that directory
must exist before the gateway starts. Without it the gateway answers
`VersioningNotConfigured`, which this resource reports with the flag's name.

## Versioning cannot be turned off

~> S3 has no "off" once versioning was enabled, only `Suspended`, and the
gateway follows that. Destroying this resource removes it from state and
leaves the bucket as it is; nothing is sent. To stop new versions from being
created, set `status = "Suspended"`.

While a `versitygw_bucket_object_lock_configuration` is present on the
bucket, the gateway refuses to change the versioning state
(`InvalidBucketState`).

## Import

```shell
terraform import versitygw_bucket_versioning.archive archive
```
