resource "versitygw_bucket" "uploads" {
  name  = "uploads"
  owner = versitygw_user.app.access_key
}

# Rules are ordered: a preflight is answered by the first rule that matches.
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
