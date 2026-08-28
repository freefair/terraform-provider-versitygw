---
page_title: "versitygw_bucket_cors_configuration Resource - versitygw"
description: |-
  CORS rules of a bucket, for browsers talking to the gateway directly.
---

# versitygw_bucket_cors_configuration (Resource)

CORS configuration of a bucket, shaped like `aws_s3_bucket_cors_configuration`.
Needed whenever a browser uploads to or downloads from the gateway directly.

## Example Usage

```terraform
resource "versitygw_bucket" "uploads" {
  name  = "uploads"
  owner = versitygw_user.app.access_key
}

resource "versitygw_bucket_cors_configuration" "uploads" {
  bucket = versitygw_bucket.uploads.name

  cors_rule {
    id              = "browser-uploads"
    allowed_headers = ["*"]
    allowed_methods = ["PUT", "POST"]
    allowed_origins = ["https://app.example.com"]
    expose_headers  = ["ETag"]
    max_age_seconds = 3000
  }

  cors_rule {
    allowed_methods = ["GET"]
    allowed_origins = ["*"]
  }
}
```

## Schema

### Required

- `bucket` (String) Name of the bucket. Immutable — changing it forces
  replacement.
- `cors_rule` (Block List, at least one) One rule per block, in order.
  - `allowed_methods` (List of String, required) `GET`, `HEAD`, `PUT`, `POST`
    or `DELETE`. Anything else is refused at plan time; the gateway would
    refuse it too (`InvalidRequest`).
  - `allowed_origins` (List of String, required) `*` allows any origin.
  - `allowed_headers` (List of String) Headers a preflight may request.
  - `expose_headers` (List of String) Response headers the browser may read.
  - `id` (String) Identifier of the rule, for humans.
  - `max_age_seconds` (Number) How long the browser may cache the preflight.

Optional lists must not be empty when set — the gateway stores nothing for
an empty list, and the resource would diff on every plan. Omit them instead.

## Order matters

The gateway answers a preflight (`OPTIONS`) from the first rule whose origin
and method match, with `Access-Control-Allow-Origin`, `-Methods`, `-Max-Age`
and `-Expose-Headers` taken from that rule; a request no rule matches gets a
`403`. The block list is kept in configuration order for that reason.

## The gateway's `--cors-allow-origin` flag is a fallback

A gateway started with that flag answers preflights for buckets that have
no CORS configuration of their own. Once this resource exists, its rules
decide and the flag is not consulted for the bucket.

## An owner change keeps the configuration

`change-bucket-owner` resets only the ACL and the policy; the CORS rules
stay, and this resource shows no diff after the bucket changes hands.

## Destroy deletes the configuration

`DELETE ?cors` is a real route; the bucket stays, and afterwards reports
`NoSuchCORSConfiguration`.

## Import

```shell
terraform import versitygw_bucket_cors_configuration.uploads uploads
```
