package provider

import (
	"context"
	"fmt"
	"math"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	_ resource.Resource                   = &bucketCORSResource{}
	_ resource.ResourceWithConfigure      = &bucketCORSResource{}
	_ resource.ResourceWithImportState    = &bucketCORSResource{}
	_ resource.ResourceWithValidateConfig = &bucketCORSResource{}
)

type bucketCORSResource struct {
	client *client.Client
}

type bucketCORSModel struct {
	Bucket types.String    `tfsdk:"bucket"`
	Rules  []corsRuleModel `tfsdk:"cors_rule"`
}

type corsRuleModel struct {
	ID             types.String `tfsdk:"id"`
	AllowedHeaders types.List   `tfsdk:"allowed_headers"`
	AllowedMethods types.List   `tfsdk:"allowed_methods"`
	AllowedOrigins types.List   `tfsdk:"allowed_origins"`
	ExposeHeaders  types.List   `tfsdk:"expose_headers"`
	MaxAgeSeconds  types.Int64  `tfsdk:"max_age_seconds"`
}

// NewBucketCORSResource returns the versitygw_bucket_cors_configuration resource.
func NewBucketCORSResource() resource.Resource { return &bucketCORSResource{} }

func (r *bucketCORSResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_cors_configuration"
}

func (r *bucketCORSResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	// An optional list that is set but empty would come back absent from
	// the gateway and diff forever; require at least one element instead.
	nonEmptyStrings := []validator.List{listvalidator.SizeAtLeast(1), listvalidator.NoNullValues()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "CORS configuration of a bucket, shaped like " +
			"`aws_s3_bucket_cors_configuration`.\n\n" +
			"Rules are ordered: the gateway answers a preflight from the first rule " +
			"that matches. Destroying this resource deletes the configuration.",
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
			"cors_rule": schema.ListNestedBlock{
				MarkdownDescription: "One rule; repeat the block for more. At least one is required.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Identifier of the rule, for humans.",
							Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
						},
						"allowed_headers": schema.ListAttribute{
							ElementType:         types.StringType,
							Optional:            true,
							MarkdownDescription: "Headers a preflight may request; `*` allows any.",
							Validators:          nonEmptyStrings,
						},
						"allowed_methods": schema.ListAttribute{
							ElementType:         types.StringType,
							Required:            true,
							MarkdownDescription: "`GET`, `HEAD`, `PUT`, `POST` or `DELETE`.",
							Validators: []validator.List{
								listvalidator.SizeAtLeast(1),
								listvalidator.NoNullValues(),
								listvalidator.ValueStringsAre(stringvalidator.OneOf(client.CORSMethods...)),
							},
						},
						"allowed_origins": schema.ListAttribute{
							ElementType:         types.StringType,
							Required:            true,
							MarkdownDescription: "Origins the rule applies to; `*` allows any.",
							Validators:          nonEmptyStrings,
						},
						"expose_headers": schema.ListAttribute{
							ElementType:         types.StringType,
							Optional:            true,
							MarkdownDescription: "Response headers the browser may read.",
							Validators:          nonEmptyStrings,
						},
						"max_age_seconds": schema.Int64Attribute{
							Optional:            true,
							MarkdownDescription: "How long the browser may cache the preflight answer.",
							// The gateway parses this into an int32.
							Validators: []validator.Int64{int64validator.Between(0, math.MaxInt32)},
						},
					},
				},
			},
		},
	}
}

// ValidateConfig requires at least one rule; a ListNestedBlock cannot carry
// that in the schema without also firing on an unknown block list.
func (r *bucketCORSResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg bucketCORSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(cfg.Rules) == 0 {
		resp.Diagnostics.AddAttributeError(path.Root("cors_rule"), "Missing cors_rule",
			"Add at least one cors_rule block; the gateway rejects an empty configuration.")
	}
}

func (r *bucketCORSResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketCORSResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketCORSModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketCORSResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketCORSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rules, err := r.client.GetBucketCORS(ctx, state.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the CORS configuration", err.Error())
		return
	}
	if rules == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Rules = make([]corsRuleModel, 0, len(rules))
	for _, rule := range rules {
		m := corsRuleModel{
			ID:             types.StringNull(),
			AllowedHeaders: stringList(rule.AllowedHeaders),
			AllowedMethods: stringList(rule.AllowedMethods),
			AllowedOrigins: stringList(rule.AllowedOrigins),
			ExposeHeaders:  stringList(rule.ExposeHeaders),
			MaxAgeSeconds:  types.Int64Null(),
		}
		if rule.ID != "" {
			m.ID = types.StringValue(rule.ID)
		}
		if rule.MaxAgeSeconds != nil {
			m.MaxAgeSeconds = types.Int64Value(*rule.MaxAgeSeconds)
		}
		state.Rules = append(state.Rules, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *bucketCORSResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketCORSModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketCORSResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketCORSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteBucketCORS(ctx, state.Bucket.ValueString()); err != nil {
		resp.Diagnostics.AddError("Cannot delete the CORS configuration", err.Error())
	}
}

func (r *bucketCORSResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func (r *bucketCORSResource) put(ctx context.Context, plan bucketCORSModel, diags *diagAppender) bool {
	bucket := plan.Bucket.ValueString()
	rules := make([]client.CORSRule, 0, len(plan.Rules))
	for _, m := range plan.Rules {
		rule := client.CORSRule{
			ID:             m.ID.ValueString(),
			AllowedHeaders: stringSlice(m.AllowedHeaders),
			AllowedMethods: stringSlice(m.AllowedMethods),
			AllowedOrigins: stringSlice(m.AllowedOrigins),
			ExposeHeaders:  stringSlice(m.ExposeHeaders),
		}
		if !m.MaxAgeSeconds.IsNull() {
			v := m.MaxAgeSeconds.ValueInt64()
			rule.MaxAgeSeconds = &v
		}
		rules = append(rules, rule)
	}
	err := r.client.PutBucketCORS(ctx, bucket, rules)
	if err == nil {
		return true
	}
	if apiErr, ok := asAPIError(err); ok && apiErr.Code == "NoSuchBucket" {
		diags.AddAttributeError(path.Root("bucket"), "Bucket does not exist",
			fmt.Sprintf("The gateway has no bucket named %q. Create it with versitygw_bucket "+
				"first, and reference it so Terraform orders the two.", bucket))
		return false
	}
	diags.AddError("Cannot set the CORS configuration", err.Error())
	return false
}

// stringSlice unpacks a list of strings; null and unknown give nil.
func stringSlice(l types.List) []string {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	out := make([]string, 0, len(l.Elements()))
	for _, e := range l.Elements() {
		if s, ok := e.(types.String); ok {
			out = append(out, s.ValueString())
		}
	}
	return out
}

// stringList packs a slice; an empty one becomes null, which is what an
// omitted optional list looks like in configuration.
func stringList(values []string) types.List {
	if len(values) == 0 {
		return types.ListNull(types.StringType)
	}
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}
	return types.ListValueMust(types.StringType, elems)
}
