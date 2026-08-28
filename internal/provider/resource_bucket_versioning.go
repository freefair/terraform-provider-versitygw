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
	_ resource.Resource                = &bucketVersioningResource{}
	_ resource.ResourceWithConfigure   = &bucketVersioningResource{}
	_ resource.ResourceWithImportState = &bucketVersioningResource{}
)

type bucketVersioningResource struct {
	client *client.Client
}

type bucketVersioningModel struct {
	Bucket                  types.String                  `tfsdk:"bucket"`
	VersioningConfiguration *versioningConfigurationModel `tfsdk:"versioning_configuration"`
}

type versioningConfigurationModel struct {
	Status types.String `tfsdk:"status"`
}

// NewBucketVersioningResource returns the versitygw_bucket_versioning resource.
func NewBucketVersioningResource() resource.Resource { return &bucketVersioningResource{} }

func (r *bucketVersioningResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_versioning"
}

func (r *bucketVersioningResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Versioning of a bucket, shaped like `aws_s3_bucket_versioning`.\n\n" +
			"On the posix and scoutfs backends versioning only exists when the gateway was " +
			"started with `--versioning-dir` (`VGW_VERSIONING_DIR`); without it the gateway " +
			"answers `VersioningNotConfigured` and this resource reports that.\n\n" +
			"~> **Versioning cannot be turned off, only suspended.** That is S3 semantics the " +
			"gateway follows. Destroying this resource removes it from state and leaves the " +
			"bucket as it is — there is nothing to send. Set `status = \"Suspended\"` to stop " +
			"new versions from being created.\n\n" +
			"While a `versitygw_bucket_object_lock_configuration` is present on the bucket, the " +
			"gateway refuses to change the versioning state.",
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
			"versioning_configuration": schema.SingleNestedBlock{
				MarkdownDescription: "The versioning state.",
				Attributes: map[string]schema.Attribute{
					"status": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "`Enabled` or `Suspended`.",
						Validators: []validator.String{
							stringvalidator.OneOf(client.VersioningEnabled, client.VersioningSuspended),
						},
					},
				},
			},
		},
	}
}

func (r *bucketVersioningResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketVersioningResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketVersioningModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketVersioningResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketVersioningModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := r.client.GetBucketVersioning(ctx, state.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the bucket versioning", err.Error())
		return
	}
	if status == "" {
		// Never configured, or no bucket. Either way there is nothing here
		// that this resource put there.
		resp.State.RemoveResource(ctx)
		return
	}

	state.VersioningConfiguration = &versioningConfigurationModel{Status: types.StringValue(status)}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *bucketVersioningResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketVersioningModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete only forgets the resource. S3 has no "off" for versioning, and the
// gateway would route a DELETE ?versioning to DeleteBucket.
func (r *bucketVersioningResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *bucketVersioningResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func (r *bucketVersioningResource) put(ctx context.Context, plan bucketVersioningModel, diags *diagAppender) bool {
	bucket := plan.Bucket.ValueString()
	err := r.client.PutBucketVersioning(ctx, bucket, plan.VersioningConfiguration.Status.ValueString())
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
		case "VersioningNotConfigured":
			diags.AddError("The gateway has no versioning directory",
				"On the posix and scoutfs backends bucket versioning only exists when the gateway "+
					"was started with --versioning-dir (VGW_VERSIONING_DIR). This gateway was not.")
			return false
		}
	}
	diags.AddError("Cannot set the bucket versioning", err.Error())
	return false
}
