// Package waffle is a Go client SDK for the Waffle merchant API. It is deliberately its
// own Go module (see go.mod: module github.com/getwaffle/sdk/go)
// rather than a subpackage of the backend monorepo module
// (github.com/very-good-labs/waffle), so that merchants can
// `go get github.com/getwaffle/sdk/go` without pulling in the
// entire backend (internal/httpapi, internal/domain, database drivers,
// etc.) or being tied to the backend's release cadence. It has zero
// dependency on the backend module.
//
// All money amounts throughout this package are integers in the
// currency's minor unit (e.g. IDR 12500 means Rp12.500) and are always
// represented as int64 — never float64 — per the frozen API contract.
package waffle

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// DefaultBaseURL is the default merchant API base URL — Waffle's
// production API.
const DefaultBaseURL = "https://api.getwaffle.id"

// Client is a Waffle merchant API client. Construct with NewClient.
// A Client is safe for concurrent use by multiple goroutines (it holds no
// mutable state after construction; the underlying *http.Client is itself
// safe for concurrent use).
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	// baseURLErr is set by WithBaseURL when the URL fails validation; every
	// request method returns it before any network I/O.
	baseURLErr error
}

// ErrInvalidBaseURL is returned (wrapped; test with errors.Is) by every
// request method when the client was built with a WithBaseURL value that
// is not acceptable.
var ErrInvalidBaseURL = errors.New("waffle: invalid base URL")

// ErrResponseTooLarge is returned (wrapped; test with errors.Is) when a
// response body exceeds the SDK's size cap (8 MiB for successful
// responses).
var ErrResponseTooLarge = errors.New("waffle: response body too large")

const (
	maxResponseBytes      = 8 << 20  // successful bodies
	maxErrorResponseBytes = 64 << 10 // non-2xx bodies
	maxErrorMessageBytes  = 512      // APIError.Message
	defaultHTTPTimeout    = 30 * time.Second
)

// Option configures a Client constructed by NewClient.
type Option func(*Client)

// validateBaseURL checks u and returns the normalised base URL
// (scheme://host, no trailing slash). Rules: scheme must be https (http is
// allowed only for localhost, 127.0.0.1 and ::1); no userinfo, query or
// fragment; non-empty host; no path other than trailing slashes.
func validateBaseURL(raw string) (string, error) {
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("%w: %s", ErrInvalidBaseURL, why)
	}
	trimmed := strings.TrimRight(raw, "/")
	u, err := url.Parse(trimmed)
	if err != nil {
		return bad("cannot be parsed")
	}
	if u.Host == "" || u.Hostname() == "" {
		return bad("host is empty")
	}
	if u.User != nil {
		return bad("must not contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery || strings.Contains(trimmed, "?") {
		return bad("must not contain a query")
	}
	if u.Fragment != "" || strings.Contains(trimmed, "#") {
		return bad("must not contain a fragment")
	}
	if u.Opaque != "" || u.Path != "" || u.RawPath != "" {
		return bad("must not contain a path")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1", "::1":
		default:
			return bad("http is only allowed for localhost, 127.0.0.1 and ::1")
		}
	default:
		return bad("scheme must be https")
	}
	return strings.ToLower(u.Scheme) + "://" + u.Host, nil
}

// WithBaseURL overrides the merchant API base URL. Default is
// DefaultBaseURL (Waffle's production API, "https://api.getwaffle.id").
// Trailing slashes are trimmed automatically.
//
// The URL is validated: the scheme must be https (plain http is accepted
// only for localhost, 127.0.0.1 and ::1, for local development); userinfo,
// query, fragment and any path are rejected, and the host must be
// non-empty. NewClient's signature does not return an error, so an invalid
// URL is recorded on the client and every request method then returns an
// error matching ErrInvalidBaseURL (errors.Is) before any network I/O.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		norm, err := validateBaseURL(u)
		if err != nil {
			c.baseURLErr = err
			return
		}
		c.baseURL = norm
		c.baseURLErr = nil
	}
}

// WithHTTPClient overrides the *http.Client used to make requests. Default
// is a client with a 30 s Timeout that never follows redirects. Use this to
// set timeouts, custom transports, proxies, or tracing instrumentation.
//
// Redirects: the SDK sends the API key and POST bodies, so following a
// redirect could forward them to another URL (or downgrade https to http).
// If hc.CheckRedirect is nil, the SDK uses a shallow copy of hc (your
// client is not modified) that returns the redirect response as-is
// (http.ErrUseLastResponse), surfacing as an *APIError. A CheckRedirect you
// set yourself is respected. Note that a client you pass with Timeout == 0
// has no timeout; set one or use context deadlines.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc == nil {
			c.httpClient = newDefaultHTTPClient()
			return
		}
		if hc.CheckRedirect == nil {
			cp := *hc
			cp.CheckRedirect = noRedirect
			hc = &cp
		}
		c.httpClient = hc
	}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func newDefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultHTTPTimeout, CheckRedirect: noRedirect}
}

// NewClient constructs a Waffle merchant API client. apiKey is sent as
// `Authorization: Bearer <apiKey>` on every request except GET /healthz
// (which requires no auth per the contract, but the header is harmless to
// send and is omitted here to match the contract precisely).
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:     apiKey,
		baseURL:    DefaultBaseURL,
		httpClient: newDefaultHTTPClient(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// redactedKey keeps only the mode prefix (sk_live_ / sk_test_), never any
// of the secret part.
func redactedKey(k string) string {
	if k == "" {
		return "<empty>"
	}
	for _, p := range []string{"sk_live_", "sk_test_"} {
		if strings.HasPrefix(k, p) {
			return p + "****"
		}
	}
	return "****"
}

// String returns a redacted description so a Client printed with fmt or
// logged never leaks the API key.
func (c *Client) String() string {
	if c == nil {
		return "waffle.Client(nil)"
	}
	return "waffle.Client{baseURL: " + c.baseURL + ", apiKey: " + redactedKey(c.apiKey) + "}"
}

// GoString implements fmt.GoStringer (the %#v verb) with the same redaction.
func (c *Client) GoString() string { return c.String() }

// readCapped reads at most max bytes from r and reports ok=false if the
// body is larger.
func readCapped(r io.Reader, max int64) (body []byte, ok bool, err error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(b)) > max {
		return nil, false, nil
	}
	return b, true, nil
}

// readResponse reads resp.Body applying the success/error size caps. For a
// non-2xx response that exceeds the cap it returns an *APIError (keeping
// the status code) instead of the body.
func readResponse(resp *http.Response) ([]byte, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, ok, err := readCapped(resp.Body, maxErrorResponseBytes)
		if err != nil {
			return nil, fmt.Errorf("waffle: reading response body: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("waffle: request failed: %w", &APIError{
				StatusCode: resp.StatusCode,
				Message:    "error response body exceeded 64 KiB and was discarded",
			})
		}
		return nil, translateError(resp.StatusCode, b)
	}
	b, ok, err := readCapped(resp.Body, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("waffle: reading response body: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("%w (limit %d bytes)", ErrResponseTooLarge, int64(maxResponseBytes))
	}
	return b, nil
}

// sanitizeMessage makes server-supplied text safe to log and display:
// control, format and line/paragraph-separator characters become a space,
// invalid UTF-8 becomes '?', and the result is cut to 512 bytes on a rune
// boundary.
func sanitizeMessage(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s) && sb.Len() < maxErrorMessageBytes+utf8.UTFMax; {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size <= 1:
			sb.WriteByte('?')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == '\u2028', r == '\u2029':
			sb.WriteByte(' ')
		default:
			sb.WriteRune(r)
		}
	}
	out := sb.String()
	if len(out) > maxErrorMessageBytes {
		cut := maxErrorMessageBytes
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out = out[:cut]
	}
	return out
}

// APIError represents a non-2xx JSON error response from the Waffle
// API, of the frozen shape `{"error": "human-readable message"}`. Callers
// can distinguish status codes (400/401/404/422/429/500 per the contract)
// via errors.As:
//
//	var apiErr *waffle.APIError
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
	return fmt.Sprintf("waffle: HTTP %d: %s", e.StatusCode, e.Message)
}

// PermissionError is a typed layer over a 403 *APIError whose message
// matches the API key's scope model: `"this API key lacks the <scope>
// permission"`. Scope carries just the missing scope as a plain string
// (e.g. "charges:write") — this SDK has zero dependency on the
// backend's internal/apikey package, so it never imports a Scope type,
// it just parses the literal substring out of the error message.
//
// It wraps the underlying *APIError, so both errors.As targets work on
// the same error:
//
//	var permErr *waffle.PermissionError
//	if errors.As(err, &permErr) {
//	    log.Printf("missing scope: %s", permErr.Scope)
//	}
//	var apiErr *waffle.APIError
//	errors.As(err, &apiErr) // also succeeds — same underlying 403
type PermissionError struct {
	// Scope is the single scope the API key was missing, e.g.
	// "charges:write" or "payouts:read".
	Scope string
	// Message is the server's original error message.
	Message string
	// Err is the underlying *APIError (same StatusCode/Message) that
	// errors.As(err, &apiErr) unwraps to.
	Err *APIError
}

func (e *PermissionError) Error() string { return e.Message }

func (e *PermissionError) Unwrap() error { return e.Err }

// permissionErrorPattern extracts the missing scope from the server's
// 403 body: `{"error": "this API key lacks the <scope> permission"}`.
// The literal substring "lacks the " + scope + " permission" is the
// frozen contract this SDK parses against.
var permissionErrorPattern = regexp.MustCompile(`lacks the (\S+) permission`)

// errorBody mirrors the frozen `{"error": "..."}` shape used by every
// non-2xx response on both the merchant and admin APIs.
type errorBody struct {
	Error string `json:"error"`
}

// translateError converts a non-2xx status code and raw response body
// into the SDK's error hierarchy: a 403 matching the "lacks the <scope>
// permission" shape becomes a wrapped *PermissionError (itself wrapping
// the underlying *APIError); everything else becomes a wrapped
// *APIError directly. Shared by doJSON and doRaw so every call site
// (JSON and raw-bytes alike, e.g. the PDF receipt endpoints) gets the
// same typed-error treatment.
func translateError(statusCode int, respBody []byte) error {
	var eb errorBody
	msg := string(respBody)
	if json.Unmarshal(respBody, &eb) == nil && eb.Error != "" {
		msg = eb.Error
	}
	msg = sanitizeMessage(msg)
	apiErr := &APIError{StatusCode: statusCode, Message: msg}
	if statusCode == http.StatusForbidden {
		if m := permissionErrorPattern.FindStringSubmatch(msg); m != nil {
			return fmt.Errorf("waffle: request failed: %w", &PermissionError{
				Scope:   m[1],
				Message: msg,
				Err:     apiErr,
			})
		}
	}
	return fmt.Errorf("waffle: request failed: %w", apiErr)
}

// doJSON performs an HTTP request with an optional JSON-encoded body and
// decodes a JSON response into out (if out is non-nil). It handles auth,
// Content-Type, non-2xx -> *APIError translation, and idempotency-key /
// query-param injection via the caller-supplied headers/query.
func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, headers map[string]string, body any, out any) error {
	if c.baseURLErr != nil {
		return c.baseURLErr
	}
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("waffle: encoding request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("waffle: building request: %w", err)
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
		return fmt.Errorf("waffle: performing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := readResponse(resp)
	if err != nil {
		return err
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("waffle: decoding response body: %w", err)
		}
	}
	return nil
}

// doRaw performs an authenticated GET request and returns the raw
// response body verbatim, translating a non-2xx response the same way
// doJSON does. Used by the PDF receipt endpoints, whose success
// response is `application/pdf` bytes, not JSON.
func (c *Client) doRaw(ctx context.Context, method, path string) ([]byte, error) {
	if c.baseURLErr != nil {
		return nil, c.baseURLErr
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("waffle: building request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("waffle: performing request: %w", err)
	}
	defer resp.Body.Close()

	return readResponse(resp)
}

// GenerateIdempotencyKey returns a random, URL-safe, unique-enough string
// suitable for use as the required Idempotency-Key header on
// CreateCharge and CreatePayout. It uses crypto/rand and never depends on
// an external UUID library, keeping this module dependency-free.
//
// Generate ONE key per logical operation (e.g. per order) and reuse it
// on every retry of that operation. Passing the empty string to
// CreateCharge/CreatePayout generates a NEW key on every call, so it is
// not safe to wrap in a retry loop: a retry after a timeout could create a
// duplicate.
func GenerateIdempotencyKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read failing on a supported platform is
		// exceptionally rare (kernel entropy source unavailable);
		// panicking here matches the stdlib's own crypto/rand
		// behavior guidance rather than silently returning a
		// non-unique key.
		panic("waffle: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// Healthz calls GET /healthz, which requires no auth and returns plain
// text "ok" (not JSON) on success. It returns nil on a 2xx response and a
// wrapped *APIError otherwise.
func (c *Client) Healthz(ctx context.Context) error {
	if c.baseURLErr != nil {
		return c.baseURLErr
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("waffle: building request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("waffle: performing request: %w", err)
	}
	defer resp.Body.Close()
	_, err = readResponse(resp)
	return err
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
	Channel          string `json:"channel,omitempty"`
	VABank           string `json:"va_bank,omitempty"`
	ExpiresInMinutes int    `json:"expires_in_minutes,omitempty"`
	// CheckoutChannelSelection is "merchant" (default behavior when
	// empty) or "payer": "payer" lets the payer pick their own channel
	// at checkout, in which case the charge is created unpriced (no fee
	// quote yet) and Charge.Breakdown is nil until the payer chooses.
	CheckoutChannelSelection string            `json:"checkout_channel_selection,omitempty"`
	Metadata                 map[string]string `json:"metadata,omitempty"`
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
	// CheckoutChannelSelection echoes the request's field; see
	// CreateChargeParams.CheckoutChannelSelection.
	CheckoutChannelSelection string `json:"checkout_channel_selection,omitempty"`
	// Breakdown is the fee/settlement breakdown for this charge. It is
	// nil for an unpriced "payer"-channel-selection charge that hasn't
	// had a channel picked yet.
	Breakdown *ChargeBreakdown `json:"breakdown,omitempty"`
	// PaidAt, ExpiresAt, and SettledAt are populated by GetCharge/
	// ListCharges (GET), not by CreateCharge's own 201 response.
	PaidAt    string `json:"paid_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	SettledAt string `json:"settled_at,omitempty"`
}

// ChargeBreakdown is the fee/settlement breakdown attached to a Charge.
type ChargeBreakdown struct {
	// BaseAmount is nullable: nil for an unpriced "payer"-channel-
	// selection charge.
	BaseAmount *int64 `json:"base_amount"`
	FeeAmount  int64  `json:"fee_amount"`
	// FeeBearer is "merchant" or "customer", nullable for the same
	// reason as BaseAmount.
	FeeBearer        *string       `json:"fee_bearer"`
	FeeRule          ChargeFeeRule `json:"fee_rule"`
	PayerPaid        int64         `json:"payer_paid"`
	MerchantReceives int64         `json:"merchant_receives"`
	Channel          string        `json:"channel"`
	VABank           string        `json:"va_bank"`
}

// ChargeFeeRule is the fee rule that priced a ChargeBreakdown. Type is
// "percentage" or "flat"; only the matching field (PercentBps or
// FlatAmount) is meaningful.
type ChargeFeeRule struct {
	Type       string `json:"type"`
	PercentBps int64  `json:"percent_bps"`
	FlatAmount int64  `json:"flat_amount"`
}

// CreateCharge calls POST /v1/charges. idempotencyKey is sent as the
// Idempotency-Key header — retrying the exact same key returns the
// original charge rather than creating a duplicate. Idempotency-Key
// stays required by the contract, but this SDK auto-generates one (via
// GenerateIdempotencyKey) when idempotencyKey is the empty string, so
// callers who don't care about pinning a specific key don't have to
// call the helper themselves.
//
// WARNING: an empty key generates a NEW key on every call, so a call with
// "" is not safe to wrap in a retry loop (a retry after a timeout can
// create a duplicate charge). Generate one key per logical operation (or
// use your own, e.g. an internal order id) and reuse it on every retry.
//
// Possible errors (via errors.As(err, &apiErr)): 400 malformed
// amount/currency; 422 no gateway/fee rule for the auto-routed provider,
// zero connected PSPs, or the provider call failed; 429 fraud velocity
// limit exceeded; 403 (via errors.As(err, &permErr)) the API key lacks
// the charges:write scope.
func (c *Client) CreateCharge(ctx context.Context, params CreateChargeParams, idempotencyKey string) (*Charge, error) {
	if idempotencyKey == "" {
		idempotencyKey = GenerateIdempotencyKey()
	}
	var out Charge
	err := c.doJSON(ctx, http.MethodPost, "/v1/charges", nil,
		map[string]string{"Idempotency-Key": idempotencyKey}, params, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCharge calls GET /v1/charges/{id} (scope charges:read). Returns a
// wrapped *APIError with StatusCode 404 (message "charge not found") if
// id doesn't exist or belongs to another merchant.
func (c *Client) GetCharge(ctx context.Context, id string) (*Charge, error) {
	var out Charge
	err := c.doJSON(ctx, http.MethodGet, "/v1/charges/"+url.PathEscape(id), nil, nil, nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ChargeListParams filters and paginates ListCharges/AllCharges (GET
// /v1/charges). Zero values omit the corresponding query parameter,
// letting the server apply its own default (Limit defaults to 20,
// server-side, max 100).
type ChargeListParams struct {
	Limit         int
	StartingAfter string
	// Status filters to one of "pending", "paid", "failed", "expired".
	Status string
	// CreatedGTE and CreatedLTE filter on created_at, RFC3339 or
	// YYYY-MM-DD.
	CreatedGTE string
	CreatedLTE string
}

func (p ChargeListParams) query() url.Values {
	q := url.Values{}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.StartingAfter != "" {
		q.Set("starting_after", p.StartingAfter)
	}
	if p.Status != "" {
		q.Set("status", p.Status)
	}
	if p.CreatedGTE != "" {
		q.Set("created[gte]", p.CreatedGTE)
	}
	if p.CreatedLTE != "" {
		q.Set("created[lte]", p.CreatedLTE)
	}
	return q
}

// ChargeList is one page of ListCharges (GET /v1/charges, 200).
// Cursor pagination: pass the last element's ID as the next call's
// ChargeListParams.StartingAfter, and stop once HasMore is false — or
// use AllCharges to have this done automatically.
type ChargeList struct {
	Data    []Charge `json:"data"`
	HasMore bool     `json:"has_more"`
}

// ListCharges calls GET /v1/charges (scope charges:read) and returns a
// single page. See AllCharges for an auto-paginating iterator.
func (c *Client) ListCharges(ctx context.Context, params ChargeListParams) (*ChargeList, error) {
	var out ChargeList
	err := c.doJSON(ctx, http.MethodGet, "/v1/charges", params.query(), nil, nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// AllCharges returns an iterator (Go 1.23+ range-over-func, iter.Seq2)
// over every charge matching params, auto-paginating via
// starting_after under the hood — one extra request per page of Limit
// (or the server's default of 20) charges. On a request error the
// iterator yields (nil, err) once and stops; range breaks out of it in
// the usual iter.Seq2 way:
//
//	for charge, err := range client.AllCharges(ctx, waffle.ChargeListParams{Status: "paid"}) {
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    fmt.Println(charge.ID)
//	}
func (c *Client) AllCharges(ctx context.Context, params ChargeListParams) iter.Seq2[*Charge, error] {
	return func(yield func(*Charge, error) bool) {
		p := params
		for {
			page, err := c.ListCharges(ctx, p)
			if err != nil {
				yield(nil, err)
				return
			}
			for i := range page.Data {
				if !yield(&page.Data[i], nil) {
					return
				}
			}
			if !page.HasMore || len(page.Data) == 0 {
				return
			}
			p.StartingAfter = page.Data[len(page.Data)-1].ID
		}
	}
}

// GetChargeReceipt calls GET /v1/charges/{id}/receipt.pdf (scope
// charges:read) and returns the raw PDF bytes (Content-Type
// application/pdf) — this method does no parsing, it's just the bytes.
// Returns a wrapped *APIError with StatusCode 409 if the charge isn't
// (yet) "paid".
func (c *Client) GetChargeReceipt(ctx context.Context, id string) ([]byte, error) {
	return c.doRaw(ctx, http.MethodGet, "/v1/charges/"+url.PathEscape(id)+"/receipt.pdf")
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
//
// There is deliberately no bank-account-registration method here: the
// route this SDK once called, POST /v1/bank-accounts, has been removed
// server-side. A merchant's withdrawal destination is managed
// exclusively through the dashboard (its own KYC/verification flow),
// never via API key — the credential model never lets an API key
// redirect where payouts go. See GetBankAccount for the (masked,
// read-only) bank-accounts route that remains.

// BankAccount is the response shape for GetBankAccount (GET
// /v1/bank-accounts, 200). AccountNumber is masked here (e.g.
// "••••7890") — GetPayout returns the unmasked account number for a
// specific payout instead.
type BankAccount struct {
	ID                string `json:"id"`
	BankCode          string `json:"bank_code"`
	AccountNumber     string `json:"account_number"`
	AccountHolderName string `json:"account_holder_name"`
	CreatedAt         string `json:"created_at"`
}

// GetBankAccount calls GET /v1/bank-accounts (scope payouts:read),
// returning the caller's current active withdrawal account for their
// mode, masked. Returns a wrapped *APIError with StatusCode 404 if the
// merchant has never registered one for this mode.
func (c *Client) GetBankAccount(ctx context.Context) (*BankAccount, error) {
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
	// BankCode, AccountNumber, AccountHolderName, CreatedAt, and
	// CompletedAt are populated only by GetPayout/ListPayouts (GET), not
	// by CreatePayout's own 201 response. AccountNumber here is
	// unmasked — unlike GetBankAccount's masked view — since it's
	// scoped to one specific payout the caller already knows about.
	BankCode          string `json:"bank_code,omitempty"`
	AccountNumber     string `json:"account_number,omitempty"`
	AccountHolderName string `json:"account_holder_name,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	CompletedAt       string `json:"completed_at,omitempty"`
}

// CreatePayout calls POST /v1/payouts. idempotencyKey is sent as the
// Idempotency-Key header, same semantics as CreateCharge: it stays
// required by the contract, but this SDK auto-generates one when
// idempotencyKey is the empty string. As with CreateCharge, an empty key
// yields a NEW key per call and is not safe to retry; reuse one key per
// logical payout across retries.
//
// A 422 with body {"error":"insufficient available balance"} is returned
// specifically for payout.ErrInsufficientBalance — surfaced here as an
// *APIError with StatusCode 422 and that Message, same as any other 422.
func (c *Client) CreatePayout(ctx context.Context, params CreatePayoutParams, idempotencyKey string) (*Payout, error) {
	if idempotencyKey == "" {
		idempotencyKey = GenerateIdempotencyKey()
	}
	var out Payout
	err := c.doJSON(ctx, http.MethodPost, "/v1/payouts", nil,
		map[string]string{"Idempotency-Key": idempotencyKey}, params, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPayout calls GET /v1/payouts/{id} (scope payouts:read). Returns a
// wrapped *APIError with StatusCode 404 (message "payout not found") if
// id doesn't exist or belongs to another merchant.
func (c *Client) GetPayout(ctx context.Context, id string) (*Payout, error) {
	var out Payout
	err := c.doJSON(ctx, http.MethodGet, "/v1/payouts/"+url.PathEscape(id), nil, nil, nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// PayoutListParams filters and paginates ListPayouts/AllPayouts (GET
// /v1/payouts). Same cursor-pagination shape as ChargeListParams.
type PayoutListParams struct {
	Limit         int
	StartingAfter string
	// Status filters to one of "pending", "held", "processing",
	// "completed", "failed".
	Status string
}

func (p PayoutListParams) query() url.Values {
	q := url.Values{}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.StartingAfter != "" {
		q.Set("starting_after", p.StartingAfter)
	}
	if p.Status != "" {
		q.Set("status", p.Status)
	}
	return q
}

// PayoutList is one page of ListPayouts (GET /v1/payouts, 200).
type PayoutList struct {
	Data    []Payout `json:"data"`
	HasMore bool     `json:"has_more"`
}

// ListPayouts calls GET /v1/payouts (scope payouts:read) and returns a
// single page. See AllPayouts for an auto-paginating iterator.
func (c *Client) ListPayouts(ctx context.Context, params PayoutListParams) (*PayoutList, error) {
	var out PayoutList
	err := c.doJSON(ctx, http.MethodGet, "/v1/payouts", params.query(), nil, nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// AllPayouts returns an auto-paginating iterator over every payout
// matching params, the same pattern as AllCharges.
func (c *Client) AllPayouts(ctx context.Context, params PayoutListParams) iter.Seq2[*Payout, error] {
	return func(yield func(*Payout, error) bool) {
		p := params
		for {
			page, err := c.ListPayouts(ctx, p)
			if err != nil {
				yield(nil, err)
				return
			}
			for i := range page.Data {
				if !yield(&page.Data[i], nil) {
					return
				}
			}
			if !page.HasMore || len(page.Data) == 0 {
				return
			}
			p.StartingAfter = page.Data[len(page.Data)-1].ID
		}
	}
}

// GetPayoutReceipt calls GET /v1/payouts/{id}/receipt.pdf (scope
// payouts:read) and returns the raw PDF bytes. Returns a wrapped
// *APIError with StatusCode 409 if the payout isn't (yet) "completed".
func (c *Client) GetPayoutReceipt(ctx context.Context, id string) ([]byte, error) {
	return c.doRaw(ctx, http.MethodGet, "/v1/payouts/"+url.PathEscape(id)+"/receipt.pdf")
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

// --- WhoAmI ----------------------------------------------------------------

// WhoAmIResult is the response shape for WhoAmI (GET /v1/whoami, 200):
// the identity and permissions of the API key making the call.
type WhoAmIResult struct {
	MerchantID   string `json:"merchant_id"`
	BusinessName string `json:"business_name"`
	Mode         string `json:"mode"`
	// Preset is one of "read_only", "accept_payments", "full", or
	// "custom". Scopes is the resolved scope list (5 possible values:
	// charges:read, charges:write, payouts:read, payouts:write,
	// balance:read) — the source of truth for what this key can call,
	// regardless of Preset.
	Preset string   `json:"preset"`
	Scopes []string `json:"scopes"`
}

// WhoAmI calls GET /v1/whoami. Unlike every other method in this
// package, no scope is required — any valid API key of any preset can
// call it, which makes it a good first call for a caller that doesn't
// know its own key's permissions yet.
func (c *Client) WhoAmI(ctx context.Context) (*WhoAmIResult, error) {
	var out WhoAmIResult
	err := c.doJSON(ctx, http.MethodGet, "/v1/whoami", nil, nil, nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
