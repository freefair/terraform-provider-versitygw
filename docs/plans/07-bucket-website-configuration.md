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

## Measured (v1.7.0, implementation)

- The website handler (`website/handler.go`) honours every AWS field:
  `RedirectAllRequestsTo` first, then pre-fetch routing rules
  (`KeyPrefixEquals` only), the object, post-error routing rules
  (`HttpErrorCodeReturnedEquals`), `ErrorDocument`, and `IndexDocument`
  for keys ending in `/`. All of them are in the schema.
- `PUT ?website` validates (`s3response/website.go`): `RedirectAllRequestsTo`
  excludes the other blocks and needs a host; otherwise `IndexDocument` is
  required, its suffix non-empty and without `/`; `ErrorDocument.Key`
  non-empty; at most 50 routing rules; a rule's `Redirect` non-empty with
  at most one of the two key replacements; a `Condition`, if present,
  non-empty with an error code in 400–417 or 500–505; redirect codes in
  {301, 302, 303, 304, 305, 307, 308}; protocol `http`/`https`. Violations
  answer `MalformedXML` or `InvalidArgument`. `ValidateConfig` mirrors all
  of it.
- Storing works without a website listener; the flag only affects serving.
  The listener flag is `--website <addr>` (`VGW_WEBSITE_PORT`), not
  `--website-port` as the plan text above says; `--website-domain` is
  optional and selects virtual-host routing (`cmd/versitygw/main.go`).
- `GET ?website` always carries an empty `<RoutingRules></RoutingRules>`;
  the client reads that as no rules. `omitempty` does not suppress the
  parent of a `RoutingRules>RoutingRule` path, so the client wraps the
  list in a pointer.
- `DELETE ?website` is real and idempotent; afterwards `GET` answers
  `NoSuchWebsiteConfiguration`.
- Serving through the website listener (test 4) was not exercised; the CI
  gateway runs without one.

## Tests

1. index + error document → read back.
2. change the error document → in-place.
3. import.
4. Optional, only if the CI gateway gets a website listener: `GET /` on the
   website port with `Host: <bucket>.<domain>` returns the index object.

## Docs

`docs/resources/bucket_website_configuration.md` with the listener
requirement up front; example; README table.
