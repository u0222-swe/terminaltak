package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withFakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	return dir
}

func TestLoadCreatesDefaultWhenMissing(t *testing.T) {
	withFakeHome(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SelfPos.IntervalSeconds != defaultPLISecs {
		t.Errorf("IntervalSeconds = %d, want %d", cfg.SelfPos.IntervalSeconds, defaultPLISecs)
	}
	if cfg.Server.EnrollPort != 8446 {
		t.Errorf("EnrollPort = %d, want 8446", cfg.Server.EnrollPort)
	}
	if _, err := os.Stat(Path()); err != nil {
		t.Errorf("expected config file to be written; stat: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withFakeHome(t)
	cfg := Default()
	cfg.Server.Host = "takserver.example.com"
	cfg.SelfPos.Lat = 59.33
	cfg.SelfPos.Lon = 18.07
	cfg.SelfPos.Callsign = "ALICE"
	cfg.SelfPos.Group = "testchan_common"
	cfg.SelfPos.Role = "HQ"
	cfg.SelfPos.UID = "11111111-2222-4333-8444-555555555555"
	cfg.SelfPos.IntervalSeconds = 60

	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Server.Host != "takserver.example.com" {
		t.Errorf("Host = %q", got.Server.Host)
	}
	if got.SelfPos.Lat != 59.33 || got.SelfPos.Lon != 18.07 {
		t.Errorf("lat/lon = %v/%v", got.SelfPos.Lat, got.SelfPos.Lon)
	}
	if got.SelfPos.UID != cfg.SelfPos.UID {
		t.Errorf("UID = %q, want %q", got.SelfPos.UID, cfg.SelfPos.UID)
	}
	if got.SelfPos.IntervalSeconds != 60 {
		t.Errorf("IntervalSeconds = %d", got.SelfPos.IntervalSeconds)
	}
}

func TestNormaliseClampsInterval(t *testing.T) {
	withFakeHome(t)
	cases := []struct {
		in, want int
	}{
		{0, defaultPLISecs},
		{-5, defaultPLISecs},
		{1, minPLISecs},
		{5, 5},
		{30, 30},
		{300, 300},
		{1000, maxPLISecs},
	}
	for _, c := range cases {
		cfg := Default()
		cfg.SelfPos.IntervalSeconds = c.in
		cfg.normalise()
		if cfg.SelfPos.IntervalSeconds != c.want {
			t.Errorf("normalise(%d) = %d, want %d", c.in, cfg.SelfPos.IntervalSeconds, c.want)
		}
	}
}

func TestEnsureSelfUIDIsStable(t *testing.T) {
	withFakeHome(t)
	cfg := Default()
	changed, err := EnsureSelfUID(cfg)
	if err != nil {
		t.Fatalf("EnsureSelfUID: %v", err)
	}
	if !changed {
		t.Error("expected changed=true on first call")
	}
	first := cfg.SelfPos.UID
	if first == "" {
		t.Fatal("UID still empty after EnsureSelfUID")
	}
	if !looksLikeUUIDv4(first) {
		t.Errorf("UID %q does not look like a UUIDv4", first)
	}

	changed, err = EnsureSelfUID(cfg)
	if err != nil {
		t.Fatalf("EnsureSelfUID 2nd: %v", err)
	}
	if changed {
		t.Error("expected changed=false on second call")
	}
	if cfg.SelfPos.UID != first {
		t.Errorf("UID changed unexpectedly: %q != %q", cfg.SelfPos.UID, first)
	}
}

// looksLikeUUIDv4 checks length 36, dashes at the right positions, and that
// the version nibble is 4 and the variant nibble is 8/9/a/b. Good enough for
// a unit test.
func looksLikeUUIDv4(s string) bool {
	if len(s) != 36 {
		return false
	}
	for _, i := range []int{8, 13, 18, 23} {
		if s[i] != '-' {
			return false
		}
	}
	if s[14] != '4' {
		return false
	}
	if !strings.ContainsRune("89ab", rune(s[19])) {
		return false
	}
	return true
}

func TestPathsLandUnderConfigDir(t *testing.T) {
	dir := withFakeHome(t)
	cfg := Default()
	want := filepath.Join(dir, appDir)
	for _, p := range []string{cfg.Identity.CertPath, cfg.Identity.KeyPath, cfg.Identity.CAPath} {
		if !strings.HasPrefix(p, want) {
			t.Errorf("path %q not under %q", p, want)
		}
	}
}
