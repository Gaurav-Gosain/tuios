#!/usr/bin/env bash

# tuios-web Installation Script
# Usage: curl -fsSL https://raw.githubusercontent.com/Gaurav-Gosain/tuios/main/install-web.sh | bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# GitHub repository
REPO="Gaurav-Gosain/tuios"
BINARY_NAME="tuios-web"

# Print colored output
print_info() {
    echo -e "${BLUE}i${NC} $1"
}

print_success() {
    echo -e "${GREEN}+${NC} $1"
}

print_error() {
    echo -e "${RED}x${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}!${NC} $1"
}

# Detect OS
detect_os() {
    case "$(uname -s)" in
        Linux*)     OS="Linux";;
        Darwin*)    OS="Darwin";;
        CYGWIN*|MINGW*|MSYS*) OS="Windows";;
        FreeBSD*)   OS="FreeBSD";;
        OpenBSD*)   OS="OpenBSD";;
        *)          OS="UNKNOWN";;
    esac
    echo "$OS"
}

# Detect architecture
detect_arch() {
    ARCH=$(uname -m)
    case $ARCH in
        x86_64)  echo "x86_64";;
        amd64)   echo "x86_64";;
        arm64)   echo "arm64";;
        aarch64) echo "arm64";;
        armv7l)  echo "armv7";;
        armv6l)  echo "armv6";;
        i386|i686) echo "i386";;
        *)       echo "unknown";;
    esac
}

# Get latest release version from GitHub API
get_latest_version() {
    if command -v curl &> /dev/null; then
        VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    elif command -v wget &> /dev/null; then
        VERSION=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    else
        print_error "Neither curl nor wget found. Please install one of them."
        exit 1
    fi

    if [ -z "$VERSION" ]; then
        print_error "Failed to get latest version from GitHub"
        exit 1
    fi

    echo "$VERSION"
}

# Download file
download_file() {
    URL=$1
    OUTPUT=$2

    if command -v curl &> /dev/null; then
        curl -fsSL "$URL" -o "$OUTPUT"
    elif command -v wget &> /dev/null; then
        wget -qO "$OUTPUT" "$URL"
    else
        print_error "Neither curl nor wget found"
        exit 1
    fi
}

# Main installation
main() {
    print_info "Installing tuios-web (Web Terminal Server)..."
    echo ""

    # Detect system
    OS=$(detect_os)
    ARCH=$(detect_arch)

    print_info "Detected OS: $OS"
    print_info "Detected Architecture: $ARCH"

    # Check if OS/arch is supported
    if [ "$OS" = "UNKNOWN" ] || [ "$ARCH" = "unknown" ]; then
        print_error "Unsupported OS or architecture: $OS/$ARCH"
        exit 1
    fi

    if [ "$OS" = "Windows" ]; then
        print_error "Windows is not supported by this script. Please download the binary manually from:"
        print_info "https://github.com/${REPO}/releases/latest"
        exit 1
    fi

    # Get latest version
    print_info "Fetching latest release..."
    VERSION=$(get_latest_version)
    print_success "Latest version: $VERSION"

    # Construct download URL
    # Format: tuios-web_0.4.0_Linux_x86_64.tar.gz
    VERSION_NO_V="${VERSION#v}"  # Remove leading 'v' from version

    if [ "$OS" = "Darwin" ]; then
        OS_FORMATTED="Darwin"
    else
        OS_FORMATTED="$OS"
    fi

    ARCHIVE_NAME="${BINARY_NAME}_${VERSION_NO_V}_${OS_FORMATTED}_${ARCH}.tar.gz"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE_NAME}"

    # Create temporary directory
    TMP_DIR=$(mktemp -d)
    trap 'rm -rf -- $TMP_DIR' EXIT

    print_info "Downloading $ARCHIVE_NAME..."
    if ! download_file "$DOWNLOAD_URL" "$TMP_DIR/$ARCHIVE_NAME"; then
        print_error "Failed to download release"
        print_info "URL: $DOWNLOAD_URL"
        exit 1
    fi
    print_success "Downloaded successfully"

    # Extract archive
    print_info "Extracting archive..."
    tar -xzf "$TMP_DIR/$ARCHIVE_NAME" -C "$TMP_DIR"
    print_success "Extracted successfully"

    # Determine installation directory
    if [ -w "/usr/local/bin" ]; then
        INSTALL_DIR="/usr/local/bin"
    elif [ -w "$HOME/.local/bin" ]; then
        INSTALL_DIR="$HOME/.local/bin"
        mkdir -p "$INSTALL_DIR"
    else
        INSTALL_DIR="$HOME/bin"
        mkdir -p "$INSTALL_DIR"
    fi

    # Install binary
    print_info "Installing to $INSTALL_DIR..."

    if [ -w "$INSTALL_DIR" ]; then
        mv "$TMP_DIR/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
        chmod +x "$INSTALL_DIR/$BINARY_NAME"
    else
        print_info "Need sudo permissions to install to $INSTALL_DIR"
        sudo mv "$TMP_DIR/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
        sudo chmod +x "$INSTALL_DIR/$BINARY_NAME"
    fi

    print_success "Installed $BINARY_NAME to $INSTALL_DIR"

    # Check if directory is in PATH
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        print_warning "$INSTALL_DIR is not in your PATH"
        echo ""
        print_info "Add it to your PATH by adding this line to your shell config:"
        print_info "  export PATH=\"\$PATH:$INSTALL_DIR\""
        echo ""
    fi

    # Verify installation
    if command -v "$BINARY_NAME" &> /dev/null; then
        print_success "Installation complete!"
        echo ""
        print_info "Run '$BINARY_NAME --version' to verify"
        echo ""
        "$BINARY_NAME" --version
    else
        print_success "Binary installed at $INSTALL_DIR/$BINARY_NAME"
        print_info "You may need to restart your shell or run:"
        print_info "  source ~/.bashrc  # or ~/.zshrc, etc."
    fi

    echo ""
    print_info "To start the web server, run: $BINARY_NAME"
    print_info "Then open http://localhost:7681 in your browser"
    print_info "For help, run: $BINARY_NAME --help"
}

# Run main installation
main
