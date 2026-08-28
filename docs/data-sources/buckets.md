---
page_title: "versitygw_buckets Data Source - versitygw"
description: |-
  Every bucket on the gateway with its owning account.
---

# versitygw_buckets (Data Source)

Every bucket on the gateway with its owning account.

The practical use is finding buckets nothing manages. An account deleted outside
Terraform leaves its buckets behind, owned by an access key ID that no longer
resolves — this is where they surface.

## Example Usage

```terraform
data "versitygw_buckets" "all" {}

data "versitygw_users" "all" {}

# Buckets whose owner no longer exists.
output "orphaned_buckets" {
  value = [
    for b in data.versitygw_buckets.all.buckets : b.name
    if !contains([for u in data.versitygw_users.all.users : u.access_key], b.owner)
  ]
}
```

## Schema

### Read-Only

- `buckets` (Attributes List)
  - `name` (String)
  - `owner` (String) Access key ID of the owning account.
