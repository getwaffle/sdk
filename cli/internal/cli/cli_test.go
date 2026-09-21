package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	waffle "github.com/getwaffle/sdk/go"
)

// recorder captures the requests a CLI invocation makes, so tests can
// assert which credential landed on which endpoint.
type recorder struct {
	mu     sync.Mutex
	path   string
	auth   string
	idem   string
	bodies []map[string]any
}

func (rec *recorder) record(r *http.Request) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.path, rec.auth = r.URL.Path, r.Header.Get("Authorization")
	rec.idem = r.Header.Get("Idempotency-Key")
	var b map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&b)
	}
	rec.bodies = append(rec.bodies, b)
}

// runCLI executes one CLI invocation with an isolated config dir,
// discarded stderr, and captured stdout. Returns stdout.
// withConfig isolates the config file for the whole test — all CLI
// invocations in one test must share it (login stores, next command reads).
func withConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	g = globals{}

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	execErr := root.Execute()

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), execErr
}

func TestLoginThenWhoamiUsesSessionToken(t *testing.T) {
	withConfig(t)
	var profileAuth, loginAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/merchants/login":
			loginAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "sess_123"})
		case "/v1/merchants/me/profile":
			profileAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"merchant_id":"m1","email":"a@b.co","status":"active","review_status":"approved","role":"owner"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	setStdin(strings.NewReader("hunter22\n"))
	if _, err := runCLI(t, "login", "--email", "a@b.co", "--base-url", srv.URL); err != nil {
		t.Fatalf("login: %v", err)
	}
	if loginAuth != "" {
		t.Errorf("login must not carry a credential, got %q", loginAuth)
	}

	out, err := runCLI(t, "whoami")
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if profileAuth != "Bearer sess_123" {
		t.Errorf("whoami Authorization = %q, want Bearer sess_123", profileAuth)
	}
	if !strings.Contains(out, "a@b.co") || !strings.Contains(out, "m1") {
		t.Errorf("whoami output missing fields: %q", out)
	}
}

// The core auth-model invariant: session tokens and API keys never swap
// surfaces. A session token cannot move money; an API key must not
// reach the account surface.
func TestCredentialDiscipline(t *testing.T) {
	withConfig(t)
	var mu sync.Mutex
	auths := map[string]string{} // path -> last Authorization
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths[r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		switch r.URL.Path {
		case "/v1/merchants/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "sess_abc"})
		case "/v1/merchants/me/profile":
			_, _ = w.Write([]byte(`{"merchant_id":"m1"}`))
		case "/v1/balance":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"currency":"IDR","amount":287500}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	setStdin(strings.NewReader("pw123456\n"))
	if _, err := runCLI(t, "login", "--email", "a@b.co", "--base-url", srv.URL); err != nil {
		t.Fatalf("login: %v", err)
	}
	// configure stores the money credential alongside the session
	if _, err := runCLI(t, "configure", "--api-key", "sk_sandbox_key", "--base-url", srv.URL); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if _, err := runCLI(t, "balance"); err != nil {
		t.Fatalf("balance: %v", err)
	}
	if _, err := runCLI(t, "whoami"); err != nil {
		t.Fatalf("whoami: %v", err)
	}

	if got := auths["/v1/balance"]; got != "Bearer sk_sandbox_key" {
		t.Errorf("balance Authorization = %q, want the API key", got)
	}
	if got := auths["/v1/merchants/me/profile"]; got != "Bearer sess_abc" {
		t.Errorf("profile Authorization = %q, want the session token", got)
	}
}

func TestKeysCreateStoresKeyForMoneyCommands(t *testing.T) {
	withConfig(t)
	var issueAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/merchants/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "sess_k"})
		case "/v1/merchants/me/api-keys":
			if r.Method == http.MethodPost {
				issueAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]string{"mode": "sandbox", "api_key": "sk_sandbox_minted"})
				return
			}
			t.Errorf("unexpected api-keys method %s", r.Method)
		case "/v1/balance":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"currency":"IDR","amount":5000}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	setStdin(strings.NewReader("pw123456\n"))
	if _, err := runCLI(t, "login", "--email", "a@b.co", "--base-url", srv.URL); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := runCLI(t, "keys", "create", "--name", "laptop", "--base-url", srv.URL); err != nil {
		t.Fatalf("keys create: %v", err)
	}
	if issueAuth != "Bearer sess_k" {
		t.Errorf("keys create Authorization = %q, want the session token", issueAuth)
	}

	// the minted key must already power money commands — stored config, no flags
	out, err := runCLI(t, "balance", "--json", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("balance after keys create: %v", err)
	}
	var bal waffle.Balance
	if err := json.Unmarshal([]byte(out), &bal); err != nil {
		t.Fatalf("balance --json not machine-readable: %v (%q)", err, out)
	}
	if bal.Amount != 5000 || bal.Currency != "IDR" {
		t.Errorf("balance = %+v", bal)
	}
}

func TestChargesCreateIdempotencyAndBody(t *testing.T) {
	withConfig(t)
	var rec recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"chg_1","mode":"sandbox","status":"pending","gross_amount":100000,"fee_amount":3000,"net_amount":97000,"currency":"IDR","created_at":"2026-09-08T01:00:00Z"}`))
	}))
	defer srv.Close()

	t.Setenv("WAFFLE_API_KEY", "sk_test_env")

	if _, err := runCLI(t, "charges", "create", "--amount", "100000", "--base-url", srv.URL); err != nil {
		t.Fatalf("charges create: %v", err)
	}
	first := rec.idem
	if first == "" {
		t.Fatal("no Idempotency-Key sent")
	}
	if auth := rec.auth; auth != "Bearer sk_test_env" {
		t.Errorf("Authorization = %q, want env API key", auth)
	}
	if amount := rec.bodies[0]["amount"]; amount != float64(100000) {
		t.Errorf("amount = %v, want integer 100000", amount)
	}

	if _, err := runCLI(t, "charges", "create", "--amount", "100000", "--base-url", srv.URL); err != nil {
		t.Fatalf("charges create #2: %v", err)
	}
	if second := rec.idem; second == "" || second == first {
		t.Errorf("each CLI invocation must mint a fresh idempotency key, got %q then %q", first, second)
	}

	// explicit key passes through untouched
	if _, err := runCLI(t, "charges", "create", "--amount", "100000", "--idempotency-key", "order-42", "--base-url", srv.URL); err != nil {
		t.Fatalf("charges create #3: %v", err)
	}
	if rec.idem != "order-42" {
		t.Errorf("--idempotency-key = %q, want order-42", rec.idem)
	}
}

func TestChargesCreateRejectsVAWithoutBank(t *testing.T) {
	withConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server must not be called: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("WAFFLE_API_KEY", "sk_test")

	_, err := runCLI(t, "charges", "create", "--amount", "100000", "--channel", "virtual_account", "--base-url", srv.URL)
	if err == nil {
		t.Fatal("expected error for virtual_account without --va-bank")
	}
	if !strings.Contains(err.Error(), "--va-bank") {
		t.Errorf("error should name the missing flag, got: %v", err)
	}
}

func TestMissingAPIKeyErrorIsActionable(t *testing.T) {
	withConfig(t)
	_, err := runCLI(t, "balance")
	if err == nil {
		t.Fatal("expected error without any API key")
	}
	for _, want := range []string{"keys create", "configure", "WAFFLE_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing guidance %q", err, want)
		}
	}
}

func TestNotLoggedInError(t *testing.T) {
	withConfig(t)
	_, err := runCLI(t, "whoami")
	if err == nil {
		t.Fatal("expected error without a session")
	}
	if !strings.Contains(err.Error(), "login") {
		t.Errorf("error should point at login, got: %v", err)
	}
}

func TestLogoutClearsCredentials(t *testing.T) {
	withConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "sess_z"})
	}))
	defer srv.Close()

	setStdin(strings.NewReader("pw123456\n"))
	if _, err := runCLI(t, "login", "--email", "a@b.co", "--base-url", srv.URL); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := runCLI(t, "configure", "--api-key", "sk_sandbox_k"); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if _, err := runCLI(t, "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := runCLI(t, "whoami"); err == nil {
		t.Error("whoami after logout must fail")
	}
	if _, err := runCLI(t, "balance"); err == nil {
		t.Error("balance after logout must fail (key cleared)")
	}
}

func TestBankAccountsRegisterSendsAPIKey(t *testing.T) {
	withConfig(t)
	var rec recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ba_1","bank_code":"BCA","account_number":"1234567890","account_holder_name":"Budi"}`))
	}))
	defer srv.Close()
	t.Setenv("WAFFLE_API_KEY", "sk_live_x")

	if _, err := runCLI(t, "bank-accounts", "register", "--bank-code", "BCA",
		"--account-number", "1234567890", "--account-holder-name", "Budi", "--base-url", srv.URL); err != nil {
		t.Fatalf("register: %v", err)
	}
	if rec.path != "/v1/bank-accounts" || rec.auth != "Bearer sk_live_x" {
		t.Errorf("got %s auth %q", rec.path, rec.auth)
	}
	if rec.bodies[0]["account_holder_name"] != "Budi" {
		t.Errorf("body = %v", rec.bodies[0])
	}
}

func TestPayoutsCreateAutoIdempotency(t *testing.T) {
	withConfig(t)
	var rec recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"po_1","bank_account_id":"ba_1","mode":"sandbox","status":"completed","amount":20000,"currency":"IDR"}`))
	}))
	defer srv.Close()
	t.Setenv("WAFFLE_API_KEY", "sk_live_x")

	if _, err := runCLI(t, "payouts", "create", "--bank-account-id", "ba_1", "--amount", "20000", "--base-url", srv.URL); err != nil {
		t.Fatalf("payouts create: %v", err)
	}
	if rec.path != "/v1/payouts" || rec.idem == "" {
		t.Errorf("path=%q idem=%q", rec.path, rec.idem)
	}
}

func TestHealthzNoAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	if _, err := runCLI(t, "healthz", "--base-url", srv.URL); err != nil {
		t.Fatalf("healthz: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("healthz must send no credential, got %q", gotAuth)
	}
}

func TestMoneyFormat(t *testing.T) {
	cases := []struct {
		amount int64
		cur    string
		want   string
	}{
		{287500, "IDR", "Rp287.500"},
		{12500, "IDR", "Rp12.500"},
		{500, "IDR", "Rp500"},
		{-3000, "IDR", "-Rp3.000"},
		{1234567, "USD", "1234567 USD"},
	}
	for _, tc := range cases {
		if got := money(tc.amount, tc.cur); got != tc.want {
			t.Errorf("money(%d,%q) = %q, want %q", tc.amount, tc.cur, got, tc.want)
		}
	}
}

func TestParseMetadata(t *testing.T) {
	m, err := parseMetadata([]string{"order=42", "plan=pro"})
	if err != nil {
		t.Fatal(err)
	}
	if m["order"] != "42" || m["plan"] != "pro" {
		t.Errorf("m = %v", m)
	}
	if _, err := parseMetadata([]string{"noequals"}); err == nil {
		t.Error("expected error for pair without =")
	}
	if m, err := parseMetadata(nil); err != nil || m != nil {
		t.Errorf("nil metadata = %v, %v", m, err)
	}
}

func TestAPIErrorsSurfaceServerMessage(t *testing.T) {
	withConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"insufficient available balance"}`))
	}))
	defer srv.Close()
	t.Setenv("WAFFLE_API_KEY", "sk_x")

	_, err := runCLI(t, "payouts", "create", "--bank-account-id", "ba", "--amount", "10", "--base-url", srv.URL)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *waffle.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 422 || apiErr.Message != "insufficient available balance" {
		t.Errorf("want 422 APIError with server message, got %v", err)
	}
}

// Regression tests for the CTO re-review blockers.

func TestMaskKeyNeverPanicsOnShortKeys(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"a", "..."},
		{"abc", "..."},
		{"sk_1", "..."},
		{"sk_12345", "sk_1..."},
		{"sk_12345678", "sk_1..."},
		{"sk_1234567890123", "sk_123456789..."},
	}
	for _, tc := range cases {
		if got := maskKey(tc.in); got != tc.want {
			t.Errorf("maskKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDefaultDashboardURLMatchesPortedBaseURL(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		// the documented default — host WITH a port must still match
		{"http://localhost:8080", "http://localhost:3001"},
		{"http://127.0.0.1:8080", "http://localhost:3001"},
		{"https://api.getwaffle.id", "https://getwaffle.id"},
		{"https://api.example.com", ""},
		{"", ""},
		// no scheme -> no host -> unknown
		{"localhost:8080", ""},
	}
	for _, tc := range cases {
		if got := defaultDashboardURL(tc.base); got != tc.want {
			t.Errorf("defaultDashboardURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestBrowserOpenableURLGate(t *testing.T) {
	if got, ok := browserOpenableURL("http://localhost:3001/integrations/api-keys"); !ok || got != "http://localhost:3001/integrations/api-keys" {
		t.Errorf("http URL refused: %q %v", got, ok)
	}
	if got, ok := browserOpenableURL("https://dashboard.example.com"); !ok {
		t.Errorf("https URL refused: %q %v", got, ok)
	}
	for _, bad := range []string{
		"javascript:alert(1)",
		"file:///etc/passwd",
		"ftp://x",
		"//no-scheme.host",
		"http://", // scheme but no host
		"://broken",
	} {
		if got, ok := browserOpenableURL(bad); ok {
			t.Errorf("browserOpenableURL(%q) = (%q, true), want refused", bad, got)
		}
	}
}
