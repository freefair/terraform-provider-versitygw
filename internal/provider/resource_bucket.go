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
	_ resource.Resource                = &bucketResource{}
	_ resource.ResourceWithConfigure   = &bucketResource{}
	_ resource.ResourceWithImportState = &bucketResource{}
)

type bucketResource struct {
	client *client.Client
}

type bucketModel struct {
	Name  types.String `tfsdk:"name"`
	Owner types.String `tfsdk:"owner"`
}

// NewBucketResource returns the versitygw_bucket resource.
func NewBucketResource() resource.Resource { return &bucketResource{} }

func (r *bucketResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket"
}

func (r *bucketResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A bucket on the gateway, together with the account that owns it.\n\n" +
			"On the posix and scoutfs backends a bucket is a directory under the gateway's " +
			"data root, and deleting one requires it to be empty — a destroy against a " +
			"bucket that still holds objects fails rather than removing them.\n\n" +
			"~> **Changing `owner` discards the bucket's ACL and policy.** The gateway " +
			"applies a fresh default ACL for the new owner instead of migrating the old " +
			"one, so anything set on the bucket has to be reapplied afterwards.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Bucket name. Immutable — the gateway has no rename.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthBetween(3, 63),
					stringvalidator.RegexMatches(
						bucketNamePattern,
						"must be lower case, start and end alphanumerically, and contain only letters, digits, dots and hyphens",
					),
				},
			},
			"owner": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Access key ID of the owning account. Reference a " +
					"`versitygw_user` rather than repeating the string, so that destroy " +
					"order and replacement stay correct.",
			},
		},
	}
}

func (r *bucketResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()
	if err := r.client.CreateBucket(ctx, name, plan.Owner.ValueString()); err != nil {
		if apiErr, ok := asAPIError(err); ok && apiErr.IsConflict() {
			resp.Diagnostics.AddError(
				"Bucket already exists",
				fmt.Sprintf("The gateway already has a bucket named %q. Bucket names are "+
					"shared across every account. Import it instead:\n\n"+
					"  terraform import versitygw_bucket.example %s", name, name),
			)
			return
		}
		resp.Diagnostics.AddError("Cannot create the bucket", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket, err := r.client.GetBucket(ctx, state.Name.ValueString())
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Cannot read the bucket", err.Error())
		return
	}
	if bucket == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state.Owner = types.StringValue(bucket.Owner)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *bucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// `name` requires replacement, so an update can only be an ownership
	// change — which resets ACL and policy on the gateway side.
	if err := r.client.ChangeBucketOwner(ctx, plan.Name.ValueString(), plan.Owner.ValueString()); err != nil {
		resp.Diagnostics.AddError("Cannot change the bucket owner", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The admin API has no delete route; this one goes to the S3 API, which
	// refuses a bucket that still holds objects. That refusal is deliberate —
	// a destroy must not be a way to lose data by accident.
	if err := r.client.DeleteBucket(ctx, state.Name.ValueString()); err != nil {
		if isNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Cannot delete the bucket", err.Error())
	}
}

func (r *bucketResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
