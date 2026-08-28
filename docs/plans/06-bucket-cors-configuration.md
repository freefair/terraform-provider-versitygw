# 06 — `versitygw_bucket_cors_configuration`

Counterpart of `aws_s3_bucket_cors_configuration`. Needed by anyone serving
browser uploads or downloads straight from the gateway.

## Schema

```hcl
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

`cors_rule` — list nested block, 1..100 (S3 limit; **verify** the gateway's).
`allowed_methods` and `allowed_origins` required; the rest optional. Order
matters to S3 (first matching rule wins), so a list, not a set.

## API mapping

| Op | Request |
|---|---|
| Create / Update | `PUT /{bucket}?cors`, body `CORSConfiguration/CORSRule[]` XML |
| Read | `GET /{bucket}?cors`; `NoSuchCORSConfiguration` → absent |
| Delete | `DELETE /{bucket}?cors` |

## Semantics and edge cases

- posix stores the raw XML (`PutBucketCors` → `StoreAttribute`,
  `backend/posix/posix.go`); the read returns what was written, so the
  round trip is exact. Still compare structurally, not by bytes.
- The gateway also has a global `--cors-allow-origin` flag. **Verify** how
  it interacts with per-bucket CORS (global wins? merged?) and document;
  the resource cannot see the flag.
- Method validation: S3 accepts `GET HEAD PUT POST DELETE`; use
  `stringvalidator.OneOf` in the schema so a typo fails at plan time.
- Owner change does not touch CORS (plan 00).

## Measured (v1.7.0, implementation)

- `DELETE ?cors` is a real route; the bucket survives and then answers
  `NoSuchCORSConfiguration`. Deleting an absent configuration answers 204.
- `PUT` validates: an unsupported method (`PATCH`) → `InvalidRequest` 400
  "Found unsupported HTTP method in CORS config"; a rule without
  `AllowedOrigin`, no rule at all, or unparsable XML → `MalformedXML` 400.
  The schema refuses the same at plan time.
- `GET` returns the rules in the order they were sent, with the elements
  inside a rule reordered (methods, headers, expose, origins, id, max-age).
  Structural comparison is the right one, as the plan said.
- The gateway applies the rules: an `OPTIONS` preflight from an allowed
  origin answers 200 with `Access-Control-Allow-Origin/-Methods/-Max-Age/
  -Expose-Headers` from the matching rule; a foreign origin answers 403.
  Asserted in `TestAccBucketCORSConfiguration`.
- No rule-count limit was probed.
- The interaction with the global `--cors-allow-origin` flag was **not**
  measured; the test gateway runs without it.

## Tests

1. two rules → read back both in order.
2. edit the first rule's origins → in-place.
3. remove a rule → in-place.
4. an `OPTIONS` preflight against the bucket from an allowed origin answers
   `Access-Control-Allow-Origin` (proves the gateway applies it, not just
   stores it).
5. import.

## Docs

`docs/resources/bucket_cors_configuration.md`, example, README table.
