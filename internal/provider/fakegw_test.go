package provider_test

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGateway is a minimal in-memory gateway that speaks the wire format of
// the real one, with one addition: any route can be told to fail. The real
// gateway proves the happy path; this one proves the provider says something
// useful when the gateway does not cooperate — the branches a healthy
// container never exercises.
//
// It mirrors only what the provider sends: PATCH admin routes, DELETE bucket,
// and PUT/GET/DELETE ?policy. Anything else is a test bug and answers 500.
type fakeGateway struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	users    map[string]fakeAccount
	buckets  map[string]string // name -> owner
	policies map[string][]byte
	// versioning status per bucket ("" = never configured) and the raw
	// object lock document, mirroring what posix stores as xattrs.
	versioning map[string]string
	locks      map[string][]byte
	// ownership per bucket; a fresh bucket answers BucketOwnerEnforced and
	// a deleted entry answers not-found — exactly what the gateway does.
	ownership map[string]string
	faults    map[string]fault // "METHOD /path[?sub]" -> answer
}

type fakeAccount struct {
	Access, Secret, Role       string
	UserID, GroupID, ProjectID int
}

type fault struct {
	status int
	code   string
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	g := &fakeGateway{
		t:          t,
		users:      map[string]fakeAccount{},
		buckets:    map[string]string{},
		policies:   map[string][]byte{},
		versioning: map[string]string{},
		locks:      map[string][]byte{},
		ownership:  map[string]string{},
		faults:     map[string]fault{},
	}
	g.srv = httptest.NewServer(http.HandlerFunc(g.handle))
	t.Cleanup(g.srv.Close)

	// The provider reads its configuration from the environment; pointing
	// it at the fake is a matter of three variables. t.Setenv restores them.
	t.Setenv("VERSITYGW_ENDPOINT", g.srv.URL)
	t.Setenv("VERSITYGW_ADMIN_ENDPOINT", "")
	t.Setenv("VERSITYGW_ACCESS_KEY", "root")
	t.Setenv("VERSITYGW_SECRET_KEY", "rootsecret")
	return g
}

// fail makes a route answer with the given error until cleared.
func (g *fakeGateway) fail(route string, status int, code string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.faults[route] = fault{status, code}
}

func (g *fakeGateway) clearFaults() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.faults = map[string]fault{}
}

// forget drops objects behind Terraform's back, the way an admin with the
// CLI would.
func (g *fakeGateway) forgetUser(access string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.users, access)
}

func (g *fakeGateway) forgetBucket(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.buckets, name)
	delete(g.policies, name)
	delete(g.versioning, name)
	delete(g.locks, name)
	delete(g.ownership, name)
}

// forgetOwnership drops the ownership entry, as a DELETE from the CLI would.
func (g *fakeGateway) forgetOwnership(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.ownership, name)
}

// hasBucket reports whether the bucket still exists — the check that proves
// a state-only destroy sent nothing the gateway would act on.
func (g *fakeGateway) hasBucket(name string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.buckets[name]
	return ok
}

// forgetLock drops the lock document, which the real gateway cannot do —
// but a test needs a way to say "the configuration is gone".
func (g *fakeGateway) forgetLock(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.locks, name)
}

func (g *fakeGateway) handle(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()

	route := r.Method + " " + r.URL.Path
	for _, sub := range []string{"policy", "versioning", "object-lock", "ownershipControls"} {
		if r.URL.Query().Has(sub) {
			route += "?" + sub
		}
	}
	if f, ok := g.faults[route]; ok {
		s3Error(w, f.status, f.code)
		return
	}

	body, _ := io.ReadAll(r.Body)
	q := r.URL.Query()

	switch {
	case r.Method == http.MethodPatch && r.URL.Path == "/create-user":
		var acct fakeAccount
		if err := xml.Unmarshal(body, &acct); err != nil {
			s3Error(w, 400, "MalformedXML")
			return
		}
		if _, exists := g.users[acct.Access]; exists {
			s3Error(w, 409, "XAdminUserExists")
			return
		}
		g.users[acct.Access] = acct
		w.WriteHeader(201)

	case r.Method == http.MethodPatch && r.URL.Path == "/update-user":
		acct, ok := g.users[q.Get("access")]
		if !ok {
			s3Error(w, 404, "XAdminUserNotFound")
			return
		}
		var props struct {
			Secret                     *string
			Role                       string
			UserID, GroupID, ProjectID *int
		}
		_ = xml.Unmarshal(body, &props)
		if props.Secret != nil {
			acct.Secret = *props.Secret
		}
		if props.Role != "" {
			acct.Role = props.Role
		}
		if props.UserID != nil {
			acct.UserID = *props.UserID
		}
		if props.GroupID != nil {
			acct.GroupID = *props.GroupID
		}
		if props.ProjectID != nil {
			acct.ProjectID = *props.ProjectID
		}
		g.users[acct.Access] = acct
		w.WriteHeader(200)

	case r.Method == http.MethodPatch && r.URL.Path == "/delete-user":
		if _, ok := g.users[q.Get("access")]; !ok {
			s3Error(w, 404, "XAdminUserNotFound")
			return
		}
		delete(g.users, q.Get("access"))
		w.WriteHeader(200)

	case r.Method == http.MethodPatch && r.URL.Path == "/list-users":
		var sb strings.Builder
		sb.WriteString("<ListUserAccountsResult>")
		for _, a := range g.users {
			fmt.Fprintf(&sb, "<Accounts><Access>%s</Access><Secret>%s</Secret><Role>%s</Role><UserID>%d</UserID><GroupID>%d</GroupID><ProjectID>%d</ProjectID></Accounts>",
				a.Access, a.Secret, a.Role, a.UserID, a.GroupID, a.ProjectID)
		}
		sb.WriteString("</ListUserAccountsResult>")
		_, _ = io.WriteString(w, sb.String())

	case r.Method == http.MethodPatch && r.URL.Path == "/list-buckets":
		var sb strings.Builder
		sb.WriteString("<ListBucketsResult>")
		for name, owner := range g.buckets {
			fmt.Fprintf(&sb, "<Buckets><Name>%s</Name><Owner>%s</Owner></Buckets>", name, owner)
		}
		sb.WriteString("</ListBucketsResult>")
		_, _ = io.WriteString(w, sb.String())

	case r.Method == http.MethodPatch && r.URL.Path == "/change-bucket-owner":
		if _, ok := g.buckets[q.Get("bucket")]; !ok {
			s3Error(w, 404, "NoSuchBucket")
			return
		}
		g.buckets[q.Get("bucket")] = q.Get("owner")
		delete(g.policies, q.Get("bucket")) // upstream drops the policy too
		w.WriteHeader(200)

	case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/create"):
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/create")
		if _, exists := g.buckets[name]; exists {
			s3Error(w, 409, "BucketAlreadyExists")
			return
		}
		g.buckets[name] = r.Header.Get("x-vgw-owner")
		g.ownership[name] = "BucketOwnerEnforced"
		w.WriteHeader(201)

	case r.URL.Query().Has("policy"):
		name := strings.TrimPrefix(r.URL.Path, "/")
		if _, ok := g.buckets[name]; !ok {
			s3Error(w, 404, "NoSuchBucket")
			return
		}
		switch r.Method {
		case http.MethodPut:
			g.policies[name] = body
			w.WriteHeader(204)
		case http.MethodGet:
			p, ok := g.policies[name]
			if !ok {
				s3Error(w, 404, "NoSuchBucketPolicy")
				return
			}
			_, _ = w.Write(p)
		case http.MethodDelete:
			delete(g.policies, name)
			w.WriteHeader(204)
		default:
			// An implicit 200 here would hide a provider sending the wrong
			// verb; the real gateway answers 405.
			g.t.Errorf("fake gateway: %s on ?policy", r.Method)
			s3Error(w, 405, "MethodNotAllowed")
		}

	case r.URL.Query().Has("versioning") && r.Method != http.MethodDelete:
		name := strings.TrimPrefix(r.URL.Path, "/")
		if _, ok := g.buckets[name]; !ok {
			s3Error(w, 404, "NoSuchBucket")
			return
		}
		switch r.Method {
		case http.MethodPut:
			var cfg struct{ Status string }
			_ = xml.Unmarshal(body, &cfg)
			if cfg.Status != "Enabled" && cfg.Status != "Suspended" {
				s3Error(w, 400, "MalformedXML")
				return
			}
			if _, locked := g.locks[name]; locked {
				s3Error(w, 400, "InvalidBucketState")
				return
			}
			g.versioning[name] = cfg.Status
			w.WriteHeader(200)
		case http.MethodGet:
			fmt.Fprintf(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>%s</Status></VersioningConfiguration>`, g.versioning[name])
		default:
			g.t.Errorf("fake gateway: %s on ?versioning", r.Method)
			s3Error(w, 405, "MethodNotAllowed")
		}

	case r.URL.Query().Has("object-lock") && r.Method != http.MethodDelete:
		name := strings.TrimPrefix(r.URL.Path, "/")
		if _, ok := g.buckets[name]; !ok {
			s3Error(w, 404, "NoSuchBucket")
			return
		}
		switch r.Method {
		case http.MethodPut:
			if r.Header.Get("Content-MD5") == "" {
				s3Error(w, 400, "InvalidRequest")
				return
			}
			if g.versioning[name] != "Enabled" {
				s3Error(w, 409, "InvalidBucketState")
				return
			}
			g.locks[name] = body
			w.WriteHeader(200)
		case http.MethodGet:
			doc, ok := g.locks[name]
			if !ok {
				s3Error(w, 404, "ObjectLockConfigurationNotFoundError")
				return
			}
			_, _ = w.Write(doc)
		default:
			g.t.Errorf("fake gateway: %s on ?object-lock", r.Method)
			s3Error(w, 405, "MethodNotAllowed")
		}

	case r.URL.Query().Has("ownershipControls"):
		name := strings.TrimPrefix(r.URL.Path, "/")
		if _, ok := g.buckets[name]; !ok {
			s3Error(w, 404, "NoSuchBucket")
			return
		}
		switch r.Method {
		case http.MethodPut:
			var cfg struct {
				Rule struct{ ObjectOwnership string }
			}
			_ = xml.Unmarshal(body, &cfg)
			g.ownership[name] = cfg.Rule.ObjectOwnership
			w.WriteHeader(200)
		case http.MethodGet:
			o, ok := g.ownership[name]
			if !ok {
				s3Error(w, 404, "OwnershipControlsNotFoundError")
				return
			}
			fmt.Fprintf(w, `<OwnershipControls xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Rule><ObjectOwnership>%s</ObjectOwnership></Rule></OwnershipControls>`, o)
		case http.MethodDelete:
			delete(g.ownership, name)
			w.WriteHeader(204)
		default:
			g.t.Errorf("fake gateway: %s on ?ownershipControls", r.Method)
			s3Error(w, 405, "MethodNotAllowed")
		}

	case r.Method == http.MethodDelete:
		// The real gateway routes a DELETE with a sub-resource it does not
		// know (?versioning, ?object-lock) here as well — and deletes the
		// bucket. Mirrored on purpose, so a provider that ever sends one
		// loses the bucket in the test and not in production.
		name := strings.TrimPrefix(r.URL.Path, "/")
		if _, ok := g.buckets[name]; !ok {
			s3Error(w, 404, "NoSuchBucket")
			return
		}
		delete(g.buckets, name)
		w.WriteHeader(204)

	default:
		g.t.Errorf("fake gateway: unexpected %s", route)
		s3Error(w, 500, "InternalError")
	}
}

func s3Error(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	fmt.Fprintf(w, "<Error><Code>%s</Code><Message>injected %s</Message></Error>", code, code)
}
