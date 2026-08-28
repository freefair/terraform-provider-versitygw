terraform {
  required_providers {
    versitygw = {
      source  = "freefair/versitygw"
      version = "~> 0.1"
    }
  }
}

# Nothing in the block: endpoint and credentials come from VERSITYGW_ENDPOINT,
# VERSITYGW_ACCESS_KEY and VERSITYGW_SECRET_KEY. That keeps them out of version
# control without anyone having to remember to keep them out.
provider "versitygw" {}

# Or explicitly, when the values are not secret and the endpoint differs per
# workspace. A gateway started WITH --admin-port serves its admin routes
# nowhere else, so admin_endpoint has to name it.
provider "versitygw" {
  alias = "staging"

  endpoint       = "https://s3.staging.example.com"
  admin_endpoint = "https://s3-admin.staging.example.com"
  region         = "us-east-1"
}
