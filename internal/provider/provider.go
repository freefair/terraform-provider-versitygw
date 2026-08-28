package provider

import (
	"context"
	"os"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/freefair/terraform-provider-versitygw/internal/client"
)

var _ provider.Provider = &versitygwProvider{}

type versitygwProvider struct {
	version string
}

type providerModel struct {
	Endpoint      types.String `tfsdk:"endpoint"`
	AdminEndpoint types.String `tfsdk:"admin_endpoint"`
	AccessKey     types.String `tfsdk:"access_key"`
	SecretKey     types.String `tfsdk:"secret_key"`
	Region        types.String `tfsdk:"region"`
	Insecure      types.Bool   `tfsdk:"insecure"`
}

// New returns the provider factory the plugin server serves.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &versitygwProvider{version: version}
	}
}

func (p *versitygwProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "versitygw"
	resp.Version = p.version
}

func (p *versitygwProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages accounts, buckets and bucket policies on a " +
			"[Versity S3 Gateway](https://github.com/versity/versitygw).\n\n" +
			"The gateway keeps account management behind a separate admin API, so this " +
			"provider needs credentials for an `admin` or `root` account — a regular " +
			"user cannot create buckets or accounts.\n\n" +
			"Every argument can be left out of the configuration and supplied through the " +
			"environment instead, which is the recommended way to keep credentials out of " +
			"version control.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "S3 API endpoint, for example `https://s3.example.com`. " +
					"Falls back to `VERSITYGW_ENDPOINT`.",
			},
			"admin_endpoint": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Admin API endpoint. Falls back to `VERSITYGW_ADMIN_ENDPOINT`, " +
					"and then to `endpoint`.\n\n" +
					"A gateway started without `--admin-port` serves the admin routes on the " +
					"S3 listener, in which case the fallback is correct. A gateway started " +
					"with one serves them nowhere else, and this has to be set.",
			},
			"access_key": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Access key ID of an `admin` or `root` account. " +
					"Falls back to `VERSITYGW_ACCESS_KEY`.",
			},
			"secret_key": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Secret key for `access_key`. Falls back to `VERSITYGW_SECRET_KEY`.",
			},
			"region": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Region string requests are signed with. Must match the " +
					"gateway's `--region`. Falls back to `VERSITYGW_REGION`, then `us-east-1`.",
			},
			"insecure": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Skip TLS certificate verification. Falls back to " +
					"`VERSITYGW_INSECURE`. Turning this on removes the only protection a " +
					"certificate provides; prefer trusting the issuing CA.",
			},
		},
	}
}

func (p *versitygwProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Configuration wins over the environment; unknown values mean another
	// resource has to be applied first, which the framework cannot defer here.
	for name, attr := range map[string]types.String{
		"endpoint":       cfg.Endpoint,
		"admin_endpoint": cfg.AdminEndpoint,
		"access_key":     cfg.AccessKey,
		"secret_key":     cfg.SecretKey,
		"region":         cfg.Region,
	} {
		if attr.IsUnknown() {
			resp.Diagnostics.AddAttributeError(
				pathOf(name),
				"Provider configuration is not known at plan time",
				"The "+name+" of the versitygw provider comes from a value that is only "+
					"available after apply. Set it statically, or supply it through the "+
					"environment instead.",
			)
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	conf := client.Config{
		Endpoint:      firstNonEmpty(cfg.Endpoint.ValueString(), os.Getenv("VERSITYGW_ENDPOINT")),
		AdminEndpoint: firstNonEmpty(cfg.AdminEndpoint.ValueString(), os.Getenv("VERSITYGW_ADMIN_ENDPOINT")),
		AccessKey:     firstNonEmpty(cfg.AccessKey.ValueString(), os.Getenv("VERSITYGW_ACCESS_KEY")),
		SecretKey:     firstNonEmpty(cfg.SecretKey.ValueString(), os.Getenv("VERSITYGW_SECRET_KEY")),
		Region:        firstNonEmpty(cfg.Region.ValueString(), os.Getenv("VERSITYGW_REGION")),
	}
	if !cfg.Insecure.IsNull() {
		conf.Insecure = cfg.Insecure.ValueBool()
	} else if env := os.Getenv("VERSITYGW_INSECURE"); env != "" {
		if v, err := strconv.ParseBool(env); err == nil {
			conf.Insecure = v
		}
	}

	if conf.Endpoint == "" {
		resp.Diagnostics.AddAttributeError(pathOf("endpoint"), "Missing endpoint",
			"Set the endpoint argument or the VERSITYGW_ENDPOINT environment variable.")
	}
	if conf.AccessKey == "" || conf.SecretKey == "" {
		resp.Diagnostics.AddError("Missing credentials",
			"The provider needs the access key and secret key of an admin or root account. "+
				"Set access_key/secret_key, or VERSITYGW_ACCESS_KEY/VERSITYGW_SECRET_KEY.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.New(conf)
	if err != nil {
		resp.Diagnostics.AddError("Cannot build the gateway client", err.Error())
		return
	}

	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *versitygwProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewUserResource,
		NewBucketResource,
		NewBucketPolicyResource,
		NewBucketVersioningResource,
		NewBucketObjectLockResource,
		NewBucketOwnershipResource,
		NewBucketACLResource,
	}
}

func (p *versitygwProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewUsersDataSource,
		NewBucketsDataSource,
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
