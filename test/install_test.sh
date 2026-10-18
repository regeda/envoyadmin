#!/bin/sh
# Tests for install.sh. Runs the real script with curl, uname and sudo replaced
# by the stubs in test/stubs, against a fixture release in a temp directory.
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
FAILED=0
PASSED=0

new_case() {
  CASE=$1
  T=$(mktemp -d)
  FIXTURE_DIR=$T/fixtures
  mkdir -p "$FIXTURE_DIR" "$T/tmp"
  : >"$FIXTURE_DIR/curl.log"
  : >"$FIXTURE_DIR/sudo.log"
}

# run_install [VAR=value ...]: runs install.sh with the stubs first on PATH.
# Sets STATUS and OUT (stdout and stderr together).
run_install() {
  OUT=$(env \
    PATH="$ROOT/test/stubs:$PATH" \
    FIXTURE_DIR="$FIXTURE_DIR" \
    FAKE_OS=Linux \
    FAKE_ARCH=x86_64 \
    FAKE_LATEST_URL=https://github.com/regeda/envoyadmin/releases/tag/v1.2.3 \
    INSTALL_DIR="$T/bin" \
    TMPDIR="$T/tmp" \
    "$@" \
    sh "$ROOT/install.sh" 2>&1)
  STATUS=$?
}

ok() { PASSED=$((PASSED + 1)); }
fail() {
  FAILED=$((FAILED + 1))
  printf 'FAIL %s: %s\n' "$CASE" "$1"
}

assert_eq() {
  if [ "$2" = "$3" ]; then ok; else fail "$1: want '$2', got '$3'"; fi
}

assert_contains() {
  case $3 in
    *"$2"*) ok ;;
    *) fail "$1: '$2' not found in: $3" ;;
  esac
}

test_linux_amd64() {
  new_case linux_amd64
  run_install FAKE_OS=Linux FAKE_ARCH=x86_64
  assert_contains "curl log" "_linux_amd64.tar.gz" "$(cat "$FIXTURE_DIR/curl.log")"
}

test_darwin_arm64() {
  new_case darwin_arm64
  run_install FAKE_OS=Darwin FAKE_ARCH=arm64
  assert_contains "curl log" "_darwin_arm64.tar.gz" "$(cat "$FIXTURE_DIR/curl.log")"
}

test_linux_arm64_from_aarch64() {
  new_case linux_arm64_from_aarch64
  run_install FAKE_OS=Linux FAKE_ARCH=aarch64
  assert_contains "curl log" "_linux_arm64.tar.gz" "$(cat "$FIXTURE_DIR/curl.log")"
}

test_unsupported_os() {
  new_case unsupported_os
  run_install FAKE_OS=FreeBSD
  assert_eq "status" 1 "$STATUS"
  assert_contains "output" "FreeBSD" "$OUT"
}

test_unsupported_arch() {
  new_case unsupported_arch
  run_install FAKE_ARCH=riscv64
  assert_eq "status" 1 "$STATUS"
  assert_contains "output" "riscv64" "$OUT"
}

test_linux_amd64
test_darwin_arm64
test_linux_arm64_from_aarch64
test_unsupported_os
test_unsupported_arch

printf '%s passed, %s failed\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
