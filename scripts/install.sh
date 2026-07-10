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

fetch() {
  local url="$1" output="${2:-}"
  if check_command curl; then
    if [ -n "$output" ]; then
      curl -fsSL "$url" -o "$output"
    else
      curl -fsSL "$url"
    fi
  elif check_command wget; then
    if [ -n "$output" ]; then
      wget -qO "$output" "$url"
    else
      wget -qO- "$url"
    fi
  else
    error "curl or wget is required but neither is installed."
  fi
}

latest_tag() {
  if check_command gh; then
    gh release view --repo "$REPO" --json tagName --jq .tagName
  else
    fetch "https://api.github.com/repos/${REPO}/releases/latest" |
      grep '"tag_name"' | head -n1 | cut -d'"' -f4
  fi
}

main() {
  info "Installing $BINARY_NAME..."

  local os arch tag asset_name download_url install_path

  os="$(detect_os)"
  arch="$(detect_arch)"

  info "Detected platform: ${os}-${arch}"

  mkdir -p "$INSTALL_DIR"

  install_path="${INSTALL_DIR}/${BINARY_NAME}"

  tag="$(latest_tag)"
  [ -n "$tag" ] || error "Failed to resolve latest release tag from ${REPO}."

  info "Latest version: ${tag}"

  asset_name="${BINARY_NAME}-${tag}-${os}-${arch}"

  info "Downloading ${asset_name} from ${REPO}..."
  if check_command gh; then
    if ! gh release download "$tag" --repo "$REPO" --pattern "$asset_name" --output "$install_path" --clobber; then
      error "Failed to download binary. Please check if the release exists and you have access."
    fi
  else
    download_url="https://github.com/${REPO}/releases/download/${tag}/${asset_name}"
    if ! fetch "$download_url" "$install_path"; then
      error "Failed to download binary. Please check if the release exists and you have access."
    fi
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
