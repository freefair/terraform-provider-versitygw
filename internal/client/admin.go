package client

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
)

// Every admin route is a PATCH. That is upstream's choice, not a REST opinion
// of this package — see s3api/admin-router.go.

// CreateUser adds an account.
func (c *Client) CreateUser(ctx context.Context, acct Account) error {
	body, err := xml.Marshal(acct)
	if err != nil {
		return fmt.Errorf("marshal account: %w", err)
	}
	_, err = c.do(ctx, http.MethodPatch, c.adminURL("/create-user", nil), nil, body)
	return err
}

// UpdateUser changes the mutable properties of an account. A nil Secret leaves
// the stored key untouched.
func (c *Client) UpdateUser(ctx context.Context, access string, props MutableProps) error {
	body, err := xml.Marshal(props)
	if err != nil {
		return fmt.Errorf("marshal properties: %w", err)
	}
	q := url.Values{"access": []string{access}}
	_, err = c.do(ctx, http.MethodPatch, c.adminURL("/update-user", q), nil, body)
	return err
}

// DeleteUser removes an account. Buckets it owned are left behind and become
// unreachable until an admin reassigns them — the gateway does not cascade.
func (c *Client) DeleteUser(ctx context.Context, access string) error {
	q := url.Values{"access": []string{access}}
	_, err := c.do(ctx, http.MethodPatch, c.adminURL("/delete-user", q), nil, nil)
	return err
}

// ListUsers returns every account, each including its secret key. There is no
// route for a single account, which is why GetUser filters this list.
func (c *Client) ListUsers(ctx context.Context) ([]Account, error) {
	payload, err := c.do(ctx, http.MethodPatch, c.adminURL("/list-users", nil), nil, nil)
	if err != nil {
		return nil, err
	}
	var result listUsersResult
	if err := xml.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("parse user list: %w", err)
	}
	return result.Accounts, nil
}

// GetUser returns one account, or nil when it does not exist.
//
// The root account is deliberately invisible here: it is configured on the
// gateway's command line and never stored in the IAM service, so it appears in
// no listing. A resource that tried to manage it would read back as missing on
// every plan.
func (c *Client) GetUser(ctx context.Context, access string) (*Account, error) {
	accounts, err := c.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		if accounts[i].Access == access {
			return &accounts[i], nil
		}
	}
	return nil, nil
}

// CreateBucket creates a bucket and assigns its owner in one call. The owner
// travels as a header, so there is no window in which the bucket exists
// unowned.
func (c *Client) CreateBucket(ctx context.Context, name, owner string) error {
	headers := map[string]string{"x-vgw-owner": owner}
	path := "/" + url.PathEscape(name) + "/create"
	_, err := c.do(ctx, http.MethodPatch, c.adminURL(path, nil), headers, nil)
	return err
}

// ListBuckets returns every bucket with its owner.
func (c *Client) ListBuckets(ctx context.Context) ([]Bucket, error) {
	payload, err := c.do(ctx, http.MethodPatch, c.adminURL("/list-buckets", nil), nil, nil)
	if err != nil {
		return nil, err
	}
	var result listBucketsResult
	if err := xml.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("parse bucket list: %w", err)
	}
	return result.Buckets, nil
}

// GetBucket returns one bucket, or nil when it does not exist.
func (c *Client) GetBucket(ctx context.Context, name string) (*Bucket, error) {
	buckets, err := c.ListBuckets(ctx)
	if err != nil {
		return nil, err
	}
	for i := range buckets {
		if buckets[i].Name == name {
			return &buckets[i], nil
		}
	}
	return nil, nil
}

// ChangeBucketOwner reassigns a bucket.
//
// This DISCARDS the bucket's ACL and policy — upstream applies a default ACL
// for the new owner rather than migrating the old one. Anything set on the
// bucket has to be reapplied afterwards.
func (c *Client) ChangeBucketOwner(ctx context.Context, bucket, owner string) error {
	q := url.Values{"bucket": []string{bucket}, "owner": []string{owner}}
	_, err := c.do(ctx, http.MethodPatch, c.adminURL("/change-bucket-owner", q), nil, nil)
	return err
}

// DeleteBucket removes a bucket. This one goes to the S3 API: the admin API
// can create a bucket and reassign it, but has no route to delete it.
func (c *Client) DeleteBucket(ctx context.Context, name string) error {
	rawURL := c.cfg.Endpoint + "/" + url.PathEscape(name)
	_, err := c.do(ctx, http.MethodDelete, rawURL, nil, nil)
	return err
}
