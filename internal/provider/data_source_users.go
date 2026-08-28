package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/freefair/terraform-provider-versitygw/internal/client"
)

var (
	_ datasource.DataSource              = &usersDataSource{}
	_ datasource.DataSourceWithConfigure = &usersDataSource{}
)

type usersDataSource struct {
	client *client.Client
}

type userEntryModel struct {
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
	Role      types.String `tfsdk:"role"`
	UserID    types.Int64  `tfsdk:"user_id"`
	GroupID   types.Int64  `tfsdk:"group_id"`
	ProjectID types.Int64  `tfsdk:"project_id"`
}

type usersDataSourceModel struct {
	Users []userEntryModel `tfsdk:"users"`
}

// NewUsersDataSource returns the versitygw_users data source.
func NewUsersDataSource() datasource.DataSource { return &usersDataSource{} }

func (d *usersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *usersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Every account in the gateway's IAM service.\n\n" +
			"~> The gateway returns each account **with its secret key**, so this data " +
			"source writes every secret on the gateway into the state of whatever root " +
			"reads it. Use it to audit, not to wire credentials into another resource.\n\n" +
			"The root account is absent: it is configured on the command line and never " +
			"stored in the IAM service.",
		Attributes: map[string]schema.Attribute{
			"users": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"access_key": schema.StringAttribute{Computed: true, MarkdownDescription: "Access key ID."},
						"secret_key": schema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "Secret key, as stored by the gateway."},
						"role":       schema.StringAttribute{Computed: true, MarkdownDescription: "`user`, `userplus` or `admin`."},
						"user_id":    schema.Int64Attribute{Computed: true, MarkdownDescription: "POSIX UID."},
						"group_id":   schema.Int64Attribute{Computed: true, MarkdownDescription: "POSIX GID."},
						"project_id": schema.Int64Attribute{Computed: true, MarkdownDescription: "Project ID."},
					},
				},
			},
		},
	}
}

func (d *usersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *usersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	accounts, err := d.client.ListUsers(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot list the accounts", err.Error())
		return
	}

	state := usersDataSourceModel{Users: make([]userEntryModel, 0, len(accounts))}
	for _, a := range accounts {
		state.Users = append(state.Users, userEntryModel{
			AccessKey: types.StringValue(a.Access),
			SecretKey: types.StringValue(a.Secret),
			Role:      types.StringValue(a.Role),
			UserID:    types.Int64Value(int64(a.UserID)),
			GroupID:   types.Int64Value(int64(a.GroupID)),
			ProjectID: types.Int64Value(int64(a.ProjectID)),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
