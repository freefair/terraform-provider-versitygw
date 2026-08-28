---
page_title: "versitygw_users Data Source - versitygw"
description: |-
  Every account in the gateway's IAM service.
---

# versitygw_users (Data Source)

Every account in the gateway's IAM service.

~> The gateway returns each account **with its secret key**. Reading this data
source writes every secret on the gateway into the state of whatever root reads
it. Use it to audit what exists — not to wire credentials into another resource.

The root account is absent from the result: it is configured on the gateway's
command line and never stored in the IAM service.

## Example Usage

```terraform
data "versitygw_users" "all" {}

# Accounts on the gateway that this configuration does not manage.
output "unmanaged_accounts" {
  value = setsubtract(
    [for u in data.versitygw_users.all.users : u.access_key],
    [for u in versitygw_user.managed : u.access_key],
  )
}
```

## Schema

### Read-Only

- `users` (Attributes List)
  - `access_key` (String)
  - `secret_key` (String, Sensitive)
  - `role` (String) `user`, `userplus` or `admin`.
  - `user_id` (Number)
  - `group_id` (Number)
  - `project_id` (Number)
