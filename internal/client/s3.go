package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// Bucket sub-resources — policy, ACL, versioning and the rest — live on the
// S3 API, not the admin API. This file is the plumbing they share: one URL
// shape, three verbs, and a single "absent" convention. Each feature adds its
// typed wrappers below.

// s3URL addresses a bucket sub-resource on the S3 endpoint, path-style:
// https://s3.example.com/my-bucket?policy. Path-style is what DeleteBucket
// already uses; the gateway's --virtual-domain mode is not assumed.
func (c *Client) s3URL(bucket, subresource string) string {
	return c.cfg.Endpoint + "/" + url.PathEscape(bucket) + "?" + subresource
}

// putBucketSubresource replaces a sub-resource. S3 semantics: a PUT on a
// bucket that already carries the configuration overwrites it, so there is
// no conflict to detect here.
func (c *Client) putBucketSubresource(ctx context.Context, bucket, subresource string, headers map[string]string, body []byte) error {
	_, err := c.do(ctx, http.MethodPut, c.s3URL(bucket, subresource), headers, body)
	return err
}

// isAbsent reports whether err is the gateway saying this sub-resource, or
// the bucket it belongs to, does not exist. Only those two codes count: a
// bare 404 from a proxy or a wrong path must not remove a resource from
// state or turn a failed delete into a success.
func isAbsent(err error, absentCode string) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == absentCode || apiErr.Code == "NoSuchBucket"
}

// getBucketSubresource reads a sub-resource. It returns (nil, nil) when the
// gateway answers absentCode or NoSuchBucket, so callers keep the convention
// GetUser and GetBucket set.
func (c *Client) getBucketSubresource(ctx context.Context, bucket, subresource, absentCode string) ([]byte, error) {
	payload, err := c.do(ctx, http.MethodGet, c.s3URL(bucket, subresource), nil, nil)
	if err != nil {
		if isAbsent(err, absentCode) {
			return nil, nil
		}
		return nil, err
	}
	return payload, nil
}

// deleteBucketSubresource removes a sub-resource. Absence is not an error:
// a destroy that finds nothing to remove has nothing left to do.
func (c *Client) deleteBucketSubresource(ctx context.Context, bucket, subresource, absentCode string) error {
	_, err := c.do(ctx, http.MethodDelete, c.s3URL(bucket, subresource), nil, nil)
	if isAbsent(err, absentCode) {
		return nil
	}
	return err
}

// PutBucketPolicy sets a bucket's policy. The body is the policy document as
// JSON; the gateway validates it and answers MalformedPolicy with a message
// naming the problem, which is passed through untouched.
func (c *Client) PutBucketPolicy(ctx context.Context, bucket string, policy []byte) error {
	return c.putBucketSubresource(ctx, bucket, "policy", nil, policy)
}

// GetBucketPolicy returns a bucket's policy document, or nil when the bucket
// has none (or does not exist).
func (c *Client) GetBucketPolicy(ctx context.Context, bucket string) ([]byte, error) {
	return c.getBucketSubresource(ctx, bucket, "policy", "NoSuchBucketPolicy")
}

// DeleteBucketPolicy removes a bucket's policy.
func (c *Client) DeleteBucketPolicy(ctx context.Context, bucket string) error {
	return c.deleteBucketSubresource(ctx, bucket, "policy", "NoSuchBucketPolicy")
}
