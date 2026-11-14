// Package cli implements the waffle developer CLI: cobra commands on
// top of two clients — github.com/getwaffle/sdk/go for every
// API-key-authenticated money-moving call, and the private internal/
// session package for the dashboard-authenticated /v1/merchants/me/*
// surface. The two credentials are never interchangeable: a session
// token cannot create a charge (docs/api-contract.md), so the CLI never
// sends one where an API key is expected, or vice versa.
package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	waffle "github.com/getwaffle/sdk/go"
	"github.com/spf13/cobra"

	"github.com/getwaffle/sdk/cli/internal/config"
	"github.com/getwaffle/sdk/cli/internal/session"
)

// globals holds the persistent flags every command reads.
type globals struct {
	profileName string
	apiKeyFlag  string
	apiKeyStdin bool
	stdinKey    string // cached key read from stdin (read once)
	stdinRead   bool
	baseURLFlag string
	jsonOut     bool
}

var g globals

// Version is set at build time via -ldflags
// "-X github.com/getwaffle/sdk/cli/internal/cli.Version=v1.2.3" (see
// .github/workflows/release-cli.yml). "dev" here means a local `go
// build`/`go run` with no ldflags, i.e. not a tagged release.
var Version = "dev"

// resolved carries everything a command needs after flag/env/config
// precedence is applied.
type resolved struct {
	cfg  *config.Config
	name string
	prof *config.Profile
}

const (
	envAPIKey  = "WAFFLE_API_KEY"
	envBaseURL = "WAFFLE_BASE_URL"
)

// newRootCmd wires the full command tree with the persistent flags:
//
//	--api-key   explicit API key, beating WAFFLE_API_KEY and config
//	--base-url  merchant API base URL (default https://api.getwaffle.id)
//	--profile   named section of the config file
//	--json      machine-readable output on every command
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "waffle",
		Short: "Developer CLI for the waffle payment orchestration platform",
		Long: `Developer CLI for the waffle payment orchestration platform.

Two credentials, never interchangeable:
  - an API key (Authorization: Bearer sk_...) — moves money: balance,
    charges, payouts, bank accounts. Get one via "waffle login" +
    "waffle keys create", or export WAFFLE_API_KEY.
  - a dashboard session token — manages your account: whoami, keys.

Credentials resolve in precedence order: --api-key-stdin / --api-key >
WAFFLE_API_KEY environment variable > the value stored by
"waffle login"/"waffle configure" in the config file. Prefer stdin, the
environment, or the stored value: --api-key is visible in process
listings and shell history.

A stored API key is only ever sent to the default API host, a loopback
host, or a base URL you pass with the --base-url flag — never to a host
named only by the WAFFLE_BASE_URL environment variable.`,
		Version:           Version,
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&g.profileName, "profile", "p", "", "config profile to use (default \"default\")")
	pf.StringVar(&g.apiKeyFlag, "api-key", "", "merchant API key (INSECURE: visible in ps and shell history; prefer --api-key-stdin or WAFFLE_API_KEY)")
	pf.BoolVar(&g.apiKeyStdin, "api-key-stdin", false, "read the merchant API key from stdin (overrides WAFFLE_API_KEY and stored config)")
	pf.StringVar(&g.baseURLFlag, "base-url", "", "merchant API base URL (overrides WAFFLE_BASE_URL and stored config)")
	pf.BoolVar(&g.jsonOut, "json", false, "output raw JSON instead of human-readable text")

	root.AddCommand(
		newLoginCmd(),
		newLogoutCmd(),
		newWhoamiCmd(),
		newConfigureCmd(),
		newKeysCmd(),
		newBalanceCmd(),
		newBanksCmd(),
		newChargesCmd(),
		newBankAccountsCmd(),
		newPayoutsCmd(),
		newHealthzCmd(),
		newWebhooksCmd(),
	)
	return root
}

// load resolves the active profile from flags, env, and the config file.
// It never errors when the file is missing (fresh machine) — only when
// it exists and cannot be parsed, because silently discarding a corrupt
// file would destroy stored credentials.
func (g *globals) load() (*resolved, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	name := cfg.Name(g.profileName)
	return &resolved{cfg: cfg, name: name, prof: cfg.Profile(name)}, nil
}

// baseSource says where the effective base URL came from.
type baseSource int

const (
	srcDefault baseSource = iota
	srcProfile
	srcEnv
	srcFlag
)

// apiBaseSrc applies precedence: flag > env > stored profile > SDK
// default, and reports which one won.
func (r *resolved) apiBaseSrc() (string, baseSource) {
	if g.baseURLFlag != "" {
		return strings.TrimRight(g.baseURLFlag, "/"), srcFlag
	}
	if v := os.Getenv(envBaseURL); v != "" {
		return strings.TrimRight(v, "/"), srcEnv
	}
	if r.prof.BaseURL != "" {
		return r.prof.BaseURL, srcProfile
	}
	return waffle.DefaultBaseURL, srcDefault
}

func (r *resolved) apiBase() string {
	b, _ := r.apiBaseSrc()
	return b
}

// base returns the API base URL after validating it is safe to send
// credentials to: https, or plain http only for a loopback host (local
// dev/tests). Anything else (http to a remote host, userinfo, paths,
// queries, other schemes) is refused so a stray WAFFLE_BASE_URL or
// config edit cannot downgrade to cleartext or smuggle credentials
// elsewhere silently.
func (r *resolved) base() (string, error) {
	b, _, err := r.baseChecked()
	return b, err
}

func (r *resolved) baseChecked() (string, baseSource, error) {
	b, src := r.apiBaseSrc()
	if err := validateBaseURL(b); err != nil {
		return "", src, err
	}
	return b, src, nil
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid base URL %q", raw)
	}
	if u.User != nil {
		return fmt.Errorf("invalid base URL %q: must not contain credentials", raw)
	}
	if u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("invalid base URL %q: must not contain a path", raw)
	}
	if u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "?") {
		return fmt.Errorf("invalid base URL %q: must not contain a query string", raw)
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("invalid base URL %q: must not contain a fragment", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("refusing base URL %q: must be https (http is only allowed for localhost)", raw)
}

func isLoopbackHost(h string) bool {
	switch strings.ToLower(h) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// isDefaultAPIHost reports whether base is the production API origin.
func isDefaultAPIHost(base string) bool {
	d, err1 := url.Parse(waffle.DefaultBaseURL)
	u, err2 := url.Parse(base)
	if err1 != nil || err2 != nil {
		return false
	}
	return u.Scheme == "https" && strings.EqualFold(u.Hostname(), d.Hostname()) &&
		(u.Port() == "" || u.Port() == "443")
}

// credsTrusted reports whether credentials stored in the config file
// may be attached for this base URL. A base URL taken from the
// environment (which a hostile repo's .envrc/.env/Makefile/CI can set)
// is trusted only when it is the default API host or loopback; one
// given with the --base-url flag, read from the stored profile, or the
// default is trusted.
func credsTrusted(base string, src baseSource) bool {
	if src != srcEnv {
		return true
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	return isDefaultAPIHost(base) || isLoopbackHost(u.Hostname())
}

func baseHost(base string) string {
	if u, err := url.Parse(base); err == nil {
		return u.Host
	}
	return base
}

// newHTTPClient is the client for all API calls: bounded timeout and no
// redirect following (the API never redirects; following one would let a
// compromised or misconfigured endpoint steer an Authorization header).
func newHTTPClient() *http.Client { return session.NewHTTPClient() }

// moneyClientFor builds an SDK client with the safe HTTP client.
func newSDKClient(key, base string) *waffle.Client {
	return waffle.NewClient(key, waffle.WithBaseURL(base), waffle.WithHTTPClient(newHTTPClient()))
}

// sessionClient builds a session client for the validated base URL.
func (r *resolved) sessionClient(token string) (*session.Client, error) {
	b, src, err := r.baseChecked()
	if err != nil {
		return nil, err
	}
	if !credsTrusted(b, src) || (token != "" && src == srcEnv && !isDefaultAPIHost(b)) {
		return nil, envBaseRefusal(b, "credentials")
	}
	return session.New(b, token), nil
}

// envBaseRefusal is the error for a credential + an untrusted
// environment-supplied base URL.
func envBaseRefusal(base, what string) error {
	return fmt.Errorf("refusing to send %s to %s: that base URL comes only from %s, which a project file (.envrc, .env, Makefile, CI) can set. Pass --base-url %s explicitly (the flag, not the environment) if you trust it, or unset %s",
		what, baseHost(base), envBaseURL, base, envBaseURL)
}

// readStdinKey reads the API key from stdin once.
func readStdinKey() (string, error) {
	if g.stdinRead {
		return g.stdinKey, nil
	}
	b, err := io.ReadAll(io.LimitReader(stdin, 4097))
	if err != nil {
		return "", fmt.Errorf("reading API key from stdin: %w", err)
	}
	if len(b) > 4096 {
		return "", fmt.Errorf("API key from stdin is too long")
	}
	k := strings.TrimSpace(string(b))
	if k == "" {
		return "", fmt.Errorf("--api-key-stdin: no API key on stdin")
	}
	g.stdinRead, g.stdinKey = true, k
	return k, nil
}

const apiKeyFlagWarning = "warning: --api-key is visible in process listings and shell history; prefer --api-key-stdin or the WAFFLE_API_KEY environment variable"

// apiKey applies the credential precedence: stdin/flag > env > stored
// profile. explicit is true unless the key came from the config file.
func (r *resolved) apiKey() (key string, explicit bool, err error) {
	if g.apiKeyStdin {
		k, err := readStdinKey()
		return k, true, err
	}
	if g.apiKeyFlag != "" {
		fmt.Fprintln(stderr, apiKeyFlagWarning)
		return g.apiKeyFlag, true, nil
	}
	if v := os.Getenv(envAPIKey); v != "" {
		return v, true, nil
	}
	if r.prof.APIKey != "" {
		return r.prof.APIKey, false, nil
	}
	return "", false, fmt.Errorf("no API key configured — create one with %q, store one with %q, or export %s=<key>",
		"waffle keys create", "waffle configure --api-key-stdin", envAPIKey)
}

// requireAPIKey resolves the key for the validated base URL. When the
// base URL comes only from the environment and is not the default API
// host or loopback, NO credential is attached (stored, env, flag or
// stdin). For an environment-supplied loopback URL a stored key is also
// refused (a local port squatter could capture it); an explicit key is
// allowed. Money-moving commands call this; the session surface must
// never fall through to it.
func (r *resolved) requireAPIKey(base string, src baseSource) (string, error) {
	if !credsTrusted(base, src) {
		return "", envBaseRefusal(base, "an API key")
	}
	key, explicit, err := r.apiKey()
	if err != nil {
		return "", err
	}
	if src == srcEnv && !explicit && !isDefaultAPIHost(base) {
		return "", envBaseRefusal(base, "the stored API key")
	}
	return key, nil
}

// requireSession returns the stored dashboard session token. Only
// /v1/merchants/me/* commands call this; it must never feed a
// money-moving endpoint.
func (r *resolved) requireSession() (string, error) {
	if r.prof.SessionToken == "" {
		return "", fmt.Errorf("not logged in — run %q first", "waffle login")
	}
	return r.prof.SessionToken, nil
}

// Execute runs the CLI and translates errors for the terminal: API
// errors print as "HTTP <status>: <message>" (the server's {"error"}
// body is the message), everything else prints as-is. Exit code is 1 on
// any error.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		var apiErr *waffle.APIError
		if errors.As(err, &apiErr) {
			fmt.Fprintf(os.Stderr, "error: HTTP %d: %s\n", apiErr.StatusCode, sanitize(apiErr.Message))
		} else {
			fmt.Fprintf(os.Stderr, "error: %s\n", sanitize(err.Error()))
		}
		os.Exit(1)
	}
}
