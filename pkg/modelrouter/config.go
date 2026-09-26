// Package modelrouter provides a local model catalog and gateway for coding agents.
package modelrouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Provider struct {
	ID        string   `json:"id"`
	BaseURL   string   `json:"base_url,omitempty"`
	Protocol  string   `json:"protocol,omitempty"`
	APIKey    string   `json:"api_key,omitempty"`
	APIKeyEnv string   `json:"api_key_env,omitempty"`
	Models    []string `json:"models"`
}

type Config struct {
	Providers []Provider `json:"providers"`
}

var providerID = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func (c Config) Validate() error {
	seen := make(map[string]bool, len(c.Providers))
	for _, p := range c.Providers {
		if !providerID.MatchString(p.ID) || seen[p.ID] {
			return fmt.Errorf("invalid or duplicate provider ID %q", p.ID)
		}
		seen[p.ID] = true
		if p.Protocol != "" && p.Protocol != "chat" && p.Protocol != "responses" {
			return fmt.Errorf("provider %s: protocol must be chat or responses", p.ID)
		}
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("provider %s: invalid base URL", p.ID)
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && localHost(u.Hostname())) {
			return fmt.Errorf("provider %s: base URL must use HTTPS or local HTTP", p.ID)
		}
		if len(p.Models) == 0 {
			return fmt.Errorf("provider %s: at least one model is required", p.ID)
		}
		for _, model := range p.Models {
			if strings.TrimSpace(model) != model || model == "" {
				return fmt.Errorf("provider %s: invalid model %q", p.ID, model)
			}
		}
	}
	return nil
}

func localHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, cfg.Validate()
}

func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".modelrouter-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
