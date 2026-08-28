---
page_title: "versitygw_bucket_website_configuration Resource - versitygw"
description: |-
  Static website configuration of a bucket; served only with a website listener.
---

# versitygw_bucket_website_configuration (Resource)

Static website configuration of a bucket, shaped like
`aws_s3_bucket_website_configuration`.

~> The gateway stores and validates the configuration on any deployment, but
only **serves** the site when started with a website listener
(`--website-port` / `VGW_WEBSITE_PORT` and `--website-domain`). This resource
cannot tell whether that listener exists; a configuration on a gateway without
one succeeds and does nothing. Public access to the objects is an ACL or
policy matter, as on AWS.

## Example Usage

```terraform
resource "versitygw_bucket" "site" {
  name  = "site"
  owner = versitygw_user.web.access_key
}

resource "versitygw_bucket_website_configuration" "site" {
  bucket = versitygw_bucket.site.name

  index_document {
    suffix = "index.html"
  }

  error_document {
    key = "404.html"
  }

  routing_rule {
    condition {
      key_prefix_equals = "docs/"
    }
    redirect {
      host_name               = "docs.example.com"
      protocol                = "https"
      http_redirect_code      = "301"
      replace_key_prefix_with = "documents/"
    }
  }
}
```

## Schema

### Required

- `bucket` (String) Name of the bucket. Immutable — changing it forces
  replacement.
- Exactly one of:
  - `index_document` (Block) with `suffix` (String): appended to a key ending
    in `/`, e.g. `index.html`. Must not contain `/`.
  - `redirect_all_requests_to` (Block) with `host_name` (String) and optional
    `protocol` (`http` or `https`): send every request elsewhere. Cannot be
    combined with `error_document` or `routing_rule`.

### Optional

- `error_document` (Block) with `key` (String): object served on a 4xx.
- `routing_rule` (Block List, at most 50) Conditional redirects, in order.
  - `condition` (Block) at least one of `http_error_code_returned_equals`
    (String, 400–417 or 500–505) and `key_prefix_equals` (String).
  - `redirect` (Block, required) at least one of `host_name`,
    `http_redirect_code` (`301`, `302`, `303`, `304`, `305`, `307`, `308`),
    `protocol` (`http` or `https`), `replace_key_prefix_with`,
    `replace_key_with`. The two `replace_*` are mutually exclusive.

Every rule above is what the gateway enforces on `PUT ?website`
(`s3response/website.go`); the resource checks them at plan time so a
mistake fails before apply.

## How the gateway evaluates it

`redirect_all_requests_to` wins over everything. Otherwise a routing rule
with only `key_prefix_equals` is applied before the object is fetched, one
with `http_error_code_returned_equals` after a matching error; then
`error_document` is served for a 4xx. `index_document` is appended to keys
ending in `/`.

## An owner change keeps the configuration

`change-bucket-owner` resets only the ACL and the policy.

## Destroy deletes the configuration

`DELETE ?website` is a real route; the bucket stays, and afterwards reports
`NoSuchWebsiteConfiguration`.

## Import

```shell
terraform import versitygw_bucket_website_configuration.site site
```
