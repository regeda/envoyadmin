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

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | awk '{print $1}'
}

# make_release TAG OS ARCH: puts a release archive and checksums.txt in $FIXTURE_DIR.
# The archive holds an envoyadmin script that prints TAG for "version".
make_release() {
  _tag=$1 _os=$2 _arch=$3
  _ver=${_tag#v}
  _name=envoyadmin_${_ver}_${_os}_${_arch}.tar.gz
  _src=$T/release-src
  mkdir -p "$_src"
  # shellcheck disable=SC2016 # the fixture script must expand $1 itself
  printf '#!/bin/sh\n[ "$1" = version ] && echo %s\n' "$_tag" >"$_src/envoyadmin"
  chmod +x "$_src/envoyadmin"
  tar -czf "$FIXTURE_DIR/$_name" -C "$_src" envoyadmin
  printf '%s  %s\n' "$(sha256 "$FIXTURE_DIR/$_name")" "$_name" >"$FIXTURE_DIR/checksums.txt"
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

test_latest_resolves_tag() {
  new_case latest_resolves_tag
  make_release v1.2.3 linux amd64
  run_install
  assert_eq "status" 0 "$STATUS"
  log=$(cat "$FIXTURE_DIR/curl.log")
  assert_contains "archive url" "/download/v1.2.3/envoyadmin_1.2.3_linux_amd64.tar.gz" "$log"
  assert_contains "checksums url" "/download/v1.2.3/checksums.txt" "$log"
}

test_pinned_version() {
  new_case pinned_version
  make_release v0.9.0 linux amd64
  run_install VERSION=v0.9.0 FAKE_LATEST_URL=https://github.com/regeda/envoyadmin/releases/tag/v9.9.9
  assert_eq "status" 0 "$STATUS"
  log=$(cat "$FIXTURE_DIR/curl.log")
  assert_contains "archive url" "/download/v0.9.0/envoyadmin_0.9.0_linux_amd64.tar.gz" "$log"
  case $log in *releases/latest*) fail "pinned version must not ask for the latest release" ;; *) ok ;; esac
}

test_pinned_without_v() {
  new_case pinned_without_v
  make_release v0.9.0 linux amd64
  run_install VERSION=0.9.0
  assert_eq "status" 0 "$STATUS"
  assert_contains "archive url" "/download/v0.9.0/envoyadmin_0.9.0_linux_amd64.tar.gz" "$(cat "$FIXTURE_DIR/curl.log")"
}

test_no_stable_release() {
  new_case no_stable_release
  run_install FAKE_LATEST_URL=https://github.com/regeda/envoyadmin/releases
  assert_eq "status" 1 "$STATUS"
  assert_contains "output" "VERSION" "$OUT"
}

test_pinned_missing() {
  new_case pinned_missing
  run_install VERSION=v9.9.9
  assert_eq "status" 1 "$STATUS"
  assert_contains "output" "v9.9.9" "$OUT"
  if [ -e "$T/bin/envoyadmin" ]; then fail "nothing may be installed"; else ok; fi
}

test_checksum_mismatch() {
  new_case checksum_mismatch
  make_release v1.2.3 linux amd64
  printf 'tampered' >>"$FIXTURE_DIR/envoyadmin_1.2.3_linux_amd64.tar.gz"
  run_install
  assert_eq "status" 1 "$STATUS"
  assert_contains "output" "checksum" "$OUT"
  if [ -e "$T/bin/envoyadmin" ]; then fail "nothing may be installed"; else ok; fi
  assert_eq "temp dir cleaned" "" "$(ls "$T/tmp")"
}

test_checksum_line_missing() {
  new_case checksum_line_missing
  make_release v1.2.3 linux amd64
  printf '%s  %s\n' "$(sha256 "$FIXTURE_DIR/envoyadmin_1.2.3_linux_amd64.tar.gz")" other.tar.gz >"$FIXTURE_DIR/checksums.txt"
  run_install
  assert_eq "status" 1 "$STATUS"
  assert_contains "output" "checksum" "$OUT"
}

test_linux_amd64
test_darwin_arm64
test_linux_arm64_from_aarch64
test_unsupported_os
test_unsupported_arch
test_latest_resolves_tag
test_pinned_version
test_pinned_without_v
test_no_stable_release
test_pinned_missing
test_checksum_mismatch
test_checksum_line_missing

printf '%s passed, %s failed\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
