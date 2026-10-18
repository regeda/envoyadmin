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

main() {
  os=$(detect_os)
  arch=$(detect_arch)
  echo "install.sh: detected ${os}_${arch}"
}

main "$@"
