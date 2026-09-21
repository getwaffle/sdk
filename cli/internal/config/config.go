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
)

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

// Save writes the config atomically: the directory is created 0700 and
// the file 0600, then renamed over the target so a crash mid-write never
// truncates existing credentials.
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("config: creating directory: %w", err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}
	raw = append(raw, '\n')
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("config: writing: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("config: replacing %s: %w", p, err)
	}
	return nil
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
