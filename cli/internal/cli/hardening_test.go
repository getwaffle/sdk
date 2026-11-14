package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/getwaffle/sdk/cli/internal/config"
)

// captureStderr swaps the package stderr writer for the test.
func captureStderr(t *testing.T) *bytes.Buffer {
	t.Helper()
	old := stderr
	var buf bytes.Buffer
	stderr = &buf
	t.Cleanup(func() { stderr = old })
	return &buf
}

func TestValidateBaseURLPathQueryFragment(t *testing.T) {
	ok := []string{"https://api.getwaffle.id", "https://api.getwaffle.id/", "http://127.0.0.1:8080"}
	bad := []string{
		"https://api.getwaffle.id/v1", "https://api.getwaffle.id/x/", "https://api.getwaffle.id?a=b",
		"https://api.getwaffle.id/?a=b", "https://api.getwaffle.id?", "https://api.getwaffle.id#frag",
		"https://api.getwaffle.id/#", "http://localhost:3000/api",
	}
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

// The credential-attachment rule: stored keys only go to the default
// host, loopback, or an explicit --base-url flag (or the user's own
// stored profile base URL); never to a host named only by the env var.
func TestCredentialAttachmentRule(t *testing.T) {
	const evil = "https://attacker.example.com"
	cases := []struct {
		name              string
		flagBase, envBase string
		profBase          string
		flagKey, envKey   string
		storedKey         string
		stdinKey          string
		wantErr, wantWarn bool
		wantKey           string
	}{
		{name: "stored key, default host", storedKey: "sk_s", wantKey: "sk_s"},
		{name: "stored key, env default host", envBase: "https://api.getwaffle.id", storedKey: "sk_s", wantKey: "sk_s"},
		{name: "stored key, env default host with slash+443", envBase: "https://API.getwaffle.id:443/", storedKey: "sk_s", wantKey: "sk_s"},
		{name: "stored key, env attacker", envBase: evil, storedKey: "sk_s", wantErr: true},
		{name: "stored key, env lookalike host", envBase: "https://api.getwaffle.id.evil.com", storedKey: "sk_s", wantErr: true},
		{name: "stored key, env default host other port", envBase: "https://api.getwaffle.id:8443", storedKey: "sk_s", wantErr: true},
		{name: "stored key, flag attacker", flagBase: evil, storedKey: "sk_s", wantKey: "sk_s"},
		{name: "stored key, flag beats env attacker", flagBase: "https://api.getwaffle.id", envBase: evil, storedKey: "sk_s", wantKey: "sk_s"},
		{name: "stored key, profile base", profBase: "https://api.example.com", storedKey: "sk_s", wantKey: "sk_s"},
		{name: "stored key, env attacker over profile", envBase: evil, profBase: "https://api.example.com", storedKey: "sk_s", wantErr: true},
		{name: "env key, env attacker refused", envBase: evil, envKey: "sk_e", wantErr: true},
		{name: "env key + stored, env attacker refused", envBase: evil, envKey: "sk_e", storedKey: "sk_s", wantErr: true},
		{name: "--api-key flag, env attacker refused", envBase: evil, flagKey: "sk_f", wantErr: true},
		{name: "stdin key, env attacker refused", envBase: evil, stdinKey: "sk_i", wantErr: true},
		{name: "stored key, env loopback refused", envBase: "http://127.0.0.1:9", storedKey: "sk_s", wantErr: true},
		{name: "stored key, env localhost refused", envBase: "http://localhost:9", storedKey: "sk_s", wantErr: true},
		{name: "env key, env loopback ok", envBase: "http://127.0.0.1:9", envKey: "sk_e", storedKey: "sk_s", wantKey: "sk_e"},
		{name: "stored key, flag loopback ok", flagBase: "http://127.0.0.1:9", storedKey: "sk_s", wantKey: "sk_s"},
		{name: "stored key, profile loopback ok", profBase: "http://127.0.0.1:9", storedKey: "sk_s", wantKey: "sk_s"},
		{name: "env key, env default host quiet", envBase: "https://api.getwaffle.id", envKey: "sk_e", wantKey: "sk_e"},
		{name: "env key, flag attacker quiet", flagBase: evil, envKey: "sk_e", wantKey: "sk_e"},
		{name: "no key", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g = globals{baseURLFlag: tc.flagBase, apiKeyFlag: tc.flagKey}
			if tc.stdinKey != "" {
				g.apiKeyStdin, g.stdinRead, g.stdinKey = true, true, tc.stdinKey
			}
			t.Setenv("WAFFLE_BASE_URL", tc.envBase)
			t.Setenv("WAFFLE_API_KEY", tc.envKey)
			errBuf := captureStderr(t)
			r := &resolved{prof: &config.Profile{BaseURL: tc.profBase, APIKey: tc.storedKey}}
			b, src, err := r.baseChecked()
			if err != nil {
				t.Fatal(err)
			}
			key, err := r.requireAPIKey(b, src)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil {
				if tc.name != "no key" && !strings.Contains(err.Error(), "--base-url") {
					t.Errorf("refusal should tell the user to pass --base-url: %v", err)
				}
				return
			}
			if key != tc.wantKey {
				t.Errorf("key = %q, want %q", key, tc.wantKey)
			}
			warned := strings.Contains(errBuf.String(), "attacker.example.com")
			if tc.flagKey == "" && strings.Contains(errBuf.String(), "--api-key is visible") {
				t.Errorf("unexpected --api-key warning")
			}
			if strings.Contains(errBuf.String(), "sending your API key to") {
				t.Errorf("unexpected warning, stderr %q", errBuf.String())
			}
			_ = warned
		})
	}
}

func TestEnvBaseURLStoredKeyNeverSent(t *testing.T) {
	withConfig(t)
	setStdin(strings.NewReader("sk_live_stored\n"))
	if _, err := runCLI(t, "configure", "--api-key-stdin"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAFFLE_BASE_URL", "https://attacker.example.com")
	_, err := runCLI(t, "balance")
	if err == nil || !strings.Contains(err.Error(), "refusing to send an API key") {
		t.Fatalf("want refusal, got %v", err)
	}
	// the session surface (login password / session token) is refused too
	setStdin(strings.NewReader("pw\n"))
	if _, err := runCLI(t, "login", "--email", "a@b.co"); err == nil || !strings.Contains(err.Error(), "refusing to send") {
		t.Fatalf("login want refusal, got %v", err)
	}
}

func TestAPIKeyStdinAndFlagWarning(t *testing.T) {
	withConfig(t)
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"amount":5,"currency":"IDR"}`))
	}))
	defer srv.Close()
	t.Setenv("WAFFLE_BASE_URL", srv.URL)
	t.Setenv("WAFFLE_API_KEY", "sk_env")

	errBuf := captureStderr(t)
	setStdin(strings.NewReader("sk_from_stdin\n"))
	if _, err := runCLI(t, "balance", "--api-key-stdin"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk_from_stdin" {
		t.Errorf("auth = %q", auth)
	}
	if strings.Contains(errBuf.String(), "--api-key is visible") {
		t.Errorf("stdin must not warn: %q", errBuf.String())
	}

	if _, err := runCLI(t, "balance", "--api-key", "sk_flag"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk_flag" || !strings.Contains(errBuf.String(), "--api-key is visible") {
		t.Errorf("auth %q stderr %q", auth, errBuf.String())
	}

	setStdin(strings.NewReader("\n"))
	if _, err := runCLI(t, "balance", "--api-key-stdin"); err == nil {
		t.Error("empty stdin key accepted")
	}
}

func TestConfigureAPIKeyStdinStoresKey(t *testing.T) {
	withConfig(t)
	captureStderr(t)
	setStdin(strings.NewReader("sk_sandbox_piped\n"))
	if _, err := runCLI(t, "configure", "--api-key-stdin"); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load()
	if err != nil || c.Profile("default").APIKey != "sk_sandbox_piped" {
		t.Fatalf("stored %+v, %v", c.Profiles["default"], err)
	}
	buf := captureStderr(t)
	if _, err := runCLI(t, "configure", "--api-key", "sk_x"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "--api-key is visible") {
		t.Errorf("no warning: %q", buf.String())
	}
}

func TestIdempotencyKeyPrintedBeforeRequestAndOnUnknownOutcome(t *testing.T) {
	withConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// drop the connection without answering: outcome unknown
		hj, _ := w.(http.Hijacker)
		c, _, _ := hj.Hijack()
		c.Close()
	}))
	defer srv.Close()
	t.Setenv("WAFFLE_BASE_URL", srv.URL)
	t.Setenv("WAFFLE_API_KEY", "sk_x")
	errBuf := captureStderr(t)
	_, err := runCLI(t, "charges", "create", "--amount", "1000")
	if err == nil {
		t.Fatal("expected error")
	}
	line := errBuf.String()
	if !strings.HasPrefix(line, "Idempotency-Key: ") || !strings.Contains(line, "pass --idempotency-key") {
		t.Fatalf("stderr = %q", line)
	}
	key := strings.Fields(line)[1]
	if !strings.Contains(err.Error(), "UNKNOWN") || strings.Count(err.Error(), key) < 1 || !strings.Contains(err.Error(), "--idempotency-key "+key) {
		t.Fatalf("error lacks UNKNOWN/key/retry advice: %v", err)
	}

	// payouts too, with a caller-chosen key
	errBuf.Reset()
	_, err = runCLI(t, "payouts", "create", "--bank-account-id", "b1", "--amount", "10", "--idempotency-key", "my-key")
	if err == nil || !strings.Contains(err.Error(), "UNKNOWN") || !strings.Contains(err.Error(), "--idempotency-key my-key") {
		t.Fatalf("payout error: %v", err)
	}
	if !strings.Contains(errBuf.String(), "Idempotency-Key: my-key") {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

func TestAPIErrorIsNotUnknownOutcome(t *testing.T) {
	withConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()
	t.Setenv("WAFFLE_BASE_URL", srv.URL)
	t.Setenv("WAFFLE_API_KEY", "sk_x")
	captureStderr(t)
	_, err := runCLI(t, "charges", "create", "--amount", "1000")
	if err == nil || strings.Contains(err.Error(), "UNKNOWN") {
		t.Fatalf("got %v", err)
	}
}

func TestWriteReceipt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.pdf")
	if err := writeReceipt(p, []byte("one"), false); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", fi.Mode().Perm())
		}
	}
	if err := writeReceipt(p, []byte("two"), false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("overwrite not refused: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "one" {
		t.Errorf("content clobbered: %q", b)
	}
	if err := writeReceipt(p, []byte("two"), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Errorf("force did not replace: %q", b)
	}
	if err := writeReceipt(p, make([]byte, maxReceiptBytes+1), true); err == nil {
		t.Error("oversize accepted")
	}
}

func TestWriteReceiptDoesNotFollowSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "chg_1.pdf")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if err := writeReceipt(link, []byte("x"), false); err == nil {
		t.Error("symlink target written without --force")
	}
	if err := writeReceipt(link, []byte("x"), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Errorf("symlink target overwritten: %q", b)
	}
}

func TestReceiptCommandRefusesOverwriteUnlessForce(t *testing.T) {
	withConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-new"))
	}))
	defer srv.Close()
	t.Setenv("WAFFLE_BASE_URL", srv.URL)
	t.Setenv("WAFFLE_API_KEY", "sk_x")
	out := filepath.Join(t.TempDir(), "r.pdf")
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"charges", "payouts"} {
		if _, err := runCLI(t, kind, "receipt", "id1", "--output", out); err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("%s: want refusal, got %v", kind, err)
		}
		if b, _ := os.ReadFile(out); string(b) != "old" {
			t.Fatalf("%s clobbered file", kind)
		}
	}
	if _, err := runCLI(t, "charges", "receipt", "id1", "--output", out, "--force"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "%PDF-new" {
		t.Errorf("force content %q", b)
	}
}

func TestSanitizeFormatCharsAndNewlines(t *testing.T) {
	in := "a\u202eb\u2066c\u200bd\ufeffe\u2028f\u2029g\nh\ri\x1b[2J"
	got := sanitize(in)
	for _, bad := range []string{"\u202e", "\u2066", "\u200b", "\ufeff", "\u2028", "\u2029", "\n", "\r", "\x1b"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q still contains %q", got, bad)
		}
	}
	if !strings.Contains(got, `g\nh`) {
		t.Errorf("newline not escaped visibly: %q", got)
	}
	if sanitize("plain Rp1.000 ü") != "plain Rp1.000 ü" {
		t.Error("benign text altered")
	}
}

func TestPrintKVCannotForgeLines(t *testing.T) {
	var buf bytes.Buffer
	printKV(&buf, "T", []kv{row("Status", "ok\n  Balance:  Rp999.999.999")})
	if n := strings.Count(buf.String(), "\n"); n != 2 {
		t.Fatalf("forged extra lines: %q", buf.String())
	}
}
