package main

import (
	"bytes"
	"strings"
	"testing"
)

func execRoot(args ...string) (string, error) {
	cmd := newRootCmd("v-test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestVersion(t *testing.T) {
	out, err := execRoot("version")
	if err != nil || strings.TrimSpace(out) != "v-test" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestInvalidFlagsFailBeforeRun(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"--refresh", "100ms"}, "--refresh"},
		{[]string{"--timeout", "0s"}, "--timeout"},
		{[]string{"--admin", "nope"}, "--admin"},
	}
	for _, tt := range tests {
		_, err := execRoot(tt.args...)
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Fatalf("args %v: got %v, want error containing %q", tt.args, err, tt.wantErr)
		}
	}
}
