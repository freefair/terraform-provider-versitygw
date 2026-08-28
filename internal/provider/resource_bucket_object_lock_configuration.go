package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/freefair/terraform-provider-versitygw/internal/client"
)

var (
	_ resource.Resource                   = &bucketObjectLockResource{}
	_ resource.ResourceWithConfigure      = &bucketObjectLockResource{}
	_ resource.ResourceWithImportState    = &bucketObjectLockResource{}
	_ resource.ResourceWithValidateConfig = &bucketObjectLockResource{}
)

type bucketObjectLockResource struct {
	client *client.Client
}

type bucketObjectLockModel struct {
	Bucket            types.String         `tfsdk:"bucket"`
	ObjectLockEnabled types.String         `tfsdk:"object_lock_enabled"`
	Rule              *objectLockRuleModel `tfsdk:"rule"`
}

type objectLockRuleModel struct {
	DefaultRetention *defaultRetentionModel `tfsdk:"default_retention"`
}

type defaultRetentionModel struct {
	Mode  types.String `tfsdk:"mode"`
	Days  types.Int64  `tfsdk:"days"`
	Years types.Int64  `tfsdk:"years"`
}

// NewBucketObjectLockResource returns the versitygw_bucket_object_lock_configuration resource.
func NewBucketObjectLockResource() resource.Resource { return &bucketObjectLockResource{} }

func (r *bucketObjectLockResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_object_lock_configuration"
}

func (r *bucketObjectLockResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Object lock of a bucket, shaped like " +
			"`aws_s3_bucket_object_lock_configuration`.\n\n" +
			"The gateway requires the bucket's versioning to be `Enabled` before a lock " +
			"configuration is accepted; add `depends_on` to the `versitygw_bucket_versioning` " +
			"of the same bucket. Unlike AWS, the bucket does not have to be created with " +
			"object lock — any versioned bucket qualifies.\n\n" +
			"~> **Object lock cannot be turned off.** Destroying this resource removes it " +
			"from state and leaves the configuration on the bucket; there is nothing the S3 " +
			"API offers to send. Removing `rule` in place clears the default retention " +
			"while keeping the lock enabled. While the lock is present, the gateway refuses " +
			"to change the bucket's versioning state.",
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
			"object_lock_enabled": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("Enabled"),
				MarkdownDescription: "Always `Enabled` — the only value S3 defines. Present for parity with the AWS resource.",
				Validators:          []validator.String{stringvalidator.OneOf("Enabled")},
			},
		},
		Blocks: map[string]schema.Block{
			"rule": schema.SingleNestedBlock{
				MarkdownDescription: "Default retention applied to objects written without an explicit retention.",
				Blocks: map[string]schema.Block{
					"default_retention": schema.SingleNestedBlock{
						Attributes: map[string]schema.Attribute{
							// Required-ness and the days/years choice are checked in
							// ValidateConfig: attribute validators inside a nested
							// block also fire when the block is absent, and a lock
							// without a rule is a legitimate configuration.
							"mode": schema.StringAttribute{
								Optional:            true,
								MarkdownDescription: "`GOVERNANCE` or `COMPLIANCE`. Required when `default_retention` is set.",
								Validators:          []validator.String{stringvalidator.OneOf("GOVERNANCE", "COMPLIANCE")},
							},
							"days": schema.Int64Attribute{
								Optional:            true,
								MarkdownDescription: "Retention period in days. Exactly one of `days` and `years`.",
								Validators:          []validator.Int64{int64validator.AtLeast(1)},
							},
							"years": schema.Int64Attribute{
								Optional:            true,
								MarkdownDescription: "Retention period in years. Exactly one of `days` and `years`.",
								Validators:          []validator.Int64{int64validator.AtLeast(1)},
							},
						},
					},
				},
			},
		},
	}
}

// ValidateConfig enforces what the nested block cannot: a default retention
// needs a mode and exactly one period.
func (r *bucketObjectLockResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg bucketObjectLockModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Rule == nil || cfg.Rule.DefaultRetention == nil {
		return
	}
	ret := cfg.Rule.DefaultRetention
	base := path.Root("rule").AtName("default_retention")
	if ret.Mode.IsNull() {
		resp.Diagnostics.AddAttributeError(base.AtName("mode"), "Missing retention mode",
			"default_retention needs a mode: GOVERNANCE or COMPLIANCE.")
	}
	hasDays, hasYears := !ret.Days.IsNull(), !ret.Years.IsNull()
	if hasDays == hasYears {
		resp.Diagnostics.AddAttributeError(base, "Retention period",
			"Set exactly one of days and years.")
	}
}

func (r *bucketObjectLockResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketObjectLockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketObjectLockModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketObjectLockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketObjectLockModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg, err := r.client.GetObjectLockConfiguration(ctx, state.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the object lock configuration", err.Error())
		return
	}
	if cfg == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ObjectLockEnabled = types.StringValue(cfg.ObjectLockEnabled)
	state.Rule = nil
	if cfg.Rule != nil && cfg.Rule.DefaultRetention != nil {
		ret := &defaultRetentionModel{Mode: types.StringValue(cfg.Rule.DefaultRetention.Mode)}
		if cfg.Rule.DefaultRetention.Days > 0 {
			ret.Days = types.Int64Value(int64(cfg.Rule.DefaultRetention.Days))
		}
		if cfg.Rule.DefaultRetention.Years > 0 {
			ret.Years = types.Int64Value(int64(cfg.Rule.DefaultRetention.Years))
		}
		state.Rule = &objectLockRuleModel{DefaultRetention: ret}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *bucketObjectLockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketObjectLockModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete only forgets the resource. S3 has no way to disable object lock,
// and the gateway would route a DELETE ?object-lock to DeleteBucket.
func (r *bucketObjectLockResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *bucketObjectLockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func (r *bucketObjectLockResource) put(ctx context.Context, plan bucketObjectLockModel, diags *diagAppender) bool {
	bucket := plan.Bucket.ValueString()
	cfg := client.ObjectLockConfiguration{ObjectLockEnabled: "Enabled"}
	if plan.Rule != nil && plan.Rule.DefaultRetention != nil {
		cfg.Rule = &client.ObjectLockRule{DefaultRetention: &client.DefaultRetention{
			Mode:  plan.Rule.DefaultRetention.Mode.ValueString(),
			Days:  int(plan.Rule.DefaultRetention.Days.ValueInt64()),
			Years: int(plan.Rule.DefaultRetention.Years.ValueInt64()),
		}}
	}

	err := r.client.PutObjectLockConfiguration(ctx, bucket, cfg)
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
		case "InvalidBucketState":
			diags.AddError("Versioning must be enabled first",
				fmt.Sprintf("The gateway refused the object lock configuration: %s\n\n"+
					"Enable versioning on the bucket with versitygw_bucket_versioning and add a "+
					"depends_on so it is applied before this resource.", apiErr.Message))
			return false
		}
	}
	diags.AddError("Cannot set the object lock configuration", err.Error())
	return false
}
