# An account created outside Terraform. Reading it puts its secret key
# into state, as the versitygw_user resource does.
data "versitygw_user" "ci" {
  access_key = "build-pipeline"
}

resource "versitygw_bucket" "artifacts" {
  name  = "build-artifacts"
  owner = data.versitygw_user.ci.access_key
}
