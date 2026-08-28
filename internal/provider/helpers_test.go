package provider

import (
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/freefair/terraform-provider-versitygw/internal/client"
)

func TestClientFromRejectsForeignProviderData(t *testing.T) {
	var diags diag.Diagnostics
	if c := clientFrom(nil, &diags); c != nil || diags.HasError() {
		t.Error("nil provider data must be a quiet no-op")
	}
	if c := clientFrom("not a client", &diags); c != nil || !diags.HasError() {
		t.Error("foreign provider data must be reported")
	}
	if pathOf("x").String() != "x" {
		t.Errorf("pathOf = %q", pathOf("x"))
	}
}

func TestIsNotFoundSeesThroughWrapping(t *testing.T) {
	wrapped := errors.Join(errors.New("context"), &client.APIError{Code: "NoSuchBucket", StatusCode: 404})
	if !isNotFound(wrapped) {
		t.Error("wrapped not-found not recognised")
	}
	if isNotFound(errors.New("plain")) {
		t.Error("plain error mistaken for not-found")
	}
}
