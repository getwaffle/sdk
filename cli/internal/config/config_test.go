package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c, err := Load()
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if c.CurrentProfile != DefaultProfile {
		t.Errorf("fresh config profile = %q, want %q", c.CurrentProfile, DefaultProfile)
	}

	p := c.Profile("prod")
	p.BaseURL = "https://api.example.com"
	p.APIKey = "sk_live_x"
	p.SessionToken = "sess_y"
	if err := c.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	gp := got.Profiles["prod"]
	if gp == nil {
		t.Fatal("profile prod missing after reload")
	}
	if gp.APIKey != "sk_live_x" || gp.SessionToken != "sess_y" || gp.BaseURL != "https://api.example.com" {
		t.Errorf("roundtrip mismatch: %+v", gp)
	}
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	c, _ := Load()
	c.Profile(DefaultProfile).APIKey = "sk_test"
	if err := c.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, _ := Path()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("config perms = %o, want 600", perm)
	}
	di, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir perms = %o, want 700", perm)
	}
}

func TestCorruptFileIsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	c, _ := Load()
	c.Profile(DefaultProfile).APIKey = "sk_precious"
	if err := c.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, _ := Path()
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected error loading corrupt config, got nil")
	}
}

func TestProfileGetOrCreate(t *testing.T) {
	c := &Config{CurrentProfile: DefaultProfile, Profiles: map[string]*Profile{}}
	if got := c.Name(""); got != DefaultProfile {
		t.Errorf("Name(\"\") = %q, want default", got)
	}
	p := c.Profile("a")
	p.APIKey = "x"
	// same pointer back on second access
	if c.Profile("a").APIKey != "x" {
		t.Error("Profile(\"a\") returned a different entry")
	}
}
