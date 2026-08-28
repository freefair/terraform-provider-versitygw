package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/freefair/terraform-provider-versitygw/internal/client"
)

var (
	_ resource.Resource                   = &bucketACLResource{}
	_ resource.ResourceWithConfigure      = &bucketACLResource{}
	_ resource.ResourceWithImportState    = &bucketACLResource{}
	_ resource.ResourceWithValidateConfig = &bucketACLResource{}
)

type bucketACLResource struct {
	client *client.Client
}

type bucketACLModel struct {
	Bucket              types.String              `tfsdk:"bucket"`
	ACL                 types.String              `tfsdk:"acl"`
	AccessControlPolicy *accessControlPolicyModel `tfsdk:"access_control_policy"`
}

type accessControlPolicyModel struct {
	Grants []grantModel `tfsdk:"grant"`
}

type grantModel struct {
	Permission types.String  `tfsdk:"permission"`
	Grantee    *granteeModel `tfsdk:"grantee"`
}

type granteeModel struct {
	Type types.String `tfsdk:"type"`
	ID   types.String `tfsdk:"id"`
}

// NewBucketACLResource returns the versitygw_bucket_acl resource.
func NewBucketACLResource() resource.Resource { return &bucketACLResource{} }

func (r *bucketACLResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_acl"
}

func (r *bucketACLResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The ACL of a bucket, shaped like `aws_s3_bucket_acl`: either a canned " +
			"`acl` or an explicit `access_control_policy`, never both.\n\n" +
			"~> **A fresh bucket refuses ACLs.** Its ownership is `BucketOwnerEnforced`, and " +
			"in that state every ACL write answers `AccessControlListNotSupported`. Put a " +
			"`versitygw_bucket_ownership_controls` with `ObjectWriter` or " +
			"`BucketOwnerPreferred` on the bucket first, and add `depends_on` to it here.\n\n" +
			"The owner is not configured: the gateway accepts only the bucket's actual owner " +
			"and the provider fills it in. The owner's `FULL_CONTROL` grant is implicit — " +
			"the gateway always carries it — and is not part of `grant`. Grantee IDs are " +
			"access key IDs. **Public access is only available through the canned ACLs**: the " +
			"gateway resolves every explicit grantee as an account and answers an internal " +
			"error for a group, so `grant` takes `CanonicalUser` grantees only.\n\n" +
			"Changing the bucket's `owner` resets the ACL on the gateway side; Terraform sees " +
			"the drift on the next plan. Destroying this resource only forgets it: S3 has no " +
			"delete for ACLs.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the bucket. Reference `versitygw_bucket.<name>.name`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthBetween(3, 63),
					stringvalidator.RegexMatches(bucketNamePattern, "must be a valid bucket name"),
				},
			},
			"acl": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Canned ACL: `private`, `public-read` or `public-read-write`.",
				Validators: []validator.String{stringvalidator.OneOf(
					client.ACLPrivate, client.ACLPublicRead, client.ACLPublicReadWrite)},
			},
		},
		Blocks: map[string]schema.Block{
			"access_control_policy": schema.SingleNestedBlock{
				MarkdownDescription: "Explicit grants. The owner's `FULL_CONTROL` is implicit and must not be listed.",
				Blocks: map[string]schema.Block{
					"grant": schema.SetNestedBlock{
						NestedObject: schema.NestedBlockObject{
							Attributes: map[string]schema.Attribute{
								"permission": schema.StringAttribute{
									Required:            true,
									MarkdownDescription: "`FULL_CONTROL`, `READ`, `WRITE`, `READ_ACP` or `WRITE_ACP`.",
									Validators: []validator.String{stringvalidator.OneOf(
										"FULL_CONTROL", "READ", "WRITE", "READ_ACP", "WRITE_ACP")},
								},
							},
							Blocks: map[string]schema.Block{
								"grantee": schema.SingleNestedBlock{
									Attributes: map[string]schema.Attribute{
										"type": schema.StringAttribute{
											Required: true,
											MarkdownDescription: "`CanonicalUser`. Group grantees are read back from a " +
												"canned public ACL but cannot be written explicitly.",
											Validators: []validator.String{stringvalidator.OneOf(client.GranteeCanonicalUser)},
										},
										"id": schema.StringAttribute{
											Required:            true,
											MarkdownDescription: "Access key ID of the account.",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// ValidateConfig: exactly one of acl / access_control_policy, and a grantee
// on every grant.
func (r *bucketACLResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg bucketACLModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	hasCanned := !cfg.ACL.IsNull()
	hasPolicy := cfg.AccessControlPolicy != nil
	if hasCanned == hasPolicy {
		resp.Diagnostics.AddError("Choose one ACL form",
			"Set exactly one of acl (canned) and access_control_policy (explicit grants).")
		return
	}
	if !hasPolicy {
		return
	}
	for i, g := range cfg.AccessControlPolicy.Grants {
		p := path.Root("access_control_policy").AtName("grant").AtListIndex(i)
		if g.Grantee == nil {
			resp.Diagnostics.AddAttributeError(p, "Missing grantee", "Every grant needs a grantee block.")
		}
	}
}

func (r *bucketACLResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketACLResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketACLModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketACLResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketACLModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	acl, err := r.client.GetBucketACL(ctx, state.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the bucket ACL", err.Error())
		return
	}
	if acl == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	grants := explicitGrants(acl)
	if !state.ACL.IsNull() {
		// Canned form: report the canned name the grants amount to. When
		// they amount to none, the ACL was changed by hand — show the
		// grants so the plan says what it will overwrite.
		if canned, ok := cannedFor(grants); ok {
			state.ACL = types.StringValue(canned)
			state.AccessControlPolicy = nil
			resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
			return
		}
		state.ACL = types.StringNull()
	}
	state.AccessControlPolicy = &accessControlPolicyModel{Grants: make([]grantModel, 0, len(grants))}
	for _, g := range grants {
		state.AccessControlPolicy.Grants = append(state.AccessControlPolicy.Grants, grantModel{
			Permission: types.StringValue(g.Permission),
			Grantee:    &granteeModel{Type: types.StringValue(g.Type), ID: types.StringValue(g.ID)},
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *bucketACLResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketACLModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete only forgets the resource: S3 has no delete for ACLs, and the
// gateway would route a DELETE ?acl to DeleteBucket.
func (r *bucketACLResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *bucketACLResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func (r *bucketACLResource) put(ctx context.Context, plan bucketACLModel, diags *diagAppender) bool {
	bucket := plan.Bucket.ValueString()
	var err error
	if !plan.ACL.IsNull() {
		err = r.client.PutBucketCannedACL(ctx, bucket, plan.ACL.ValueString())
	} else {
		// The gateway insists on the real owner; ask it rather than the user.
		current, gerr := r.client.GetBucketACL(ctx, bucket)
		if gerr != nil {
			diags.AddError("Cannot read the bucket ACL", gerr.Error())
			return false
		}
		if current == nil {
			err = &client.APIError{Code: "NoSuchBucket", StatusCode: 404}
		} else {
			grants := make([]client.Grant, 0, len(plan.AccessControlPolicy.Grants))
			for _, g := range plan.AccessControlPolicy.Grants {
				grants = append(grants, client.Grant{
					Type:       g.Grantee.Type.ValueString(),
					ID:         g.Grantee.ID.ValueString(),
					Permission: g.Permission.ValueString(),
				})
			}
			err = r.client.PutBucketACL(ctx, bucket, current.Owner, grants)
		}
	}
	if err == nil {
		return true
	}
	if apiErr, ok := asAPIError(err); ok {
		switch apiErr.Code {
		case "NoSuchBucket":
			diags.AddAttributeError(path.Root("bucket"), "Bucket does not exist",
				fmt.Sprintf("The gateway has no bucket named %q. Create it with versitygw_bucket "+
					"first, and reference it so Terraform orders the two.", bucket))
			return false
		case "AccessControlListNotSupported":
			diags.AddError("The bucket does not allow ACLs",
				"Its object ownership is BucketOwnerEnforced — the gateway's default for a new "+
					"bucket. Set ObjectWriter or BucketOwnerPreferred with "+
					"versitygw_bucket_ownership_controls and add a depends_on on it here.")
			return false
		case "InternalError":
			// Measured: a grant naming an account that does not exist makes
			// the gateway answer 500 — it resolves every grantee as an
			// account. Say so, rather than "internal error".
			diags.AddError("The gateway rejected the ACL with an internal error",
				"On this gateway that is what a grant for a non-existent account looks like. "+
					"Check that every grantee names an existing access key ID.\n\n"+err.Error())
			return false
		}
	}
	diags.AddError("Cannot set the bucket ACL", err.Error())
	return false
}

// explicitGrants drops the owner's FULL_CONTROL grant, which the gateway
// carries on every bucket and which the user therefore never configures.
func explicitGrants(acl *client.BucketACL) []client.Grant {
	out := make([]client.Grant, 0, len(acl.Grants))
	for _, g := range acl.Grants {
		if g.Type == client.GranteeCanonicalUser && g.ID == acl.Owner && g.Permission == "FULL_CONTROL" {
			continue
		}
		out = append(out, g)
	}
	return out
}

// cannedFor maps explicit grants back to the canned ACL that produces
// them, if one does.
func cannedFor(grants []client.Grant) (string, bool) {
	var read, write bool
	for _, g := range grants {
		if g.Type != client.GranteeGroup || g.ID != client.GroupAllUsers {
			return "", false
		}
		switch g.Permission {
		case "READ":
			read = true
		case "WRITE":
			write = true
		default:
			return "", false
		}
	}
	switch {
	case !read && !write && len(grants) == 0:
		return client.ACLPrivate, true
	case read && !write:
		return client.ACLPublicRead, true
	case read && write:
		return client.ACLPublicReadWrite, true
	}
	return "", false
}
