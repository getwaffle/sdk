package cli

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/getwaffle/sdk/cli/internal/config"
	"github.com/getwaffle/sdk/cli/internal/session"
)

// stdin/stderr indirection so tests can drive the prompts. stdinReader
// wraps stdin once so consecutive prompts (email, then password) share
// one buffered reader — a second bufio over the same pipe would swallow
// buffered lines and hit EOF.
var (
	stdin       io.Reader = os.Stdin
	stderr      io.Writer = os.Stderr
	stdinReader *bufio.Reader
)

// setStdin swaps the prompt input (tests); pass os.Stdin to restore.
func setStdin(r io.Reader) {
	stdin = r
	stdinReader = nil
}

// readLine prints prompt to stderr and reads one line from stdin. When
// stdin is an interactive terminal, hidden is honored via term.ReadPassword
// (no echo); when stdin is a pipe (scripts, tests) the value is read
// plainly — the caller is responsible for not piping secrets on shared
// machines, same tradeoff gh/aws CLIs document.
func readLine(prompt string, hidden bool) (string, error) {
	fmt.Fprint(stderr, prompt)
	if f, ok := stdin.(*os.File); ok && hidden && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", prompt, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	if stdinReader == nil {
		stdinReader = bufio.NewReader(stdin)
	}
	line, err := stdinReader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("reading %s: %w", prompt, err)
	}
	if hidden {
		fmt.Fprintln(stderr)
	}
	return strings.TrimSpace(line), nil
}

// defaultDashboardURL guesses the merchant dashboard base URL for the
// two topologies this CLI ships defaults for: Waffle's production API
// (api.getwaffle.id -> the production dashboard at getwaffle.id) and
// local dev (:8080 -> the dashboard dev server on :3001). Any other
// deployment sets dashboard_url via "waffle configure --dashboard-url".
// Hostname() (not the raw authority string) is matched so a port on the
// base URL — e.g. the local-dev default http://localhost:8080 — still
// resolves to the local dashboard.
func defaultDashboardURL(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return "http://localhost:3001"
	case "api.getwaffle.id":
		return "https://getwaffle.id"
	}
	return ""
}

// dashboardAPIKeysURL returns the merchant dashboard page where API keys
// live, or "" when the dashboard location is unknown.
func dashboardAPIKeysURL(r *resolved) string {
	base := r.prof.DashboardURL
	if base == "" {
		base = defaultDashboardURL(r.apiBase())
	}
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/integrations/api-keys"
}

// openBrowser best-effort opens an http/https URL in the system
// browser; anything else (or a parse failure) is refused — the value
// ultimately comes from user input, and xdg-open/open/rundll32 have a
// history of argument-injection CVEs, so the scheme is validated before
// the URL reaches exec.Command. Failure is reported to stderr but never
// fatal — the URL is printed to stdout regardless.
func openBrowser(rawURL string) {
	openable, ok := browserOpenableURL(rawURL)
	if !ok {
		fmt.Fprintf(stderr, "(not opening %q — only http/https URLs are opened)\n", rawURL)
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", openable)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", openable)
	default:
		cmd = exec.Command("xdg-open", openable)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "(could not open a browser: %v — open the URL above manually)\n", err)
	}
}

// browserOpenableURL returns the normalized URL to open and whether it
// passed the http/https gate. The gate is the security boundary: only a
// well-formed http(s) URL ever flows into a subprocess argument.
func browserOpenableURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return u.String(), true
}
func newLoginCmd() *cobra.Command {
	var (
		emailFlag  string
		apiKeyFlag string
		open       bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with email+password, or store an API key directly",
		Long: `Authenticate and store credentials in the waffle config file.

With --api-key, stores that key and skips the password flow entirely.
Otherwise prompts for email+password, exchanges them for a dashboard
session (POST /v1/merchants/login), and stores the session — enough for
"whoami" and "keys". Money-moving commands additionally need an API key:
if none is stored, login prints how to create one ("waffle keys
create") and, when the dashboard location is known, its API-keys page.`,
		Example: `  waffle login
  waffle login --email ops@myshop.id
  waffle login --api-key sk_sandbox_xxx
  waffle configure --dashboard-url https://dashboard.example.com`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := g.load()
			if err != nil {
				return err
			}
			if g.baseURLFlag != "" {
				r.prof.BaseURL = strings.TrimRight(g.baseURLFlag, "/")
			}

			if apiKeyFlag != "" {
				r.prof.APIKey = apiKeyFlag
				if err := r.cfg.Save(); err != nil {
					return err
				}
				printKV(os.Stdout, fmt.Sprintf("API key stored for profile %q.", r.name), []kv{
					row("Profile", r.name),
					row("Base URL", r.apiBase()),
					row("Config file", mustConfigPath()),
				})
				return nil
			}

			email := emailFlag
			if email == "" {
				if email, err = readLine("Email: ", false); err != nil {
					return err
				}
			}
			if email == "" {
				return fmt.Errorf("email is required")
			}
			password, err := readLine("Password: ", true)
			if err != nil {
				return err
			}
			if password == "" {
				return fmt.Errorf("password is required")
			}

			sc := session.New(r.apiBase(), "")
			token, err := sc.Login(cmd.Context(), email, password)
			if err != nil {
				return err
			}
			r.prof.SessionToken = token
			r.prof.Email = email
			if err := r.cfg.Save(); err != nil {
				return err
			}

			rows := []kv{
				row("Logged in", email),
				row("Profile", r.name),
				row("Session", maskKey(token)),
			}
			printKV(os.Stdout, "Logged in. Session stored in "+mustConfigPath()+".", rows)
			if r.prof.APIKey == "" {
				fmt.Fprintln(os.Stdout, `
No API key stored yet — money commands (balance, charges, payouts) need one.
  waffle keys create            mint + store a sandbox key right here`)
				if url := dashboardAPIKeysURL(r); url != "" {
					fmt.Fprintf(os.Stdout, "  or copy an existing key from %s\n", url)
					if open {
						openBrowser(url)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&emailFlag, "email", "", "account email (skips the email prompt)")
	cmd.Flags().StringVar(&apiKeyFlag, "api-key", "", "store this API key instead of running the email+password flow")
	cmd.Flags().BoolVar(&open, "open", false, "open the dashboard API-keys page in a browser when no key is stored")
	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear stored session token and API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := g.load()
			if err != nil {
				return err
			}
			hadSession := r.prof.SessionToken != ""
			r.prof.SessionToken = ""
			r.prof.Email = ""
			r.prof.APIKey = ""
			if err := r.cfg.Save(); err != nil {
				return err
			}
			state := "nothing to clear"
			if hadSession {
				state = "session and API key cleared"
			}
			fmt.Fprintf(os.Stdout, "Profile %q: %s.\n", r.name, state)
			return nil
		},
	}
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the merchant account behind the stored session",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := g.load()
			if err != nil {
				return err
			}
			token, err := r.requireSession()
			if err != nil {
				return err
			}
			sc := session.New(r.apiBase(), token)
			prof, err := sc.GetProfile(cmd.Context())
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, prof)
			}
			printKV(os.Stdout, "", []kv{
				row("Merchant ID", prof.MerchantID),
				row("Business", prof.BusinessName),
				row("Email", prof.Email),
				row("Status", prof.Status),
				row("Review", prof.ReviewStatus),
				row("Entity", prof.EntityKind),
				row("Role", prof.Role),
				row("Created", prof.CreatedAt),
			})
			return nil
		},
	}
}

func newConfigureCmd() *cobra.Command {
	var (
		apiKey       string
		dashboardURL string
	)
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Store an API key, base URL, or dashboard URL without logging in",
		Long: `Store credentials/endpoints in the config file directly.

A merchant who already holds an API key never needs "login": configure
the key once and every money command works. Precedence for money
commands remains flag > WAFFLE_API_KEY > this stored value.`,
		Example: `  waffle configure --api-key sk_sandbox_xxx
  waffle configure --base-url https://api.example.com
  waffle configure --dashboard-url https://dashboard.example.com
  waffle --profile prod configure --api-key sk_live_xxx`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if apiKey == "" && g.baseURLFlag == "" && dashboardURL == "" {
				return fmt.Errorf("nothing to configure — pass --api-key, --base-url, and/or --dashboard-url")
			}
			r, err := g.load()
			if err != nil {
				return err
			}
			if apiKey != "" {
				r.prof.APIKey = apiKey
			}
			if g.baseURLFlag != "" {
				r.prof.BaseURL = strings.TrimRight(g.baseURLFlag, "/")
			}
			if dashboardURL != "" {
				r.prof.DashboardURL = strings.TrimRight(dashboardURL, "/")
			}
			if err := r.cfg.Save(); err != nil {
				return err
			}
			printKV(os.Stdout, fmt.Sprintf("Profile %q configured.", r.name), []kv{
				row("API key", maskKey(r.prof.APIKey)),
				row("Base URL", r.prof.BaseURL),
				row("Dashboard URL", r.prof.DashboardURL),
				row("Config file", mustConfigPath()),
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&apiKey, "api-key", "", "merchant API key to store")
	cmd.Flags().StringVar(&dashboardURL, "dashboard-url", "", "merchant dashboard base URL (used for post-login links)")
	return cmd
}

// maskKey renders just enough of a key to recognize it. Keys shorter
// than 8 chars are fully masked — never sliced out of range, never
// echoed whole.
func maskKey(k string) string {
	switch {
	case k == "":
		return ""
	case len(k) < 8:
		return "..."
	case len(k) <= 12:
		return k[:4] + "..."
	default:
		return k[:12] + "..."
	}
}

// mustConfigPath is config.Path for display; errors fall back to "<config file>".
func mustConfigPath() string {
	p, err := config.Path()
	if err != nil {
		return "<config file>"
	}
	return p
}
