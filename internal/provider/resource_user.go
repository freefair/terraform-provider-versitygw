package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/freefair/terraform-provider-versitygw/internal/client"
)

var (
	_ resource.Resource                = &userResource{}
	_ resource.ResourceWithConfigure   = &userResource{}
	_ resource.ResourceWithImportState = &userResource{}
)

type userResource struct {
	client *client.Client
}

type userModel struct {
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
	Role      types.String `tfsdk:"role"`
	UserID    types.Int64  `tfsdk:"user_id"`
	GroupID   types.Int64  `tfsdk:"group_id"`
	ProjectID types.Int64  `tfsdk:"project_id"`
}

// NewUserResource returns the versitygw_user resource.
func NewUserResource() resource.Resource { return &userResource{} }

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An account in the gateway's IAM service.\n\n" +
			"The account **is** its access key ID — the gateway offers no rename — so " +
			"changing `access_key` replaces the account. Buckets it owned keep pointing at " +
			"the old ID until they are updated too, which is why the owner of a " +
			"`versitygw_bucket` should reference this resource rather than repeat a string.\n\n" +
			"The root account configured on the gateway's command line is not managed here. " +
			"It is never written to the IAM service and appears in no listing, so a resource " +
			"describing it would read back as missing on every plan.",
		Attributes: map[string]schema.Attribute{
			"access_key": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Access key ID. Also the account's identity; changing it replaces the account.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(3),
				},
			},
			"secret_key": schema.StringAttribute{
				Required:  true,
				Sensitive: true,
				MarkdownDescription: "Secret key. Stored in state, and returned by the gateway's " +
					"own user listing — which is what lets the provider detect a key changed " +
					"outside Terraform.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(8),
				},
			},
			"role": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(client.RoleUser),
				MarkdownDescription: "One of `user`, `userplus`, `admin`. Defaults to `user`.\n\n" +
					"A `user` cannot create buckets and reaches only what it owns, which is " +
					"what makes one account per consumer a real boundary. `userplus` adds " +
					"bucket policy support. `admin` can create buckets and manage accounts.",
				Validators: []validator.String{
					stringvalidator.OneOf(client.Roles()...),
				},
			},
			"user_id": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(0),
				MarkdownDescription: "POSIX UID the gateway creates this account's objects as. " +
					"Only meaningful on the posix and scoutfs backends.",
			},
			"group_id": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				MarkdownDescription: "POSIX GID, with the same caveat as `user_id`.",
			},
			"project_id": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				MarkdownDescription: "Project ID, used for filesystem project quotas where the backend supports them.",
			},
		},
	}
}

func (r *userResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	acct := client.Account{
		Access:    plan.AccessKey.ValueString(),
		Secret:    plan.SecretKey.ValueString(),
		Role:      plan.Role.ValueString(),
		UserID:    int(plan.UserID.ValueInt64()),
		GroupID:   int(plan.GroupID.ValueInt64()),
		ProjectID: int(plan.ProjectID.ValueInt64()),
	}

	if err := r.client.CreateUser(ctx, acct); err != nil {
		if apiErr, ok := asAPIError(err); ok && apiErr.IsConflict() {
			resp.Diagnostics.AddError(
				"Account already exists",
				fmt.Sprintf("The gateway already has an account with access key ID %q. "+
					"Import it instead:\n\n  terraform import versitygw_user.example %s",
					acct.Access, acct.Access),
			)
			return
		}
		resp.Diagnostics.AddError("Cannot create the account", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	acct, err := r.client.GetUser(ctx, state.AccessKey.ValueString())
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Cannot read the account", err.Error())
		return
	}
	if acct == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state.SecretKey = types.StringValue(acct.Secret)
	state.Role = types.StringValue(acct.Role)
	state.UserID = types.Int64Value(int64(acct.UserID))
	state.GroupID = types.Int64Value(int64(acct.GroupID))
	state.ProjectID = types.Int64Value(int64(acct.ProjectID))

	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	secret := plan.SecretKey.ValueString()
	userID := int(plan.UserID.ValueInt64())
	groupID := int(plan.GroupID.ValueInt64())
	projectID := int(plan.ProjectID.ValueInt64())

	props := client.MutableProps{
		Secret:    &secret,
		Role:      plan.Role.ValueString(),
		UserID:    &userID,
		GroupID:   &groupID,
		ProjectID: &projectID,
	}

	if err := r.client.UpdateUser(ctx, plan.AccessKey.ValueString(), props); err != nil {
		resp.Diagnostics.AddError("Cannot update the account", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Deleting an account does NOT delete the buckets it owned. They stay on
	// the gateway with an owner nobody can authenticate as, until an admin
	// reassigns them. Terraform destroys buckets before their owner because
	// versitygw_bucket depends on this resource.
	if err := r.client.DeleteUser(ctx, state.AccessKey.ValueString()); err != nil {
		if isNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Cannot delete the account", err.Error())
	}
}

func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("access_key"), req, resp)
}
