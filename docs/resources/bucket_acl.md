---
page_title: "versitygw_bucket_acl Resource - versitygw"
description: |-
  The ACL of a bucket — canned, or explicit grants to accounts.
---

# versitygw_bucket_acl (Resource)

The ACL of a bucket, shaped like `aws_s3_bucket_acl`: either a canned `acl`
or an explicit `access_control_policy`, never both.

## Example Usage

```terraform
resource "versitygw_bucket_ownership_controls" "site" {
  bucket = versitygw_bucket.site.name
  rule {
    object_ownership = "ObjectWriter"
  }
}

resource "versitygw_bucket_acl" "site" {
  bucket     = versitygw_bucket.site.name
  acl        = "public-read"
  depends_on = [versitygw_bucket_ownership_controls.site]
}

resource "versitygw_bucket_acl" "reports" {
  bucket = versitygw_bucket.reports.name
  access_control_policy {
    grant {
      permission = "READ"
      grantee {
        type = "CanonicalUser"
        id   = versitygw_user.auditor.access_key
      }
    }
  }
  depends_on = [versitygw_bucket_ownership_controls.reports]
}
```

## Schema

### Required

- `bucket` (String) Name of the bucket. Immutable — changing it forces
  replacement.

### Optional (exactly one)

- `acl` (String) Canned ACL: `private`, `public-read` or
  `public-read-write`.
- `access_control_policy` (Block) with `grant` blocks (set):
  - `permission` (String) `FULL_CONTROL`, `READ`, `WRITE`, `READ_ACP` or
    `WRITE_ACP`.
  - `grantee` (Block): `type` = `CanonicalUser`, `id` = the account's access
    key ID.

## A fresh bucket refuses ACLs

~> A new bucket's ownership is `BucketOwnerEnforced`, and in that state
every ACL write answers `AccessControlListNotSupported`. Put a
`versitygw_bucket_ownership_controls` with `ObjectWriter` or
`BucketOwnerPreferred` on the bucket first, and give this resource a
`depends_on` on it.

## What differs from AWS

- **The owner is not configured.** The gateway accepts only the bucket's
  actual owner in the document; the provider reads it and fills it in.
- **The owner's `FULL_CONTROL` grant is implicit.** The gateway carries it on
  every bucket and would duplicate it if sent, so it is not part of `grant`;
  listing it is refused at apply time.
- **Public access exists only through the canned ACLs.** The gateway
  resolves every explicit grantee as an account and answers an internal
  error for a group in any spelling. `grant` therefore takes `CanonicalUser`
  grantees only; a canned public ACL is read back as the group grants it
  produces.
- **Grantee IDs are access key IDs**, not canonical user IDs. A grant for an
  account that does not exist is refused by the gateway with an internal
  error, which the provider explains.

## Owner change resets the ACL

Changing the bucket's `owner` makes the gateway apply a fresh default ACL for
the new owner. Terraform sees the difference on the next plan and puts the
configured ACL back.

## Destroy forgets, nothing more

S3 has no delete for ACLs. Destroying this resource removes it from state
and leaves the bucket's ACL as it is.

## Import

```shell
terraform import versitygw_bucket_acl.site site
```
