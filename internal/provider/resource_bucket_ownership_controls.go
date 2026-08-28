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
	_ resource.Resource                   = &bucketOwnershipResource{}
	_ resource.ResourceWithConfigure      = &bucketOwnershipResource{}
	_ resource.ResourceWithImportState    = &bucketOwnershipResource{}
	_ resource.ResourceWithValidateConfig = &bucketOwnershipResource{}
)

type bucketOwnershipResource struct {
	client *client.Client
}

type bucketOwnershipModel struct {
	Bucket types.String        `tfsdk:"bucket"`
	Rule   *ownershipRuleModel `tfsdk:"rule"`
}

type ownershipRuleModel struct {
	ObjectOwnership types.String `tfsdk:"object_ownership"`
}

// NewBucketOwnershipResource returns the versitygw_bucket_ownership_controls resource.
func NewBucketOwnershipResource() resource.Resource { return &bucketOwnershipResource{} }

func (r *bucketOwnershipResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_ownership_controls"
}

func (r *bucketOwnershipResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Object ownership of a bucket, shaped like " +
			"`aws_s3_bucket_ownership_controls`.\n\n" +
			"A fresh bucket is `BucketOwnerEnforced`, which **disables ACLs**: every " +
			"`PUT ?acl` answers `AccessControlListNotSupported`. Set `ObjectWriter` or " +
			"`BucketOwnerPreferred` here before using `versitygw_bucket_acl`, and add " +
			"`depends_on` so the order holds.\n\n" +
			"Destroying this resource deletes the controls; the gateway then reports none " +
			"at all rather than falling back to its default.",
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
		},
		Blocks: map[string]schema.Block{
			"rule": schema.SingleNestedBlock{
				MarkdownDescription: "The ownership rule.",
				Attributes: map[string]schema.Attribute{
					"object_ownership": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "`BucketOwnerEnforced`, `BucketOwnerPreferred` or `ObjectWriter`.",
						Validators: []validator.String{stringvalidator.OneOf(
							client.OwnershipBucketOwnerEnforced, client.OwnershipBucketOwnerPreferred, client.OwnershipObjectWriter)},
					},
				},
			},
		},
	}
}

// ValidateConfig requires the block; a SingleNestedBlock cannot be marked
// required in the schema.
func (r *bucketOwnershipResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg bucketOwnershipModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.Rule == nil {
		resp.Diagnostics.AddAttributeError(path.Root("rule"), "Missing rule",
			"Add a rule block with object_ownership.")
	}
}

func (r *bucketOwnershipResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketOwnershipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketOwnershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketOwnershipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketOwnershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ownership, err := r.client.GetBucketOwnershipControls(ctx, state.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the ownership controls", err.Error())
		return
	}
	if ownership == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Rule = &ownershipRuleModel{ObjectOwnership: types.StringValue(ownership)}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *bucketOwnershipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketOwnershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketOwnershipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketOwnershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteBucketOwnershipControls(ctx, state.Bucket.ValueString()); err != nil {
		resp.Diagnostics.AddError("Cannot delete the ownership controls", err.Error())
	}
}

func (r *bucketOwnershipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func (r *bucketOwnershipResource) put(ctx context.Context, plan bucketOwnershipModel, diags *diagAppender) bool {
	bucket := plan.Bucket.ValueString()
	err := r.client.PutBucketOwnershipControls(ctx, bucket, plan.Rule.ObjectOwnership.ValueString())
	if err == nil {
		return true
	}
	if apiErr, ok := asAPIError(err); ok && apiErr.Code == "NoSuchBucket" {
		diags.AddAttributeError(path.Root("bucket"), "Bucket does not exist",
			fmt.Sprintf("The gateway has no bucket named %q. Create it with versitygw_bucket "+
				"first, and reference it so Terraform orders the two.", bucket))
		return false
	}
	diags.AddError("Cannot set the ownership controls", err.Error())
	return false
}
