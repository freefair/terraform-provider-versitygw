# One account per consumer. A `user` cannot create buckets and reaches only
# what it owns, which is what turns "an account per pipeline" into a real
# boundary rather than bookkeeping.
resource "random_password" "ci" {
  length = 40
  # Alphanumeric on purpose. Special characters survive SigV4 fine, but not
  # every consumer of the key: a curl configuration reads a backslash as an
  # escape, a shell profile reads a dollar sign as an expansion.
  special = false
}

resource "versitygw_user" "ci" {
  access_key = "build-pipeline"
  secret_key = random_password.ci.result
  role       = "user"
}

# Optional, and only meaningful on the posix and scoutfs backends: objects this
# account writes are owned by this UID/GID on the filesystem underneath.
resource "versitygw_user" "archival" {
  access_key = "archival"
  secret_key = random_password.ci.result
  role       = "userplus"
  user_id    = 1500
  group_id   = 1500
}
