package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/freefair/terraform-provider-versitygw/internal/client"
)

var (
	_ datasource.DataSource              = &bucketsDataSource{}
	_ datasource.DataSourceWithConfigure = &bucketsDataSource{}
)

type bucketsDataSource struct {
	client *client.Client
}

type bucketEntryModel struct {
	Name  types.String `tfsdk:"name"`
	Owner types.String `tfsdk:"owner"`
}

type bucketsDataSourceModel struct {
	Buckets []bucketEntryModel `tfsdk:"buckets"`
}

// NewBucketsDataSource returns the versitygw_buckets data source.
func NewBucketsDataSource() datasource.DataSource { return &bucketsDataSource{} }

func (d *bucketsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_buckets"
}

func (d *bucketsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Every bucket on the gateway with its owning account.\n\n" +
			"Useful for finding buckets that exist but are not managed here — an account " +
			"deleted outside Terraform leaves its buckets behind with an owner nobody can " +
			"authenticate as, and this is where they show up.",
		Attributes: map[string]schema.Attribute{
			"buckets": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":  schema.StringAttribute{Computed: true, MarkdownDescription: "Bucket name."},
						"owner": schema.StringAttribute{Computed: true, MarkdownDescription: "Access key ID of the owning account."},
					},
				},
			},
		},
	}
}

func (d *bucketsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *bucketsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	buckets, err := d.client.ListBuckets(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot list the buckets", err.Error())
		return
	}

	state := bucketsDataSourceModel{Buckets: make([]bucketEntryModel, 0, len(buckets))}
	for _, b := range buckets {
		state.Buckets = append(state.Buckets, bucketEntryModel{
			Name:  types.StringValue(b.Name),
			Owner: types.StringValue(b.Owner),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
