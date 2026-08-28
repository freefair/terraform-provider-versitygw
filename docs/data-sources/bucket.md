---
page_title: "versitygw_bucket Data Source - versitygw"
description: |-
  One bucket, looked up by name.
---

# versitygw_bucket (Data Source)

One bucket, looked up by name — the counterpart of `data.aws_s3_bucket`. A
missing bucket is an error, not an empty result, so a typo cannot turn into a
plan against nothing.

## Example Usage

```terraform
# A bucket created outside Terraform, given a policy here.
data "versitygw_bucket" "artifacts" {
  name = "build-artifacts"
}

resource "versitygw_bucket_policy" "artifacts" {
  bucket = data.versitygw_bucket.artifacts.name
  policy = file("${path.module}/read-only.json")
}
```

## Schema

### Required

- `name` (String) Bucket name.

### Read-Only

- `owner` (String) Access key ID of the owning account.
- `tags` (Map of String) Tags on the bucket; empty when it has none.
