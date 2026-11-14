package config

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestSaveUsesTempInSameDirAndLeavesNoStrays(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, _ := Load()
	c.Profile("default").APIKey = "sk_x"
	// a pre-planted predictable tmp name must not be followed
	p, _ := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(victim, p+".tmp"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); err == nil {
		t.Fatal("symlinked tmp was followed")
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range ents {
		if e.Name() != "config.json" && e.Name() != "config.json.tmp" {
			t.Errorf("stray file %s", e.Name())
		}
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode %v", fi.Mode().Perm())
		}
	}
}

func TestSaveTightensLooseOwnedDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p, _ := Path()
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c, _ := Load()
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v, want 0700", fi.Mode().Perm())
	}
}
