package waffle

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNoRedirectFollowingKeyNotForwarded(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	for name, c := range map[string]*Client{
		"default":        NewClient("sk_test_secret", WithBaseURL(origin.URL)),
		"callers client": NewClient("sk_test_secret", WithBaseURL(origin.URL), WithHTTPClient(&http.Client{})),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.CreateCharge(context.Background(), CreateChargeParams{Amount: 1, Currency: "IDR"}, "k")
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTemporaryRedirect {
				t.Fatalf("want 307 APIError, got %v", err)
			}
			if targetHits.Load() != 0 {
				t.Fatal("redirect was followed")
			}
		})
	}
}

func TestHTTPClientDefaultsAndNoMutation(t *testing.T) {
	c := NewClient("k")
	if c.httpClient.Timeout != 30*time.Second {
		t.Fatalf("default timeout = %v", c.httpClient.Timeout)
	}
	if c.httpClient.CheckRedirect == nil {
		t.Fatal("default CheckRedirect missing")
	}
	mine := &http.Client{Timeout: time.Second}
	c = NewClient("k", WithHTTPClient(mine))
	if mine.CheckRedirect != nil {
		t.Fatal("caller's client was mutated")
	}
	if c.httpClient == mine || c.httpClient.CheckRedirect == nil || c.httpClient.Timeout != time.Second {
		t.Fatal("expected a shallow copy with redirects disabled")
	}
	called := false
	own := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { called = true; return nil }}
	c = NewClient("k", WithHTTPClient(own))
	if c.httpClient != own {
		t.Fatal("caller CheckRedirect must be respected as-is")
	}
	_ = called
}

func TestBaseURLValidation(t *testing.T) {
	tests := []struct {
		url  string
		ok   bool
		want string
	}{
		{"https://api.example.com", true, "https://api.example.com"},
		{"https://api.example.com///", true, "https://api.example.com"},
		{"http://localhost:8080", true, "http://localhost:8080"},
		{"http://127.0.0.1:1", true, "http://127.0.0.1:1"},
		{"http://[::1]:8080", true, "http://[::1]:8080"},
		{"http://evil.example.com", false, ""},
		{"http://localhost.evil.com", false, ""},
		{"ftp://api.example.com", false, ""},
		{"https://user:pw@api.example.com", false, ""},
		{"https://api.example.com?x=1", false, ""},
		{"https://api.example.com/?x=1", false, ""},
		{"https://api.example.com#f", false, ""},
		{"https://api.example.com/v1", false, ""},
		{"https://", false, ""},
		{"", false, ""},
		{"api.example.com", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			var hit atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Add(1) }))
			defer srv.Close()
			c := NewClient("k", WithBaseURL(tc.url))
			if tc.ok {
				if c.baseURLErr != nil || c.baseURL != tc.want {
					t.Fatalf("baseURL=%q err=%v", c.baseURL, c.baseURLErr)
				}
				return
			}
			for _, err := range []error{
				c.Healthz(context.Background()),
				func() error { _, e := c.GetCharge(context.Background(), "x"); return e }(),
			} {
				if !errors.Is(err, ErrInvalidBaseURL) {
					t.Fatalf("want ErrInvalidBaseURL, got %v", err)
				}
			}
		})
	}
}

func TestResponseBodyCap(t *testing.T) {
	big := strings.Repeat("a", 8<<20+10)
	c, _ := newTestClient(t, "k", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(200)
		}
		fmt.Fprint(w, big)
	})
	_, err := c.GetCharge(context.Background(), "x")
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("success cap: %v", err)
	}
	err = c.Healthz(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 || !strings.Contains(apiErr.Message, "64 KiB") {
		t.Fatalf("error cap: %v", err)
	}
}

func TestErrorMessageSanitised(t *testing.T) {
	body := "bad\x1b[31m\r\nred‮ x\x00\x85\x7f\xff" + strings.Repeat("é", 400)
	err := translateError(400, []byte(body))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal(err)
	}
	if len(apiErr.Message) > 512 || !utf8.ValidString(apiErr.Message) {
		t.Fatalf("len=%d valid=%v", len(apiErr.Message), utf8.ValidString(apiErr.Message))
	}
	for _, r := range apiErr.Message {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x202e || r == 0x2028 {
			t.Fatalf("control char %U left in %q", r, apiErr.Message)
		}
	}
	if !strings.HasPrefix(apiErr.Message, "bad [31m  red") {
		t.Fatalf("unexpected: %q", apiErr.Message)
	}
	// JSON error field is sanitised too.
	err = translateError(400, []byte(`{"error":"a\u0007b c"}`))
	errors.As(err, &apiErr)
	if apiErr.Message != "a b c" {
		t.Fatalf("json message %q", apiErr.Message)
	}
}

func TestWebhookDefaultRejectsLegacyAcceptsV2(t *testing.T) {
	legacyOnly := whHeaders("X-Waffle-Signature", "sha256="+whBody1Legacy)
	_, err := VerifyWebhook([]byte(whBody1), legacyOnly, whSecret, whClock())
	wantReason(t, err, WebhookMissingSignature)

	both := whHeaders("X-Waffle-Signature-V2", "t=1767323045,v1="+whBody1V2)
	both.Set("X-Waffle-Signature", "sha256="+whBody1Legacy)
	if _, err := VerifyWebhook([]byte(whBody1), both, whSecret, whClock()); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookReplayAndOverflow(t *testing.T) {
	// Replay: valid V2 signature but "now" is an hour later.
	h := whHeaders("X-Waffle-Signature-V2", "t=1767323045,v1="+whBody1V2)
	_, err := VerifyWebhook([]byte(whBody1), h, whSecret,
		WithWebhookClock(func() time.Time { return time.Unix(1767323045+3600, 0) }))
	wantReason(t, err, WebhookTimestampOutOfTolerance)

	// Huge timestamp and huge tolerance must not wrap into acceptance.
	ts := int64(1<<63 - 1)
	mac := hmac.New(sha256.New, []byte(whSecret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "." + whBody1))
	h = whHeaders("X-Waffle-Signature-V2", "t="+strconv.FormatInt(ts, 10)+",v1="+hex.EncodeToString(mac.Sum(nil)))
	_, err = VerifyWebhook([]byte(whBody1), h, whSecret, whClock())
	wantReason(t, err, WebhookTimestampOutOfTolerance)
	_, err = VerifyWebhook([]byte(whBody1), h, whSecret, whClock(), WithWebhookTolerance(time.Duration(1<<63-1)))
	wantReason(t, err, WebhookTimestampOutOfTolerance)
}

func TestClientRedaction(t *testing.T) {
	c := NewClient("sk_live_SUPERSECRET1234", WithBaseURL("https://api.example.com"))
	for _, f := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(f, c)
		if strings.Contains(out, "SUPERSECRET") || strings.Contains(out, "1234") {
			t.Fatalf("%s leaked: %s", f, out)
		}
		if !strings.Contains(out, "sk_live_****") {
			t.Fatalf("%s = %s", f, out)
		}
	}
	if got := NewClient("sk_test_abc").String(); !strings.Contains(got, "sk_test_****") {
		t.Fatal(got)
	}
	if got := NewClient("weird").String(); strings.Contains(got, "weird") {
		t.Fatal(got)
	}
}
