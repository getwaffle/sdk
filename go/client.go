// Package paybridge is a Go client SDK for the Paybridge merchant API
// (the ":8080" listener in the Paybridge backend). It is deliberately its
// own Go module (see go.mod: module github.com/very-good-labs/paybridge-go)
// rather than a subpackage of the backend monorepo module
// (github.com/very-good-labs/paybridge), so that merchants can
// `go get github.com/very-good-labs/paybridge-go` without pulling in the
// entire backend (internal/httpapi, internal/domain, database drivers,
// etc.) or being tied to the backend's release cadence. It has zero
// dependency on the backend module.
//
// All money amounts throughout this package are integers in the
// currency's minor unit (e.g. IDR 12500 means Rp12.500) and are always
// represented as int64 — never float64 — per the frozen API contract.
package paybridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// DefaultBaseURL is the default merchant API base URL, matching local dev
// via docker-compose.yml (see docs/api-contract.md).
const DefaultBaseURL = "http://localhost:8080"

// Client is a Paybridge merchant API client. Construct with NewClient.
// A Client is safe for concurrent use by multiple goroutines (it holds no
// mutable state after construction; the underlying *http.Client is itself
// safe for concurrent use).
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Client constructed by NewClient.
type Option func(*Client)

// WithBaseURL overrides the merchant API base URL. Default is
// DefaultBaseURL ("http://localhost:8080"), suitable for local dev against
// docker-compose.yml. Point this at your production/staging merchant API
// host in other environments. The value must not have a trailing slash
// requirement — trailing slashes are trimmed automatically.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		for len(u) > 0 && u[len(u)-1] == '/' {
			u = u[:len(u)-1]
		}
		c.baseURL = u
	}
}

// WithHTTPClient overrides the *http.Client used to make requests. Default
// is http.DefaultClient. Use this to set timeouts, custom transports,
// proxies, or tracing instrumentation.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// NewClient constructs a Paybridge merchant API client. apiKey is sent as
// `Authorization: Bearer <apiKey>` on every request except GET /healthz
// (which requires no auth per the contract, but the header is harmless to
// send and is omitted here to match the contract precisely).
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:     apiKey,
		baseURL:    DefaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError represents a non-2xx JSON error response from the Paybridge
// API, of the frozen shape `{"error": "human-readable message"}`. Callers
// can distinguish status codes (400/401/404/422/429/500 per the contract)
// via errors.As:
//
//	var apiErr *paybridge.APIError
//	if errors.As(err, &apiErr) {
//	    switch apiErr.StatusCode {
//	    case http.StatusUnprocessableEntity:
//	        // business-logic rejection
//	    case http.StatusTooManyRequests:
//	        // rate limit or fraud velocity limit; retry with backoff
//	    }
//	}
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("paybridge: HTTP %d: %s", e.StatusCode, e.Message)
}

// errorBody mirrors the frozen `{"error": "..."}` shape used by every
// non-2xx response on both the merchant and admin APIs.
type errorBody struct {
	Error string `json:"error"`
}

// doJSON performs an HTTP request with an optional JSON-encoded body and
// decodes a JSON response into out (if out is non-nil). It handles auth,
// Content-Type, non-2xx -> *APIError translation, and idempotency-key /
// query-param injection via the caller-supplied headers/query.
func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, headers map[string]string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("paybridge: encoding request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("paybridge: building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("paybridge: performing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("paybridge: reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var eb errorBody
		msg := string(respBody)
		if json.Unmarshal(respBody, &eb) == nil && eb.Error != "" {
			msg = eb.Error
		}
		return fmt.Errorf("paybridge: request failed: %w", &APIError{
			StatusCode: resp.StatusCode,
			Message:    msg,
		})
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("paybridge: decoding response body: %w", err)
		}
	}
	return nil
}

// GenerateIdempotencyKey returns a random, URL-safe, unique-enough string
// suitable for use as the required Idempotency-Key header on
// CreateCharge and CreatePayout. It uses crypto/rand and never depends on
// an external UUID library, keeping this module dependency-free.
//
// Idempotency keys are never generated silently by this SDK — every
// method that requires one takes it as an explicit caller-supplied
// parameter. Call this helper yourself when you don't have your own key
// scheme (e.g. an internal order id) to reuse.
func GenerateIdempotencyKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read failing on a supported platform is
		// exceptionally rare (kernel entropy source unavailable);
		// panicking here matches the stdlib's own crypto/rand
		// behavior guidance rather than silently returning a
		// non-unique key.
		panic("paybridge: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// Healthz calls GET /healthz, which requires no auth and returns plain
// text "ok" (not JSON) on success. It returns nil on a 2xx response and a
// wrapped *APIError otherwise.
func (c *Client) Healthz(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("paybridge: building request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("paybridge: performing request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("paybridge: request failed: %w", &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
		})
	}
	return nil
}

// --- Charges ---------------------------------------------------------------

// CreateChargeParams is the request body for CreateCharge (POST
// /v1/charges). There is no Provider field: the server always
// auto-routes to the merchant's highest-priority connected PSP (xendit >
// doku > gdc > sandbox) — which PSPs are connected, and their priority
// order, is an admin-controlled decision the merchant never names or is
// told. Metadata is optional; leave nil to omit it.
type CreateChargeParams struct {
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description,omitempty"`
	CustomerRef string `json:"customer_ref,omitempty"`
	ReturnURL   string `json:"return_url,omitempty"`
	// Channel selects a specific payment channel instead of the default
	// redirect-based checkout flow. Accepted values are "qris" and
	// "virtual_account"; leave it as the empty string for the original
	// redirect-only behavior (a CheckoutURL is returned). VABank is
	// required only when Channel is "virtual_account".
	Channel          string            `json:"channel,omitempty"`
	VABank           string            `json:"va_bank,omitempty"`
	ExpiresInMinutes int               `json:"expires_in_minutes,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// Charge is the response shape for CreateCharge (POST /v1/charges,
// 201). Status is one of "pending", "paid", "failed", "expired". There is
// no Provider field — which PSP handled the charge is never surfaced to
// the merchant.
type Charge struct {
	ID          string `json:"id"`
	Mode        string `json:"mode"`
	Status      string `json:"status"`
	GrossAmount int64  `json:"gross_amount"`
	FeeAmount   int64  `json:"fee_amount"`
	NetAmount   int64  `json:"net_amount"`
	Currency    string `json:"currency"`
	// Channel echoes the request's Channel, empty when the default
	// redirect flow was used.
	Channel string `json:"channel,omitempty"`
	// CheckoutURL is present for the default redirect flow (Channel
	// empty); empty when a channel was requested.
	CheckoutURL string `json:"checkout_url,omitempty"`
	// QRString is the raw QRIS payload string, present only when Channel
	// was "qris".
	QRString string `json:"qr_string,omitempty"`
	// VABank and VANumber are present only when Channel was
	// "virtual_account".
	VABank    string            `json:"va_bank,omitempty"`
	VANumber  string            `json:"va_number,omitempty"`
	CreatedAt string            `json:"created_at"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// CreateCharge calls POST /v1/charges. idempotencyKey is required (sent as
// the Idempotency-Key header) — retrying the exact same key returns the
// original charge rather than creating a duplicate. Use
// GenerateIdempotencyKey or your own scheme (e.g. an internal order id);
// this SDK never generates one silently.
//
// Possible errors (via errors.As(err, &apiErr)): 400 malformed
// amount/currency; 422 no gateway/fee rule for the auto-routed provider,
// zero connected PSPs, or the provider call failed; 429 fraud velocity
// limit exceeded.
func (c *Client) CreateCharge(ctx context.Context, params CreateChargeParams, idempotencyKey string) (*Charge, error) {
	if idempotencyKey == "" {
		return nil, errors.New("paybridge: idempotencyKey is required for CreateCharge")
	}
	var out Charge
	err := c.doJSON(ctx, http.MethodPost, "/v1/charges", nil,
		map[string]string{"Idempotency-Key": idempotencyKey}, params, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Fees --------------------------------------------------------------

// CalculateFeeParams is the request body for CalculateFee (POST
// /v1/fees/calculate). There is no Provider field: the quote resolves
// against the same auto-routed provider a real CreateCharge would use,
// so a previewed fee always matches what a real charge would be billed.
type CalculateFeeParams struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	// Channel narrows the quote to one product under the auto-routed
	// provider (e.g. "qris" vs "virtual_account" can price differently).
	// Empty matches only a wildcard-channel fee rule, mirroring the
	// server's calculateFeeRequest. VABank narrows a
	// Channel="virtual_account" quote to one bank; it is ignored by the
	// server for non-VA quotes.
	Channel string `json:"channel,omitempty"`
	VABank  string `json:"va_bank,omitempty"`
}

// FeeQuote is the response shape for CalculateFee (POST
// /v1/fees/calculate, 200). This is a preview only: no charge, no ledger
// write, no provider call is made.
type FeeQuote struct {
	GrossAmount int64  `json:"gross_amount"`
	FeeAmount   int64  `json:"fee_amount"`
	NetAmount   int64  `json:"net_amount"`
	Currency    string `json:"currency"`
}

// CalculateFee calls POST /v1/fees/calculate. No Idempotency-Key header is
// sent or needed (preview-only, no side effects).
func (c *Client) CalculateFee(ctx context.Context, params CalculateFeeParams) (*FeeQuote, error) {
	var out FeeQuote
	err := c.doJSON(ctx, http.MethodPost, "/v1/fees/calculate", nil, nil, params, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Bank accounts -------------------------------------------------------

// RegisterBankAccountParams is the request body for RegisterBankAccount
// (POST /v1/bank-accounts). All three fields are required; the server
// returns 400 if any is empty.
type RegisterBankAccountParams struct {
	BankCode          string `json:"bank_code"`
	AccountNumber     string `json:"account_number"`
	AccountHolderName string `json:"account_holder_name"`
}

// BankAccount is the response shape for RegisterBankAccount (POST
// /v1/bank-accounts, 201) and GetActiveBankAccount (GET
// /v1/bank-accounts, 200).
type BankAccount struct {
	ID                string `json:"id"`
	BankCode          string `json:"bank_code"`
	AccountNumber     string `json:"account_number"`
	AccountHolderName string `json:"account_holder_name"`
	// CreatedAt is when this became the active withdrawal account for
	// the caller's mode — empty on RegisterBankAccount's own response
	// (echoed by the server; use GetActiveBankAccount to read it). A
	// payout drawn against an account within 6 hours of this timestamp
	// is held rather than dispatched; see Payout.Status's "held" doc.
	CreatedAt string `json:"created_at,omitempty"`
}

// RegisterBankAccount calls POST /v1/bank-accounts. Registering a new
// account disables any prior active one for the caller's mode — only one
// active withdrawal destination per mode at a time.
func (c *Client) RegisterBankAccount(ctx context.Context, params RegisterBankAccountParams) (*BankAccount, error) {
	var out BankAccount
	err := c.doJSON(ctx, http.MethodPost, "/v1/bank-accounts", nil, nil, params, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetActiveBankAccount calls GET /v1/bank-accounts, returning the
// caller's current active withdrawal account for their mode. Returns a
// wrapped *APIError with StatusCode 404 if the merchant has never
// registered one for this mode.
func (c *Client) GetActiveBankAccount(ctx context.Context) (*BankAccount, error) {
	var out BankAccount
	err := c.doJSON(ctx, http.MethodGet, "/v1/bank-accounts", nil, nil, nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Payouts -------------------------------------------------------------

// CreatePayoutParams is the request body for CreatePayout (POST
// /v1/payouts). There is no Provider field: like CreateCharge, this
// auto-routes to the merchant's highest-priority connected PSP — the
// asymmetry where payouts once required naming a provider explicitly is
// gone.
type CreatePayoutParams struct {
	BankAccountID string `json:"bank_account_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
}

// Payout is the response shape for CreatePayout (POST /v1/payouts, 201).
// Status is one of "pending", "held", "processing", "completed",
// "failed". "held" means the payout was claimed (debited) but drawn
// against a bank account registered within the last 6 hours — a security
// hold on withdrawal-account changes (see BankAccount.CreatedAt) — so it
// is deliberately not yet dispatched to a PSP; the server resumes it
// automatically once the account has aged past the window, no caller
// action needed. There is no Provider field — which PSP handled the
// payout is never surfaced to the merchant.
type Payout struct {
	ID            string `json:"id"`
	BankAccountID string `json:"bank_account_id"`
	Mode          string `json:"mode"`
	Status        string `json:"status"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	FailureReason string `json:"failure_reason,omitempty"`
}

// CreatePayout calls POST /v1/payouts. idempotencyKey is required (sent as
// the Idempotency-Key header), same semantics as CreateCharge.
//
// A 422 with body {"error":"insufficient available balance"} is returned
// specifically for payout.ErrInsufficientBalance — surfaced here as an
// *APIError with StatusCode 422 and that Message, same as any other 422.
func (c *Client) CreatePayout(ctx context.Context, params CreatePayoutParams, idempotencyKey string) (*Payout, error) {
	if idempotencyKey == "" {
		return nil, errors.New("paybridge: idempotencyKey is required for CreatePayout")
	}
	var out Payout
	err := c.doJSON(ctx, http.MethodPost, "/v1/payouts", nil,
		map[string]string{"Idempotency-Key": idempotencyKey}, params, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Balance -------------------------------------------------------------

// Balance is the response shape for GetBalance (GET /v1/balance, 200).
// Amount is the withdrawable balance (settled paid charges minus
// non-failed payouts) — a charge inside its settlement hold window does
// not count yet even if Status is "paid".
type Balance struct {
	Currency string `json:"currency"`
	Amount   int64  `json:"amount"`
}

// GetBalance calls GET /v1/balance?currency=<currency>. Per the contract,
// the server itself defaults currency to "IDR" when the query param is
// omitted; this SDK passes an empty currency straight through (omitting
// the query param entirely) rather than second-guessing the server's
// default, so that a future change to the server default is inherited by
// callers automatically. Pass "IDR" explicitly if you want to be
// unambiguous at the call site regardless of server-side defaults.
func (c *Client) GetBalance(ctx context.Context, currency string) (*Balance, error) {
	var query url.Values
	if currency != "" {
		query = url.Values{"currency": []string{currency}}
	}

	var out Balance
	err := c.doJSON(ctx, http.MethodGet, "/v1/balance", query, nil, nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Banks ---------------------------------------------------------------

// Bank is one entry of GET /v1/banks's response: the public, active-only
// bank directory (ordered by SortOrder) backing virtual-account bank
// pickers. LogoURL is nil when the bank has no logo on file.
type Bank struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	LogoURL   *string `json:"logo_url"`
	SortOrder int     `json:"sort_order"`
}

// ListBanks calls GET /v1/banks. The route is unauthenticated by design
// (public checkout UI uses it before any API key exists); this SDK sends
// the Authorization header only when the client was constructed with a
// non-empty key, and the server accepts the call either way.
func (c *Client) ListBanks(ctx context.Context) ([]Bank, error) {
	var out []Bank
	err := c.doJSON(ctx, http.MethodGet, "/v1/banks", nil, nil, nil, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}
