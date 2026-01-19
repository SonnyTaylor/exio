#!/bin/sh
# Exio installer script
# Usage: curl -fsSL https://raw.githubusercontent.com/SonnyTaylor/exio/main/install.sh | sh

set -e

REPO="SonnyTaylor/exio"
BINARY_NAME="exio"
INSTALL_DIR="/usr/local/bin"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

info() {
    printf "${BLUE}==>${NC} %s\n" "$1"
}

success() {
    printf "${GREEN}==>${NC} %s\n" "$1"
}

warn() {
    printf "${YELLOW}Warning:${NC} %s\n" "$1"
}

error() {
    printf "${RED}Error:${NC} %s\n" "$1" >&2
    exit 1
}

# Detect OS
detect_os() {
    OS="$(uname -s)"
    case "$OS" in
        Linux*)     OS="linux" ;;
        Darwin*)    OS="darwin" ;;
        MINGW*|MSYS*|CYGWIN*)
            error "Please use install.ps1 for Windows"
            ;;
        *)
            error "Unsupported operating system: $OS"
            ;;
    esac
    echo "$OS"
}

# Detect architecture
detect_arch() {
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)   ARCH="amd64" ;;
        aarch64|arm64)  ARCH="arm64" ;;
        *)
            error "Unsupported architecture: $ARCH"
            ;;
    esac
    echo "$ARCH"
}

# Get latest version from GitHub
get_latest_version() {
    if command -v curl >/dev/null 2>&1; then
        curl -sL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/'
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/'
    else
        error "Neither curl nor wget found. Please install one of them."
    fi
}

# Download file
download() {
    URL="$1"
    OUTPUT="$2"
    
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$URL" -o "$OUTPUT"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$URL" -O "$OUTPUT"
    else
        error "Neither curl nor wget found. Please install one of them."
    fi
}

# Verify checksum
verify_checksum() {
    FILE="$1"
    EXPECTED="$2"
    
    if command -v sha256sum >/dev/null 2>&1; then
        ACTUAL=$(sha256sum "$FILE" | cut -d' ' -f1)
    elif command -v shasum >/dev/null 2>&1; then
        ACTUAL=$(shasum -a 256 "$FILE" | cut -d' ' -f1)
    else
        warn "sha256sum not found, skipping checksum verification"
        return 0
    fi
    
    if [ "$ACTUAL" != "$EXPECTED" ]; then
        error "Checksum verification failed!\nExpected: $EXPECTED\nActual: $ACTUAL"
    fi
}

# Main installation
main() {
    echo ""
    echo "  ╭───────────────────────────────────╮"
    echo "  │       Exio Installer              │"
    echo "  │   High-performance tunneling      │"
    echo "  ╰───────────────────────────────────╯"
    echo ""

    # Detect platform
    OS=$(detect_os)
    ARCH=$(detect_arch)
    info "Detected platform: ${OS}/${ARCH}"

    # Get latest version
    info "Fetching latest version..."
    VERSION=$(get_latest_version)
    
    if [ -z "$VERSION" ]; then
        error "Could not determine latest version. Check your internet connection."
    fi
    
    info "Latest version: ${VERSION}"

    # Construct download URL
    BINARY="${BINARY_NAME}-${OS}-${ARCH}"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}.tar.gz"
    CHECKSUM_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

    # Create temp directory
    TMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TMP_DIR"' EXIT

    # Download binary
    info "Downloading ${BINARY}.tar.gz..."
    download "$DOWNLOAD_URL" "${TMP_DIR}/${BINARY}.tar.gz"

    # Download and verify checksum
    info "Verifying checksum..."
    download "$CHECKSUM_URL" "${TMP_DIR}/checksums.txt"
    EXPECTED_CHECKSUM=$(grep "${BINARY}.tar.gz" "${TMP_DIR}/checksums.txt" | cut -d' ' -f1 || echo "")
    
    if [ -n "$EXPECTED_CHECKSUM" ]; then
        verify_checksum "${TMP_DIR}/${BINARY}.tar.gz" "$EXPECTED_CHECKSUM"
        success "Checksum verified"
    fi

    # Extract
    info "Extracting..."
    tar -xzf "${TMP_DIR}/${BINARY}.tar.gz" -C "$TMP_DIR"

    # Determine install location
    if [ -w "$INSTALL_DIR" ]; then
        FINAL_INSTALL_DIR="$INSTALL_DIR"
    elif [ -w "$HOME/.local/bin" ]; then
        FINAL_INSTALL_DIR="$HOME/.local/bin"
        mkdir -p "$FINAL_INSTALL_DIR"
        warn "Installing to ~/.local/bin (no write access to /usr/local/bin)"
        warn "Make sure ~/.local/bin is in your PATH"
    else
        # Try with sudo
        info "Requesting sudo access to install to ${INSTALL_DIR}..."
        FINAL_INSTALL_DIR="$INSTALL_DIR"
        SUDO="sudo"
    fi

    # Install
    info "Installing to ${FINAL_INSTALL_DIR}..."
    ${SUDO:-} mv "${TMP_DIR}/${BINARY}" "${FINAL_INSTALL_DIR}/${BINARY_NAME}"
    ${SUDO:-} chmod +x "${FINAL_INSTALL_DIR}/${BINARY_NAME}"

    # Verify installation
    if command -v exio >/dev/null 2>&1; then
        INSTALLED_VERSION=$(exio version 2>/dev/null || echo "unknown")
        success "Exio installed successfully!"
        echo ""
        echo "  Version: ${INSTALLED_VERSION}"
        echo "  Location: ${FINAL_INSTALL_DIR}/${BINARY_NAME}"
        echo ""
        echo "  Get started:"
        echo "    exio init              # Configure your connection"
        echo "    exio http 3000         # Expose port 3000"
        echo ""
    else
        success "Exio installed to ${FINAL_INSTALL_DIR}/${BINARY_NAME}"
        warn "Make sure ${FINAL_INSTALL_DIR} is in your PATH"
    fi
}

main "$@"
