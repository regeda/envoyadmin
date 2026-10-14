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
	for _, args := range [][]string{
		{"--refresh", "100ms"},
		{"--timeout", "0s"},
		{"--admin", "nope"},
	} {
		if _, err := execRoot(args...); err == nil {
			t.Fatalf("args %v: expected validation error", args)
		}
	}
}
