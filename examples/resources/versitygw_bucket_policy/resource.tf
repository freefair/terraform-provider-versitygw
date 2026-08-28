resource "versitygw_user" "ci" {
  access_key = "build-pipeline"
  secret_key = var.ci_secret
}

resource "versitygw_user" "reader" {
  access_key = "release-page"
  secret_key = var.reader_secret
}

resource "versitygw_bucket" "artifacts" {
  name  = "build-artifacts"
  owner = versitygw_user.ci.access_key
}

# A policy is the one way to let an account reach a bucket it does not own.
# Principals are access key IDs — the gateway has no canonical user IDs — so
# reference the user resource and the account exists before the policy names it.
resource "versitygw_bucket_policy" "artifacts_read" {
  bucket = versitygw_bucket.artifacts.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = [versitygw_user.reader.access_key] }
      Action    = ["s3:GetObject", "s3:ListBucket"]
      Resource = [
        "arn:aws:s3:::${versitygw_bucket.artifacts.name}",
        "arn:aws:s3:::${versitygw_bucket.artifacts.name}/*",
      ]
    }]
  })
}
