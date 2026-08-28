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
	_ datasource.DataSource              = &userDataSource{}
	_ datasource.DataSourceWithConfigure = &userDataSource{}
)

type userDataSource struct {
	client *client.Client
}

// NewUserDataSource returns the versitygw_user data source.
func NewUserDataSource() datasource.DataSource { return &userDataSource{} }

func (d *userDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One account in the gateway's IAM service, looked up by access key ID. " +
			"A missing account is an error, not an empty result.\n\n" +
			"~> The gateway hands out the secret key with the account, so reading this " +
			"data source writes that secret into state — as `versitygw_user` does.\n\n" +
			"The root account cannot be looked up: it lives on the command line, not in " +
			"the IAM service.",
		Attributes: map[string]schema.Attribute{
			"access_key": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Access key ID of the account.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"secret_key": schema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "Secret key, as stored by the gateway."},
			"role":       schema.StringAttribute{Computed: true, MarkdownDescription: "`user`, `userplus` or `admin`."},
			"user_id":    schema.Int64Attribute{Computed: true, MarkdownDescription: "POSIX UID."},
			"group_id":   schema.Int64Attribute{Computed: true, MarkdownDescription: "POSIX GID."},
			"project_id": schema.Int64Attribute{Computed: true, MarkdownDescription: "Project ID."},
		},
	}
}

func (d *userDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg userEntryModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	access := cfg.AccessKey.ValueString()

	account, err := d.client.GetUser(ctx, access)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the account", err.Error())
		return
	}
	if account == nil {
		resp.Diagnostics.AddAttributeError(path.Root("access_key"), "Account does not exist",
			fmt.Sprintf("The gateway has no account with access key ID %q. The root account "+
				"never appears here; for any other, check the spelling or create it with "+
				"versitygw_user.", access))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, userEntryModel{
		AccessKey: types.StringValue(account.Access),
		SecretKey: types.StringValue(account.Secret),
		Role:      types.StringValue(account.Role),
		UserID:    types.Int64Value(int64(account.UserID)),
		GroupID:   types.Int64Value(int64(account.GroupID)),
		ProjectID: types.Int64Value(int64(account.ProjectID)),
	})...)
}
