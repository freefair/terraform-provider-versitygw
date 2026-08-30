package provider_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// These drive the provider through Terraform like the real acceptance tests,
// but against fakeGateway, because the branches they cover are the ones a
// healthy gateway never takes. newFakeGateway sets TF_ACC, so they run in a
// plain `go test ./...` — they need a terraform binary, not a gateway.

const faultUser = `
resource "versitygw_user" "test" {
  access_key = "fault-user"
  secret_key = "faultsecret"
  role       = "user"
}
`

const faultUserChanged = `
resource "versitygw_user" "test" {
  access_key = "fault-user"
  secret_key = "faultsecret"
  role       = "userplus"
}
`

const faultBucket = faultUser + `
resource "versitygw_bucket" "test" {
  name  = "fault-bucket"
  owner = versitygw_user.test.access_key
}
`

const faultBucketOtherOwner = faultUser + `
resource "versitygw_user" "other" {
  access_key = "fault-other"
  secret_key = "faultsecret"
}

resource "versitygw_bucket" "test" {
  name  = "fault-bucket"
  owner = versitygw_user.other.access_key
}
`

const faultPolicy = faultBucket + `
resource "versitygw_bucket_policy" "test" {
  bucket = versitygw_bucket.test.name
  policy = jsonencode({ Version = "2012-10-17", Statement = [] })
}
`

const faultPolicyChanged = faultBucket + `
resource "versitygw_bucket_policy" "test" {
  bucket = versitygw_bucket.test.name
  policy = jsonencode({ Version = "2012-10-17", Statement = [], Id = "v2" })
}
`

func faultCase(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	// No PreCheck: the fake sets the variables itself, and the provider
	// configuration cases below blank them on purpose.
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

func expectError(config, pattern string) resource.TestStep {
	return resource.TestStep{Config: config, ExpectError: regexp.MustCompile(pattern)}
}

// A step that repairs the gateway and tears everything down, so the
// framework's final destroy does not trip over an injected fault.
func recover(g *fakeGateway) resource.TestStep {
	return resource.TestStep{PreConfig: g.clearFaults, Config: `# nothing`}
}

func TestFaultUserCreate(t *testing.T) {
	g := newFakeGateway(t)
	g.fail("PATCH /create-user", 409, "XAdminUserExists")
	faultCase(t, expectError(faultUser, `Account already exists`), recover(g))

	g = newFakeGateway(t)
	g.fail("PATCH /create-user", 500, "InternalError")
	faultCase(t, expectError(faultUser, `Cannot create the account`), recover(g))
}

func TestFaultUserReadUpdateDelete(t *testing.T) {
	g := newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultUser},
		resource.TestStep{PreConfig: func() { g.fail("PATCH /list-users", 500, "InternalError") },
			Config: faultUser, ExpectError: regexp.MustCompile(`Cannot read the account`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("PATCH /update-user", 500, "InternalError") },
			Config: faultUserChanged, ExpectError: regexp.MustCompile(`Cannot update the account`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("PATCH /delete-user", 500, "InternalError") },
			Config: `# nothing`, ExpectError: regexp.MustCompile(`Cannot delete the account`)},
		recover(g),
	)
}

func TestFaultUserVanished(t *testing.T) {
	g := newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultUser},
		// Deleted behind Terraform's back: the refresh drops it from state
		// and the plan wants it back.
		resource.TestStep{PreConfig: func() { g.forgetUser("fault-user") },
			Config: faultUser, PlanOnly: true, ExpectNonEmptyPlan: true},
		// The listing itself answering not-found is the other way to vanish.
		resource.TestStep{PreConfig: func() { g.fail("PATCH /list-users", 404, "XAdminUserNotFound") },
			Config: faultUser, PlanOnly: true, ExpectNonEmptyPlan: true},
		// A delete that finds nothing has nothing left to do.
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.forgetUser("fault-user") }, Config: `# nothing`},
	)
}

func TestFaultBucketCreate(t *testing.T) {
	g := newFakeGateway(t)
	g.fail("PATCH /fault-bucket/create", 409, "BucketAlreadyExists")
	faultCase(t, expectError(faultBucket, `Bucket already exists`), recover(g))

	g = newFakeGateway(t)
	g.fail("PATCH /fault-bucket/create", 500, "InternalError")
	faultCase(t, expectError(faultBucket, `Cannot create the bucket`), recover(g))
}

func TestFaultBucketReadUpdateDelete(t *testing.T) {
	g := newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultBucket},
		resource.TestStep{PreConfig: func() { g.fail("PATCH /list-buckets", 500, "InternalError") },
			Config: faultBucket, ExpectError: regexp.MustCompile(`Cannot read the bucket`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("PATCH /change-bucket-owner", 500, "InternalError") },
			Config: faultBucketOtherOwner, ExpectError: regexp.MustCompile(`Cannot change the bucket owner`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("DELETE /fault-bucket", 500, "InternalError") },
			Config: faultUser, ExpectError: regexp.MustCompile(`Cannot delete the bucket`)},
		recover(g),
	)
}

func TestFaultBucketVanished(t *testing.T) {
	g := newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultBucket},
		resource.TestStep{PreConfig: func() { g.forgetBucket("fault-bucket") },
			Config: faultBucket, PlanOnly: true, ExpectNonEmptyPlan: true},
		resource.TestStep{PreConfig: func() { g.fail("PATCH /list-buckets", 404, "NoSuchBucket") },
			Config: faultBucket, PlanOnly: true, ExpectNonEmptyPlan: true},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.forgetBucket("fault-bucket") }, Config: faultUser},
		recover(g),
	)
}

func TestFaultPolicy(t *testing.T) {
	g := newFakeGateway(t)
	g.fail("PUT /fault-bucket?policy", 404, "NoSuchBucket")
	faultCase(t, expectError(faultPolicy, `Bucket does not exist`), recover(g))

	g = newFakeGateway(t)
	g.fail("PUT /fault-bucket?policy", 500, "InternalError")
	faultCase(t, expectError(faultPolicy, `Cannot set the bucket policy`), recover(g))

	g = newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultPolicy},
		resource.TestStep{PreConfig: func() { g.fail("GET /fault-bucket?policy", 500, "InternalError") },
			Config: faultPolicy, ExpectError: regexp.MustCompile(`Cannot read the bucket policy`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("PUT /fault-bucket?policy", 500, "InternalError") },
			Config: faultPolicyChanged, ExpectError: regexp.MustCompile(`Cannot set the bucket policy`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("DELETE /fault-bucket?policy", 500, "InternalError") },
			Config: faultBucket, ExpectError: regexp.MustCompile(`Cannot delete the bucket policy`)},
		recover(g),
	)
}

func TestFaultDataSources(t *testing.T) {
	g := newFakeGateway(t)
	g.fail("PATCH /list-users", 500, "InternalError")
	faultCase(t, expectError(`data "versitygw_users" "all" {}`, `Cannot list the accounts`))

	g = newFakeGateway(t)
	g.fail("PATCH /list-buckets", 500, "InternalError")
	faultCase(t, expectError(`data "versitygw_buckets" "all" {}`, `Cannot list the buckets`))
}

func TestFaultProviderConfiguration(t *testing.T) {
	newFakeGateway(t)
	t.Setenv("VERSITYGW_ENDPOINT", "")
	faultCase(t, expectError(`data "versitygw_users" "all" {}`, `Missing endpoint`))

	newFakeGateway(t)
	t.Setenv("VERSITYGW_SECRET_KEY", "")
	faultCase(t, expectError(`data "versitygw_users" "all" {}`, `Missing credentials`))

	newFakeGateway(t)
	faultCase(t, expectError(`
provider "versitygw" {
  endpoint = "ftp://gateway.invalid"
}
data "versitygw_users" "all" {}
`, `Cannot build the gateway client`))

	// Explicit configuration beats the environment, and the boolean from
	// the environment is parsed rather than string-compared.
	g := newFakeGateway(t)
	t.Setenv("VERSITYGW_INSECURE", "true")
	faultCase(t, resource.TestStep{Config: `
provider "versitygw" {
  endpoint = "` + g.srv.URL + `"
  region   = "eu-central-1"
}
data "versitygw_users" "all" {}
`, Check: resource.TestCheckResourceAttr("data.versitygw_users.all", "users.#", "0")})
}

const faultVersioning = faultBucket + `
resource "versitygw_bucket_versioning" "test" {
  bucket = versitygw_bucket.test.name
  versioning_configuration {
    status = "Enabled"
  }
}
`

const faultVersioningSuspended = faultBucket + `
resource "versitygw_bucket_versioning" "test" {
  bucket = versitygw_bucket.test.name
  versioning_configuration {
    status = "Suspended"
  }
}
`

const faultLock = faultVersioning + `
resource "versitygw_bucket_object_lock_configuration" "test" {
  bucket = versitygw_bucket.test.name
  rule {
    default_retention {
      mode = "GOVERNANCE"
      days = 2
    }
  }
  depends_on = [versitygw_bucket_versioning.test]
}
`

const faultLockChanged = faultVersioning + `
resource "versitygw_bucket_object_lock_configuration" "test" {
  bucket = versitygw_bucket.test.name
  rule {
    default_retention {
      mode = "COMPLIANCE"
      days = 2
    }
  }
  depends_on = [versitygw_bucket_versioning.test]
}
`

func TestFaultVersioning(t *testing.T) {
	g := newFakeGateway(t)
	g.fail("PUT /fault-bucket?versioning", 404, "NoSuchBucket")
	faultCase(t, expectError(faultVersioning, `Bucket does not exist`), recover(g))

	g = newFakeGateway(t)
	g.fail("PUT /fault-bucket?versioning", 409, "VersioningNotConfigured")
	faultCase(t, expectError(faultVersioning, `no versioning directory`), recover(g))

	g = newFakeGateway(t)
	g.fail("PUT /fault-bucket?versioning", 500, "InternalError")
	faultCase(t, expectError(faultVersioning, `Cannot set the bucket versioning`), recover(g))

	g = newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultVersioning},
		resource.TestStep{PreConfig: func() { g.fail("GET /fault-bucket?versioning", 500, "InternalError") },
			Config: faultVersioning, ExpectError: regexp.MustCompile(`Cannot read the bucket versioning`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("PUT /fault-bucket?versioning", 500, "InternalError") },
			Config: faultVersioningSuspended, ExpectError: regexp.MustCompile(`Cannot set the bucket versioning`)},
		// Bucket gone behind Terraform's back: the read answers an empty
		// configuration and the resource leaves state.
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.forgetBucket("fault-bucket") },
			Config: faultVersioning, PlanOnly: true, ExpectNonEmptyPlan: true},
		recover(g),
	)
}

func TestFaultVersioningConfigValidation(t *testing.T) {
	newFakeGateway(t)
	faultCase(t, expectError(faultBucket+`
resource "versitygw_bucket_versioning" "test" {
  bucket = versitygw_bucket.test.name
}
`, `Missing versioning_configuration`))
}

func TestFaultObjectLockConfigValidation(t *testing.T) {
	lock := func(body string) string {
		return faultVersioning + `
resource "versitygw_bucket_object_lock_configuration" "test" {
  bucket = versitygw_bucket.test.name
` + body + `
}
`
	}
	// HCL allows no nested single-line blocks, hence the layout.
	cases := map[string]string{
		"rule {}": `Empty rule`,
		"rule {\n default_retention {\n days = 1\n }\n}":                                     `Missing retention mode`,
		"rule {\n default_retention {\n mode = \"GOVERNANCE\"\n }\n}":                        `exactly one of days and years`,
		"rule {\n default_retention {\n mode = \"GOVERNANCE\"\n days = 1\n years = 1\n }\n}": `exactly one of days and years`,
	}
	for body, pattern := range cases {
		newFakeGateway(t)
		faultCase(t, expectError(lock(body), pattern))
	}
}

// Destroying versioning or object lock must not send anything: the gateway
// would delete the bucket. The fake mirrors that, so the bucket surviving
// the destroy is the proof.
func TestFaultStateOnlyDestroyKeepsTheBucket(t *testing.T) {
	g := newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultLock},
		resource.TestStep{Config: faultBucket, Check: func(*terraform.State) error {
			if !g.hasBucket("fault-bucket") {
				return fmt.Errorf("destroying versioning/object lock deleted the bucket")
			}
			return nil
		}},
		recover(g),
	)
}

func TestFaultObjectLock(t *testing.T) {
	g := newFakeGateway(t)
	g.fail("PUT /fault-bucket?object-lock", 404, "NoSuchBucket")
	faultCase(t, expectError(faultLock, `Bucket does not exist`), recover(g))

	g = newFakeGateway(t)
	g.fail("PUT /fault-bucket?object-lock", 500, "InternalError")
	faultCase(t, expectError(faultLock, `Cannot set the object lock configuration`), recover(g))

	g = newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultLock,
			Check: resource.TestCheckResourceAttr("versitygw_bucket_object_lock_configuration.test", "rule.default_retention.days", "2")},
		resource.TestStep{PreConfig: func() { g.fail("GET /fault-bucket?object-lock", 500, "InternalError") },
			Config: faultLock, ExpectError: regexp.MustCompile(`Cannot read the object lock configuration`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("PUT /fault-bucket?object-lock", 500, "InternalError") },
			Config: faultLockChanged, ExpectError: regexp.MustCompile(`Cannot set the object lock configuration`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.forgetLock("fault-bucket") },
			Config: faultLock, PlanOnly: true, ExpectNonEmptyPlan: true},
		recover(g),
	)
}

const faultOwnership = faultBucket + `
resource "versitygw_bucket_ownership_controls" "test" {
  bucket = versitygw_bucket.test.name
  rule {
    object_ownership = "ObjectWriter"
  }
}
`

const faultOwnershipChanged = faultBucket + `
resource "versitygw_bucket_ownership_controls" "test" {
  bucket = versitygw_bucket.test.name
  rule {
    object_ownership = "BucketOwnerPreferred"
  }
}
`

func TestFaultOwnershipControls(t *testing.T) {
	newFakeGateway(t)
	faultCase(t, expectError(faultBucket+`
resource "versitygw_bucket_ownership_controls" "test" {
  bucket = versitygw_bucket.test.name
}
`, `Missing rule`))

	g := newFakeGateway(t)
	g.fail("PUT /fault-bucket?ownershipControls", 404, "NoSuchBucket")
	faultCase(t, expectError(faultOwnership, `Bucket does not exist`), recover(g))

	g = newFakeGateway(t)
	g.fail("PUT /fault-bucket?ownershipControls", 500, "InternalError")
	faultCase(t, expectError(faultOwnership, `Cannot set the ownership controls`), recover(g))

	g = newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultOwnership},
		resource.TestStep{PreConfig: func() { g.fail("GET /fault-bucket?ownershipControls", 500, "InternalError") },
			Config: faultOwnership, ExpectError: regexp.MustCompile(`Cannot read the ownership controls`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("PUT /fault-bucket?ownershipControls", 500, "InternalError") },
			Config: faultOwnershipChanged, ExpectError: regexp.MustCompile(`Cannot set the ownership controls`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("DELETE /fault-bucket?ownershipControls", 500, "InternalError") },
			Config: faultBucket, ExpectError: regexp.MustCompile(`Cannot delete the ownership controls`)},
		// Deleted behind Terraform's back → not-found → gone from state.
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.forgetOwnership("fault-bucket") },
			Config: faultOwnership, PlanOnly: true, ExpectNonEmptyPlan: true},
		recover(g),
	)
}

const faultOwnershipWriter = faultBucket + `
resource "versitygw_bucket_ownership_controls" "test" {
  bucket = versitygw_bucket.test.name
  rule {
    object_ownership = "ObjectWriter"
  }
}
`

const faultACLCanned = faultOwnershipWriter + `
resource "versitygw_bucket_acl" "test" {
  bucket     = versitygw_bucket.test.name
  acl        = "public-read"
  depends_on = [versitygw_bucket_ownership_controls.test]
}
`

const faultACLExplicit = faultOwnershipWriter + `
resource "versitygw_bucket_acl" "test" {
  bucket = versitygw_bucket.test.name
  access_control_policy {
    grant {
      permission = "READ"
      grantee {
        type = "CanonicalUser"
        id   = versitygw_user.test.access_key
      }
    }
  }
  depends_on = [versitygw_bucket_ownership_controls.test]
}
`

func TestFaultACLConfigValidation(t *testing.T) {
	newFakeGateway(t)
	faultCase(t, expectError(faultOwnershipWriter+`
resource "versitygw_bucket_acl" "test" {
  bucket = versitygw_bucket.test.name
}
`, `Choose one ACL form`))

	newFakeGateway(t)
	faultCase(t, expectError(faultOwnershipWriter+`
resource "versitygw_bucket_acl" "test" {
  bucket = versitygw_bucket.test.name
  acl    = "private"
  access_control_policy {}
}
`, `Choose one ACL form`))

	newFakeGateway(t)
	faultCase(t, expectError(faultOwnershipWriter+`
resource "versitygw_bucket_acl" "test" {
  bucket = versitygw_bucket.test.name
  access_control_policy {
    grant {
      permission = "READ"
      grantee {
        type = "Group"
        id   = "all-users"
      }
    }
  }
}
`, `value must be one of`))
}

func TestFaultACL(t *testing.T) {
	// No ownership controls → the fake, like the gateway, refuses.
	newFakeGateway(t)
	faultCase(t, expectError(faultBucket+`
resource "versitygw_bucket_acl" "test" {
  bucket = versitygw_bucket.test.name
  acl    = "private"
}
`, `does not allow ACLs`))

	// A grant for an account that does not exist → 500 upstream.
	newFakeGateway(t)
	faultCase(t, expectError(faultOwnershipWriter+`
resource "versitygw_bucket_acl" "test" {
  bucket = versitygw_bucket.test.name
  access_control_policy {
    grant {
      permission = "READ"
      grantee {
        type = "CanonicalUser"
        id   = "nobody"
      }
    }
  }
  depends_on = [versitygw_bucket_ownership_controls.test]
}
`, `non-existent account`))

	// The owner's FULL_CONTROL is implicit; a configured copy would be
	// duplicated by the gateway and could never converge.
	newFakeGateway(t)
	faultCase(t, expectError(faultOwnershipWriter+`
resource "versitygw_bucket_acl" "test" {
  bucket = versitygw_bucket.test.name
  access_control_policy {
    grant {
      permission = "FULL_CONTROL"
      grantee {
        type = "CanonicalUser"
        id   = versitygw_user.test.access_key
      }
    }
  }
  depends_on = [versitygw_bucket_ownership_controls.test]
}
`, `Owner grant is implicit`))

	g := newFakeGateway(t)
	g.fail("PUT /fault-bucket?acl", 404, "NoSuchBucket")
	faultCase(t, expectError(faultACLCanned, `Bucket does not exist`), recover(g))

	g = newFakeGateway(t)
	g.fail("GET /fault-bucket?acl", 404, "NoSuchBucket")
	faultCase(t, expectError(faultACLExplicit, `Bucket does not exist`), recover(g))

	g = newFakeGateway(t)
	g.fail("PUT /fault-bucket?acl", 500, "InternalError")
	faultCase(t, expectError(faultACLCanned, `internal error`), recover(g))

	g = newFakeGateway(t)
	g.fail("PUT /fault-bucket?acl", 403, "AccessDenied")
	faultCase(t, expectError(faultACLCanned, `Cannot set the bucket ACL`), recover(g))

	g = newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultACLCanned},
		resource.TestStep{PreConfig: func() { g.fail("GET /fault-bucket?acl", 500, "InternalError") },
			Config: faultACLCanned, ExpectError: regexp.MustCompile(`Cannot read the bucket ACL`)},
		// Explicit form as an in-place update.
		resource.TestStep{PreConfig: g.clearFaults, Config: faultACLExplicit,
			Check: resource.TestCheckResourceAttr("versitygw_bucket_acl.test", "access_control_policy.grant.#", "1")},
		// Back to canned; the grants read back as public-read.
		resource.TestStep{Config: faultACLCanned,
			Check: resource.TestCheckResourceAttr("versitygw_bucket_acl.test", "acl", "public-read")},
		// The ACL reset by an owner change is drift, shown as the grants
		// the bucket now carries.
		resource.TestStep{PreConfig: func() { g.resetACL("fault-bucket") },
			Config: faultACLCanned, PlanOnly: true, ExpectNonEmptyPlan: true},
		// Bucket gone → resource leaves state.
		resource.TestStep{PreConfig: func() { g.forgetBucket("fault-bucket") },
			Config: faultACLCanned, PlanOnly: true, ExpectNonEmptyPlan: true},
		recover(g),
	)
}

const faultBucketTagged = faultUser + `
resource "versitygw_bucket" "test" {
  name  = "fault-bucket"
  owner = versitygw_user.test.access_key
  tags  = { k = "v" }
}
`

func TestFaultBucketTags(t *testing.T) {
	// Tags failing after the bucket was created: the bucket stays in
	// state, the next apply retries only the tags.
	g := newFakeGateway(t)
	g.fail("PUT /fault-bucket?tagging", 500, "InternalError")
	faultCase(t,
		expectError(faultBucketTagged, `Cannot set the bucket tags`),
		resource.TestStep{PreConfig: g.clearFaults, Config: faultBucketTagged,
			Check: resource.TestCheckResourceAttr("versitygw_bucket.test", "tags.k", "v")},
		recover(g),
	)

	g = newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultBucketTagged},
		resource.TestStep{PreConfig: func() { g.fail("GET /fault-bucket?tagging", 500, "InternalError") },
			Config: faultBucketTagged, ExpectError: regexp.MustCompile(`Cannot read the bucket tags`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("DELETE /fault-bucket?tagging", 500, "InternalError") },
			Config: faultBucket, ExpectError: regexp.MustCompile(`Cannot set the bucket tags`)},
		resource.TestStep{PreConfig: g.clearFaults, Config: faultBucket,
			Check: resource.TestCheckResourceAttr("versitygw_bucket.test", "tags.%", "0")},
		recover(g),
	)

	// Owner and tags change in one apply, the tag write fails: the owner
	// change already happened and stays in state, so the next apply only
	// retries the tags.
	g = newFakeGateway(t)
	ownerAndTags := func(owner, tags string) string {
		return faultUser + `
resource "versitygw_user" "other" {
  access_key = "fault-other"
  secret_key = "faultsecret"
}

resource "versitygw_bucket" "test" {
  name  = "fault-bucket"
  owner = ` + owner + `
  tags  = ` + tags + `
}
`
	}
	faultCase(t,
		resource.TestStep{Config: ownerAndTags("versitygw_user.test.access_key", `{ k = "v" }`)},
		resource.TestStep{
			PreConfig:   func() { g.fail("PUT /fault-bucket?tagging", 500, "InternalError") },
			Config:      ownerAndTags("versitygw_user.other.access_key", `{ k = "w" }`),
			ExpectError: regexp.MustCompile(`Cannot set the bucket tags`),
		},
		resource.TestStep{
			PreConfig: g.clearFaults,
			Config:    ownerAndTags("versitygw_user.other.access_key", `{ k = "w" }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("versitygw_bucket.test", "owner", "fault-other"),
				resource.TestCheckResourceAttr("versitygw_bucket.test", "tags.k", "w"),
			),
		},
		recover(g),
	)
	if g.buckets["fault-bucket"] != "" {
		t.Errorf("bucket left behind")
	}
}

func TestFaultBucketTagsConfigValidation(t *testing.T) {
	g := newFakeGateway(t)
	faultCase(t,
		expectError(faultUser+`
resource "versitygw_bucket" "test" {
  name  = "fault-bucket"
  owner = versitygw_user.test.access_key
  tags  = { k = null }
}
`, `Null tag value`),
		recover(g),
	)
}

const faultCORS = faultBucket + `
resource "versitygw_bucket_cors_configuration" "test" {
  bucket = versitygw_bucket.test.name
  cors_rule {
    allowed_methods = ["GET"]
    allowed_origins = ["*"]
  }
}
`

const faultCORSChanged = faultBucket + `
resource "versitygw_bucket_cors_configuration" "test" {
  bucket = versitygw_bucket.test.name
  cors_rule {
    id              = "x"
    allowed_headers = ["*"]
    allowed_methods = ["PUT"]
    allowed_origins = ["https://a.example"]
    expose_headers  = ["ETag"]
    max_age_seconds = 10
  }
}
`

func TestFaultCORSConfigValidation(t *testing.T) {
	newFakeGateway(t)
	cases := map[string]string{
		`Missing cors_rule`: `
resource "versitygw_bucket_cors_configuration" "test" {
  bucket = versitygw_bucket.test.name
}
`,
		`value must be one of`: `
resource "versitygw_bucket_cors_configuration" "test" {
  bucket = versitygw_bucket.test.name
  cors_rule {
    allowed_methods = ["PATCH"]
    allowed_origins = ["*"]
  }
}
`,
		`Null List Value`: `
resource "versitygw_bucket_cors_configuration" "test" {
  bucket = versitygw_bucket.test.name
  cors_rule {
    allowed_methods = ["GET"]
    allowed_origins = [null]
  }
}
`,
		`max_age_seconds value must be between 0 and`: `
resource "versitygw_bucket_cors_configuration" "test" {
  bucket = versitygw_bucket.test.name
  cors_rule {
    allowed_methods = ["GET"]
    allowed_origins = ["*"]
    max_age_seconds = 2147483648
  }
}
`,
		`list must contain at least 1 elements`: `
resource "versitygw_bucket_cors_configuration" "test" {
  bucket = versitygw_bucket.test.name
  cors_rule {
    allowed_headers = []
    allowed_methods = ["GET"]
    allowed_origins = ["*"]
  }
}
`,
	}
	for pattern, cfg := range cases {
		faultCase(t, expectError(faultBucket+cfg, pattern))
	}
}

func TestFaultCORS(t *testing.T) {
	g := newFakeGateway(t)
	g.fail("PUT /fault-bucket?cors", 404, "NoSuchBucket")
	faultCase(t, expectError(faultCORS, `Bucket does not exist`), recover(g))

	g = newFakeGateway(t)
	g.fail("PUT /fault-bucket?cors", 500, "InternalError")
	faultCase(t, expectError(faultCORS, `Cannot set the CORS configuration`), recover(g))

	g = newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultCORS},
		resource.TestStep{PreConfig: func() { g.fail("GET /fault-bucket?cors", 500, "InternalError") },
			Config: faultCORS, ExpectError: regexp.MustCompile(`Cannot read the CORS configuration`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("PUT /fault-bucket?cors", 500, "InternalError") },
			Config: faultCORSChanged, ExpectError: regexp.MustCompile(`Cannot set the CORS configuration`)},
		resource.TestStep{PreConfig: g.clearFaults, Config: faultCORSChanged,
			Check: resource.TestCheckResourceAttr("versitygw_bucket_cors_configuration.test", "cors_rule.0.max_age_seconds", "10")},
		resource.TestStep{PreConfig: func() { g.fail("DELETE /fault-bucket?cors", 500, "InternalError") },
			Config: faultBucket, ExpectError: regexp.MustCompile(`Cannot delete the CORS configuration`)},
		// Deleted behind Terraform's back → not-found → gone from state.
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.forgetCORS("fault-bucket") },
			Config: faultCORS, PlanOnly: true, ExpectNonEmptyPlan: true},
		recover(g),
	)
}

const faultWebsite = faultBucket + `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  index_document {
    suffix = "index.html"
  }
}
`

const faultWebsiteChanged = faultBucket + `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  redirect_all_requests_to {
    host_name = "example.com"
  }
}
`

func TestFaultWebsiteConfigValidation(t *testing.T) {
	newFakeGateway(t)
	rule := func(body string) string {
		return `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  index_document {
    suffix = "index.html"
  }
  routing_rule {
` + body + `
  }
}
`
	}
	cases := map[string]string{
		`Missing block`: `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
}
`,
		`Missing suffix`: `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  index_document {}
}
`,
		`Missing key`: `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  index_document {
    suffix = "index.html"
  }
  error_document {}
}
`,
		`Missing host_name`: `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  redirect_all_requests_to {}
}
`,
		`redirect_all_requests_to and index_document are mutually exclusive`: `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  index_document {
    suffix = "index.html"
  }
  redirect_all_requests_to {
    host_name = "example.com"
  }
}
`,
		`cannot be combined with error_document`: `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  redirect_all_requests_to {
    host_name = "example.com"
  }
  error_document {
    key = "404.html"
  }
}
`,
		`must not contain a slash`: `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  index_document {
    suffix = "sub/index.html"
  }
}
`,
		`protocol value must be one of: \["http"`: `
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  redirect_all_requests_to {
    host_name = "example.com"
    protocol  = "ftp"
  }
}
`,
		`Missing redirect`: rule(`
    condition {
      key_prefix_equals = "a/"
    }
`),
		`Empty redirect`: rule(`
    redirect {}
`),
		`Conflicting replacements`: rule(`
    redirect {
      replace_key_with        = "a"
      replace_key_prefix_with = "b"
    }
`),
		`Empty condition`: rule(`
    condition {}
    redirect {
      host_name = "example.com"
    }
`),
		`Invalid error code`: rule(`
    condition {
      http_error_code_returned_equals = "418"
    }
    redirect {
      host_name = "example.com"
    }
`),
		`http_redirect_code value must be one of`: rule(`
    redirect {
      http_redirect_code = "300"
    }
`),
	}
	for pattern, cfg := range cases {
		faultCase(t, expectError(faultBucket+cfg, pattern))
	}

	var many strings.Builder
	for i := 0; i < 51; i++ { // one over the gateway's limit of 50
		fmt.Fprintf(&many, `
  routing_rule {
    condition {
      key_prefix_equals = "p%d/"
    }
    redirect {
      host_name = "example.com"
    }
  }
`, i)
	}
	faultCase(t, expectError(faultBucket+`
resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
  index_document {
    suffix = "index.html"
  }
`+many.String()+`
}
`, `Too many routing rules`))
}

func TestFaultWebsite(t *testing.T) {
	g := newFakeGateway(t)
	g.fail("PUT /fault-bucket?website", 404, "NoSuchBucket")
	faultCase(t, expectError(faultWebsite, `Bucket does not exist`), recover(g))

	g = newFakeGateway(t)
	g.fail("PUT /fault-bucket?website", 500, "InternalError")
	faultCase(t, expectError(faultWebsite, `Cannot set the website configuration`), recover(g))

	g = newFakeGateway(t)
	faultCase(t,
		resource.TestStep{Config: faultWebsite},
		resource.TestStep{PreConfig: func() { g.fail("GET /fault-bucket?website", 500, "InternalError") },
			Config: faultWebsite, ExpectError: regexp.MustCompile(`Cannot read the website configuration`)},
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.fail("PUT /fault-bucket?website", 500, "InternalError") },
			Config: faultWebsiteChanged, ExpectError: regexp.MustCompile(`Cannot set the website configuration`)},
		resource.TestStep{PreConfig: g.clearFaults, Config: faultWebsiteChanged,
			Check: resource.TestCheckResourceAttr("versitygw_bucket_website_configuration.test", "redirect_all_requests_to.host_name", "example.com")},
		resource.TestStep{PreConfig: func() { g.fail("DELETE /fault-bucket?website", 500, "InternalError") },
			Config: faultBucket, ExpectError: regexp.MustCompile(`Cannot delete the website configuration`)},
		// Deleted behind Terraform's back → not-found → gone from state.
		resource.TestStep{PreConfig: func() { g.clearFaults(); g.forgetWebsite("fault-bucket") },
			Config: faultWebsite, PlanOnly: true, ExpectNonEmptyPlan: true},
		recover(g),
	)
}

func TestFaultSingleDataSources(t *testing.T) {
	g := newFakeGateway(t)
	g.fail("PATCH /list-users", 500, "InternalError")
	faultCase(t, expectError(`data "versitygw_user" "one" { access_key = "x" }`, `Cannot read the account`))

	g = newFakeGateway(t)
	g.fail("PATCH /list-buckets", 500, "InternalError")
	faultCase(t, expectError(`data "versitygw_bucket" "one" { name = "fault-bucket" }`, `Cannot read the bucket`))

	g = newFakeGateway(t)
	g.fail("GET /fault-bucket?tagging", 500, "InternalError")
	faultCase(t, expectError(faultBucket+`
data "versitygw_bucket" "one" {
  name       = versitygw_bucket.test.name
  depends_on = [versitygw_bucket.test]
}
`, `Cannot read the bucket tags`), recover(g))
}
