// Package client is a thin HTTP client for the Versity S3 Gateway.
//
// The gateway has two APIs and this package speaks both, because neither is
// enough on its own: accounts and bucket ownership live behind the admin API,
// while deleting a bucket exists only in the S3 API. Every request is signed
// with AWS SigV4 for service "s3" — the admin API uses the same signature
// scheme as the data path.
package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// DefaultRegion matches the gateway's own default for --region.
const DefaultRegion = "us-east-1"

// Config describes how to reach a gateway.
type Config struct {
	// Endpoint is the S3 API, e.g. http://s3.example.com.
	Endpoint string
	// AdminEndpoint is the admin API. When the gateway runs without
	// --admin-port it serves the admin routes on the S3 listener, so leaving
	// this empty falls back to Endpoint.
	AdminEndpoint string
	AccessKey     string
	SecretKey     string
	Region        string
	// Insecure skips TLS certificate verification.
	Insecure bool
	// Timeout bounds a single request. Zero selects 60s.
	Timeout time.Duration
}

// Client talks to one gateway.
type Client struct {
	cfg  Config
	http *http.Client
}

// New validates the configuration and returns a client.
func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("access_key and secret_key are required")
	}
	for _, ep := range []string{cfg.Endpoint, cfg.AdminEndpoint} {
		if ep == "" {
			continue
		}
		u, err := url.Parse(ep)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", ep, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("endpoint %q must use http or https", ep)
		}
	}

	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	if cfg.AdminEndpoint == "" {
		cfg.AdminEndpoint = cfg.Endpoint
	}
	cfg.AdminEndpoint = strings.TrimRight(cfg.AdminEndpoint, "/")
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}

	httpClient := &http.Client{Timeout: cfg.Timeout}
	if cfg.Insecure {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 — opt-in
		httpClient.Transport = transport
	}

	return &Client{cfg: cfg, http: httpClient}, nil
}

// Endpoint returns the configured S3 endpoint.
func (c *Client) Endpoint() string { return c.cfg.Endpoint }

// AdminEndpoint returns the configured admin endpoint.
func (c *Client) AdminEndpoint() string { return c.cfg.AdminEndpoint }

// do signs and performs one request. Headers must be set before signing —
// SigV4 covers them, and one added afterwards invalidates the signature.
func (c *Client) do(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	sum := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}

	creds := aws.Credentials{AccessKeyID: c.cfg.AccessKey, SecretAccessKey: c.cfg.SecretKey}
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, payloadHash, "s3", c.cfg.Region, time.Now()); err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, parseAPIError(resp.StatusCode, payload)
	}
	return payload, nil
}

// parseAPIError turns an error body into an APIError. A body that is not the
// expected XML still produces a usable error rather than a parse failure —
// that case is a proxy answering instead of the gateway, and hiding it behind
// "malformed XML" sends the reader looking in the wrong place.
func parseAPIError(status int, body []byte) error {
	apiErr := &APIError{StatusCode: status}
	if err := xml.Unmarshal(body, apiErr); err != nil || apiErr.Code == "" {
		apiErr.Code = http.StatusText(status)
		apiErr.Message = strings.TrimSpace(string(body))
		if apiErr.Message == "" {
			apiErr.Message = "no response body"
		}
	}
	return apiErr
}

func (c *Client) adminURL(path string, query url.Values) string {
	u := c.cfg.AdminEndpoint + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}
