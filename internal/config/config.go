// Package config stores the CLI's sign-in and context in a file only the user can read.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultAPI is the hosted control plane.
const DefaultAPI = "https://zenavo.app"

// Config is the saved state. The token belongs to API: it is never sent to another host.
type Config struct {
	API   string `json:"api"`
	Token string `json:"token,omitempty"`
	// Context: later commands use these when no flag names them. Switching teams clears the rest,
	// because projects and services belong to one team.
	Team    string `json:"team,omitempty"`
	Project string `json:"project,omitempty"`
	Service string `json:"service,omitempty"`
}

// Path is $ZENAVO_CONFIG, or config.json in the user's config directory (~/.config/zenavo on Linux and macOS).
func Path() (string, error) {
	if p := os.Getenv("ZENAVO_CONFIG"); p != "" {
		return p, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "zenavo", "config.json"), nil
}

// Load reads the config. A missing file is an empty config; a file other users can read is refused,
// because it may hold a token.
func Load(path string) (*Config, error) {
	c := &Config{}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s is readable by other users (mode %04o); run: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	if err := json.NewDecoder(f).Decode(c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save writes the config atomically with mode 0600 in a 0700 directory.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// SetTeam switches the team and clears the project and service chosen in the previous one.
func (c *Config) SetTeam(id string) {
	if c.Team != id {
		c.Project, c.Service = "", ""
	}
	c.Team = id
}

// SetProject switches the project and clears the service chosen in the previous one.
func (c *Config) SetProject(id string) {
	if c.Project != id {
		c.Service = ""
	}
	c.Project = id
}

// NormalizeAPI checks an API origin: https, or http only for this machine, with no path, query or credentials.
func NormalizeAPI(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid API URL %q: use an origin such as %s", raw, DefaultAPI)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("invalid API URL %q: use only scheme and host, such as %s", raw, DefaultAPI)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" && h != "::1" && !strings.HasSuffix(h, ".localhost") {
			return "", fmt.Errorf("refusing plain http to %s: tokens would be sent unencrypted; use https", h)
		}
	default:
		return "", fmt.Errorf("invalid API URL %q: scheme must be https", raw)
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}
