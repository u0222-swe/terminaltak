// Package config loads, validates, and persists TerminalTak's user-level configuration.
package config

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	appDir         = "terminaltak"
	configFile     = "config.yaml"
	certFile       = "cert.pem"
	keyFile        = "key.pem"
	caFile         = "ca.pem"
	defaultPLISecs = 30
	minPLISecs     = 5
	maxPLISecs     = 300
)

// Config is the root configuration loaded from config.yaml.
type Config struct {
	Server   Server   `yaml:"server"`
	Identity Identity `yaml:"identity"`
	UI       UI       `yaml:"ui"`
	SelfPos  SelfPos  `yaml:"self_pos"`
}

// Server holds TAK server connection parameters.
type Server struct {
	Host                string `yaml:"host"`
	EnrollPort          int    `yaml:"enroll_port"`
	StreamPort          int    `yaml:"stream_port"`
	InsecureSkipVerify  bool   `yaml:"insecure_skip_verify"`
}

// Identity holds paths to the enrolled client certificate material.
// All paths are absolute. The files are written by the enroll package.
type Identity struct {
	CertPath string `yaml:"cert_path"`
	KeyPath  string `yaml:"key_path"`
	CAPath   string `yaml:"ca_path"`
}

// UI holds presentation preferences.
type UI struct {
	RefreshHz int `yaml:"refresh_hz"`
}

// SelfPos holds the user's published position and PLI metadata. The UID is
// generated once on first run and persisted so the server-side track stays
// stable across restarts.
type SelfPos struct {
	Lat             float64 `yaml:"lat"`
	Lon             float64 `yaml:"lon"`
	HAE             float64 `yaml:"hae"`
	Callsign        string  `yaml:"callsign"`
	Group           string  `yaml:"group"`
	Role            string  `yaml:"role"`
	UID             string  `yaml:"uid"`
	IntervalSeconds int     `yaml:"interval_seconds"`
	// RandomWalkSweden, when true, makes the PLI publisher pick a fresh
	// random latitude/longitude inside Sweden on every interval tick.
	// Useful for end-to-end testing against ATAK clients without having
	// to nudge the configured position by hand.
	RandomWalkSweden bool `yaml:"random_walk_sweden"`
}

// Default returns a Config populated with sensible zero-state defaults. The
// returned Config is not valid for connecting (Server.Host and SelfPos lat/lon
// remain empty) — the first-run flow must fill those in.
func Default() *Config {
	dir := mustDir()
	return &Config{
		Server: Server{
			EnrollPort: 8446,
			StreamPort: 8089,
		},
		Identity: Identity{
			CertPath: filepath.Join(dir, certFile),
			KeyPath:  filepath.Join(dir, keyFile),
			CAPath:   filepath.Join(dir, caFile),
		},
		UI: UI{
			RefreshHz: 4,
		},
		SelfPos: SelfPos{
			IntervalSeconds: defaultPLISecs,
		},
	}
}

// Path returns the absolute path of the YAML config file.
func Path() string { return filepath.Join(mustDir(), configFile) }

// Dir returns the absolute path of the config directory.
func Dir() string { return mustDir() }

// EnsureDir creates the config directory with mode 0700 if it does not exist.
func EnsureDir() error {
	return os.MkdirAll(mustDir(), 0o700)
}

// Load reads and parses the config file. If the file does not exist, Load
// writes a default skeleton and returns it. On any other I/O or parse error
// it returns the error unmodified.
func Load() (*Config, error) {
	if err := EnsureDir(); err != nil {
		return nil, fmt.Errorf("ensure config dir: %w", err)
	}
	cfg := Default()
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		if err := Save(cfg); err != nil {
			return nil, fmt.Errorf("write default config: %w", err)
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.normalise()
	return cfg, nil
}

// Save serialises cfg to YAML and writes it atomically (write-then-rename).
func Save(cfg *Config) error {
	if err := EnsureDir(); err != nil {
		return fmt.Errorf("ensure config dir: %w", err)
	}
	cfg.normalise()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, Path()); err != nil {
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// HasClientCert reports whether all three identity PEMs exist on disk and
// the client cert parses and is not yet expired. It does not validate the
// chain — TLS handshake will catch chain issues.
func HasClientCert(cfg *Config) bool {
	cert, err := loadClientCert(cfg)
	if err != nil {
		return false
	}
	if !cert.NotAfter.After(time.Now()) {
		return false
	}
	for _, p := range []string{cfg.Identity.KeyPath, cfg.Identity.CAPath} {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

// LoadClientCert returns the parsed leaf certificate referenced by
// cfg.Identity.CertPath, or an error if the file is missing or malformed.
// Useful for displaying CN and expiry in the TUI status bar.
func LoadClientCert(cfg *Config) (*x509.Certificate, error) {
	return loadClientCert(cfg)
}

func loadClientCert(cfg *Config) (*x509.Certificate, error) {
	data, err := os.ReadFile(cfg.Identity.CertPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block in cert file")
	}
	return x509.ParseCertificate(block.Bytes)
}

// EnsureSelfUID assigns a fresh UUIDv4 to cfg.SelfPos.UID if it is empty and
// returns whether an assignment happened (so the caller can decide whether to
// Save).
func EnsureSelfUID(cfg *Config) (changed bool, err error) {
	if cfg.SelfPos.UID != "" {
		return false, nil
	}
	id, err := newUUIDv4()
	if err != nil {
		return false, err
	}
	cfg.SelfPos.UID = id
	return true, nil
}

// normalise clamps configurable numeric fields to their valid ranges and
// fills any missing identity paths with their defaults under Dir().
func (c *Config) normalise() {
	if c.SelfPos.IntervalSeconds <= 0 {
		c.SelfPos.IntervalSeconds = defaultPLISecs
	}
	if c.SelfPos.IntervalSeconds < minPLISecs {
		c.SelfPos.IntervalSeconds = minPLISecs
	}
	if c.SelfPos.IntervalSeconds > maxPLISecs {
		c.SelfPos.IntervalSeconds = maxPLISecs
	}
	if c.UI.RefreshHz <= 0 {
		c.UI.RefreshHz = 4
	}
	if c.Server.EnrollPort == 0 {
		c.Server.EnrollPort = 8446
	}
	if c.Server.StreamPort == 0 {
		c.Server.StreamPort = 8089
	}
	dir := mustDir()
	if c.Identity.CertPath == "" {
		c.Identity.CertPath = filepath.Join(dir, certFile)
	}
	if c.Identity.KeyPath == "" {
		c.Identity.KeyPath = filepath.Join(dir, keyFile)
	}
	if c.Identity.CAPath == "" {
		c.Identity.CAPath = filepath.Join(dir, caFile)
	}
}

func mustDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		// UserConfigDir only fails when neither $XDG_CONFIG_HOME nor $HOME is
		// set — extremely rare. Fall back to a relative dir so callers can
		// still proceed (Save will then surface a useful error).
		base = "./.config"
	}
	return filepath.Join(base, appDir)
}

func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
