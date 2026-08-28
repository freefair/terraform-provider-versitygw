# Roadmap: feature completeness against versitygw

The provider covers the gateway's **admin API completely** — every one of its
seven routes (`s3api/admin-router.go`) is behind `versitygw_user`,
`versitygw_bucket` or one of the two list data sources. What is missing is
everything versitygw offers **per bucket over the S3 API**, which the AWS
provider models as separate `aws_s3_bucket_*` resources. This provider should
feel the same: one resource per bucket sub-configuration, `bucket` as the
required key, import by bucket name.

Measured against `versity/versitygw` main at release v1.7.0 (2026-07-15).
Re-check the "Backend interface" column (`backend/backend.go`) before
implementing a plan — a feature that the router accepts but the backend
interface does not carry is a `NotImplemented` answer with extra steps.

## Plans, in implementation order

| # | Plan | Resource | AWS counterpart | Depends on |
|---|---|---|---|---|
| 00 | [Shared S3 sub-resource client](00-shared-s3-subresource-client.md) — **done** (`internal/client/s3.go`) | — | — | — |
| 01 | [Bucket policy](01-bucket-policy.md) — **done** | `versitygw_bucket_policy` | `aws_s3_bucket_policy` | 00 |
| 03 | [Bucket versioning](03-bucket-versioning.md) — **done** | `versitygw_bucket_versioning` | `aws_s3_bucket_versioning` | 00 |
| 04 | [Object lock configuration](04-bucket-object-lock-configuration.md) — **done** | `versitygw_bucket_object_lock_configuration` | `aws_s3_bucket_object_lock_configuration` | 00, 03 |
| 02 | [Bucket ACL](02-bucket-acl.md) | `versitygw_bucket_acl` | `aws_s3_bucket_acl` | 00, 08 |
| 05 | [Bucket tagging](05-bucket-tagging.md) | `tags` on `versitygw_bucket` | `tags` on `aws_s3_bucket` | 00 |
| 08 | [Ownership controls](08-bucket-ownership-controls.md) — **done** | `versitygw_bucket_ownership_controls` | `aws_s3_bucket_ownership_controls` | 00 |
| 06 | [CORS configuration](06-bucket-cors-configuration.md) | `versitygw_bucket_cors_configuration` | `aws_s3_bucket_cors_configuration` | 00 |
| 07 | [Website configuration](07-bucket-website-configuration.md) | `versitygw_bucket_website_configuration` | `aws_s3_bucket_website_configuration` | 00 |
| 09 | [Single-object data sources](09-data-sources-single.md) | `data.versitygw_user`, `data.versitygw_bucket` | `data.aws_iam_user`, `data.aws_s3_bucket` | — |

Policy first: it is the only feature that grants access across account
boundaries, which is what most people reach for right after creating a user
and a bucket. Versioning and object lock next because they are a pair.
Ownership controls before ACL, because a fresh bucket is
`BucketOwnerEnforced` and refuses every ACL until that changes (measured).
The rest is ordered by how often it is needed.

## Deliberately not planned

| Feature | Why not |
|---|---|
| Lifecycle, encryption, replication, logging, notification, public access block, analytics / inventory / metrics / intelligent tiering, request payment, accelerate | `s3api/router.go` routes every one of these to `s3err.ErrNotImplemented`; none is in the `backend.Backend` interface. A resource for them would fail on every apply. Re-check the router when bumping the pinned gateway version. |
| Objects (`aws_s3_object`) | The data path, not administration. It is feasible — the client already signs S3 requests — but it belongs to a different kind of provider use, and Terraform is a poor fit for objects that change outside it. Optional, later, only on demand. |
| Root account | Lives on the gateway's command line, never in the IAM service, appears in no listing. See CLAUDE.md. |
| Object ACLs, retention, legal hold | Per object; same reasoning as objects. |

## Common shape of every plan

Purpose and AWS counterpart · schema with an HCL example · API mapping
(method, sub-resource, body) · CRUD semantics · edge cases and upstream facts
(with file references into versitygw; unmeasured claims are marked **verify**)
· acceptance test steps · documentation and examples · dependencies.
