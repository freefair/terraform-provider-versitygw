package client

import (
	"context"
	"crypto/md5" // #nosec G501 — S3 mandates MD5 for Content-MD5; it is an integrity check, not a security primitive
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
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
//
// Every PUT carries Content-MD5. The gateway insists on it for object lock
// (measured: "Missing required header for this request: Content-MD5") and
// S3 does for several others; sending it always costs nothing and spares
// each feature from finding out the hard way.
func (c *Client) putBucketSubresource(ctx context.Context, bucket, subresource string, headers map[string]string, body []byte) error {
	sum := md5.Sum(body) // #nosec G401
	h := map[string]string{"Content-MD5": base64.StdEncoding.EncodeToString(sum[:])}
	for k, v := range headers {
		h[k] = v
	}
	_, err := c.do(ctx, http.MethodPut, c.s3URL(bucket, subresource), h, body)
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

// Bucket versioning. There is no DELETE — S3 has no way to turn versioning
// off once it was on, only to suspend it — and none must ever be sent: the
// gateway routes a DELETE with a sub-resource it does not know to
// DeleteBucket (measured against v1.7.0; the probe bucket was gone). The
// same applies to object lock below.

// VersioningStatus values the gateway accepts. "Disabled" is not one of
// them: the gateway answers MalformedXML.
const (
	VersioningEnabled   = "Enabled"
	VersioningSuspended = "Suspended"
)

type versioningConfiguration struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	Status  string   `xml:"Status,omitempty"`
}

// PutBucketVersioning sets the versioning status. The posix backend refuses
// this without a versioning directory (VersioningNotConfigured), and refuses
// to suspend while an object lock configuration is present
// (InvalidBucketState).
func (c *Client) PutBucketVersioning(ctx context.Context, bucket, status string) error {
	body, err := xml.Marshal(versioningConfiguration{Status: status})
	if err != nil {
		return fmt.Errorf("marshal versioning configuration: %w", err)
	}
	return c.putBucketSubresource(ctx, bucket, "versioning", nil, body)
}

// GetBucketVersioning returns the status, "" when versioning was never
// configured (the gateway answers an empty VersioningConfiguration), and
// ("", nil) when the bucket does not exist.
func (c *Client) GetBucketVersioning(ctx context.Context, bucket string) (string, error) {
	payload, err := c.getBucketSubresource(ctx, bucket, "versioning", "NoSuchBucket")
	if err != nil || payload == nil {
		return "", err
	}
	var cfg versioningConfiguration
	if err := xml.Unmarshal(payload, &cfg); err != nil {
		return "", fmt.Errorf("parse versioning configuration: %w", err)
	}
	return cfg.Status, nil
}

// ObjectLockConfiguration mirrors the S3 document. A nil Rule means lock is
// enabled without a default retention; the gateway then answers an empty
// <Rule></Rule>, which unmarshals to a Rule with an empty DefaultRetention.
type ObjectLockConfiguration struct {
	XMLName           xml.Name        `xml:"ObjectLockConfiguration"`
	ObjectLockEnabled string          `xml:"ObjectLockEnabled"`
	Rule              *ObjectLockRule `xml:"Rule,omitempty"`
}

// ObjectLockRule holds the default retention applied to new objects.
type ObjectLockRule struct {
	DefaultRetention *DefaultRetention `xml:"DefaultRetention,omitempty"`
}

// DefaultRetention is the retention mode and period. Exactly one of Days
// and Years is set.
type DefaultRetention struct {
	Mode  string `xml:"Mode"`
	Days  int    `xml:"Days,omitempty"`
	Years int    `xml:"Years,omitempty"`
}

// PutObjectLockConfiguration sets the object lock configuration. The
// gateway requires versioning to be Enabled first (InvalidBucketState).
func (c *Client) PutObjectLockConfiguration(ctx context.Context, bucket string, cfg ObjectLockConfiguration) error {
	body, err := xml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal object lock configuration: %w", err)
	}
	return c.putBucketSubresource(ctx, bucket, "object-lock", nil, body)
}

// GetObjectLockConfiguration returns the configuration, or nil when the
// bucket has none or does not exist. An empty Rule from the gateway is
// normalised to no Rule.
func (c *Client) GetObjectLockConfiguration(ctx context.Context, bucket string) (*ObjectLockConfiguration, error) {
	payload, err := c.getBucketSubresource(ctx, bucket, "object-lock", "ObjectLockConfigurationNotFoundError")
	if err != nil || payload == nil {
		return nil, err
	}
	var cfg ObjectLockConfiguration
	if err := xml.Unmarshal(payload, &cfg); err != nil {
		return nil, fmt.Errorf("parse object lock configuration: %w", err)
	}
	if cfg.Rule != nil && cfg.Rule.DefaultRetention == nil {
		cfg.Rule = nil
	}
	return &cfg, nil
}

// Ownership controls. Unlike versioning and object lock these have a real
// DELETE route (measured: the bucket survives it).

// ObjectOwnership values the gateway accepts.
const (
	OwnershipBucketOwnerEnforced  = "BucketOwnerEnforced"
	OwnershipBucketOwnerPreferred = "BucketOwnerPreferred"
	OwnershipObjectWriter         = "ObjectWriter"
)

type ownershipControls struct {
	XMLName xml.Name `xml:"OwnershipControls"`
	Rules   []struct {
		ObjectOwnership string `xml:"ObjectOwnership"`
	} `xml:"Rule"`
}

// PutBucketOwnershipControls sets the object ownership rule.
func (c *Client) PutBucketOwnershipControls(ctx context.Context, bucket, ownership string) error {
	var cfg ownershipControls
	cfg.Rules = append(cfg.Rules, struct {
		ObjectOwnership string `xml:"ObjectOwnership"`
	}{ownership})
	body, err := xml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal ownership controls: %w", err)
	}
	return c.putBucketSubresource(ctx, bucket, "ownershipControls", nil, body)
}

// GetBucketOwnershipControls returns the object ownership, or "" when the
// bucket has no controls (after a delete) or does not exist. A fresh bucket
// answers BucketOwnerEnforced without anyone having set it — that is the
// gateway's default, and it is what disables ACLs.
func (c *Client) GetBucketOwnershipControls(ctx context.Context, bucket string) (string, error) {
	payload, err := c.getBucketSubresource(ctx, bucket, "ownershipControls", "OwnershipControlsNotFoundError")
	if err != nil || payload == nil {
		return "", err
	}
	var cfg ownershipControls
	if err := xml.Unmarshal(payload, &cfg); err != nil {
		return "", fmt.Errorf("parse ownership controls: %w", err)
	}
	if len(cfg.Rules) == 0 {
		return "", nil
	}
	return cfg.Rules[0].ObjectOwnership, nil
}

// DeleteBucketOwnershipControls removes the controls.
func (c *Client) DeleteBucketOwnershipControls(ctx context.Context, bucket string) error {
	return c.deleteBucketSubresource(ctx, bucket, "ownershipControls", "OwnershipControlsNotFoundError")
}
