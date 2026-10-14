package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	ok := Default()
	tests := []struct {
		name    string
		mut     func(*Config)
		wantErr string
	}{
		{"defaults are valid", func(*Config) {}, ""},
		{"https admin", func(c *Config) { c.Admin = "https://envoy.local:9901" }, ""},
		{"admin without scheme", func(c *Config) { c.Admin = "localhost:15000" }, "--admin"},
		{"admin ftp", func(c *Config) { c.Admin = "ftp://x" }, "--admin"},
		{"admin empty", func(c *Config) { c.Admin = "" }, "--admin"},
		{"demo ignores admin", func(c *Config) { c.Demo = true; c.Admin = "garbage" }, ""},
		{"refresh at floor", func(c *Config) { c.Refresh = MinRefresh }, ""},
		{"refresh below floor", func(c *Config) { c.Refresh = 100 * time.Millisecond }, "--refresh"},
		{"timeout zero", func(c *Config) { c.Timeout = 0 }, "--timeout"},
		{"timeout longer than refresh is fine", func(c *Config) { c.Timeout = time.Minute }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ok
			tt.mut(&c)
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
