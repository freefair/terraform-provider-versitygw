resource "versitygw_bucket" "uploads" {
  name  = "uploads"
  owner = versitygw_user.app.access_key
}

# A fresh bucket is BucketOwnerEnforced, which disables ACLs. Switch to
# ObjectWriter (or BucketOwnerPreferred) before putting an ACL on the bucket.
resource "versitygw_bucket_ownership_controls" "uploads" {
  bucket = versitygw_bucket.uploads.name
  rule {
    object_ownership = "ObjectWriter"
  }
}
