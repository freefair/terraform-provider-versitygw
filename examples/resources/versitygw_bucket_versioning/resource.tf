resource "versitygw_user" "archive" {
  access_key = "archive"
  secret_key = var.archive_secret
}

resource "versitygw_bucket" "archive" {
  name  = "archive"
  owner = versitygw_user.archive.access_key
}

# Versioning cannot be turned off once on — only suspended. Destroying this
# resource forgets it; the bucket keeps its state.
resource "versitygw_bucket_versioning" "archive" {
  bucket = versitygw_bucket.archive.name
  versioning_configuration {
    status = "Enabled"
  }
}
