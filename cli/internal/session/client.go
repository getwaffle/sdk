// Package session is the waffle CLI's client for the dashboard-
// authenticated merchant-account surface: POST /v1/merchants/login and
// the /v1/merchants/me/* routes. It is deliberately private to the CLI —
// these are account-management calls, out of scope for the merchant
// transactional SDK (github.com/very-good-labs/waffle-go), whose
// API-key client the CLI reuses for every money-moving command.
//
// A session token must never reach a money-moving endpoint and an API
// key must never reach a /me endpoint (per the contract a session token
// cannot create a charge or payout at all). This package only ever
// attaches the session token; the CLI's money commands only ever attach
// the API key via the waffle-go client.
package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	waffle "github.com/very-good-labs/waffle-go"
)

// Client calls the /v1/merchants session surface with a dashboard
// session token. Construct with New.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the *http.Client (tests inject time outs).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// New builds a session client. token is the dashboard session from
// Login; empty is valid only for the Login call itself.
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, httpClient: http.DefaultClient}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// doJSON mirrors waffle-go's request/decode discipline: JSON in and
// out, Content-Type on bodies, Bearer session token, non-2xx translated
// to *waffle.APIError using the frozen {"error": "..."} shape.
func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("session: encoding request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("session: building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("session: performing request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("session: reading response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var eb struct {
			Error string `json:"error"`
		}
		msg := string(respBody)
		if json.Unmarshal(respBody, &eb) == nil && eb.Error != "" {
			msg = eb.Error
		}
		return fmt.Errorf("session: request failed: %w", &waffle.APIError{StatusCode: resp.StatusCode, Message: msg})
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("session: decoding response body: %w", err)
		}
	}
	return nil
}

// Login exchanges email+password for a dashboard session token via
// POST /v1/merchants/login. A 401 means wrong credentials — the server
// deliberately returns one indistinguishable response for a wrong
// password, an unknown email, and an account with no password set.
func (c *Client) Login(ctx context.Context, email, password string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/v1/merchants/login", map[string]string{
		"email": email, "password": password,
	}, &out)
	if err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("session: login succeeded but response carried no token")
	}
	return out.Token, nil
}

// Profile is GET /v1/merchants/me/profile's response: the caller's own
// merchant account. Field names match the running handlers in
// internal/httpapi/merchant_me.go (toProfileResponse).
type Profile struct {
	MerchantID      string  `json:"merchant_id"`
	BusinessName    string  `json:"business_name"`
	Email           string  `json:"email"`
	EntityKind      string  `json:"entity_kind"`
	BusinessType    string  `json:"business_type"`
	OwnerName       string  `json:"owner_name"`
	BusinessURL     string  `json:"business_url"`
	Status          string  `json:"status"`
	ReviewStatus    string  `json:"review_status"`
	KYCSubmittedAt  *string `json:"kyc_submitted_at"`
	EmailVerifiedAt *string `json:"email_verified_at"`
	CreatedAt       string  `json:"created_at"`
	Role            string  `json:"role"`
}

// GetProfile fetches the caller's merchant profile.
func (c *Client) GetProfile(ctx context.Context) (*Profile, error) {
	var out Profile
	if err := c.doJSON(ctx, http.MethodGet, "/v1/merchants/me/profile", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// APIKeySummary is one row of GET /v1/merchants/me/api-keys. The
// plaintext key is never listed — only its Prefix.
type APIKeySummary struct {
	ID        string  `json:"id"`
	Mode      string  `json:"mode"`
	Prefix    string  `json:"key_prefix"`
	Name      *string `json:"name"`
	CreatedAt string  `json:"created_at"`
	RevokedAt *string `json:"revoked_at"`
}

// ListAPIKeys lists the caller's API keys (all modes; revoked included).
func (c *Client) ListAPIKeys(ctx context.Context) ([]APIKeySummary, error) {
	var out []APIKeySummary
	if err := c.doJSON(ctx, http.MethodGet, "/v1/merchants/me/api-keys", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// IssuedKey is POST /v1/merchants/me/api-keys' response. Plaintext is
// shown exactly once, at creation.
type IssuedKey struct {
	Mode   string `json:"mode"`
	APIKey string `json:"api_key"`
}

// CreateAPIKey mints a new SANDBOX API key for the caller's account
// (name is an optional label). Live keys are exclusively admin-issued —
// the self-service route cannot and must not produce one.
func (c *Client) CreateAPIKey(ctx context.Context, name string) (*IssuedKey, error) {
	var out IssuedKey
	err := c.doJSON(ctx, http.MethodPost, "/v1/merchants/me/api-keys", map[string]string{"name": name}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeAPIKey revokes one of the caller's own keys. Revoking an
// unknown or already-revoked key is a 404.
func (c *Client) RevokeAPIKey(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/merchants/me/api-keys/"+id+"/revoke", nil, nil)
}
