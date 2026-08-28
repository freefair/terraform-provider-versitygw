package client

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
		"admin user": {APIError{Code: "XAdminUserNotFound", StatusCode: 404}, true},
		"s3 bucket":  {APIError{Code: "NoSuchBucket", StatusCode: 404}, true},
		// A proxy or an unmounted admin endpoint answers 404 without a
		// gateway code. That is an error to show, not an absent object.
		"bare 404":      {APIError{Code: "Not Found", StatusCode: 404}, false},
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

func TestBucketSubresourcesGoToTheS3Endpoint(t *testing.T) {
	var got struct {
		method, path, query string
		body                string
	}
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		// The SigV4 signer canonicalises the query to `policy=`; what
		// matters is that the sub-resource key is present.
		got.method, got.path, got.body = r.Method, r.URL.Path, string(b)
		if r.URL.Query().Has("policy") {
			got.query = "policy"
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// A separate admin endpoint must not attract sub-resource calls: they
	// belong to the S3 API even when the admin API lives elsewhere.
	c.cfg.AdminEndpoint = "http://admin.invalid"
	_ = srv

	if err := c.PutBucketPolicy(context.Background(), "my-bucket", []byte(`{"Version":"2012-10-17"}`)); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}
	if got.method != http.MethodPut || got.path != "/my-bucket" || got.query != "policy" {
		t.Errorf("PUT went to %s %s?%s, want PUT /my-bucket?policy", got.method, got.path, got.query)
	}
	if got.body != `{"Version":"2012-10-17"}` {
		t.Errorf("body = %q, sent verbatim expected", got.body)
	}

	if err := c.DeleteBucketPolicy(context.Background(), "my-bucket"); err != nil {
		t.Fatalf("DeleteBucketPolicy: %v", err)
	}
	if got.method != http.MethodDelete || got.query != "policy" {
		t.Errorf("DELETE went to %s ?%s", got.method, got.query)
	}
}

func TestGetBucketSubresourceReturnsNilWhenAbsent(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   []byte
	}{
		"no policy":  {http.StatusNotFound, `<Error><Code>NoSuchBucketPolicy</Code></Error>`, nil},
		"no bucket":  {http.StatusNotFound, `<Error><Code>NoSuchBucket</Code></Error>`, nil},
		"has policy": {http.StatusOK, `{"Version":"2012-10-17"}`, []byte(`{"Version":"2012-10-17"}`)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			got, err := c.GetBucketPolicy(context.Background(), "b")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != string(tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	// Anything that is not "absent" is an error the caller must see — a
	// bare 404 from a proxy or a wrong path especially, because treating it
	// as absence would drop the resource from state.
	for name, body := range map[string]string{
		"access denied":   `<Error><Code>AccessDenied</Code></Error>`,
		"bare 404":        `<html>not found</html>`,
		"other not-found": `<Error><Code>NoSuchKey</Code></Error>`,
	} {
		status := http.StatusNotFound
		if name == "access denied" {
			status = http.StatusForbidden
		}
		c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		})
		if _, err := c.GetBucketPolicy(context.Background(), "b"); err == nil {
			t.Errorf("%s was swallowed as absence", name)
		}
		if err := c.DeleteBucketPolicy(context.Background(), "b"); err == nil {
			t.Errorf("delete: %s was swallowed as success", name)
		}
	}
}

func TestDeleteBucketSubresourceTreatsAbsenceAsDone(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchBucketPolicy</Code></Error>`))
	})
	if err := c.DeleteBucketPolicy(context.Background(), "b"); err != nil {
		t.Errorf("deleting an absent policy should succeed, got %v", err)
	}
}

func TestSubresourceNotFoundCodesAreRecognised(t *testing.T) {
	for _, code := range []string{
		"NoSuchBucketPolicy", "NoSuchCORSConfiguration", "NoSuchWebsiteConfiguration",
		"NoSuchTagSet", "ObjectLockConfigurationNotFoundError", "OwnershipControlsNotFoundError",
	} {
		if !(&APIError{Code: code, StatusCode: http.StatusOK}).IsNotFound() {
			t.Errorf("%s not recognised as not-found", code)
		}
	}
	for _, code := range []string{"NotImplemented", "XAdminMethodNotSupported"} {
		if !(&APIError{Code: code}).IsNotImplemented() {
			t.Errorf("%s not recognised as not-implemented", code)
		}
	}
}

func TestUpdateUserSendsOnlyWhatChanges(t *testing.T) {
	var got struct {
		path, query, body string
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.path, got.query, got.body = r.URL.Path, r.URL.Query().Get("access"), string(b)
		w.WriteHeader(http.StatusOK)
	})
	secret := "newsecret"
	uid := 7
	if err := c.UpdateUser(context.Background(), "alice", MutableProps{Secret: &secret, Role: RoleAdmin, UserID: &uid}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if got.path != "/update-user" || got.query != "alice" {
		t.Errorf("went to %s?access=%s", got.path, got.query)
	}
	for _, want := range []string{"<Secret>newsecret</Secret>", "<Role>admin</Role>", "<UserID>7</UserID>"} {
		if !strings.Contains(got.body, want) {
			t.Errorf("body lacks %s: %s", want, got.body)
		}
	}
}

func TestListBucketsParsesUpstreamShape(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/list-buckets" || r.Method != http.MethodPatch {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`<ListBucketsResult><Buckets><Name>a</Name><Owner>alice</Owner></Buckets><Buckets><Name>b</Name><Owner>bob</Owner></Buckets></ListBucketsResult>`))
	})
	buckets, err := c.ListBuckets(context.Background())
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	if len(buckets) != 2 || buckets[1].Name != "b" || buckets[1].Owner != "bob" {
		t.Errorf("parsed %+v", buckets)
	}

	if got, err := c.GetBucket(context.Background(), "b"); err != nil || got == nil || got.Owner != "bob" {
		t.Errorf("GetBucket(b) = %+v, %v", got, err)
	}
	if got, err := c.GetBucket(context.Background(), "zzz"); err != nil || got != nil {
		t.Errorf("GetBucket(zzz) = %+v, %v; want nil, nil", got, err)
	}
}

func TestListingErrorsPropagate(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<Error><Code>InternalError</Code><Message>boom</Message></Error>`))
	})
	if _, err := c.ListBuckets(context.Background()); err == nil {
		t.Error("ListBuckets swallowed a 500")
	}
	if _, err := c.GetBucket(context.Background(), "b"); err == nil {
		t.Error("GetBucket swallowed a 500")
	}
	if _, err := c.GetUser(context.Background(), "u"); err == nil {
		t.Error("GetUser swallowed a 500")
	}

	// A body that is not the documented XML is a parse error, not a panic
	// and not a silently empty list.
	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<<not xml`))
	})
	if _, err := c.ListBuckets(context.Background()); err == nil {
		t.Error("ListBuckets accepted garbage")
	}
	if _, err := c.ListUsers(context.Background()); err == nil {
		t.Error("ListUsers accepted garbage")
	}
}

func TestNewHonoursExplicitSettings(t *testing.T) {
	c, err := New(Config{
		Endpoint:      "https://s3.example.com",
		AdminEndpoint: "https://admin.example.com/",
		AccessKey:     "a",
		SecretKey:     "s",
		Region:        "eu-central-1",
		Insecure:      true,
		Timeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.AdminEndpoint() != "https://admin.example.com" {
		t.Errorf("admin endpoint = %q", c.AdminEndpoint())
	}
	if c.cfg.Region != "eu-central-1" || c.cfg.Timeout != 5*time.Second {
		t.Errorf("settings not applied: %+v", c.cfg)
	}
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("Insecure did not configure the transport")
	}
	if _, err := New(Config{Endpoint: "http://[::1", AccessKey: "a", SecretKey: "s"}); err == nil {
		t.Error("unparseable endpoint accepted")
	}
}

func TestTransportFailuresNameTheRequest(t *testing.T) {
	c, srv := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
	srv.Close()
	_, err := c.ListUsers(context.Background())
	if err == nil || !strings.Contains(err.Error(), "/list-users") {
		t.Errorf("error does not say which request failed: %v", err)
	}
}

func TestAPIErrorVocabulary(t *testing.T) {
	if got := (&APIError{Code: "NoSuchBucket", StatusCode: 404}).Error(); got != "NoSuchBucket (HTTP 404)" {
		t.Errorf("message-less error = %q", got)
	}
	for _, code := range []string{"XAdminUserExists", "BucketAlreadyExists", "BucketAlreadyOwnedByYou"} {
		if !(&APIError{Code: code}).IsConflict() {
			t.Errorf("%s not a conflict", code)
		}
	}
	if (&APIError{Code: "AccessDenied"}).IsConflict() {
		t.Error("AccessDenied is not a conflict")
	}
	if roles := Roles(); len(roles) != 3 || roles[0] != RoleUser {
		t.Errorf("Roles() = %v", roles)
	}
}

func TestEmptyErrorBodyStaysReadable(t *testing.T) {
	err := parseAPIError(http.StatusBadGateway, nil)
	if !strings.Contains(err.Error(), "no response body") {
		t.Errorf("empty body not explained: %v", err)
	}
}

func TestVersioningRoundTrip(t *testing.T) {
	var got struct {
		method, body, md5 string
	}
	stored := ""
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.body, got.md5 = r.Method, string(b), r.Header.Get("Content-MD5")
		if r.Method == http.MethodPut {
			stored = string(b)
			return
		}
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+stored+`</VersioningConfiguration>`)
	})
	if err := c.PutBucketVersioning(context.Background(), "b", VersioningEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	if got.body != `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>` {
		t.Errorf("body = %s", got.body)
	}
	if got.md5 == "" {
		t.Error("Content-MD5 missing — the gateway rejects object lock without it")
	}
	// Read back what was stored, then read an unconfigured bucket.
	stored = `<Status>Enabled</Status>`
	if s, err := c.GetBucketVersioning(context.Background(), "b"); err != nil || s != "Enabled" {
		t.Errorf("GetBucketVersioning = %q, %v", s, err)
	}
	stored = ""
	if s, err := c.GetBucketVersioning(context.Background(), "b"); err != nil || s != "" {
		t.Errorf("unconfigured bucket = %q, %v; want empty", s, err)
	}
}

func TestObjectLockRoundTrip(t *testing.T) {
	answers := map[string]string{
		"rule":       `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Days>3</Days><Mode>GOVERNANCE</Mode></DefaultRetention></Rule></ObjectLockConfiguration>`,
		"empty rule": `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule></Rule></ObjectLockConfiguration>`,
	}
	var sent string
	current := "rule"
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			sent = string(b)
			return
		}
		_, _ = io.WriteString(w, answers[current])
	})

	cfg := ObjectLockConfiguration{ObjectLockEnabled: "Enabled", Rule: &ObjectLockRule{DefaultRetention: &DefaultRetention{Mode: "COMPLIANCE", Years: 1}}}
	if err := c.PutObjectLockConfiguration(context.Background(), "b", cfg); err != nil {
		t.Fatalf("PutObjectLockConfiguration: %v", err)
	}
	if !strings.Contains(sent, "<Years>1</Years>") || strings.Contains(sent, "<Days>") {
		t.Errorf("body = %s", sent)
	}

	got, err := c.GetObjectLockConfiguration(context.Background(), "b")
	if err != nil || got == nil || got.Rule == nil || got.Rule.DefaultRetention.Days != 3 || got.Rule.DefaultRetention.Mode != "GOVERNANCE" {
		t.Errorf("parsed %+v, %v", got, err)
	}
	// The gateway answers <Rule></Rule> when no default retention is set;
	// that must read as "no rule", not as a rule with nothing in it.
	current = "empty rule"
	got, err = c.GetObjectLockConfiguration(context.Background(), "b")
	if err != nil || got == nil || got.Rule != nil {
		t.Errorf("empty rule parsed as %+v, %v", got, err)
	}

	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`))
	})
	if got, err := c.GetObjectLockConfiguration(context.Background(), "b"); err != nil || got != nil {
		t.Errorf("absent lock = %+v, %v; want nil, nil", got, err)
	}

	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `<<garbage`) })
	if _, err := c.GetObjectLockConfiguration(context.Background(), "b"); err == nil {
		t.Error("garbage accepted as object lock configuration")
	}
	if _, err := c.GetBucketVersioning(context.Background(), "b"); err == nil {
		t.Error("garbage accepted as versioning configuration")
	}
}

func TestOwnershipControlsRoundTrip(t *testing.T) {
	var sent, method string
	stored := `<OwnershipControls><Rule><ObjectOwnership>BucketOwnerEnforced</ObjectOwnership></Rule></OwnershipControls>`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			sent = string(b)
		case http.MethodGet:
			_, _ = io.WriteString(w, stored)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	if err := c.PutBucketOwnershipControls(context.Background(), "b", OwnershipObjectWriter); err != nil {
		t.Fatalf("PutBucketOwnershipControls: %v", err)
	}
	if sent != `<OwnershipControls><Rule><ObjectOwnership>ObjectWriter</ObjectOwnership></Rule></OwnershipControls>` {
		t.Errorf("body = %s", sent)
	}
	if got, err := c.GetBucketOwnershipControls(context.Background(), "b"); err != nil || got != OwnershipBucketOwnerEnforced {
		t.Errorf("Get = %q, %v", got, err)
	}
	stored = `<OwnershipControls></OwnershipControls>`
	if got, err := c.GetBucketOwnershipControls(context.Background(), "b"); err != nil || got != "" {
		t.Errorf("no rule = %q, %v; want empty", got, err)
	}
	stored = `<<garbage`
	if _, err := c.GetBucketOwnershipControls(context.Background(), "b"); err == nil {
		t.Error("garbage accepted")
	}
	if err := c.DeleteBucketOwnershipControls(context.Background(), "b"); err != nil || method != http.MethodDelete {
		t.Errorf("delete: %v (method %s)", err, method)
	}

	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>OwnershipControlsNotFoundError</Code></Error>`))
	})
	if got, err := c.GetBucketOwnershipControls(context.Background(), "b"); err != nil || got != "" {
		t.Errorf("absent = %q, %v", got, err)
	}
	if err := c.DeleteBucketOwnershipControls(context.Background(), "b"); err != nil {
		t.Errorf("deleting absent controls: %v", err)
	}
}

func TestBucketACLRoundTrip(t *testing.T) {
	var sent, header string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			sent, header = string(b), r.Header.Get("x-amz-acl")
			return
		}
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<AccessControlPolicy xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Owner><ID>alice</ID></Owner><AccessControlList><Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="CanonicalUser"><ID>alice</ID></Grantee><Permission>FULL_CONTROL</Permission></Grant><Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="Group"><ID>all-users</ID></Grantee><Permission>READ</Permission></Grant></AccessControlList></AccessControlPolicy>`)
	})
	if err := c.PutBucketCannedACL(context.Background(), "b", ACLPublicRead); err != nil {
		t.Fatalf("PutBucketCannedACL: %v", err)
	}
	if header != "public-read" || sent != "" {
		t.Errorf("canned ACL sent as header=%q body=%q", header, sent)
	}
	if err := c.PutBucketACL(context.Background(), "b", "alice", []Grant{{GranteeCanonicalUser, "bob", "READ"}, {GranteeGroup, GroupAllUsers, "READ"}}); err != nil {
		t.Fatalf("PutBucketACL: %v", err)
	}
	for _, want := range []string{`<Owner><ID>alice</ID></Owner>`, `xsi:type="CanonicalUser"><ID>bob</ID>`, `xsi:type="Group"><ID>all-users</ID>`, `<Permission>READ</Permission>`} {
		if !strings.Contains(sent, want) {
			t.Errorf("body lacks %s: %s", want, sent)
		}
	}
	acl, err := c.GetBucketACL(context.Background(), "b")
	if err != nil || acl == nil || acl.Owner != "alice" || len(acl.Grants) != 2 {
		t.Fatalf("GetBucketACL = %+v, %v", acl, err)
	}
	if acl.Grants[1] != (Grant{GranteeGroup, GroupAllUsers, "READ"}) {
		t.Errorf("group grant parsed as %+v — the xsi:type attribute must survive", acl.Grants[1])
	}

	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchBucket</Code></Error>`))
	})
	if acl, err := c.GetBucketACL(context.Background(), "b"); err != nil || acl != nil {
		t.Errorf("absent bucket = %+v, %v", acl, err)
	}
	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `<<garbage`) })
	if _, err := c.GetBucketACL(context.Background(), "b"); err == nil {
		t.Error("garbage accepted as ACL")
	}
}

func TestBucketTaggingRoundTrip(t *testing.T) {
	var sent, method string
	stored := `<Tagging><TagSet><Tag><Key>team</Key><Value>platform</Value></Tag><Tag><Key>empty</Key><Value></Value></Tag></TagSet></Tagging>`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			sent = string(b)
		case http.MethodGet:
			_, _ = io.WriteString(w, stored)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	if err := c.PutBucketTagging(context.Background(), "b", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("PutBucketTagging: %v", err)
	}
	if sent != `<Tagging><TagSet><Tag><Key>k</Key><Value>v</Value></Tag></TagSet></Tagging>` {
		t.Errorf("body = %s", sent)
	}
	got, err := c.GetBucketTagging(context.Background(), "b")
	if err != nil || len(got) != 2 || got["team"] != "platform" || got["empty"] != "" {
		t.Errorf("Get = %v, %v", got, err)
	}
	stored = `<Tagging><TagSet></TagSet></Tagging>`
	if got, err := c.GetBucketTagging(context.Background(), "b"); err != nil || len(got) != 0 {
		t.Errorf("empty set = %v, %v", got, err)
	}
	stored = `<<garbage`
	if _, err := c.GetBucketTagging(context.Background(), "b"); err == nil {
		t.Error("garbage accepted")
	}
	if err := c.DeleteBucketTagging(context.Background(), "b"); err != nil || method != http.MethodDelete {
		t.Errorf("delete: %v (%s)", err, method)
	}

	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchTagSet</Code></Error>`))
	})
	if got, err := c.GetBucketTagging(context.Background(), "b"); err != nil || len(got) != 0 {
		t.Errorf("no tag set = %v, %v; want empty map", got, err)
	}
	if err := c.DeleteBucketTagging(context.Background(), "b"); err != nil {
		t.Errorf("deleting absent tags: %v", err)
	}
}

func TestBucketCORSRoundTrip(t *testing.T) {
	var sent string
	stored := `<CORSConfiguration><CORSRule><AllowedMethod>PUT</AllowedMethod><AllowedMethod>POST</AllowedMethod><AllowedHeader>*</AllowedHeader><ExposeHeader>ETag</ExposeHeader><AllowedOrigin>https://app.example.com</AllowedOrigin><ID>a</ID><MaxAgeSeconds>3000</MaxAgeSeconds></CORSRule><CORSRule><AllowedMethod>GET</AllowedMethod><AllowedOrigin>*</AllowedOrigin></CORSRule></CORSConfiguration>`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			sent = string(b)
		case http.MethodGet:
			_, _ = io.WriteString(w, stored)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	age := int64(3000)
	err := c.PutBucketCORS(context.Background(), "b", []CORSRule{
		{ID: "a", AllowedHeaders: []string{"*"}, AllowedMethods: []string{"PUT", "POST"},
			AllowedOrigins: []string{"https://app.example.com"}, ExposeHeaders: []string{"ETag"}, MaxAgeSeconds: &age},
		{AllowedMethods: []string{"GET"}, AllowedOrigins: []string{"*"}},
	})
	if err != nil {
		t.Fatalf("PutBucketCORS: %v", err)
	}
	want := `<CORSConfiguration><CORSRule><ID>a</ID><AllowedHeader>*</AllowedHeader><AllowedMethod>PUT</AllowedMethod><AllowedMethod>POST</AllowedMethod><AllowedOrigin>https://app.example.com</AllowedOrigin><ExposeHeader>ETag</ExposeHeader><MaxAgeSeconds>3000</MaxAgeSeconds></CORSRule><CORSRule><AllowedMethod>GET</AllowedMethod><AllowedOrigin>*</AllowedOrigin></CORSRule></CORSConfiguration>`
	if sent != want {
		t.Errorf("body = %s", sent)
	}
	rules, err := c.GetBucketCORS(context.Background(), "b")
	if err != nil || len(rules) != 2 {
		t.Fatalf("Get = %v, %v", rules, err)
	}
	if rules[0].ID != "a" || rules[0].MaxAgeSeconds == nil || *rules[0].MaxAgeSeconds != 3000 ||
		len(rules[0].AllowedMethods) != 2 || rules[1].MaxAgeSeconds != nil || rules[1].AllowedOrigins[0] != "*" {
		t.Errorf("rules = %+v", rules)
	}
	stored = `<<garbage`
	if _, err := c.GetBucketCORS(context.Background(), "b"); err == nil {
		t.Error("garbage accepted")
	}
	if err := c.DeleteBucketCORS(context.Background(), "b"); err != nil {
		t.Errorf("delete: %v", err)
	}

	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchCORSConfiguration</Code></Error>`))
	})
	if rules, err := c.GetBucketCORS(context.Background(), "b"); err != nil || rules != nil {
		t.Errorf("absent = %v, %v; want nil, nil", rules, err)
	}
	if err := c.DeleteBucketCORS(context.Background(), "b"); err != nil {
		t.Errorf("deleting absent cors: %v", err)
	}
}

func TestBucketWebsiteRoundTrip(t *testing.T) {
	var sent string
	stored := `<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument><ErrorDocument><Key>error.html</Key></ErrorDocument><RoutingRules></RoutingRules></WebsiteConfiguration>`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			sent = string(b)
		case http.MethodGet:
			_, _ = io.WriteString(w, stored)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	err := c.PutBucketWebsite(context.Background(), "b", WebsiteConfiguration{
		IndexDocument: &IndexDocument{Suffix: "index.html"},
		ErrorDocument: &ErrorDocument{Key: "error.html"},
	})
	if err != nil {
		t.Fatalf("PutBucketWebsite: %v", err)
	}
	if sent != `<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument><ErrorDocument><Key>error.html</Key></ErrorDocument></WebsiteConfiguration>` {
		t.Errorf("body = %s", sent)
	}
	err = c.PutBucketWebsite(context.Background(), "b", WebsiteConfiguration{
		IndexDocument: &IndexDocument{Suffix: "index.html"},
		RoutingRules: &RoutingRules{Rules: []RoutingRule{{
			Condition: &RoutingRuleCondition{KeyPrefixEquals: "docs/"},
			Redirect:  &Redirect{HostName: "example.com", HTTPRedirectCode: "301", Protocol: "https", ReplaceKeyPrefixWith: "documents/"},
		}}},
	})
	if err != nil {
		t.Fatalf("PutBucketWebsite: %v", err)
	}
	if want := `<RoutingRules><RoutingRule><Condition><KeyPrefixEquals>docs/</KeyPrefixEquals></Condition><Redirect><HostName>example.com</HostName><HttpRedirectCode>301</HttpRedirectCode><Protocol>https</Protocol><ReplaceKeyPrefixWith>documents/</ReplaceKeyPrefixWith></Redirect></RoutingRule></RoutingRules>`; !strings.Contains(sent, want) {
		t.Errorf("body = %s", sent)
	}

	cfg, err := c.GetBucketWebsite(context.Background(), "b")
	if err != nil || cfg == nil || cfg.IndexDocument == nil || cfg.IndexDocument.Suffix != "index.html" ||
		cfg.ErrorDocument == nil || cfg.ErrorDocument.Key != "error.html" || cfg.RoutingRules != nil || cfg.RedirectAllRequestsTo != nil {
		t.Errorf("Get = %+v, %v", cfg, err)
	}
	stored = `<WebsiteConfiguration><RedirectAllRequestsTo><HostName>example.com</HostName><Protocol>https</Protocol></RedirectAllRequestsTo><RoutingRules></RoutingRules></WebsiteConfiguration>`
	cfg, err = c.GetBucketWebsite(context.Background(), "b")
	if err != nil || cfg == nil || cfg.RedirectAllRequestsTo == nil || cfg.RedirectAllRequestsTo.HostName != "example.com" || cfg.IndexDocument != nil {
		t.Errorf("Get redirect = %+v, %v", cfg, err)
	}
	stored = `<<garbage`
	if _, err := c.GetBucketWebsite(context.Background(), "b"); err == nil {
		t.Error("garbage accepted")
	}
	if err := c.DeleteBucketWebsite(context.Background(), "b"); err != nil {
		t.Errorf("delete: %v", err)
	}

	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchWebsiteConfiguration</Code></Error>`))
	})
	if cfg, err := c.GetBucketWebsite(context.Background(), "b"); err != nil || cfg != nil {
		t.Errorf("absent = %v, %v; want nil, nil", cfg, err)
	}
	if err := c.DeleteBucketWebsite(context.Background(), "b"); err != nil {
		t.Errorf("deleting absent website: %v", err)
	}
}
