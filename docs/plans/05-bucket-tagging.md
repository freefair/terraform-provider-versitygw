# 05 — Bucket tags

Counterpart: `tags` on `aws_s3_bucket`. No separate resource — the AWS
provider keeps tags on the bucket, and a second resource for one map of
strings on the same object would only add ordering questions.

## Schema change on `versitygw_bucket`

```hcl
resource "versitygw_bucket" "artifacts" {
  name  = "build-artifacts"
  owner = versitygw_user.ci.access_key
  tags = {
    team = "platform"
    cost = "ci"
  }
}
```

`tags` — `map(string)`, optional. Null/empty means "no tag set".

Considered and rejected: a `versitygw_bucket_tagging` resource. Pro: keeps
`versitygw_bucket` purely an admin-API resource. Contra: the AWS shape is
tags-on-bucket, and everything else in this roadmap already puts S3-API
calls behind bucket-related resources.

## API mapping

| Op | Request |
|---|---|
| Create (tags set) | after the admin `create`, `PUT /{bucket}?tagging` with `Tagging/TagSet/Tag{Key,Value}` XML |
| Read | `GET /{bucket}?tagging`; `NoSuchTagSet` → empty map |
| Update | tags changed → PUT (replaces the whole set); tags removed → `DELETE /{bucket}?tagging` |
| Delete | bucket deletion removes the tags |

The bucket's `Read` grows one S3 call. Keep the admin listing as the
existence check; only call `?tagging` when the bucket exists.

## Semantics and edge cases

- **Owner change keeps tags** — `auth.UpdateBucketACLOwner` touches ACL and
  policy only (plan 00). Nothing to do, but the test should prove it.
- posix stores tags as an xattr (`getAttrTags` / `PutBucketTagging`,
  `backend/posix/posix.go`). Limits on key/value length and count follow S3
  (**verify**: the gateway validates 50 tags, 128/256 chars — or not).
- A bucket created outside Terraform with tags and then imported: `Read`
  fills `tags`; the first plan shows them as to-be-removed if the config has
  none — same as AWS. Document.
- Empty map vs null: treat both as "delete the tag set" to avoid a perpetual
  diff between `{}` and absent.

## Measured (v1.7.0, implementation)

- `DELETE ?tagging` is a real route: the bucket survives, a following `GET`
  answers `NoSuchTagSet`. Deleting an absent tag set also answers 204.
- `PUT ?tagging` with an empty `<TagSet/>` is stored as an empty set — `GET`
  answers 200 with no tags rather than `NoSuchTagSet`. The provider reads
  both as an empty map, so the distinction never reaches state.
- Tags come back sorted by key; the provider's map type does not care.
- Owner change keeps the tags (asserted in `TestAccBucketTags`).
- No tag count or length limits were hit with normal values; the gateway's
  validation of the S3 limits (50 tags, 128/256 characters) was not measured.
- Chosen: `tags` is `Optional + Computed` with an empty-map default. Null and
  `{}` in config are thus the same state and neither produces a diff.

## Tests

Extend `TestAccBucketResource`:
1. create with two tags → `tags.%` = 2 and the values.
2. change one, drop one → in-place, PUT with the new set.
3. remove `tags` entirely → DELETE, read back empty.
4. change owner in the same step as keeping tags → tags survive.
5. import with tags present.

## Docs

`docs/resources/bucket.md` gets the attribute; example extended; the
"Key behaviours" section in README stays as is (owner change note is
unchanged in substance).
