// Package config stores the waffle CLI's local state: a single JSON
// file at $XDG_CONFIG_HOME/waffle/config.json (defaulting to
// ~/.config/waffle/config.json), mode 0600, holding per-profile
// credentials — the same plaintext-file-at-rest posture the Stripe CLI,
// gh, and aws-cli use by default.
//
// Two independent credentials live here, mirroring the frozen API
// contract (docs/api-contract.md): a dashboard SessionToken (only ever
// sent to /v1/merchants/me/* routes) and an APIKey (the only credential
// that can move money). They are never interchangeable and the CLI never
// sends one where the other is expected.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// skipPermWarning is true where POSIX modes do not apply.
var skipPermWarning = runtime.GOOS == "windows"

// DefaultProfile is the profile used when --profile is not passed.
const DefaultProfile = "default"

// Profile is one named set of local credentials and endpoint overrides.
type Profile struct {
	// BaseURL overrides the merchant API base URL for this profile.
	BaseURL string `json:"base_url,omitempty"`
	// DashboardURL is the merchant dashboard's base URL, used only to
	// print a link to its API-keys page after login. Never called by
	// the CLI itself.
	DashboardURL string `json:"dashboard_url,omitempty"`
	// SessionToken is the dashboard session from POST
	// /v1/merchants/login. Authorizes /v1/merchants/me/* only.
	SessionToken string `json:"session_token,omitempty"`
	// Email is the account the SessionToken belongs to, for display.
	Email string `json:"email,omitempty"`
	// APIKey is a merchant API key. The only credential accepted by the
	// money-moving surface (/v1/charges, /v1/balance, ...).
	APIKey string `json:"api_key,omitempty"`
}

// Config is the on-disk document: a map of named profiles plus which one
// is current.
type Config struct {
	CurrentProfile string              `json:"current_profile"`
	Profiles       map[string]*Profile `json:"profiles"`
}

// Path returns the config file location, resolving XDG_CONFIG_HOME on
// every call so tests can redirect it.
func Path() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: resolving home directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "waffle", "config.json"), nil
}

// Load reads the config file. A missing file is not an error: it returns
// an empty config with the default profile selected. A corrupt file IS an
// error — silently starting fresh would destroy stored credentials.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{CurrentProfile: DefaultProfile, Profiles: map[string]*Profile{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", p, err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("config: %s is not valid JSON — fix or remove it: %w", p, err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]*Profile{}
	}
	if c.CurrentProfile == "" {
		c.CurrentProfile = DefaultProfile
	}
	return &c, nil
}

// Save writes the config atomically: the directory is created 0700 (and
// tightened to 0700 if it is ours and group/world accessible), the data
// goes to a fresh O_EXCL 0600 temp file in the same directory, is
// fsynced, and is renamed over the target so a crash mid-write never
// truncates existing credentials and a pre-planted file or symlink at a
// predictable temp name is never followed.
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: creating directory: %w", err)
	}
	secureDir(dir)
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(dir, ".config-*.tmp") // 0600, O_EXCL
	if err != nil {
		return fmt.Errorf("config: writing: %w", err)
	}
	name := tmp.Name()
	fail := func(what string, err error) error {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("config: %s: %w", what, err)
	}
	if _, err := tmp.Write(raw); err != nil {
		return fail("writing", err)
	}
	if err := tmp.Sync(); err != nil {
		return fail("sync", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail("chmod", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("config: closing: %w", err)
	}
	if err := os.Rename(name, p); err != nil {
		os.Remove(name)
		return fmt.Errorf("config: replacing %s: %w", p, err)
	}
	return nil
}

// secureDir tightens a config directory that is group/world accessible
// to 0700 when this user owns it, and warns on stderr otherwise.
func secureDir(dir string) {
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm()&0o077 == 0 || !ownsDir(fi) {
		if err == nil && fi.Mode().Perm()&0o077 != 0 && !skipPermWarning {
			fmt.Fprintf(os.Stderr, "warning: config directory %s is accessible to other users and not owned by you\n", dir)
		}
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not tighten permissions of %s: %v\n", dir, err)
	}
}

// Profile returns the named profile, creating it (empty) if missing, so
// callers can mutate and save. The returned pointer aliases the map
// entry; mutate through it and call Save.
func (c *Config) Profile(name string) *Profile {
	if name == "" {
		name = DefaultProfile
	}
	p, ok := c.Profiles[name]
	if !ok {
		p = &Profile{}
		c.Profiles[name] = p
	}
	return p
}

// Name returns the profile name in use, defaulting sensibly.
func (c *Config) Name(name string) string {
	if name == "" {
		return DefaultProfile
	}
	return name
}
