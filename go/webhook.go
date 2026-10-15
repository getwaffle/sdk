package waffle

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Webhook header names sent by Waffle with every delivery.
const (
	HeaderWebhookSignature   = "X-Waffle-Signature"    // legacy: sha256=<hex hmac(secret, body)>
	HeaderWebhookSignatureV2 = "X-Waffle-Signature-V2" // t=<unix>,v1=<hex hmac(secret, "<t>.<body>")>
	HeaderWebhookDelivery    = "X-Waffle-Delivery"     // stable across retries: the idempotency key
	HeaderWebhookAttempt     = "X-Waffle-Attempt"
	HeaderWebhookTimestamp   = "X-Waffle-Timestamp"
	HeaderWebhookEvent       = "X-Waffle-Event"
)

// DefaultWebhookTolerance is the maximum clock skew / age VerifyWebhook
// accepts between the signed timestamp and "now", in either direction.
const DefaultWebhookTolerance = 300 * time.Second

// WebhookErrorReason is the machine-readable cause of a failed verification.
type WebhookErrorReason string

const (
	// WebhookMissingSignature: no usable signature header was present (or
	// only the legacy header was present and legacy verification is off).
	WebhookMissingSignature WebhookErrorReason = "missing_signature"
	// WebhookMalformedHeader: a signature header was present but not parseable.
	WebhookMalformedHeader WebhookErrorReason = "malformed_header"
	// WebhookTimestampOutOfTolerance: the signature is valid but the signed
	// timestamp is too old or too far in the future (possible replay).
	WebhookTimestampOutOfTolerance WebhookErrorReason = "timestamp_out_of_tolerance"
	// WebhookSignatureMismatch: no signature matched the body and secret.
	WebhookSignatureMismatch WebhookErrorReason = "signature_mismatch"
	// WebhookInvalidJSON: the signature is valid but the body is not a JSON object.
	WebhookInvalidJSON WebhookErrorReason = "invalid_json"
)

// WebhookError is returned by VerifyWebhook on every verification failure.
// Branch on Reason; use errors.As to extract it. It never contains the
// signing secret or the signature values.
type WebhookError struct {
	Reason  WebhookErrorReason
	Message string
}

func (e *WebhookError) Error() string {
	return "waffle: webhook verification failed (" + string(e.Reason) + "): " + e.Message
}

// WebhookEvent is the verified event envelope. Fields absent from the
// payload are zero values; Raw holds the complete decoded object (numbers
// decoded as json.Number so integer minor-unit amounts never lose
// precision).
type WebhookEvent struct {
	Event     string `json:"event"`
	EventID   string `json:"event_id"`
	CreatedAt string `json:"created_at"` // RFC3339 UTC, as sent
	ChargeID  string `json:"charge_id,omitempty"`
	PayoutID  string `json:"payout_id,omitempty"`
	Status    string `json:"status"`
	Amount    int64  `json:"amount"` // integer minor units
	Currency  string `json:"currency"`
	Mode      string `json:"mode"`

	// DeliveryID is the X-Waffle-Delivery header: stable across retries of
	// the same event, so it is the key to deduplicate on.
	DeliveryID string `json:"-"`
	// Raw is the full decoded JSON object (including fields not modelled above).
	Raw map[string]any `json:"-"`
}

type webhookConfig struct {
	tolerance   time.Duration
	now         func() time.Time
	allowLegacy bool
}

// WebhookOption configures VerifyWebhook.
type WebhookOption func(*webhookConfig)

// WithWebhookTolerance overrides the replay tolerance (default
// DefaultWebhookTolerance, applied in both directions).
func WithWebhookTolerance(d time.Duration) WebhookOption {
	return func(c *webhookConfig) { c.tolerance = d }
}

// WithWebhookClock injects the clock used for the tolerance check (tests).
func WithWebhookClock(now func() time.Time) WebhookOption {
	return func(c *webhookConfig) { c.now = now }
}

// WithWebhookAllowLegacy controls the fallback to the legacy
// X-Waffle-Signature header (default true). The legacy signature covers the
// body only, so it offers NO replay protection; pass false once your
// receiver only needs to accept V2-signed deliveries.
func WithWebhookAllowLegacy(allow bool) WebhookOption {
	return func(c *webhookConfig) { c.allowLegacy = allow }
}

// VerifyWebhook authenticates a Waffle webhook delivery and decodes it.
//
// body MUST be the exact raw request bytes, read before any JSON parsing or
// re-serialisation. headers lookup is case-insensitive. secret is the
// endpoint signing secret.
//
// The replay-protected X-Waffle-Signature-V2 header is verified when
// present (constant-time compare, timestamp tolerance in both directions).
// Only when it is absent, and WithWebhookAllowLegacy is true, the legacy
// X-Waffle-Signature header is verified over the raw body (no replay
// protection). A present-but-invalid V2 header never falls back to legacy.
//
// Failures are *WebhookError. A successfully verified delivery may still be
// a retry: deduplicate on WebhookEvent.DeliveryID.
func VerifyWebhook(body []byte, headers http.Header, secret string, opts ...WebhookOption) (*WebhookEvent, error) {
	if secret == "" {
		return nil, errors.New("waffle: VerifyWebhook requires a non-empty signing secret")
	}
	cfg := webhookConfig{tolerance: DefaultWebhookTolerance, now: time.Now, allowLegacy: true}
	for _, o := range opts {
		o(&cfg)
	}

	if v2, ok := lookupHeader(headers, HeaderWebhookSignatureV2); ok {
		ts, sigs, err := parseSignatureV2(v2)
		if err != nil {
			return nil, err
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(strconv.FormatInt(ts, 10)))
		mac.Write([]byte("."))
		mac.Write(body)
		want := mac.Sum(nil)
		matched := false
		for _, s := range sigs {
			if hmac.Equal(s, want) { // keep scanning: no early exit on match
				matched = true
			}
		}
		if !matched {
			return nil, &WebhookError{WebhookSignatureMismatch, "signature does not match payload and secret"}
		}
		skew := cfg.now().Unix() - ts
		if skew < 0 {
			skew = -skew
		}
		if time.Duration(skew)*time.Second > cfg.tolerance {
			return nil, &WebhookError{WebhookTimestampOutOfTolerance,
				fmt.Sprintf("timestamp is %ds from now, tolerance is %ds", skew, int64(cfg.tolerance/time.Second))}
		}
	} else {
		legacy, ok := lookupHeader(headers, HeaderWebhookSignature)
		if !ok || !cfg.allowLegacy {
			msg := "no " + HeaderWebhookSignatureV2 + " header"
			if ok {
				msg += " (legacy " + HeaderWebhookSignature + " present but legacy verification is disabled)"
			}
			return nil, &WebhookError{WebhookMissingSignature, msg}
		}
		sig, err := parseLegacySignature(legacy)
		if err != nil {
			return nil, err
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		if !hmac.Equal(sig, mac.Sum(nil)) {
			return nil, &WebhookError{WebhookSignatureMismatch, "signature does not match payload and secret"}
		}
	}

	return decodeWebhookEvent(body, headers)
}

func decodeWebhookEvent(body []byte, headers http.Header) (*WebhookEvent, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil || raw == nil {
		return nil, &WebhookError{WebhookInvalidJSON, "body is not a JSON object"}
	}
	if dec.More() {
		return nil, &WebhookError{WebhookInvalidJSON, "trailing data after JSON object"}
	}
	str := func(k string) string { s, _ := raw[k].(string); return s }
	ev := &WebhookEvent{
		Event:     str("event"),
		EventID:   str("event_id"),
		CreatedAt: str("created_at"),
		ChargeID:  str("charge_id"),
		PayoutID:  str("payout_id"),
		Status:    str("status"),
		Currency:  str("currency"),
		Mode:      str("mode"),
		Raw:       raw,
	}
	if n, ok := raw["amount"].(json.Number); ok {
		a, err := n.Int64()
		if err != nil {
			return nil, &WebhookError{WebhookInvalidJSON, "amount is not an integer"}
		}
		ev.Amount = a
	}
	ev.DeliveryID, _ = lookupHeader(headers, HeaderWebhookDelivery)
	return ev, nil
}

// lookupHeader finds name case-insensitively and trims surrounding space.
// A header that is present but empty reports ok=true with "".
func lookupHeader(h http.Header, name string) (string, bool) {
	for k, vals := range h {
		if strings.EqualFold(k, name) && len(vals) > 0 {
			return strings.TrimSpace(strings.Join(vals, ",")), true
		}
	}
	return "", false
}

func parseSignatureV2(v string) (int64, [][]byte, error) {
	bad := func(msg string) (int64, [][]byte, error) {
		return 0, nil, &WebhookError{WebhookMalformedHeader, HeaderWebhookSignatureV2 + ": " + msg}
	}
	var ts int64
	haveTS := false
	var sigs [][]byte
	for part := range strings.SplitSeq(v, ",") {
		part = strings.TrimSpace(part)
		k, val, ok := strings.Cut(part, "=")
		k, val = strings.TrimSpace(k), strings.TrimSpace(val)
		if !ok || k == "" || val == "" {
			return bad("expected comma-separated key=value pairs")
		}
		switch k {
		case "t":
			if haveTS {
				return bad("duplicate t")
			}
			for _, r := range val {
				if r < '0' || r > '9' {
					return bad("t is not a unix timestamp")
				}
			}
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return bad("t is not a unix timestamp")
			}
			ts, haveTS = n, true
		case "v1":
			b, err := hex.DecodeString(val)
			if err != nil || len(b) != sha256.Size {
				return bad("v1 is not a hex SHA-256 digest")
			}
			sigs = append(sigs, b)
		}
	}
	if !haveTS || len(sigs) == 0 {
		return bad("requires t and at least one v1")
	}
	return ts, sigs, nil
}

func parseLegacySignature(v string) ([]byte, error) {
	hexPart, ok := strings.CutPrefix(v, "sha256=")
	if !ok {
		return nil, &WebhookError{WebhookMalformedHeader, HeaderWebhookSignature + ": expected sha256=<hex>"}
	}
	b, err := hex.DecodeString(strings.TrimSpace(hexPart))
	if err != nil || len(b) != sha256.Size {
		return nil, &WebhookError{WebhookMalformedHeader, HeaderWebhookSignature + ": not a hex SHA-256 digest"}
	}
	return b, nil
}
