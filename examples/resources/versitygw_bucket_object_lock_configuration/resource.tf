resource "versitygw_bucket_versioning" "archive" {
  bucket = versitygw_bucket.archive.name
  versioning_configuration {
    status = "Enabled"
  }
}

# The gateway accepts a lock configuration only on a bucket whose versioning
# is Enabled, hence the depends_on. Once present, versioning can no longer be
# suspended and the lock cannot be removed.
resource "versitygw_bucket_object_lock_configuration" "archive" {
  bucket = versitygw_bucket.archive.name
  rule {
    default_retention {
      mode = "COMPLIANCE"
      days = 30
    }
  }
  depends_on = [versitygw_bucket_versioning.archive]
}
