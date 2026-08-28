# 07 — `versitygw_bucket_website_configuration`

Counterpart of `aws_s3_bucket_website_configuration`. Lowest priority of the
bucket sub-resources: serving the website needs the gateway started with a
website listener (`--website` / `VGW_WEBSITE_PORT`, plus `--website-domain`),
which most deployments do not run.

## Schema

```hcl
resource "versitygw_bucket_website_configuration" "site" {
  bucket = versitygw_bucket.site.name

  index_document {
    suffix = "index.html"
  }
  error_document {
    key = "404.html"
  }
}
```

Mirror the AWS blocks: `index_document`, `error_document`,
`redirect_all_requests_to { host_name, protocol }`, `routing_rule[]`.
`index_document` or `redirect_all_requests_to` required, mutually exclusive
(`ExactlyOneOf`).

**Verify** which of these the gateway's website handler actually honours
(`s3api/controllers/website*.go` or wherever the listener lives). posix
stores the raw XML (`PutBucketWebsite`), so a `GET` round trip will succeed
for every field regardless — but a field the handler ignores should not be
in the schema pretending to work. Start with `index_document` and
`error_document`; add redirects and routing rules only once measured.

## API mapping

| Op | Request |
|---|---|
| Create / Update | `PUT /{bucket}?website`, body `WebsiteConfiguration` XML |
| Read | `GET /{bucket}?website`; `NoSuchWebsiteConfiguration` → absent |
| Delete | `DELETE /{bucket}?website` |

## Semantics and edge cases

- Configuring a website on a gateway without a website listener succeeds
  and does nothing. The resource cannot tell; the description says so and
  points at the flag.
- Public access to the site is an ACL/policy matter (plan 01/02), not this
  resource's — same as AWS.
- Owner change does not touch it (plan 00).

## Tests

1. index + error document → read back.
2. change the error document → in-place.
3. import.
4. Optional, only if the CI gateway gets a website listener: `GET /` on the
   website port with `Host: <bucket>.<domain>` returns the index object.

## Docs

`docs/resources/bucket_website_configuration.md` with the listener
requirement up front; example; README table.
