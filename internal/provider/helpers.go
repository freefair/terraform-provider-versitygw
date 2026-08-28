package provider

import (
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/freefair/terraform-provider-versitygw/internal/client"
)

func pathOf(name string) path.Path { return path.Root(name) }

// clientFrom converts the provider data the framework hands to a resource or
// data source. A nil providerData is normal — the framework calls Configure
// once before the provider itself is configured.
func clientFrom(providerData any, diags *diag.Diagnostics) *client.Client {
	if providerData == nil {
		return nil
	}
	c, ok := providerData.(*client.Client)
	if !ok {
		diags.AddError(
			"Unexpected provider data",
			"The provider handed something that is not a *client.Client. This is a bug in the provider.",
		)
		return nil
	}
	return c
}

// asAPIError unwraps a gateway error response, if that is what the error is.
func asAPIError(err error) (*client.APIError, bool) {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// isNotFound reports whether an error means the object does not exist.
func isNotFound(err error) bool {
	apiErr, ok := asAPIError(err)
	return ok && apiErr.IsNotFound()
}
