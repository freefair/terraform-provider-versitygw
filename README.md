# Terraform Provider: VersityGW

[![Tests](https://github.com/freefair/terraform-provider-versitygw/actions/workflows/test.yml/badge.svg)](https://github.com/freefair/terraform-provider-versitygw/actions/workflows/test.yml)
[![Compatibility](https://github.com/freefair/terraform-provider-versitygw/actions/workflows/compat.yml/badge.svg)](https://github.com/freefair/terraform-provider-versitygw/actions/workflows/compat.yml)
[![Registry](https://img.shields.io/badge/terraform-registry-blueviolet)](https://registry.terraform.io/providers/freefair/versitygw/latest)

Manages accounts, buckets and bucket policies on a [Versity S3 Gateway](https://github.com/versity/versitygw),
an Apache-2.0 S3 server that puts an S3 API over a POSIX filesystem, ScoutFS,
Azure Blob Storage or another S3 server.

VersityGW keeps account management behind an admin API of its own, so the AWS
provider cannot reach it — `aws_iam_user` has nothing to talk to. This provider
speaks that API directly, which makes accounts and bucket ownership real
Terraform resources with drift detection, rather than a `local-exec` calling the
`versitygw admin` CLI.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0, or
  [OpenTofu](https://opentofu.org/docs/intro/install/) >= 1.6
- [Go](https://go.dev/dl/) >= 1.26 (to build the provider)
- A VersityGW instance and the credentials of an `admin` or `root` account

## Installation

```hcl
terraform {
  required_providers {
    versitygw = {
      source  = "freefair/versitygw"
      version = "~> 0.1"
    }
  }
}
```

## Usage

```hcl
# Endpoint and credentials come from VERSITYGW_ENDPOINT, VERSITYGW_ACCESS_KEY
# and VERSITYGW_SECRET_KEY, so nothing secret has to live in the configuration.
provider "versitygw" {}

resource "random_password" "ci" {
  length  = 40
  special = false
}

resource "versitygw_user" "ci" {
  access_key = "build-pipeline"
  secret_key = random_password.ci.result
  role       = "user"
}

resource "versitygw_bucket" "artifacts" {
  name  = "build-artifacts"
  owner = versitygw_user.ci.access_key
}
```

## Resources

| Resource | Description |
|---|---|
| [`versitygw_user`](docs/resources/user.md) | An account in the gateway's IAM service |
| [`versitygw_bucket`](docs/resources/bucket.md) | A bucket and the account that owns it |
| [`versitygw_bucket_policy`](docs/resources/bucket_policy.md) | The policy of a bucket |
| [`versitygw_bucket_versioning`](docs/resources/bucket_versioning.md) | Versioning state of a bucket |
| [`versitygw_bucket_object_lock_configuration`](docs/resources/bucket_object_lock_configuration.md) | Object lock and default retention of a bucket |
| [`versitygw_bucket_ownership_controls`](docs/resources/bucket_ownership_controls.md) | Object ownership of a bucket; decides whether ACLs are allowed |
| [`versitygw_bucket_acl`](docs/resources/bucket_acl.md) | Canned ACL or explicit grants to accounts |
| [`versitygw_bucket_cors_configuration`](docs/resources/bucket_cors_configuration.md) | CORS rules for browsers talking to the gateway |
| [`versitygw_bucket_website_configuration`](docs/resources/bucket_website_configuration.md) | Static website configuration; served only with a website listener |

| Data source | Description |
|---|---|
| [`versitygw_users`](docs/data-sources/users.md) | Every account on the gateway |
| [`versitygw_buckets`](docs/data-sources/buckets.md) | Every bucket with its owner |

Full argument reference on the
[Terraform Registry](https://registry.terraform.io/providers/freefair/versitygw/latest/docs).

### Roadmap

The admin API is covered completely, and so is everything the gateway offers
per bucket over the S3 API — each as a resource shaped like the corresponding
`aws_s3_bucket_*` one. See [`docs/plans/`](docs/plans/README.md) for the
plans behind them and for what is deliberately left out.

## Key behaviours

Four things about VersityGW that shape how this provider works, and that are
easier to know up front than to discover from an error message.

**The admin API may or may not be on the S3 endpoint.** A gateway started
without `--admin-port` mounts the admin routes on the S3 listener; a gateway
started with one serves them nowhere else. So `admin_endpoint` falls back to
`endpoint`, and has to be set explicitly in the second case — otherwise every
account operation answers `404`.

**An account is its access key ID.** There is no rename and no separate key
rotation, so changing `access_key` replaces the account. `secret_key` on its own
is an in-place update.

**Changing a bucket's owner discards its ACL and policy.** The gateway applies a
fresh default for the new owner rather than migrating the old one. Tags are
not affected, and a tag-only update does not touch ownership.

**`list-users` returns secret keys.** That is what lets this provider detect a
key changed outside Terraform — and it is also why the credentials it uses are
the most privileged ones on the gateway, and why the admin endpoint belongs on a
network that only administrators reach.

## Developing the Provider

### Building

```bash
make build
```

### Testing

```bash
make test     # unit tests; no gateway needed
make testacc  # acceptance tests against a real gateway
```

Acceptance tests run against a real instance on purpose — a mock would only
prove the provider agrees with itself, and the wire format belongs to upstream:

```bash
# --iam-dir is what turns on the IAM service; without it the gateway runs in
# single-account mode and every account route answers 501. /tmp/vgw is the
# directory the image creates. The versioning directory has to exist too,
# and a named volume is the simplest way to make it so (a bind mount from
# macOS lacks the xattr support the backend needs).
docker run --rm -d -p 7070:7070 --name versitygw-acc \
  -v vgw-versions:/tmp/vgw-versions \
  -e ROOT_ACCESS_KEY_ID=testaccess -e ROOT_SECRET_ACCESS_KEY=testsecret \
  versity/versitygw:v1.7.0 --iam-dir /tmp/vgw posix /tmp/vgw --versioning-dir /tmp/vgw-versions

export TF_ACC=1
export VERSITYGW_ENDPOINT=http://127.0.0.1:7070
export VERSITYGW_ACCESS_KEY=testaccess
export VERSITYGW_SECRET_KEY=testsecret
make testacc
```

### Continuous Integration

Pull requests run the acceptance tests against a **pinned** gateway version
(`versity/versitygw:v1.7.0` in `.github/workflows/test.yml`) — the release the
upstream facts in `CLAUDE.md` were measured against — so a PR cannot go red
because upstream moved. Drift is caught by
[`compat.yml`](.github/workflows/compat.yml), which runs every Monday against
`versity/versitygw:latest` with the newest Terraform and the newest OpenTofu
and writes the versions in play to the job summary. When it goes red, bump the
pin deliberately and re-check the facts. Dependabot keeps Go modules and
Actions current (`.github/dependabot.yml`).

### Additional Targets

```bash
make vet
make fmt
```

### Using a Local Build

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "freefair/versitygw" = "/Users/you/go/bin"
  }
  direct {}
}
```

```bash
make install
```

With `dev_overrides` in place, `terraform init` fails trying to resolve the
overridden provider from the registry. Skip it and run `validate` / `plan` /
`apply` directly — those resolve the local binary correctly.

### Releasing

Tag-driven through GoReleaser. Requires the `GPG_PRIVATE_KEY` and `PASSPHRASE`
secrets on the repository.

```bash
git tag v0.1.0
git push origin v0.1.0
```

Targets linux, darwin and windows on amd64/arm64, plus freebsd/amd64.

## License

[MIT](LICENSE)
