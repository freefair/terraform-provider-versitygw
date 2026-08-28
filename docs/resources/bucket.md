---
page_title: "versitygw_bucket Resource - versitygw"
description: |-
  A bucket on the gateway, together with the account that owns it.
---

# versitygw_bucket (Resource)

A bucket on the gateway, together with the account that owns it.

Creating a bucket and assigning its owner is one call — the owner travels as a
header on create — so there is no window in which the bucket exists unowned.

## Example Usage

```terraform
resource "versitygw_user" "ci" {
  access_key = "build-pipeline"
  secret_key = var.ci_secret
}

resource "versitygw_bucket" "artifacts" {
  name  = "build-artifacts"
  owner = versitygw_user.ci.access_key
}
```

## Schema

### Required

- `name` (String) Bucket name, 3–63 characters, lower case, starting and ending
  alphanumerically. Immutable — changing it forces replacement.
- `owner` (String) Access key ID of the owning account.

### Optional

- `tags` (Map of String) Tags on the bucket, as on `aws_s3_bucket`. Defaults
  to an empty map, so `tags = {}` and no `tags` at all mean the same thing:
  no tag set on the gateway. Removing every tag deletes the tag set.

## Tags survive an owner change

Unlike the ACL and the policy, the tag set is not touched by
`change-bucket-owner`. Changing `owner` and `tags` in one apply performs the
ownership change first; if the tag write then fails, the new owner is kept in
state and the next apply retries only the tags.

## Changing the owner discards the ACL and the policy

~> This is upstream behaviour, not a provider limitation. When ownership moves,
the gateway removes the bucket's existing ACL and policy and applies a fresh
default for the new owner. A `versitygw_bucket_policy` on the bucket shows up
as missing on the next plan and is recreated; anything set outside Terraform
has to be reapplied by hand.

Because `name` forces replacement, an in-place update of this resource is an
ownership change, a tag change, or both — and only the ownership change
carries that consequence.

## Destroying a bucket requires it to be empty

The admin API has no delete route; the provider issues an S3 `DeleteBucket`,
which refuses a bucket that still holds objects. That refusal is worth keeping:
a `terraform destroy` should not be a way to lose data by accident. Empty the
bucket first when removing it is really what you want.

## Reference the owner, do not repeat it

Writing `owner = versitygw_user.ci.access_key` rather than `owner =
"build-pipeline"` is what tells Terraform to create the account first and to
destroy the bucket before the account that owns it. With a literal string there
is no dependency, and a destroy can leave the bucket behind with an owner that
no longer exists.

## Import

```shell
terraform import versitygw_bucket.artifacts build-artifacts
```

Import reads the bucket's tags into state. A configuration without `tags`
plans their removal on the first apply — add them to the configuration to
keep them, as with `aws_s3_bucket`.
