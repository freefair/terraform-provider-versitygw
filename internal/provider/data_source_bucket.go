package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/freefair/terraform-provider-versitygw/internal/client"
)

var (
	_ datasource.DataSource              = &bucketDataSource{}
	_ datasource.DataSourceWithConfigure = &bucketDataSource{}
)

type bucketDataSource struct {
	client *client.Client
}

// NewBucketDataSource returns the versitygw_bucket data source.
func NewBucketDataSource() datasource.DataSource { return &bucketDataSource{} }

func (d *bucketDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket"
}

func (d *bucketDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One bucket, looked up by name — the counterpart of `data.aws_s3_bucket`. " +
			"A missing bucket is an error, not an empty result, so a typo cannot turn " +
			"into a plan against nothing.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Bucket name.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(3, 63),
					stringvalidator.RegexMatches(bucketNamePattern, "must be a valid bucket name"),
				},
			},
			"owner": schema.StringAttribute{Computed: true, MarkdownDescription: "Access key ID of the owning account."},
			"tags": schema.MapAttribute{
				ElementType:         types.StringType,
				Computed:            true,
				MarkdownDescription: "Tags on the bucket; empty when it has none.",
			},
		},
	}
}

func (d *bucketDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *bucketDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg bucketModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := cfg.Name.ValueString()

	bucket, err := d.client.GetBucket(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the bucket", err.Error())
		return
	}
	if bucket == nil {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Bucket does not exist",
			fmt.Sprintf("The gateway has no bucket named %q. Check the spelling or create it "+
				"with versitygw_bucket.", name))
		return
	}
	tags, err := d.client.GetBucketTagging(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the bucket tags", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, bucketModel{
		Name:  types.StringValue(bucket.Name),
		Owner: types.StringValue(bucket.Owner),
		Tags:  tagsValue(tags),
	})...)
}
