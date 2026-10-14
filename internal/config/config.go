// Package config holds the validated runtime configuration. Every duration in
// the program comes from here; nothing else hardcodes a timeout.
package config

import (
	"fmt"
	"net/url"
	"time"
)

// MinRefresh is the floor for --refresh so a typo cannot hammer Envoy.
const MinRefresh = 500 * time.Millisecond

type Config struct {
	Admin   string
	Refresh time.Duration
	Timeout time.Duration
	Demo    bool
}

func Default() Config {
	return Config{
		Admin:   "http://localhost:15000",
		Refresh: 3 * time.Second,
		Timeout: 3 * time.Second,
	}
}

func (c Config) Validate() error {
	if c.Refresh < MinRefresh {
		return fmt.Errorf("--refresh must be at least %s, got %s", MinRefresh, c.Refresh)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("--timeout must be positive, got %s", c.Timeout)
	}
	if c.Demo {
		return nil
	}
	u, err := url.Parse(c.Admin)
	if err != nil {
		return fmt.Errorf("--admin: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("--admin must be an http(s) URL with a host, got %q", c.Admin)
	}
	return nil
}
