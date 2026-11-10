package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateBaseURL(t *testing.T) {
	ok := []string{"https://api.getwaffle.id", "http://127.0.0.1:8080", "http://localhost:3000", "http://[::1]:80"}
	bad := []string{"http://api.getwaffle.id", "ftp://x", "https://u:p@host", "api.getwaffle.id", "http://localhost.evil.com", ""}
	for _, u := range ok {
		if err := validateBaseURL(u); err != nil {
			t.Errorf("%q rejected: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := validateBaseURL(u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}

func TestEnvBaseURLHTTPDowngradeRefused(t *testing.T) {
	withConfig(t)
	t.Setenv("WAFFLE_BASE_URL", "http://evil.example.com")
	t.Setenv("WAFFLE_API_KEY", "sk_sandbox_secret")
	if _, err := runCLI(t, "balance"); err == nil || !strings.Contains(err.Error(), "refusing base URL") {
		t.Fatalf("want refusal, got %v", err)
	}
}

func TestRedirectNotFollowedWithCredentials(t *testing.T) {
	withConfig(t)
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	t.Setenv("WAFFLE_API_KEY", "sk_sandbox_secret")
	t.Setenv("WAFFLE_BASE_URL", srv.URL)
	if _, err := runCLI(t, "balance"); err == nil {
		t.Fatal("expected error on redirect")
	}
	if leaked {
		t.Fatal("redirect was followed")
	}
}

func TestSanitizeStripsEscapes(t *testing.T) {
	got := sanitize("ok\x1b[2J\x07\rx\ty\n")
	if strings.ContainsAny(got, "\x1b\x07\r") || !strings.Contains(got, "\t") {
		t.Fatalf("got %q", got)
	}
}
