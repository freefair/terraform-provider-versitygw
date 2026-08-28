---
page_title: "versitygw_bucket_object_lock_configuration Resource - versitygw"
description: |-
  Object lock and default retention of a bucket.
---

# versitygw_bucket_object_lock_configuration (Resource)

Object lock of a bucket, shaped like `aws_s3_bucket_object_lock_configuration`.

## Example Usage

```terraform
resource "versitygw_bucket_versioning" "archive" {
  bucket = versitygw_bucket.archive.name
  versioning_configuration {
    status = "Enabled"
  }
}

resource "versitygw_bucket_object_lock_configuration" "archive" {
  bucket = versitygw_bucket.archive.name
  rule {
    default_retention {
      mode = "COMPLIANCE"
      days = 30
    }
  }
  depends_on = [versitygw_bucket_versioning.archive]
}
```

## Schema

### Required

- `bucket` (String) Name of the bucket. Immutable — changing it forces
  replacement.

### Optional

- `object_lock_enabled` (String) Always `Enabled`, the only value S3
  defines. Present for parity with the AWS resource.
- `rule` (Block) with `default_retention` (Block):
  - `mode` (String) `GOVERNANCE` or `COMPLIANCE`. Required when
    `default_retention` is set.
  - `days` / `years` (Number) Retention period; exactly one of the two.

Leaving `rule` out enables the lock without a default retention; removing it
later clears the retention in place.

## Versioning first

The gateway accepts a lock configuration only on a bucket whose versioning is
`Enabled`, and answers `InvalidBucketState` otherwise. Add `depends_on` to the
`versitygw_bucket_versioning` of the same bucket so the two are applied in
order. Unlike AWS, the bucket does not have to be created with object lock —
any versioned bucket qualifies.

## Object lock cannot be turned off

~> Destroying this resource removes it from state and leaves the configuration
on the bucket; the S3 API offers nothing to send. While the lock is present,
the gateway refuses to change the bucket's versioning state.

## Import

```shell
terraform import versitygw_bucket_object_lock_configuration.archive archive
```
