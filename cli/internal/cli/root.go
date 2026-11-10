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

Credentials resolve in precedence order: --api-key flag >
WAFFLE_API_KEY environment variable > the value stored by
"waffle login"/"waffle configure" in the config file.`,
		Version:           Version,
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&g.profileName, "profile", "p", "", "config profile to use (default \"default\")")
	pf.StringVar(&g.apiKeyFlag, "api-key", "", "merchant API key (overrides WAFFLE_API_KEY and stored config)")
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

// apiBase applies precedence: flag > env > stored profile > SDK default.
func (r *resolved) apiBase() string {
	if g.baseURLFlag != "" {
		return strings.TrimRight(g.baseURLFlag, "/")
	}
	if v := os.Getenv(envBaseURL); v != "" {
		return strings.TrimRight(v, "/")
	}
	if r.prof.BaseURL != "" {
		return r.prof.BaseURL
	}
	return waffle.DefaultBaseURL
}

// base returns the API base URL after validating it is safe to send
// credentials to: https, or plain http only for a loopback host (local
// dev/tests). Anything else (http to a remote host, userinfo, other
// schemes) is refused so a stray WAFFLE_BASE_URL or config edit cannot
// downgrade to cleartext or smuggle credentials elsewhere silently.
func (r *resolved) base() (string, error) {
	b := r.apiBase()
	if err := validateBaseURL(b); err != nil {
		return "", err
	}
	return b, nil
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid base URL %q", raw)
	}
	if u.User != nil {
		return fmt.Errorf("invalid base URL %q: must not contain credentials", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
	}
	return fmt.Errorf("refusing base URL %q: must be https (http is only allowed for localhost)", raw)
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
	b, err := r.base()
	if err != nil {
		return nil, err
	}
	return session.New(b, token), nil
}

// requireAPIKey applies the credential precedence: flag > env > stored
// profile. Money-moving commands call this; the session surface must
// never fall through to it.
func (r *resolved) requireAPIKey() (string, error) {
	if g.apiKeyFlag != "" {
		return g.apiKeyFlag, nil
	}
	if v := os.Getenv(envAPIKey); v != "" {
		return v, nil
	}
	if r.prof.APIKey != "" {
		return r.prof.APIKey, nil
	}
	return "", fmt.Errorf("no API key configured — create one with %q, store one with %q, or export %s=<key>",
		"waffle keys create", "waffle configure --api-key sk_...", envAPIKey)
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
