resource "versitygw_bucket" "site" {
  name  = "site"
  owner = versitygw_user.web.access_key
}

# Served only when the gateway runs a website listener
# (--website <addr> / VGW_WEBSITE_PORT; --website-domain for virtual hosts).
resource "versitygw_bucket_website_configuration" "site" {
  bucket = versitygw_bucket.site.name

  index_document {
    suffix = "index.html"
  }

  error_document {
    key = "404.html"
  }

  routing_rule {
    condition {
      key_prefix_equals = "docs/"
    }
    redirect {
      host_name               = "docs.example.com"
      protocol                = "https"
      http_redirect_code      = "301"
      replace_key_prefix_with = "documents/"
    }
  }
}

# Or send everything elsewhere. Exclusive with the blocks above.
resource "versitygw_bucket_website_configuration" "legacy" {
  bucket = versitygw_bucket.legacy.name

  redirect_all_requests_to {
    host_name = "www.example.com"
    protocol  = "https"
  }
}
