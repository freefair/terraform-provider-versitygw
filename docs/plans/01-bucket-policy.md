# 01 — `versitygw_bucket_policy`

Counterpart of `aws_s3_bucket_policy`. Highest priority: a policy is the only
way to let an account reach a bucket it does not own, which is the first thing
needed after "one user, one bucket".

## Schema

```hcl
resource "versitygw_bucket_policy" "artifacts" {
  bucket = versitygw_bucket.artifacts.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = [versitygw_user.reader.access_key] }
      Action    = ["s3:GetObject", "s3:ListBucket"]
      Resource  = ["arn:aws:s3:::build-artifacts", "arn:aws:s3:::build-artifacts/*"]
    }]
  })
}
```

| Attribute | Type | Notes |
|---|---|---|
| `bucket` | string, required, replaces | bucket name |
| `policy` | string, required | JSON policy document |

`policy` uses `jsontypes.Normalized` from
`github.com/hashicorp/terraform-plugin-framework-jsontypes` (new dependency —
look up the current stable version, do not guess) so key order and whitespace
differences between config and what the gateway stores do not produce a
perpetual diff. Verify what the gateway returns on `GET ?policy`: posix stores
the raw bytes it was given (`backend/posix/posix.go` `PutBucketPolicy` →
`StoreAttribute`), so the round trip should be byte-identical, but normalised
comparison costs nothing and protects against a future upstream change.

## API mapping

| Op | Request |
|---|---|
| Create / Update | `PUT /{bucket}?policy`, body = policy JSON |
| Read | `GET /{bucket}?policy` → JSON; `NoSuchBucketPolicy` → absent |
| Delete | `DELETE /{bucket}?policy` |

Validation on the gateway side (`auth/policy.go`, **verify**): principals are
access key IDs (or `*`), resources must name this bucket, actions must be
known S3 actions. A rejected policy answers `MalformedPolicy` with a message
that names the problem — surface it verbatim.

## Semantics and edge cases

- **Principal is an access key ID**, not an ARN or canonical user ID. Document
  with the example above; referencing `versitygw_user.x.access_key` gives the
  right dependency order.
- **Owner change deletes the policy** (plan 00). Drift shows in the next plan;
  documented, not worked around.
- **Roles**: `userplus` and `admin` may set policies on buckets they own; the
  provider's account is admin/root, so this only matters for the note in the
  description explaining why a `user`-role consumer cannot self-manage.
- Deleting the bucket removes the policy with it; `Read` after that finds
  `NoSuchBucket`, which `IsNotFound` already covers.
- A policy referencing a principal that does not exist: **verify** whether
  the gateway rejects it (then fine) or accepts it (then document that
  `versitygw_user` must be created first, which the reference already
  guarantees).

## Tests

Acceptance (`TestAccBucketPolicyResource`):
1. user + bucket + policy → `policy` attribute matches (normalised).
2. Change a statement → in-place update.
3. Import by bucket name, `ImportStateVerify`.
4. Change bucket owner in the same config → next plan shows the policy as
   drifted and re-applies (`ExpectNonEmptyPlan` on a refresh-only step, then a
   step that applies cleanly).
5. Malformed policy → `ExpectError` with the gateway's message.

Unit: JSON normalisation (semantic equality across key order).

## Docs and examples

`docs/resources/bucket_policy.md`, `examples/resources/versitygw_bucket_policy/resource.tf`
with the cross-account read example. Add the resource to the README table and
link it from the owner-change warning in `docs/resources/bucket.md`.
