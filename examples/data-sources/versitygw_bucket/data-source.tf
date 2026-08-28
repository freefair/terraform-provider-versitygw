# A bucket created outside Terraform; a missing name fails the plan.
data "versitygw_bucket" "artifacts" {
  name = "build-artifacts"
}

output "artifacts_owner" {
  value = data.versitygw_bucket.artifacts.owner
}
