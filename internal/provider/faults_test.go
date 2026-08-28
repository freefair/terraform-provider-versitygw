package provider_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// These run under TF_ACC like the real acceptance tests — they drive the
// provider through Terraform — but against fakeGateway, because the branches
// they cover are the ones a healthy gateway never takes.

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
