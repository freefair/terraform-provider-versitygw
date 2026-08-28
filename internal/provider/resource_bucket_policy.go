package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
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
	_ resource.Resource                = &bucketPolicyResource{}
	_ resource.ResourceWithConfigure   = &bucketPolicyResource{}
	_ resource.ResourceWithImportState = &bucketPolicyResource{}
)

type bucketPolicyResource struct {
	client *client.Client
}

type bucketPolicyModel struct {
	Bucket types.String         `tfsdk:"bucket"`
	Policy jsontypes.Normalized `tfsdk:"policy"`
}

// NewBucketPolicyResource returns the versitygw_bucket_policy resource.
func NewBucketPolicyResource() resource.Resource { return &bucketPolicyResource{} }

func (r *bucketPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_policy"
}

func (r *bucketPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The policy of a bucket — the one way to let an account reach a bucket " +
			"it does not own.\n\n" +
			"Principals are **access key IDs**, not ARNs: the gateway has no canonical user " +
			"IDs. Reference `versitygw_user.<name>.access_key` so the account exists before " +
			"the policy names it. Resources are `arn:aws:s3:::<bucket>` and " +
			"`arn:aws:s3:::<bucket>/*`; the gateway rejects a policy whose resources name a " +
			"different bucket.\n\n" +
			"~> **Changing the bucket's `owner` deletes its policy.** The gateway applies a " +
			"fresh default ACL for the new owner and drops the policy with it. Terraform " +
			"sees the policy as missing on the next plan and recreates it — nothing is " +
			"silently reapplied.\n\n" +
			"A bucket carries at most one policy. Putting a second `versitygw_bucket_policy` " +
			"on the same bucket makes the two overwrite each other on every apply.",
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
			"policy": schema.StringAttribute{
				CustomType: jsontypes.NormalizedType{},
				Required:   true,
				MarkdownDescription: "Policy document as JSON, typically from `jsonencode()`. " +
					"Compared semantically, so key order and whitespace do not produce a diff.",
			},
		},
	}
}

func (r *bucketPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policy, err := r.client.GetBucketPolicy(ctx, state.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the bucket policy", err.Error())
		return
	}
	if policy == nil {
		// No policy, or no bucket — either way there is nothing to manage.
		resp.State.RemoveResource(ctx)
		return
	}

	state.Policy = jsontypes.NewNormalizedValue(string(policy))
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *bucketPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// `bucket` requires replacement, so an update is always a new document
	// for the same bucket — and a PUT replaces whatever is there.
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteBucketPolicy(ctx, state.Bucket.ValueString()); err != nil {
		resp.Diagnostics.AddError("Cannot delete the bucket policy", err.Error())
	}
}

func (r *bucketPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

// put sends the document and turns the gateway's answers into diagnostics a
// reader can act on. Returns false when a diagnostic was added.
func (r *bucketPolicyResource) put(ctx context.Context, plan bucketPolicyModel, diags *diagAppender) bool {
	bucket := plan.Bucket.ValueString()
	err := r.client.PutBucketPolicy(ctx, bucket, []byte(plan.Policy.ValueString()))
	if err == nil {
		return true
	}
	if apiErr, ok := asAPIError(err); ok {
		switch {
		case apiErr.IsNotFound():
			diags.AddAttributeError(path.Root("bucket"), "Bucket does not exist",
				fmt.Sprintf("The gateway has no bucket named %q. Create it with versitygw_bucket "+
					"first, and reference it so Terraform orders the two.", bucket))
			return false
		case apiErr.Code == "MalformedPolicy":
			// The gateway names what it disliked; that message is the
			// useful part, not a generic "invalid policy".
			diags.AddAttributeError(path.Root("policy"), "The gateway rejected the policy", apiErr.Message)
			return false
		}
	}
	diags.AddError("Cannot set the bucket policy", err.Error())
	return false
}
