# 09 — `data.versitygw_user` and `data.versitygw_bucket`

Counterparts of `data.aws_s3_bucket` (and, loosely, `data.aws_iam_user`).
Independent of every other plan; can be done any time.

Today the only way to reference an existing account or bucket that Terraform
does not manage is to filter `data.versitygw_users` / `data.versitygw_buckets`
with a `for` expression. A single-object data source says what is meant and
fails loudly when the object is missing.

## Schema

```hcl
data "versitygw_user" "ci" {
  access_key = "build-pipeline"
}

data "versitygw_bucket" "artifacts" {
  name = "build-artifacts"
}

resource "versitygw_bucket_policy" "p" {
  bucket = data.versitygw_bucket.artifacts.name
  policy = …
}
```

| Data source | Input | Outputs |
|---|---|---|
| `versitygw_user` | `access_key` | `role`, `user_id`, `group_id`, `project_id`, `secret_key` (Sensitive) |
| `versitygw_bucket` | `name` | `owner`; plus `tags` once plan 05 lands |

## Implementation

- `internal/provider/data_source_user.go`, `data_source_bucket.go`, using
  `client.GetUser` / `client.GetBucket` from `internal/client/admin.go` —
  both already return `(nil, nil)` for absence.
- Absence is an **error** ("no account with access key ID … on the
  gateway"), not an empty result: a data source that silently yields nulls
  turns a typo into a plan that applies against nothing.
- `secret_key` on the user data source: the gateway hands it out in
  `list-users`, so it is available. Include it, mark Sensitive, and say in
  the description that reading it puts the secret in state — exactly as
  `versitygw_user` already does. Leaving it out would only push people back
  to the list data source, which exposes every secret.
- Register both in `provider.go` `DataSources`.

## Tests

`TestAccUserDataSource`, `TestAccBucketDataSource`: create via resource,
read via data source, attributes match; a data source for a missing name →
`ExpectError`.

## Docs

`docs/data-sources/user.md`, `docs/data-sources/bucket.md`, examples,
README table.
