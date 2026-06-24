#!/usr/bin/env bash
set -euo pipefail

REPO="MunifTanjim/argus"
BINARY_NAME="argus"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

info() {
  printf "\033[0;34m%s\033[0m\n" "$1"
}

success() {
  printf "\033[0;32m%s\033[0m\n" "$1"
}

error() {
  printf "\033[0;31mError: %s\033[0m\n" "$1" >&2
  exit 1
}

detect_os() {
  local os
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
  linux) echo "linux" ;;
  darwin) echo "darwin" ;;
  *) error "Unsupported operating system: $os" ;;
  esac
}

detect_arch() {
  local arch
  arch="$(uname -m)"
  case "$arch" in
  x86_64 | amd64) echo "amd64" ;;
  arm64 | aarch64) echo "arm64" ;;
  *) error "Unsupported architecture: $arch" ;;
  esac
}

check_command() {
  command -v "$1" >/dev/null 2>&1
}

main() {
  info "Installing $BINARY_NAME..."

  local os arch asset_name install_path

  os="$(detect_os)"
  arch="$(detect_arch)"

  info "Detected platform: ${os}-${arch}"

  if ! check_command gh; then
    error "gh (GitHub CLI) is required but not installed. Install it from https://cli.github.com"
  fi

  mkdir -p "$INSTALL_DIR"

  asset_name="${BINARY_NAME}-*-${os}-${arch}"
  install_path="${INSTALL_DIR}/${BINARY_NAME}"

  info "Downloading ${asset_name} from ${REPO}..."
  if ! gh release download --repo "$REPO" --pattern "$asset_name" --output "$install_path" --clobber; then
    error "Failed to download binary. Please check if the release exists and you have access."
  fi

  chmod +x "$install_path"

  if ! "$install_path" --version >/dev/null 2>&1; then
    error "Installation verification failed"
  fi

  success "Successfully installed $BINARY_NAME to $install_path"

  if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    cat <<EOF

Note: \$INSTALL_DIR is not in your PATH.
Add it to your shell configuration:

  export PATH="\$PATH:$INSTALL_DIR"

EOF
  fi

  case "${SHELL:-$(ps -o comm= -p $PPID)}" in
  *zsh)
    cat <<EOF

To enable zsh completions, run the following command:

  ${BINARY_NAME} completion install
  # or
  ${BINARY_NAME} completion zsh > "\${fpath[1]}/_${BINARY_NAME}"

EOF
    ;;
  esac

}

main
