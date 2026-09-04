// Package godaddy provides a client for the GoDaddy API to manage DNS records.
package godaddy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultAPIBase is the default GoDaddy API endpoint
	DefaultAPIBase = "https://api.godaddy.com/v1"

	// DefaultTimeout is the default HTTP client timeout
	DefaultTimeout = 30 * time.Second
)

// Client is a GoDaddy API client for DNS management
type Client struct {
	apiKey    string
	apiSecret string
	apiBase   string
	client    *http.Client
}

// DNSRecord represents a DNS record in GoDaddy
type DNSRecord struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Data     string `json:"data"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority,omitempty"`
}

// DomainInfo represents domain information from GoDaddy
type DomainInfo struct {
	Domain    string `json:"domain"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires"`
}

// APIError represents an error response from GoDaddy API
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fields  []struct {
		Path    string `json:"path"`
		Message string `json:"message"`
	} `json:"fields,omitempty"`
}

func (e *APIError) Error() string {
	if len(e.Fields) > 0 {
		return fmt.Sprintf("%s: %s (field: %s - %s)", e.Code, e.Message, e.Fields[0].Path, e.Fields[0].Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewClient creates a new GoDaddy API client
func NewClient(apiKey, apiSecret string) *Client {
	return &Client{
		apiKey:    apiKey,
		apiSecret: apiSecret,
		apiBase:   DefaultAPIBase,
		client: &http.Client{
			Timeout: DefaultTimeout,
		},
	}
}

// WithAPIBase sets a custom API base URL (useful for testing)
func (c *Client) WithAPIBase(base string) *Client {
	c.apiBase = base
	return c
}

// authHeader returns the authorization header value
func (c *Client) authHeader() string {
	return fmt.Sprintf("sso-key %s:%s", c.apiKey, c.apiSecret)
}

// doRequest performs an HTTP request with authentication
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", c.authHeader())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	return c.client.Do(req)
}

// parseError parses an error response from the API
func (c *Client) parseError(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("HTTP %d: failed to read error body", resp.StatusCode)
	}

	var apiErr APIError
	if err := json.Unmarshal(body, &apiErr); err != nil {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return &apiErr
}

// VerifyCredentials verifies that the API credentials are valid
func (c *Client) VerifyCredentials(ctx context.Context) error {
	resp, err := c.doRequest(ctx, http.MethodGet, "/domains", nil)
	if err != nil {
		return fmt.Errorf("verify credentials: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("invalid API credentials")
	}

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}

	return nil
}

// GetDomain retrieves information about a specific domain
func (c *Client) GetDomain(ctx context.Context, domain string) (*DomainInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/domains/"+domain, nil)
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("domain '%s' not found in your GoDaddy account", domain)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var info DomainInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode domain info: %w", err)
	}

	return &info, nil
}

// GetRecords retrieves DNS records for a domain
func (c *Client) GetRecords(ctx context.Context, domain string, recordType string) ([]DNSRecord, error) {
	path := fmt.Sprintf("/domains/%s/records/%s", domain, recordType)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get records: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var records []DNSRecord
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		return nil, fmt.Errorf("decode records: %w", err)
	}

	return records, nil
}

// SetRecord creates or updates a DNS record
// This replaces all records of the given type and name
func (c *Client) SetRecord(ctx context.Context, domain string, record DNSRecord) error {
	path := fmt.Sprintf("/domains/%s/records/%s/%s", domain, record.Type, record.Name)

	// GoDaddy API expects an array of records
	records := []DNSRecord{{
		Data: record.Data,
		TTL:  record.TTL,
	}}

	resp, err := c.doRequest(ctx, http.MethodPut, path, records)
	if err != nil {
		return fmt.Errorf("set record: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}

	return nil
}

// SetARecord is a convenience method to set an A record
func (c *Client) SetARecord(ctx context.Context, domain, name, ip string, ttl int) error {
	if ttl <= 0 {
		ttl = 600 // Default 10 minutes
	}

	return c.SetRecord(ctx, domain, DNSRecord{
		Type: "A",
		Name: name,
		Data: ip,
		TTL:  ttl,
	})
}

// SetupHostingRecords creates the required DNS records for app hosting:
// - A record for @ (main domain) pointing to the server IP
// - A record for * (wildcard) pointing to the server IP (if includeWildcard is true)
func (c *Client) SetupHostingRecords(ctx context.Context, domain, serverIP string, includeWildcard bool) error {
	const ttl = 600 // 10 minutes

	// Create main domain A record (@)
	if err := c.SetARecord(ctx, domain, "@", serverIP, ttl); err != nil {
		return fmt.Errorf("create main domain A record: %w", err)
	}

	// Create wildcard A record (*) if requested
	if includeWildcard {
		if err := c.SetARecord(ctx, domain, "*", serverIP, ttl); err != nil {
			return fmt.Errorf("create wildcard A record: %w", err)
		}
	}

	return nil
}

// DeleteRecord deletes a DNS record
func (c *Client) DeleteRecord(ctx context.Context, domain, recordType, name string) error {
	path := fmt.Sprintf("/domains/%s/records/%s/%s", domain, recordType, name)

	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("delete record: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}

	return nil
}
