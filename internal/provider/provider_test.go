package provider_test

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/freefair/terraform-provider-versitygw/internal/provider"
)

// Acceptance tests run against a REAL gateway, not a mock. A mock would only
// prove the provider agrees with itself; what needs proving is that it agrees
// with versitygw. Start one and export the same variables the provider reads:
//
//	docker run --rm -d -p 7070:7070 --name versitygw-acc \
//	  -v vgw-versions:/tmp/vgw-versions \
//	  -e ROOT_ACCESS_KEY_ID=testaccess -e ROOT_SECRET_ACCESS_KEY=testsecret \
//	  versity/versitygw:v1.7.0 --iam-dir /tmp/vgw posix /tmp/vgw --versioning-dir /tmp/vgw-versions
//
//	export TF_ACC=1
//	export VERSITYGW_ENDPOINT=http://127.0.0.1:7070
//	export VERSITYGW_ACCESS_KEY=testaccess VERSITYGW_SECRET_KEY=testsecret
//	make testacc
//
// The container is started without --admin-port on purpose: that is the layout
// where the admin routes live on the S3 listener, and it exercises the
// provider's endpoint fallback. --iam-dir is not optional: without an IAM
// service the gateway runs in single-account mode and answers every account
// route with 501 XAdminMethodNotSupported. The versioning directory must
// exist before the gateway starts; the named volume takes care of that.

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"versitygw": providerserver.NewProtocol6WithError(provider.New("test")()),
}

func testAccPreCheck(t *testing.T) {
	t.Helper()
	for _, key := range []string{"VERSITYGW_ENDPOINT", "VERSITYGW_ACCESS_KEY", "VERSITYGW_SECRET_KEY"} {
		if os.Getenv(key) == "" {
			t.Fatalf("%s must be set for acceptance tests — see the comment in provider_test.go", key)
		}
	}
}

func TestAccUserResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccUserConfig("acc-user", "initialsecret", "user"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_user.test", "access_key", "acc-user"),
					resource.TestCheckResourceAttr("versitygw_user.test", "role", "user"),
					resource.TestCheckResourceAttr("versitygw_user.test", "user_id", "0"),
				),
			},
			{
				// The gateway returns the secret in its user listing, so an
				// in-place secret change has to survive a refresh — this is
				// the step that proves drift detection works on the key.
				Config: testAccUserConfig("acc-user", "rotatedsecret", "userplus"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_user.test", "secret_key", "rotatedsecret"),
					resource.TestCheckResourceAttr("versitygw_user.test", "role", "userplus"),
				),
			},
			{
				// The framework looks for an "id" attribute unless told
				// otherwise. This provider has none: the access key ID IS the
				// identity, and a synthetic id would be a second name for it.
				ResourceName:                         "versitygw_user.test",
				ImportState:                          true,
				ImportStateId:                        "acc-user",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_key",
			},
		},
	})
}

func TestAccBucketResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketConfig("acc-owner-one"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket.test", "name", "acc-bucket"),
					resource.TestCheckResourceAttr("versitygw_bucket.test", "owner", "acc-owner-one"),
				),
			},
			{
				// An ownership change is an in-place update, not a replacement
				// — and it resets the bucket's ACL on the gateway side.
				Config: testAccBucketConfig("acc-owner-two"),
				Check: resource.TestCheckResourceAttr(
					"versitygw_bucket.test", "owner", "acc-owner-two"),
			},
			{
				ResourceName:                         "versitygw_bucket.test",
				ImportState:                          true,
				ImportStateId:                        "acc-bucket",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
		},
	})
}

func testAccUserConfig(access, secret, role string) string {
	return fmt.Sprintf(`
resource "versitygw_user" "test" {
  access_key = %q
  secret_key = %q
  role       = %q
}
`, access, secret, role)
}

func testAccBucketConfig(owner string) string {
	return fmt.Sprintf(`
resource "versitygw_user" "one" {
  access_key = "acc-owner-one"
  secret_key = "ownersecretone"
}

resource "versitygw_user" "two" {
  access_key = "acc-owner-two"
  secret_key = "ownersecrettwo"
}

resource "versitygw_bucket" "test" {
  name  = "acc-bucket"
  owner = %q

  depends_on = [versitygw_user.one, versitygw_user.two]
}
`, owner)
}

func TestAccBucketPolicyResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketPolicyConfig("acc-policy-owner", []string{"s3:GetObject"}),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket_policy.test", "bucket", "acc-policy-bucket"),
					resource.TestCheckResourceAttrWith("versitygw_bucket_policy.test", "policy", func(v string) error {
						if !strings.Contains(v, "s3:GetObject") {
							return fmt.Errorf("policy does not carry the action: %s", v)
						}
						return nil
					}),
				),
			},
			{
				// A new statement is an in-place update: PUT replaces.
				Config: testAccBucketPolicyConfig("acc-policy-owner", []string{"s3:GetObject", "s3:ListBucket"}),
				Check: resource.TestCheckResourceAttrWith("versitygw_bucket_policy.test", "policy", func(v string) error {
					if !strings.Contains(v, "s3:ListBucket") {
						return fmt.Errorf("updated policy not read back: %s", v)
					}
					return nil
				}),
			},
			{
				ResourceName:                         "versitygw_bucket_policy.test",
				ImportState:                          true,
				ImportStateId:                        "acc-policy-bucket",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
			{
				// Changing the bucket owner deletes the policy on the gateway
				// side. The provider must surface that as drift — the plan
				// after this apply is not empty — rather than reapply it
				// behind the user's back.
				Config:             testAccBucketPolicyConfig("acc-policy-reader", []string{"s3:GetObject", "s3:ListBucket"}),
				ExpectNonEmptyPlan: true,
			},
			{
				// The next apply recreates the policy and converges.
				Config: testAccBucketPolicyConfig("acc-policy-reader", []string{"s3:GetObject", "s3:ListBucket"}),
				Check:  resource.TestCheckResourceAttr("versitygw_bucket_policy.test", "bucket", "acc-policy-bucket"),
			},
		},
	})
}

func TestAccBucketPolicyRejectsMalformedDocument(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Resources naming another bucket — the gateway refuses that,
				// and its message is what the user should see.
				Config: testAccBucketPolicyFixture("acc-policy-owner") + `
resource "versitygw_bucket_policy" "test" {
  bucket = versitygw_bucket.test.name
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = [versitygw_user.reader.access_key] }
      Action    = ["s3:GetObject"]
      Resource  = ["arn:aws:s3:::some-other-bucket/*"]
    }]
  })
}
`,
				ExpectError: regexp.MustCompile(`rejected the policy`),
			},
		},
	})
}

func testAccBucketPolicyFixture(owner string) string {
	return fmt.Sprintf(`
resource "versitygw_user" "owner" {
  access_key = "acc-policy-owner"
  secret_key = "policyownersecret"
}

resource "versitygw_user" "reader" {
  access_key = "acc-policy-reader"
  secret_key = "policyreadersecret"
}

resource "versitygw_bucket" "test" {
  name  = "acc-policy-bucket"
  owner = %q

  depends_on = [versitygw_user.owner, versitygw_user.reader]
}
`, owner)
}

func testAccBucketPolicyConfig(owner string, actions []string) string {
	quoted := make([]string, len(actions))
	for i, a := range actions {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	return testAccBucketPolicyFixture(owner) + fmt.Sprintf(`
resource "versitygw_bucket_policy" "test" {
  bucket = versitygw_bucket.test.name
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = [versitygw_user.reader.access_key] }
      Action    = [%s]
      Resource  = ["arn:aws:s3:::acc-policy-bucket", "arn:aws:s3:::acc-policy-bucket/*"]
    }]
  })
}
`, strings.Join(quoted, ", "))
}

func TestAccDataSources(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketConfig("acc-owner-one") + `
data "versitygw_users" "all" {
  depends_on = [versitygw_user.one, versitygw_user.two]
}

data "versitygw_buckets" "all" {
  depends_on = [versitygw_bucket.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					// The listing carries every account with its secret; the
					// resources created above must be among them.
					resource.TestCheckTypeSetElemNestedAttrs("data.versitygw_users.all", "users.*", map[string]string{
						"access_key": "acc-owner-one",
						"secret_key": "ownersecretone",
						"role":       "user",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("data.versitygw_buckets.all", "buckets.*", map[string]string{
						"name":  "acc-bucket",
						"owner": "acc-owner-one",
					}),
				),
			},
		},
	})
}

func TestAccBucketVersioningAndObjectLock(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccVersioningConfig("Enabled", "-"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket_versioning.test", "bucket", "acc-versioned"),
					resource.TestCheckResourceAttr("versitygw_bucket_versioning.test", "versioning_configuration.status", "Enabled"),
				),
			},
			{
				Config: testAccVersioningConfig("Suspended", "-"),
				Check:  resource.TestCheckResourceAttr("versitygw_bucket_versioning.test", "versioning_configuration.status", "Suspended"),
			},
			{
				ResourceName:                         "versitygw_bucket_versioning.test",
				ImportState:                          true,
				ImportStateId:                        "acc-versioned",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
			{
				// Lock with a default retention on the (re-enabled) bucket.
				Config: testAccVersioningConfig("Enabled", `
  rule {
    default_retention {
      mode = "GOVERNANCE"
      days = 1
    }
  }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket_object_lock_configuration.test", "object_lock_enabled", "Enabled"),
					resource.TestCheckResourceAttr("versitygw_bucket_object_lock_configuration.test", "rule.default_retention.mode", "GOVERNANCE"),
					resource.TestCheckResourceAttr("versitygw_bucket_object_lock_configuration.test", "rule.default_retention.days", "1"),
				),
			},
			{
				Config: testAccVersioningConfig("Enabled", `
  rule {
    default_retention {
      mode  = "COMPLIANCE"
      years = 1
    }
  }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket_object_lock_configuration.test", "rule.default_retention.mode", "COMPLIANCE"),
					resource.TestCheckResourceAttr("versitygw_bucket_object_lock_configuration.test", "rule.default_retention.years", "1"),
					resource.TestCheckNoResourceAttr("versitygw_bucket_object_lock_configuration.test", "rule.default_retention.days"),
				),
			},
			{
				ResourceName:                         "versitygw_bucket_object_lock_configuration.test",
				ImportState:                          true,
				ImportStateId:                        "acc-versioned",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
			{
				// Dropping the rule keeps the lock and clears the retention.
				Config: testAccVersioningConfig("Enabled", ""),
				Check:  resource.TestCheckNoResourceAttr("versitygw_bucket_object_lock_configuration.test", "rule.default_retention.mode"),
			},
			{
				// The gateway refuses to suspend versioning while a lock
				// configuration is present; that refusal reaches the user.
				Config:      testAccVersioningConfig("Suspended", ""),
				ExpectError: regexp.MustCompile(`InvalidBucketState`),
			},
		},
	})
}

func TestAccObjectLockNeedsVersioning(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "versitygw_user" "owner" {
  access_key = "acc-lock-owner"
  secret_key = "lockownersecret"
}

resource "versitygw_bucket" "test" {
  name  = "acc-unversioned"
  owner = versitygw_user.owner.access_key
}

resource "versitygw_bucket_object_lock_configuration" "test" {
  bucket = versitygw_bucket.test.name
}
`,
				ExpectError: regexp.MustCompile(`Versioning must be enabled first`),
			},
		},
	})
}

// testAccVersioningConfig renders a bucket with versioning and, unless
// lockBody is "-", an object lock configuration whose body is lockBody — an
// empty string gives a lock without a rule.
func testAccVersioningConfig(status, lockBody string) string {
	cfg := fmt.Sprintf(`
resource "versitygw_user" "owner" {
  access_key = "acc-versioning-owner"
  secret_key = "versioningownersecret"
}

resource "versitygw_bucket" "test" {
  name  = "acc-versioned"
  owner = versitygw_user.owner.access_key
}

resource "versitygw_bucket_versioning" "test" {
  bucket = versitygw_bucket.test.name
  versioning_configuration {
    status = %q
  }
}
`, status)
	if lockBody != "-" {
		cfg += fmt.Sprintf(`
resource "versitygw_bucket_object_lock_configuration" "test" {
  bucket = versitygw_bucket.test.name
%s
  depends_on = [versitygw_bucket_versioning.test]
}
`, lockBody)
	}
	return cfg
}

func TestAccBucketOwnershipControls(t *testing.T) {
	cfg := func(ownership string) string {
		return fmt.Sprintf(`
resource "versitygw_user" "owner" {
  access_key = "acc-ownership-owner"
  secret_key = "ownershipownersecret"
}

resource "versitygw_bucket" "test" {
  name  = "acc-ownership"
  owner = versitygw_user.owner.access_key
}

resource "versitygw_bucket_ownership_controls" "test" {
  bucket = versitygw_bucket.test.name
  rule {
    object_ownership = %q
  }
}
`, ownership)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("ObjectWriter"),
				Check:  resource.TestCheckResourceAttr("versitygw_bucket_ownership_controls.test", "rule.object_ownership", "ObjectWriter"),
			},
			{
				Config: cfg("BucketOwnerPreferred"),
				Check:  resource.TestCheckResourceAttr("versitygw_bucket_ownership_controls.test", "rule.object_ownership", "BucketOwnerPreferred"),
			},
			{
				ResourceName:                         "versitygw_bucket_ownership_controls.test",
				ImportState:                          true,
				ImportStateId:                        "acc-ownership",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func TestAccBucketACL(t *testing.T) {
	base := `
resource "versitygw_user" "owner" {
  access_key = "acc-acl-owner"
  secret_key = "aclownersecret"
}

resource "versitygw_user" "reader" {
  access_key = "acc-acl-reader"
  secret_key = "aclreadersecret"
}

resource "versitygw_bucket" "test" {
  name  = "acc-acl"
  owner = versitygw_user.owner.access_key
  depends_on = [versitygw_user.reader]
}

resource "versitygw_bucket_ownership_controls" "test" {
  bucket = versitygw_bucket.test.name
  rule {
    object_ownership = "ObjectWriter"
  }
}
`
	canned := func(acl string) string {
		return base + fmt.Sprintf(`
resource "versitygw_bucket_acl" "test" {
  bucket     = versitygw_bucket.test.name
  acl        = %q
  depends_on = [versitygw_bucket_ownership_controls.test]
}
`, acl)
	}
	explicit := base + `
resource "versitygw_bucket_acl" "test" {
  bucket = versitygw_bucket.test.name
  access_control_policy {
    grant {
      permission = "READ"
      grantee {
        type = "CanonicalUser"
        id   = versitygw_user.reader.access_key
      }
    }
    grant {
      permission = "WRITE_ACP"
      grantee {
        type = "CanonicalUser"
        id   = versitygw_user.reader.access_key
      }
    }
  }
  depends_on = [versitygw_bucket_ownership_controls.test]
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: canned("public-read"),
				Check:  resource.TestCheckResourceAttr("versitygw_bucket_acl.test", "acl", "public-read"),
			},
			{
				Config: canned("private"),
				Check:  resource.TestCheckResourceAttr("versitygw_bucket_acl.test", "acl", "private"),
			},
			{
				Config: explicit,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket_acl.test", "access_control_policy.grant.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("versitygw_bucket_acl.test", "access_control_policy.grant.*", map[string]string{
						"permission":   "READ",
						"grantee.type": "CanonicalUser",
						"grantee.id":   "acc-acl-reader",
					}),
				),
			},
			{
				ResourceName:                         "versitygw_bucket_acl.test",
				ImportState:                          true,
				ImportStateId:                        "acc-acl",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func TestAccBucketACLRefusedWithoutOwnershipControls(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "versitygw_user" "owner" {
  access_key = "acc-acl-enforced-owner"
  secret_key = "aclownersecret"
}

resource "versitygw_bucket" "test" {
  name  = "acc-acl-enforced"
  owner = versitygw_user.owner.access_key
}

resource "versitygw_bucket_acl" "test" {
  bucket = versitygw_bucket.test.name
  acl    = "public-read"
}
`,
				ExpectError: regexp.MustCompile(`does not allow ACLs`),
			},
		},
	})
}

func TestAccBucketTags(t *testing.T) {
	cfg := func(owner, tags string) string {
		return fmt.Sprintf(`
resource "versitygw_user" "one" {
  access_key = "acc-tags-one"
  secret_key = "tagsonesecret"
}

resource "versitygw_user" "two" {
  access_key = "acc-tags-two"
  secret_key = "tagstwosecret"
}

resource "versitygw_bucket" "test" {
  name  = "acc-tagged"
  owner = %s
  %s
  depends_on = [versitygw_user.one, versitygw_user.two]
}
`, owner, tags)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("versitygw_user.one.access_key", `tags = { team = "platform", cost = "ci" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket.test", "tags.%", "2"),
					resource.TestCheckResourceAttr("versitygw_bucket.test", "tags.team", "platform"),
				),
			},
			{
				// Owner change alone: no tagging call is made, so the
				// refresh after this step proves the gateway kept the tags.
				Config: cfg("versitygw_user.two.access_key", `tags = { team = "platform", cost = "ci" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket.test", "owner", "acc-tags-two"),
					resource.TestCheckResourceAttr("versitygw_bucket.test", "tags.%", "2"),
					resource.TestCheckResourceAttr("versitygw_bucket.test", "tags.cost", "ci"),
				),
			},
			{
				// Change one, drop one.
				Config: cfg("versitygw_user.two.access_key", `tags = { team = "data" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket.test", "tags.%", "1"),
					resource.TestCheckResourceAttr("versitygw_bucket.test", "tags.team", "data"),
				),
			},
			{
				ResourceName:                         "versitygw_bucket.test",
				ImportState:                          true,
				ImportStateId:                        "acc-tagged",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
			{
				// No tags at all deletes the tag set; the default keeps the
				// attribute an empty map rather than null.
				Config: cfg("versitygw_user.two.access_key", ""),
				Check:  resource.TestCheckResourceAttr("versitygw_bucket.test", "tags.%", "0"),
			},
		},
	})
}

func TestAccBucketCORSConfiguration(t *testing.T) {
	cfg := func(rules string) string {
		return `
resource "versitygw_user" "owner" {
  access_key = "acc-cors-owner"
  secret_key = "corsownersecret"
}

resource "versitygw_bucket" "test" {
  name  = "acc-cors"
  owner = versitygw_user.owner.access_key
}

resource "versitygw_bucket_cors_configuration" "test" {
  bucket = versitygw_bucket.test.name
` + rules + `
}
`
	}
	two := `
  cors_rule {
    id              = "browser-uploads"
    allowed_headers = ["*"]
    allowed_methods = ["PUT", "POST"]
    allowed_origins = ["https://app.example.com"]
    expose_headers  = ["ETag"]
    max_age_seconds = 3000
  }

  cors_rule {
    allowed_methods = ["GET"]
    allowed_origins = ["*"]
  }
`
	edited := `
  cors_rule {
    id              = "browser-uploads"
    allowed_headers = ["*"]
    allowed_methods = ["PUT", "POST"]
    allowed_origins = ["https://app.example.com", "https://admin.example.com"]
    expose_headers  = ["ETag"]
    max_age_seconds = 3000
  }

  cors_rule {
    allowed_methods = ["GET"]
    allowed_origins = ["*"]
  }
`
	one := `
  cors_rule {
    allowed_methods = ["GET"]
    allowed_origins = ["*"]
  }
`
	// A preflight from the browser's point of view: proves the gateway
	// applies the configuration rather than merely storing it.
	preflight := func(origin, method string, wantStatus int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			req, err := http.NewRequest(http.MethodOptions, os.Getenv("VERSITYGW_ENDPOINT")+"/acc-cors/key", nil)
			if err != nil {
				return err
			}
			req.Header.Set("Origin", origin)
			req.Header.Set("Access-Control-Request-Method", method)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer res.Body.Close()
			if res.StatusCode != wantStatus {
				return fmt.Errorf("preflight %s %s: status %d, want %d", origin, method, res.StatusCode, wantStatus)
			}
			if wantStatus == 200 && res.Header.Get("Access-Control-Allow-Origin") != origin {
				return fmt.Errorf("preflight: Access-Control-Allow-Origin = %q", res.Header.Get("Access-Control-Allow-Origin"))
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(two),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket_cors_configuration.test", "cors_rule.#", "2"),
					resource.TestCheckResourceAttr("versitygw_bucket_cors_configuration.test", "cors_rule.0.id", "browser-uploads"),
					resource.TestCheckResourceAttr("versitygw_bucket_cors_configuration.test", "cors_rule.0.max_age_seconds", "3000"),
					resource.TestCheckResourceAttr("versitygw_bucket_cors_configuration.test", "cors_rule.1.allowed_origins.0", "*"),
					resource.TestCheckNoResourceAttr("versitygw_bucket_cors_configuration.test", "cors_rule.1.id"),
					preflight("https://app.example.com", "PUT", 200),
					preflight("https://evil.example", "PUT", 403),
				),
			},
			{
				Config: cfg(edited),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket_cors_configuration.test", "cors_rule.0.allowed_origins.#", "2"),
					preflight("https://admin.example.com", "POST", 200),
				),
			},
			{
				Config: cfg(one),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("versitygw_bucket_cors_configuration.test", "cors_rule.#", "1"),
					preflight("https://app.example.com", "PUT", 403),
				),
			},
			{
				ResourceName:                         "versitygw_bucket_cors_configuration.test",
				ImportState:                          true,
				ImportStateId:                        "acc-cors",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func TestAccBucketWebsiteConfiguration(t *testing.T) {
	cfg := func(body string) string {
		return `
resource "versitygw_user" "owner" {
  access_key = "acc-website-owner"
  secret_key = "websiteownersecret"
}

resource "versitygw_bucket" "test" {
  name  = "acc-website"
  owner = versitygw_user.owner.access_key
}

resource "versitygw_bucket_website_configuration" "test" {
  bucket = versitygw_bucket.test.name
` + body + `
}
`
	}
	const res = "versitygw_bucket_website_configuration.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(`
  index_document {
    suffix = "index.html"
  }
  error_document {
    key = "404.html"
  }
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(res, "index_document.suffix", "index.html"),
					resource.TestCheckResourceAttr(res, "error_document.key", "404.html"),
					resource.TestCheckResourceAttr(res, "routing_rule.#", "0"),
				),
			},
			{
				Config: cfg(`
  index_document {
    suffix = "index.html"
  }
  error_document {
    key = "error.html"
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
  routing_rule {
    condition {
      http_error_code_returned_equals = "404"
    }
    redirect {
      replace_key_with = "missing.html"
    }
  }
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(res, "error_document.key", "error.html"),
					resource.TestCheckResourceAttr(res, "routing_rule.#", "2"),
					resource.TestCheckResourceAttr(res, "routing_rule.0.redirect.replace_key_prefix_with", "documents/"),
					resource.TestCheckResourceAttr(res, "routing_rule.1.condition.http_error_code_returned_equals", "404"),
					resource.TestCheckNoResourceAttr(res, "routing_rule.1.redirect.host_name"),
				),
			},
			{
				ResourceName:                         res,
				ImportState:                          true,
				ImportStateId:                        "acc-website",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
			{
				Config: cfg(`
  redirect_all_requests_to {
    host_name = "www.example.com"
    protocol  = "https"
  }
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(res, "redirect_all_requests_to.host_name", "www.example.com"),
					resource.TestCheckNoResourceAttr(res, "index_document.suffix"),
					resource.TestCheckResourceAttr(res, "routing_rule.#", "0"),
				),
			},
		},
	})
}

func TestAccSingleDataSources(t *testing.T) {
	base := `
resource "versitygw_user" "one" {
  access_key = "acc-single-one"
  secret_key = "singleonesecret"
  role       = "userplus"
  user_id    = 1234
}

resource "versitygw_bucket" "test" {
  name  = "acc-single"
  owner = versitygw_user.one.access_key
  tags  = { team = "platform" }
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: base + `
data "versitygw_user" "one" {
  access_key = versitygw_user.one.access_key
}

data "versitygw_bucket" "test" {
  name       = versitygw_bucket.test.name
  depends_on = [versitygw_bucket.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.versitygw_user.one", "secret_key", "singleonesecret"),
					resource.TestCheckResourceAttr("data.versitygw_user.one", "role", "userplus"),
					resource.TestCheckResourceAttr("data.versitygw_user.one", "user_id", "1234"),
					resource.TestCheckResourceAttr("data.versitygw_bucket.test", "owner", "acc-single-one"),
					resource.TestCheckResourceAttr("data.versitygw_bucket.test", "tags.team", "platform"),
				),
			},
			{
				Config: base + `
data "versitygw_user" "missing" {
  access_key = "acc-single-nobody"
}
`,
				ExpectError: regexp.MustCompile(`Account does not exist`),
			},
			{
				Config: base + `
data "versitygw_bucket" "missing" {
  name = "acc-single-nowhere"
}
`,
				ExpectError: regexp.MustCompile(`Bucket does not exist`),
			},
		},
	})
}
