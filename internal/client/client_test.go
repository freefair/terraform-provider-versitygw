package client

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient points a client at a stub and returns both. The stub records
// what the client actually sent, which is the point of these tests: the wire
// format is upstream's, and a silent change to it is what would break the
// provider in production.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := New(Config{
		Endpoint:  srv.URL,
		AccessKey: "testaccess",
		SecretKey: "testsecret",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func TestNewAppliesDefaults(t *testing.T) {
	c, err := New(Config{
		Endpoint:  "http://gateway.example.com/",
		AccessKey: "a",
		SecretKey: "s",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Endpoint(); got != "http://gateway.example.com" {
		t.Errorf("trailing slash not trimmed: %q", got)
	}
	// A gateway without --admin-port serves the admin routes on the S3
	// listener, so the fallback has to be the S3 endpoint, not an error.
	if got := c.AdminEndpoint(); got != "http://gateway.example.com" {
		t.Errorf("admin endpoint did not fall back to the S3 endpoint: %q", got)
	}
	if c.cfg.Region != DefaultRegion {
		t.Errorf("region default = %q, want %q", c.cfg.Region, DefaultRegion)
	}
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	cases := map[string]Config{
		"no endpoint":  {AccessKey: "a", SecretKey: "s"},
		"no access":    {Endpoint: "http://x", SecretKey: "s"},
		"no secret":    {Endpoint: "http://x", AccessKey: "a"},
		"bad scheme":   {Endpoint: "ftp://x", AccessKey: "a", SecretKey: "s"},
		"admin scheme": {Endpoint: "http://x", AdminEndpoint: "ftp://y", AccessKey: "a", SecretKey: "s"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("expected an error, got none")
			}
		})
	}
}

func TestRequestsAreSignedWithSigV4(t *testing.T) {
	var auth, payloadHash string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		payloadHash = r.Header.Get("X-Amz-Content-Sha256")
		_, _ = w.Write([]byte(`<ListUserAccountsResult></ListUserAccountsResult>`))
	})

	if _, err := c.ListUsers(context.Background()); err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("not a SigV4 Authorization header: %q", auth)
	}
	// The admin API is signed for service s3, not for a service of its own.
	if !strings.Contains(auth, "/"+DefaultRegion+"/s3/aws4_request") {
		t.Errorf("credential scope is not <region>/s3: %q", auth)
	}
	// Empty body, so the hash is the well-known sha256 of the empty string.
	const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if payloadHash != emptyHash {
		t.Errorf("payload hash = %q, want the empty-string hash", payloadHash)
	}
}

func TestCreateUserSendsUpstreamXML(t *testing.T) {
	var method, path, body string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		body = string(buf)
		w.WriteHeader(http.StatusCreated)
	})

	err := c.CreateUser(context.Background(), Account{
		Access: "pipeline", Secret: "s3cr3t", Role: RoleUser, UserID: 1500,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if method != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", method)
	}
	if path != "/create-user" {
		t.Errorf("path = %s, want /create-user", path)
	}
	// Upstream unmarshals into auth.Account, which carries json tags only —
	// so encoding/xml matches on the Go FIELD names. Renaming any of these
	// silently produces an account with empty fields.
	for _, want := range []string{
		"<Access>pipeline</Access>",
		"<Secret>s3cr3t</Secret>",
		"<Role>user</Role>",
		"<UserID>1500</UserID>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("request body is missing %s\ngot: %s", want, body)
		}
	}
}

func TestListUsersParsesUpstreamShape(t *testing.T) {
	// Exactly what xml.Marshal(auth.ListUserAccountsResult{...}) produces:
	// the slice field is named Accounts, so each element is <Accounts>.
	const response = `<ListUserAccountsResult>` +
		`<Accounts><Access>one</Access><Secret>s1</Secret><Role>user</Role>` +
		`<UserID>0</UserID><GroupID>0</GroupID><ProjectID>0</ProjectID></Accounts>` +
		`<Accounts><Access>two</Access><Secret>s2</Secret><Role>admin</Role>` +
		`<UserID>1</UserID><GroupID>2</GroupID><ProjectID>3</ProjectID></Accounts>` +
		`</ListUserAccountsResult>`

	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(response))
	})

	accounts, err := c.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}
	if accounts[1].Access != "two" || accounts[1].Secret != "s2" || accounts[1].Role != RoleAdmin {
		t.Errorf("second account parsed wrong: %+v", accounts[1])
	}
	if accounts[1].GroupID != 2 || accounts[1].ProjectID != 3 {
		t.Errorf("numeric fields parsed wrong: %+v", accounts[1])
	}
}

func TestGetUserReturnsNilWhenAbsent(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<ListUserAccountsResult><Accounts><Access>other</Access></Accounts></ListUserAccountsResult>`))
	})

	acct, err := c.GetUser(context.Background(), "absent")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if acct != nil {
		t.Fatalf("expected nil for an unknown account, got %+v", acct)
	}
}

func TestCreateBucketPutsTheOwnerInAHeader(t *testing.T) {
	var path, owner string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path, owner = r.URL.Path, r.Header.Get("x-vgw-owner")
		w.WriteHeader(http.StatusOK)
	})

	if err := c.CreateBucket(context.Background(), "artifacts", "pipeline"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if path != "/artifacts/create" {
		t.Errorf("path = %s, want /artifacts/create", path)
	}
	// The owner travels as a signed header, which is what closes the window in
	// which the bucket would exist without one.
	if owner != "pipeline" {
		t.Errorf("x-vgw-owner = %q, want pipeline", owner)
	}
}

func TestChangeBucketOwnerUsesQueryParameters(t *testing.T) {
	var query string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Encode()
		w.WriteHeader(http.StatusOK)
	})

	if err := c.ChangeBucketOwner(context.Background(), "artifacts", "newowner"); err != nil {
		t.Fatalf("ChangeBucketOwner: %v", err)
	}
	if query != "bucket=artifacts&owner=newowner" {
		t.Errorf("query = %q", query)
	}
}

func TestDeleteBucketGoesToTheS3Endpoint(t *testing.T) {
	var method, path string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.DeleteBucket(context.Background(), "artifacts"); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	// The admin API has no delete route at all — this has to be a plain S3
	// DELETE on the bucket, not a PATCH below the admin prefix.
	if method != http.MethodDelete || path != "/artifacts" {
		t.Errorf("got %s %s, want DELETE /artifacts", method, path)
	}
}

func TestAPIErrorsAreTyped(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`<Error><Code>XAdminUserExists</Code>` +
			`<Message>A user with the provided access key ID already exists.</Message></Error>`))
	})

	err := c.CreateUser(context.Background(), Account{Access: "dup", Secret: "x", Role: RoleUser})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error is %T, want *APIError", err)
	}
	if !apiErr.IsConflict() {
		t.Error("XAdminUserExists should be a conflict")
	}
	if apiErr.IsNotFound() {
		t.Error("XAdminUserExists should not be a not-found")
	}
	if apiErr.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", apiErr.StatusCode)
	}
}

// A proxy in front of the gateway answers with HTML, not with the S3 error
// shape. That has to survive as a readable error rather than becoming a
// "malformed XML" complaint pointing at the wrong component.
func TestNonXMLErrorBodiesStayReadable(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
	})

	err := c.DeleteUser(context.Background(), "someone")
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error is %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Error(), "502") {
		t.Errorf("error text loses the status: %q", apiErr.Error())
	}
}

func TestNotFoundIsRecognisedForBothAPIs(t *testing.T) {
	cases := map[string]struct {
		err  APIError
		want bool
	}{
		"admin user":    {APIError{Code: "XAdminUserNotFound", StatusCode: 404}, true},
		"s3 bucket":     {APIError{Code: "NoSuchBucket", StatusCode: 404}, true},
		"bare 404":      {APIError{Code: "Not Found", StatusCode: 404}, true},
		"access denied": {APIError{Code: "AccessDenied", StatusCode: 403}, false},
		"conflict":      {APIError{Code: "XAdminUserExists", StatusCode: 409}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.err.IsNotFound(); got != tc.want {
				t.Errorf("IsNotFound() = %v, want %v", got, tc.want)
			}
		})
	}
}

// MutableProps distinguishes "leave alone" from "set to empty" with pointers.
// An update that marshalled a nil Secret as an empty element would wipe the
// account's key, which the gateway would accept without complaint.
func TestMutablePropsOmitsNothingItShouldNotSend(t *testing.T) {
	secret := "newsecret"
	body, err := xml.Marshal(MutableProps{Secret: &secret, Role: RoleUserPlus})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "<Secret>newsecret</Secret>") {
		t.Errorf("secret missing: %s", got)
	}
	if !strings.Contains(got, "<Role>userplus</Role>") {
		t.Errorf("role missing: %s", got)
	}
	if strings.Contains(got, "<UserID>") {
		t.Errorf("a nil UserID must not be sent at all: %s", got)
	}
}
