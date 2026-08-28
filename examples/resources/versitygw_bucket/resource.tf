resource "versitygw_user" "ci" {
  access_key = "build-pipeline"
  secret_key = var.ci_secret
}

# Reference the user rather than repeating its access key ID. That is what
# gives Terraform the dependency it needs to create the account first and to
# destroy the bucket before the account that owns it.
resource "versitygw_bucket" "artifacts" {
  name  = "build-artifacts"
  owner = versitygw_user.ci.access_key

  # Tags survive an owner change; only ACL and policy are reset.
  tags = {
    team = "platform"
    cost = "ci"
  }
}

# Handing a bucket to a different account is an in-place update — and it
# discards the bucket's ACL and policy, because the gateway applies a fresh
# default for the new owner instead of migrating the old one.
resource "versitygw_bucket" "shared" {
  name  = "shared-reports"
  owner = versitygw_user.ci.access_key
}
