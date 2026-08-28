package client

import (
	"encoding/xml"
	"fmt"
	"net/http"
)

// Role values the gateway accepts. `user` cannot create buckets and sees only
// what it owns; `userplus` is the same with bucket policy support; `admin` can
// create buckets and manage accounts.
const (
	RoleUser     = "user"
	RoleUserPlus = "userplus"
	RoleAdmin    = "admin"
)

// Roles lists every accepted role, in the order the upstream error message
// names them.
func Roles() []string { return []string{RoleUser, RoleAdmin, RoleUserPlus} }

// Account mirrors auth.Account upstream. The gateway marshals and unmarshals
// it by Go field name — it carries json tags only — so the element names below
// are the field names and must not be renamed.
type Account struct {
	Access    string
	Secret    string
	Role      string
	UserID    int
	GroupID   int
	ProjectID int
}

// MutableProps mirrors auth.MutableProps. Pointers distinguish "leave alone"
// from "set to zero": a nil Secret keeps the stored key, an empty one would
// replace it.
type MutableProps struct {
	Secret    *string
	Role      string
	UserID    *int
	GroupID   *int
	ProjectID *int
}

// Bucket mirrors s3response.Bucket.
type Bucket struct {
	Name  string
	Owner string
}

type listUsersResult struct {
	XMLName  xml.Name  `xml:"ListUserAccountsResult"`
	Accounts []Account `xml:"Accounts"`
}

type listBucketsResult struct {
	XMLName xml.Name `xml:"ListBucketsResult"`
	Buckets []Bucket `xml:"Buckets"`
}

// APIError is an error response from the gateway. The gateway answers in the
// S3 error shape for both APIs, with admin-specific codes prefixed X.
type APIError struct {
	XMLName    xml.Name `xml:"Error"`
	Code       string   `xml:"Code"`
	Message    string   `xml:"Message"`
	StatusCode int      `xml:"-"`
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Code, e.StatusCode)
	}
	return fmt.Sprintf("%s: %s (HTTP %d)", e.Code, e.Message, e.StatusCode)
}

// IsNotFound reports whether the object the request addressed does not exist.
//
// The codes are listed because the APIs disagree: the admin API answers
// XAdminUserNotFound, the S3 API answers NoSuchBucket, and each bucket
// sub-resource has a code of its own (s3err/s3err.go upstream). A 404 alone
// is not enough to go by — a wrong endpoint path produces one too.
func (e *APIError) IsNotFound() bool {
	switch e.Code {
	case "XAdminUserNotFound", "NoSuchBucket", "NoSuchKey",
		"NoSuchBucketPolicy", "NoSuchCORSConfiguration", "NoSuchWebsiteConfiguration",
		"NoSuchTagSet", "ObjectLockConfigurationNotFoundError", "OwnershipControlsNotFoundError":
		return true
	}
	return e.StatusCode == http.StatusNotFound
}

// IsNotImplemented reports whether the gateway refuses the operation as
// such — a feature its backend lacks, or one switched off on the command
// line (--disable-acl). Distinct from a bad request: nothing in the
// configuration fixes it.
func (e *APIError) IsNotImplemented() bool {
	return e.Code == "NotImplemented" || e.Code == "XAdminMethodNotSupported"
}

// IsConflict reports whether the object already exists.
func (e *APIError) IsConflict() bool {
	switch e.Code {
	case "XAdminUserExists", "BucketAlreadyExists", "BucketAlreadyOwnedByYou":
		return true
	}
	return false
}
