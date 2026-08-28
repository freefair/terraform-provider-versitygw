resource "versitygw_bucket" "site" {
  name  = "site"
  owner = versitygw_user.web.access_key
}

# A fresh bucket is BucketOwnerEnforced and refuses ACLs; switch ownership
# first and let the ACL depend on it.
resource "versitygw_bucket_ownership_controls" "site" {
  bucket = versitygw_bucket.site.name
  rule {
    object_ownership = "ObjectWriter"
  }
}

# Public read is a canned ACL — the only way to grant to everyone on this
# gateway, which resolves every explicit grantee as an account.
resource "versitygw_bucket_acl" "site" {
  bucket     = versitygw_bucket.site.name
  acl        = "public-read"
  depends_on = [versitygw_bucket_ownership_controls.site]
}

# Explicit grants name accounts by access key ID. The owner's FULL_CONTROL
# is implicit and not listed.
resource "versitygw_bucket_acl" "reports" {
  bucket = versitygw_bucket.reports.name
  access_control_policy {
    grant {
      permission = "READ"
      grantee {
        type = "CanonicalUser"
        id   = versitygw_user.auditor.access_key
      }
    }
  }
  depends_on = [versitygw_bucket_ownership_controls.reports]
}
