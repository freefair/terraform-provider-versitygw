# 02 — `versitygw_bucket_acl`

Counterpart of `aws_s3_bucket_acl`. Lower priority than policy: ACLs are the
coarser tool, and a gateway can be started with `--disable-acl`
(`VGW_DISABLE_ACL`), which "ignores all ACL headers" — on such a gateway this
resource cannot work and must say so.

## Depends on plan 08 — measured

A fresh bucket is `BucketOwnerEnforced` and answers every `PUT ?acl` with
`AccessControlListNotSupported`. The resource must document
`versitygw_bucket_ownership_controls` (`ObjectWriter` / `BucketOwnerPreferred`)
plus `depends_on` as a prerequisite, and map that error code to a diagnostic
naming it.

Further measurements (v1.7.0):
- Canned ACLs accepted: `private`, `public-read`, `public-read-write`;
  `authenticated-read` → `InvalidArgument`.
- `GET ?acl` always lists the owner's `FULL_CONTROL` grant first; canned
  `public-read` adds a `Group` grantee with `<ID>all-users</ID>` (READ),
  `public-read-write` adds READ and WRITE. **The AWS `AllUsers` URI is
  rejected** (`MalformedACLError`); on input the group is `xsi:type="Group"`
  with `<ID>all-users</ID>`.
- `Owner.ID` must be the bucket's owner: another account → `InvalidArgument:
  Invalid id`. So `owner` is computed from the bucket, not user input.
- A grant for a non-existent account → HTTP 500 `InternalError` (upstream
  bug; map to a diagnostic suggesting the account may not exist).
- An empty grant list resets to owner-only FULL_CONTROL; the owner grant is
  always present on read even if not sent.

## Schema

Mirror the AWS resource: either a canned ACL or an explicit policy, mutually
exclusive.

```hcl
resource "versitygw_bucket_acl" "public" {
  bucket = versitygw_bucket.site.name
  acl    = "public-read"
}

resource "versitygw_bucket_acl" "shared" {
  bucket = versitygw_bucket.reports.name
  access_control_policy {
    owner {
      id = versitygw_user.ci.access_key
    }
    grant {
      permission = "READ"
      grantee {
        type = "CanonicalUser"
        id   = versitygw_user.reader.access_key
      }
    }
    grant {
      permission = "READ"
      grantee {
        type = "Group"
        uri  = "http://acs.amazonaws.com/groups/global/AllUsers"
      }
    }
  }
}
```

| Attribute | Notes |
|---|---|
| `bucket` | required, replaces |
| `acl` | optional; `private`, `public-read`, `public-read-write` — the three the gateway validates (`auth.ValidateCannedACL`, `auth/acl.go`) |
| `access_control_policy` | optional block; `owner.id` required; `grant[]` with `permission` (`FULL_CONTROL`, `READ`, `WRITE`, `READ_ACP`, `WRITE_ACP`) and `grantee { type, id | uri }` |

Exactly one of `acl` / `access_control_policy` — `objectvalidator.ExactlyOneOf`.

**Grantee IDs are access key IDs.** versitygw has no canonical user IDs; the
`ID` element of a `CanonicalUser` grantee is expected to be the account's
access key (`auth/acl.go` `Grt.ID` → `Grantee.Access`; **verify** with a
round trip before building the schema on it). Group grantee: the gateway represents "all users" as
`types.TypeGroup` with `Access == "all-users"` — **verify** which URI it
accepts on input (AWS `AllUsers` URI vs. the literal `all-users`) and mirror
that in the validator.

## API mapping

| Op | Request |
|---|---|
| Create / Update | `PUT /{bucket}?acl` with header `x-amz-acl: <canned>` **or** XML body `AccessControlPolicy` |
| Read | `GET /{bucket}?acl` → `AccessControlPolicy` XML (owner + grants) |
| Delete | none — the S3 API has no delete for ACLs. Delete removes the resource from state only (AWS does the same). Description says so. |

## Semantics and edge cases

- **Owner change rewrites the ACL** (plan 00, `auth.UpdateBucketACLOwner`):
  fresh default ACL for the new owner. Drift, documented.
- **Read of a canned ACL**: the gateway stores grants, not the canned name.
  Handle like AWS: when config used `acl`, keep `acl` in state and compare
  the returned grants against what that canned value implies; when they
  differ, plan an update. Simpler alternative — make `access_control_policy`
  Computed and always fill it from the read, leaving `acl` as write-only
  intent. Pick the simpler one unless a test proves it produces perpetual
  diffs.
- `--disable-acl`: **verify** the exact answer (`NotImplemented`? silent
  no-op?). If silent, the resource cannot detect it and the description must
  warn; if an error, map it to a clear diagnostic via `IsNotImplemented`.
- `owner.id` must equal the bucket's actual owner or the gateway rejects the
  PUT (**verify**). If so, make `owner` Computed and fill it from the bucket
  rather than asking the user for a value that has exactly one legal answer.

## Tests

1. canned `public-read` → read back shows the AllUsers READ grant.
2. explicit policy with a `CanonicalUser` grant for a second user → the
   second user can `ListObjects` (prove with an S3 call in the check
   function, not only by reading the ACL back).
3. switch canned → explicit → in-place.
4. import.
5. gateway with `VGW_DISABLE_ACL=true` (separate container in a dedicated
   test, skipped unless `VERSITYGW_ACL_DISABLED_ENDPOINT` is set) → clear
   error.

## Docs

`docs/resources/bucket_acl.md`, example, README table; cross-link from the
bucket resource's owner-change warning.
