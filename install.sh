#!/bin/sh
# Installs the envoyadmin release binary for this machine.
# Usage: curl -fsSL https://raw.githubusercontent.com/regeda/envoyadmin/main/install.sh | sh
#   VERSION      release tag to install (default: the latest release)
#   INSTALL_DIR  target folder (default: /usr/local/bin)
set -eu

detect_os() {
  uname_s=$(uname -s)
  case $uname_s in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    *)
      echo "install.sh: unsupported OS: $uname_s" >&2
      return 1
      ;;
  esac
}

detect_arch() {
  uname_m=$(uname -m)
  case $uname_m in
    x86_64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *)
      echo "install.sh: unsupported CPU architecture: $uname_m" >&2
      return 1
      ;;
  esac
}

REPO=regeda/envoyadmin
TMPDIR_INSTALL=

fetch_url_effective() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSLI -o /dev/null -w '%{url_effective}' "$1"
  else
    wget --max-redirect=0 -S -O /dev/null "$1" 2>&1 | awk 'tolower($1) == "location:" { print $2 }' | tail -n 1
  fi
}

download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$2" "$1"
  else
    wget -q -O "$2" "$1"
  fi
}

resolve_version() {
  if [ -n "${VERSION:-}" ]; then
    case $VERSION in
      v*) echo "$VERSION" ;;
      *) echo "v$VERSION" ;;
    esac
    return 0
  fi
  latest_url=$(fetch_url_effective "https://github.com/$REPO/releases/latest") || latest_url=
  case $latest_url in
    */releases/tag/*) echo "${latest_url##*/}" ;;
    *)
      echo "install.sh: could not find a stable release; set VERSION, for example VERSION=v0.1.0" >&2
      return 1
      ;;
  esac
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
  else
    shasum -a 256 "$1" | awk '{ print $1 }'
  fi
}

verify_checksum() {
  vc_file=$(basename "$1")
  vc_want=$(awk -v f="$vc_file" '$2 == f { print $1 }' "$2")
  if [ -z "$vc_want" ]; then
    echo "install.sh: checksum for $vc_file is missing from checksums.txt" >&2
    return 1
  fi
  vc_got=$(sha256_of "$1")
  if [ "$vc_want" != "$vc_got" ]; then
    echo "install.sh: checksum mismatch for $vc_file (want $vc_want, got $vc_got)" >&2
    return 1
  fi
}

cleanup() {
  if [ -n "$TMPDIR_INSTALL" ]; then
    rm -rf "$TMPDIR_INSTALL"
  fi
}

# use_sudo is 1 when the install needs root: the folder, or the nearest
# existing parent when it has to be created, is not writable.
needs_sudo() {
  ns_dir=$1
  while [ ! -d "$ns_dir" ]; do
    ns_dir=$(dirname "$ns_dir")
  done
  if [ -w "$ns_dir" ]; then
    use_sudo=0
  else
    use_sudo=1
  fi
}

as_root() {
  if [ "$use_sudo" = 1 ]; then
    sudo "$@"
  else
    "$@"
  fi
}

install_binary() {
  ib_src=$1
  ib_dir=$2
  needs_sudo "$ib_dir"
  if [ "$use_sudo" = 1 ] && ! command -v sudo >/dev/null 2>&1; then
    echo "install.sh: $ib_dir is not writable and sudo is not available; set INSTALL_DIR to a writable folder" >&2
    return 1
  fi
  if ! as_root mkdir -p "$ib_dir" || ! as_root install -m 0755 "$ib_src" "$ib_dir/envoyadmin"; then
    echo "install.sh: could not install into $ib_dir; set INSTALL_DIR to a writable folder" >&2
    return 1
  fi
}

main() {
  os=$(detect_os)
  arch=$(detect_arch)
  tag=$(resolve_version)
  version=${tag#v}
  archive=envoyadmin_${version}_${os}_${arch}.tar.gz
  base=https://github.com/$REPO/releases/download/$tag

  TMPDIR_INSTALL=$(mktemp -d)
  trap cleanup EXIT

  for file in "$archive" checksums.txt; do
    if ! download "$base/$file" "$TMPDIR_INSTALL/$file"; then
      echo "install.sh: failed to download $base/$file; does release $tag exist?" >&2
      return 1
    fi
  done
  verify_checksum "$TMPDIR_INSTALL/$archive" "$TMPDIR_INSTALL/checksums.txt"
  tar -xzf "$TMPDIR_INSTALL/$archive" -C "$TMPDIR_INSTALL" envoyadmin

  dir=${INSTALL_DIR:-/usr/local/bin}
  install_binary "$TMPDIR_INSTALL/envoyadmin" "$dir"
  installed=$("$dir/envoyadmin" version) || installed=unknown
  echo "installed $dir/envoyadmin ($installed)"
}

main "$@"
