package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
	_ resource.Resource                   = &bucketWebsiteResource{}
	_ resource.ResourceWithConfigure      = &bucketWebsiteResource{}
	_ resource.ResourceWithImportState    = &bucketWebsiteResource{}
	_ resource.ResourceWithValidateConfig = &bucketWebsiteResource{}
)

// maxRoutingRules is the gateway's limit (s3response/website.go).
const maxRoutingRules = 50

type bucketWebsiteResource struct {
	client *client.Client
}

type bucketWebsiteModel struct {
	Bucket                types.String        `tfsdk:"bucket"`
	IndexDocument         *indexDocumentModel `tfsdk:"index_document"`
	ErrorDocument         *errorDocumentModel `tfsdk:"error_document"`
	RedirectAllRequestsTo *redirectAllModel   `tfsdk:"redirect_all_requests_to"`
	RoutingRules          []routingRuleModel  `tfsdk:"routing_rule"`
}

type indexDocumentModel struct {
	Suffix types.String `tfsdk:"suffix"`
}

type errorDocumentModel struct {
	Key types.String `tfsdk:"key"`
}

type redirectAllModel struct {
	HostName types.String `tfsdk:"host_name"`
	Protocol types.String `tfsdk:"protocol"`
}

type routingRuleModel struct {
	Condition *routingConditionModel `tfsdk:"condition"`
	Redirect  *routingRedirectModel  `tfsdk:"redirect"`
}

type routingConditionModel struct {
	HTTPErrorCodeReturnedEquals types.String `tfsdk:"http_error_code_returned_equals"`
	KeyPrefixEquals             types.String `tfsdk:"key_prefix_equals"`
}

type routingRedirectModel struct {
	HostName             types.String `tfsdk:"host_name"`
	HTTPRedirectCode     types.String `tfsdk:"http_redirect_code"`
	Protocol             types.String `tfsdk:"protocol"`
	ReplaceKeyPrefixWith types.String `tfsdk:"replace_key_prefix_with"`
	ReplaceKeyWith       types.String `tfsdk:"replace_key_with"`
}

// NewBucketWebsiteResource returns the versitygw_bucket_website_configuration resource.
func NewBucketWebsiteResource() resource.Resource { return &bucketWebsiteResource{} }

func (r *bucketWebsiteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_website_configuration"
}

func (r *bucketWebsiteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	protocol := []validator.String{stringvalidator.OneOf("http", "https")}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Static website configuration of a bucket, shaped like " +
			"`aws_s3_bucket_website_configuration`.\n\n" +
			"The gateway stores and validates it on any deployment, but only **serves** " +
			"the site when started with a website listener (`--website <addr>` / " +
			"`VGW_WEBSITE_PORT`; `--website-domain` picks virtual-host routing, without " +
			"it the full hostname is the bucket name). Exactly one of `index_document` " +
			"and `redirect_all_requests_to` is required; `error_document` and " +
			"`routing_rule` go with `index_document` only. Destroying this resource " +
			"deletes the configuration.",
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
			"index_document": schema.SingleNestedBlock{
				MarkdownDescription: "Object served for a key ending in `/`.",
				Attributes: map[string]schema.Attribute{
					"suffix": schema.StringAttribute{
						Optional:            true, // required; enforced in ValidateConfig
						MarkdownDescription: "Appended to the key, e.g. `index.html`. Must not contain `/`.",
						Validators: []validator.String{
							stringvalidator.LengthAtLeast(1),
							stringvalidator.RegexMatches(noSlashPattern, "must not contain a slash"),
						},
					},
				},
			},
			"error_document": schema.SingleNestedBlock{
				MarkdownDescription: "Object served on a 4xx.",
				Attributes: map[string]schema.Attribute{
					"key": schema.StringAttribute{
						Optional:            true, // required; enforced in ValidateConfig
						MarkdownDescription: "Key of the error page, e.g. `404.html`.",
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
				},
			},
			"redirect_all_requests_to": schema.SingleNestedBlock{
				MarkdownDescription: "Send every request to another host instead of serving objects.",
				Attributes: map[string]schema.Attribute{
					"host_name": schema.StringAttribute{
						Optional:            true, // required; enforced in ValidateConfig
						MarkdownDescription: "Target host.",
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"protocol": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "`http` or `https`; the request's own when omitted.",
						Validators:          protocol,
					},
				},
			},
			"routing_rule": schema.ListNestedBlock{
				MarkdownDescription: "Conditional redirects, evaluated in order. At most 50.",
				NestedObject: schema.NestedBlockObject{
					Blocks: map[string]schema.Block{
						"condition": schema.SingleNestedBlock{
							MarkdownDescription: "When the rule applies. At least one of the two.",
							Attributes: map[string]schema.Attribute{
								"http_error_code_returned_equals": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "A 4xx (400–417) or 5xx (500–505) status, as a string.",
								},
								"key_prefix_equals": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "Key prefix the request must match.",
									Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
								},
							},
						},
						"redirect": schema.SingleNestedBlock{
							MarkdownDescription: "Where to send the request. At least one attribute.",
							Attributes: map[string]schema.Attribute{
								"host_name": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "Target host; the request's own when omitted.",
									Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
								},
								"http_redirect_code": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "`301`, `302`, `303`, `304`, `305`, `307` or `308`.",
									Validators: []validator.String{stringvalidator.OneOf(
										"301", "302", "303", "304", "305", "307", "308")},
								},
								"protocol": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "`http` or `https`.",
									Validators:          protocol,
								},
								"replace_key_prefix_with": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "Replaces the matched `key_prefix_equals`. Exclusive with `replace_key_with`.",
									Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
								},
								"replace_key_with": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "Replaces the whole key. Exclusive with `replace_key_prefix_with`.",
									Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
								},
							},
						},
					},
				},
			},
		},
	}
}

// ValidateConfig mirrors s3response.WebsiteConfiguration.Validate upstream,
// so a configuration the gateway would refuse fails at plan time. Unknown
// values are skipped; the gateway has the last word at apply.
//
// It also carries the "required" of the single-block attributes: marking
// them Required in the schema makes the framework demand them even when
// the block itself is absent.
func (r *bucketWebsiteResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg bucketWebsiteModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	switch {
	case cfg.RedirectAllRequestsTo != nil && cfg.IndexDocument != nil:
		resp.Diagnostics.AddAttributeError(path.Root("redirect_all_requests_to"), "Conflicting blocks",
			"redirect_all_requests_to and index_document are mutually exclusive.")
	case cfg.RedirectAllRequestsTo != nil && (cfg.ErrorDocument != nil || len(cfg.RoutingRules) > 0):
		resp.Diagnostics.AddAttributeError(path.Root("redirect_all_requests_to"), "Conflicting blocks",
			"redirect_all_requests_to cannot be combined with error_document or routing_rule; "+
				"the gateway refuses that as MalformedXML.")
	case cfg.RedirectAllRequestsTo == nil && cfg.IndexDocument == nil:
		resp.Diagnostics.AddAttributeError(path.Root("index_document"), "Missing block",
			"Add index_document or redirect_all_requests_to.")
	}
	if cfg.IndexDocument != nil && cfg.IndexDocument.Suffix.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("index_document").AtName("suffix"), "Missing suffix",
			"index_document needs a suffix.")
	}
	if cfg.ErrorDocument != nil && cfg.ErrorDocument.Key.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("error_document").AtName("key"), "Missing key",
			"error_document needs a key.")
	}
	if cfg.RedirectAllRequestsTo != nil && cfg.RedirectAllRequestsTo.HostName.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("redirect_all_requests_to").AtName("host_name"), "Missing host_name",
			"redirect_all_requests_to needs a host_name.")
	}
	if len(cfg.RoutingRules) > maxRoutingRules {
		resp.Diagnostics.AddAttributeError(path.Root("routing_rule"), "Too many routing rules",
			fmt.Sprintf("The gateway allows at most %d routing rules.", maxRoutingRules))
	}
	for i, rule := range cfg.RoutingRules {
		p := path.Root("routing_rule").AtListIndex(i)
		if rule.Redirect == nil {
			resp.Diagnostics.AddAttributeError(p.AtName("redirect"), "Missing redirect",
				"Every routing_rule needs a redirect block.")
		} else if allNull(rule.Redirect.HostName, rule.Redirect.HTTPRedirectCode, rule.Redirect.Protocol,
			rule.Redirect.ReplaceKeyPrefixWith, rule.Redirect.ReplaceKeyWith) {
			resp.Diagnostics.AddAttributeError(p.AtName("redirect"), "Empty redirect",
				"Set at least one of host_name, http_redirect_code, protocol, replace_key_prefix_with, replace_key_with.")
		} else if !rule.Redirect.ReplaceKeyWith.IsNull() && !rule.Redirect.ReplaceKeyPrefixWith.IsNull() {
			resp.Diagnostics.AddAttributeError(p.AtName("redirect"), "Conflicting replacements",
				"replace_key_with and replace_key_prefix_with are mutually exclusive.")
		}
		if rule.Condition != nil {
			if allNull(rule.Condition.HTTPErrorCodeReturnedEquals, rule.Condition.KeyPrefixEquals) {
				resp.Diagnostics.AddAttributeError(p.AtName("condition"), "Empty condition",
					"Set http_error_code_returned_equals or key_prefix_equals, or drop the block.")
			}
			if code := rule.Condition.HTTPErrorCodeReturnedEquals; !code.IsNull() && !code.IsUnknown() && !validErrorCode(code.ValueString()) {
				resp.Diagnostics.AddAttributeError(p.AtName("condition").AtName("http_error_code_returned_equals"),
					"Invalid error code", "Must be 400–417 or 500–505.")
			}
		}
	}
}

func allNull(values ...types.String) bool {
	for _, v := range values {
		if !v.IsNull() {
			return false
		}
	}
	return true
}

// validErrorCode is validateErrorCode upstream.
func validErrorCode(s string) bool {
	code, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || s != strings.TrimSpace(s) {
		return false
	}
	return (code >= 400 && code <= 417) || (code >= 500 && code <= 505)
}

func (r *bucketWebsiteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketWebsiteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketWebsiteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketWebsiteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketWebsiteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg, err := r.client.GetBucketWebsite(ctx, state.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the website configuration", err.Error())
		return
	}
	if cfg == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.IndexDocument, state.ErrorDocument, state.RedirectAllRequestsTo, state.RoutingRules = nil, nil, nil, nil
	if cfg.IndexDocument != nil {
		state.IndexDocument = &indexDocumentModel{Suffix: types.StringValue(cfg.IndexDocument.Suffix)}
	}
	if cfg.ErrorDocument != nil {
		state.ErrorDocument = &errorDocumentModel{Key: types.StringValue(cfg.ErrorDocument.Key)}
	}
	if cfg.RedirectAllRequestsTo != nil {
		state.RedirectAllRequestsTo = &redirectAllModel{
			HostName: types.StringValue(cfg.RedirectAllRequestsTo.HostName),
			Protocol: optionalString(cfg.RedirectAllRequestsTo.Protocol),
		}
	}
	var rules []client.RoutingRule
	if cfg.RoutingRules != nil {
		rules = cfg.RoutingRules.Rules
	}
	for _, rule := range rules {
		m := routingRuleModel{}
		if rule.Condition != nil {
			m.Condition = &routingConditionModel{
				HTTPErrorCodeReturnedEquals: optionalString(rule.Condition.HTTPErrorCodeReturnedEquals),
				KeyPrefixEquals:             optionalString(rule.Condition.KeyPrefixEquals),
			}
		}
		if rule.Redirect != nil {
			m.Redirect = &routingRedirectModel{
				HostName:             optionalString(rule.Redirect.HostName),
				HTTPRedirectCode:     optionalString(rule.Redirect.HTTPRedirectCode),
				Protocol:             optionalString(rule.Redirect.Protocol),
				ReplaceKeyPrefixWith: optionalString(rule.Redirect.ReplaceKeyPrefixWith),
				ReplaceKeyWith:       optionalString(rule.Redirect.ReplaceKeyWith),
			}
		}
		state.RoutingRules = append(state.RoutingRules, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *bucketWebsiteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketWebsiteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketWebsiteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketWebsiteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteBucketWebsite(ctx, state.Bucket.ValueString()); err != nil {
		resp.Diagnostics.AddError("Cannot delete the website configuration", err.Error())
	}
}

func (r *bucketWebsiteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func (r *bucketWebsiteResource) put(ctx context.Context, plan bucketWebsiteModel, diags *diagAppender) bool {
	bucket := plan.Bucket.ValueString()
	cfg := client.WebsiteConfiguration{}
	if plan.IndexDocument != nil {
		cfg.IndexDocument = &client.IndexDocument{Suffix: plan.IndexDocument.Suffix.ValueString()}
	}
	if plan.ErrorDocument != nil {
		cfg.ErrorDocument = &client.ErrorDocument{Key: plan.ErrorDocument.Key.ValueString()}
	}
	if plan.RedirectAllRequestsTo != nil {
		cfg.RedirectAllRequestsTo = &client.RedirectAllRequestsTo{
			HostName: plan.RedirectAllRequestsTo.HostName.ValueString(),
			Protocol: plan.RedirectAllRequestsTo.Protocol.ValueString(),
		}
	}
	for _, m := range plan.RoutingRules {
		rule := client.RoutingRule{}
		if m.Condition != nil {
			rule.Condition = &client.RoutingRuleCondition{
				HTTPErrorCodeReturnedEquals: m.Condition.HTTPErrorCodeReturnedEquals.ValueString(),
				KeyPrefixEquals:             m.Condition.KeyPrefixEquals.ValueString(),
			}
		}
		if m.Redirect != nil {
			rule.Redirect = &client.Redirect{
				HostName:             m.Redirect.HostName.ValueString(),
				HTTPRedirectCode:     m.Redirect.HTTPRedirectCode.ValueString(),
				Protocol:             m.Redirect.Protocol.ValueString(),
				ReplaceKeyPrefixWith: m.Redirect.ReplaceKeyPrefixWith.ValueString(),
				ReplaceKeyWith:       m.Redirect.ReplaceKeyWith.ValueString(),
			}
		}
		if cfg.RoutingRules == nil {
			cfg.RoutingRules = &client.RoutingRules{}
		}
		cfg.RoutingRules.Rules = append(cfg.RoutingRules.Rules, rule)
	}
	err := r.client.PutBucketWebsite(ctx, bucket, cfg)
	if err == nil {
		return true
	}
	if apiErr, ok := asAPIError(err); ok && apiErr.Code == "NoSuchBucket" {
		diags.AddAttributeError(path.Root("bucket"), "Bucket does not exist",
			fmt.Sprintf("The gateway has no bucket named %q. Create it with versitygw_bucket "+
				"first, and reference it so Terraform orders the two.", bucket))
		return false
	}
	diags.AddError("Cannot set the website configuration", err.Error())
	return false
}

// optionalString maps the gateway's "" for an omitted element to null.
func optionalString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}
