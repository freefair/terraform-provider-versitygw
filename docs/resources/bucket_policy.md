---
page_title: "versitygw_bucket_policy Resource - versitygw"
description: |-
  The policy of a bucket — the one way to let an account reach a bucket it does not own.
---

# versitygw_bucket_policy (Resource)

The policy of a bucket — the one way to let an account reach a bucket it does
not own. Shaped like `aws_s3_bucket_policy`: one resource per bucket, the
document as JSON.

## Example Usage

```terraform
resource "versitygw_user" "ci" {
  access_key = "build-pipeline"
  secret_key = var.ci_secret
}

resource "versitygw_user" "reader" {
  access_key = "release-page"
  secret_key = var.reader_secret
}

resource "versitygw_bucket" "artifacts" {
  name  = "build-artifacts"
  owner = versitygw_user.ci.access_key
}

resource "versitygw_bucket_policy" "artifacts_read" {
  bucket = versitygw_bucket.artifacts.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = [versitygw_user.reader.access_key] }
      Action    = ["s3:GetObject", "s3:ListBucket"]
      Resource = [
        "arn:aws:s3:::${versitygw_bucket.artifacts.name}",
        "arn:aws:s3:::${versitygw_bucket.artifacts.name}/*",
      ]
    }]
  })
}
```

## Schema

### Required

- `bucket` (String) Name of the bucket. Immutable — changing it forces
  replacement. Reference `versitygw_bucket.<name>.name` so the bucket exists
  before the policy does.
- `policy` (String) Policy document as JSON, typically from `jsonencode()`.
  Compared semantically: key order and whitespace never produce a diff.

## Principals are access key IDs

The gateway has no canonical user IDs and no ARNs for accounts. A principal is
written as the account's access key ID — `{ AWS = ["release-page"] }` — or `*`
for everyone. Reference `versitygw_user.<name>.access_key` rather than
repeating the string, so the account is created before the policy names it.

Resources use the usual S3 ARNs, `arn:aws:s3:::<bucket>` for bucket actions
such as `s3:ListBucket` and `arn:aws:s3:::<bucket>/*` for object actions. The
gateway rejects a policy whose resources name a different bucket, and a
statement whose actions do not match its resource type; the rejection carries
the gateway's own message.

## Changing the bucket's owner deletes the policy

~> When `owner` on the `versitygw_bucket` changes, the gateway applies a fresh
default ACL for the new owner and drops the policy with it. On the next plan
Terraform finds the policy missing and recreates it. Nothing is reapplied
silently — a plan that shows the policy being created again after an owner
change is the provider telling you what the gateway did.

## One policy per bucket

A bucket carries exactly one policy document, and a PUT replaces it. Two
`versitygw_bucket_policy` resources on the same bucket overwrite each other on
every apply; put every statement into one resource.

## Import

```shell
terraform import versitygw_bucket_policy.artifacts_read build-artifacts
```
