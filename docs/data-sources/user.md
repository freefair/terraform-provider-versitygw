---
page_title: "versitygw_user Data Source - versitygw"
description: |-
  One account, looked up by access key ID.
---

# versitygw_user (Data Source)

One account in the gateway's IAM service, looked up by access key ID. A
missing account is an error, not an empty result — a typo fails the plan
instead of quietly wiring nulls into whatever references it.

~> The gateway hands out the secret key with the account, so reading this data
source writes that secret into the state of the root that reads it — exactly
as the `versitygw_user` resource does.

The root account cannot be looked up: it lives on the command line and never
in the IAM service.

## Example Usage

```terraform
# An account created outside Terraform.
data "versitygw_user" "ci" {
  access_key = "build-pipeline"
}

resource "versitygw_bucket" "artifacts" {
  name  = "build-artifacts"
  owner = data.versitygw_user.ci.access_key
}
```

## Schema

### Required

- `access_key` (String) Access key ID of the account.

### Read-Only

- `secret_key` (String, Sensitive) Secret key, as stored by the gateway.
- `role` (String) `user`, `userplus` or `admin`.
- `user_id` (Number) POSIX UID.
- `group_id` (Number) POSIX GID.
- `project_id` (Number) Project ID.
