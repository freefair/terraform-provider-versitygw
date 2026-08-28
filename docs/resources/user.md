---
page_title: "versitygw_user Resource - versitygw"
description: |-
  An account in the gateway's IAM service.
---

# versitygw_user (Resource)

An account in the gateway's IAM service.

The account **is** its access key ID — the gateway offers no rename — so
changing `access_key` replaces the account.

The root account configured on the gateway's command line is not manageable
here. It is never written to the IAM service and appears in no listing, so a
resource describing it would read back as missing on every plan.

## Example Usage

```terraform
resource "random_password" "ci" {
  length  = 40
  special = false
}

resource "versitygw_user" "ci" {
  access_key = "build-pipeline"
  secret_key = random_password.ci.result
  role       = "user"
}
```

## Schema

### Required

- `access_key` (String) Access key ID, and the account's identity. Changing it
  forces replacement.
- `secret_key` (String, Sensitive) Secret key.

### Optional

- `role` (String) One of `user`, `userplus`, `admin`. Defaults to `user`.
- `user_id` (Number) POSIX UID objects are written as. Defaults to `0`.
- `group_id` (Number) POSIX GID. Defaults to `0`.
- `project_id` (Number) Project ID for filesystem project quotas. Defaults to `0`.

## Roles

| Role | Create buckets | Sees | Bucket policies |
|---|---|---|---|
| `user` | no | only what it owns | no |
| `userplus` | no | only what it owns | yes |
| `admin` | yes | every bucket | ignored |

`user` is the default because it is the only one of the three that is a
boundary. `admin` accounts can create and reassign buckets and manage other
accounts, and their policies are ignored by the gateway rather than enforced.

## Deleting an account does not delete its buckets

The gateway does not cascade. A destroyed account leaves its buckets in place,
owned by an access key ID nobody can authenticate as, until an admin reassigns
them. Reference the user from `versitygw_bucket.owner` so Terraform destroys the
buckets first.

## Import

```shell
terraform import versitygw_user.ci build-pipeline
```

The secret key is read back from the gateway during import, so an imported
account needs no secret in the configuration before the first plan.
