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
    printf "${BLUE}i${NC} %s\n" "$1"
}

print_success() {
    printf "${GREEN}+${NC} %s\n" "$1"
}

print_error() {
    printf "${RED}x${NC} %s\n" "$1"
}

print_warning() {
    printf "${YELLOW}!${NC} %s\n" "$1"
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

# PowerShell and cmd run "bash" as WSL's bash.exe, which reports Linux, so a
# Windows user would get the Linux build installed into their WSL distro. Run
# from a shell inside WSL, bash's parent is that shell; run from Windows, it is
# the WSL launcher (init on WSL 1, Relay or SessionLeader on WSL 2).
launched_from_windows() {
    grep -qi microsoft /proc/sys/kernel/osrelease 2>/dev/null || return 1
    case "$(cat "/proc/$PPID/comm" 2>/dev/null)" in
        init|Relay*|SessionLeader) return 0;;
        *) return 1;;
    esac
}

# Install the Windows build for this user: the .exe goes in
# %LOCALAPPDATA%\Programs\tuios, and that folder goes on the user PATH, which
# every terminal opened afterwards picks up. Runs from Git Bash, MSYS2, Cygwin,
# or WSL started from Windows, all of which can call powershell.exe.
install_windows() {
    if ! command -v powershell.exe &> /dev/null; then
        print_error "powershell.exe not found, so the Windows install cannot continue"
        print_info "Download the Windows archive from https://github.com/${REPO}/releases/latest"
        exit 1
    fi

    LOCAL_APPDATA=$(powershell.exe -NoProfile -NonInteractive -Command "[Environment]::GetFolderPath('LocalApplicationData')" < /dev/null | tr -d '\r')
    WIN_DIR="${LOCAL_APPDATA}\\Programs\\tuios"
    if command -v wslpath &> /dev/null; then
        INSTALL_DIR=$(wslpath -u "$WIN_DIR")
    else
        INSTALL_DIR=$(cygpath -u "$WIN_DIR")
    fi

    print_info "Installing to $WIN_DIR..."
    mkdir -p "$INSTALL_DIR"
    mv -f "$TMP_DIR/$BINARY_NAME.exe" "$INSTALL_DIR/$BINARY_NAME.exe"
    print_success "Installed $BINARY_NAME.exe to $WIN_DIR"

    # The user PATH is read and written in the registry as it is stored, so
    # entries such as %USERPROFILE%\bin stay unexpanded. Setting a variable
    # through [Environment] afterwards tells open programs (Explorer, Windows
    # Terminal) that the environment changed.
    PS_SCRIPT='
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment", $true)
        $path = $key.GetValue("Path", "", "DoNotExpandEnvironmentNames")
        $entries = @($path -split ";" | Where-Object { $_ })
        if ($entries -notcontains $dir) {
            $key.SetValue("Path", (($entries + $dir) -join ";"), "ExpandString")
            [Environment]::SetEnvironmentVariable("TUIOS_INSTALL", "1", "User")
            [Environment]::SetEnvironmentVariable("TUIOS_INSTALL", $null, "User")
            "added"
        }'
    ADDED=$(powershell.exe -NoProfile -NonInteractive -Command "\$dir = '${WIN_DIR//\'/\'\'}'; $PS_SCRIPT" < /dev/null | tr -d '\r')
    if [ "$ADDED" = "added" ]; then
        print_success "Added $WIN_DIR to your user PATH"
        print_info "Open a new terminal for the PATH change to take effect"
    fi

    print_success "Installation complete!"
    echo ""
    "$INSTALL_DIR/$BINARY_NAME.exe" --version < /dev/null
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

# Print the sha256 of a file, with whichever tool this system has.
sha256_of() {
    if command -v sha256sum &> /dev/null; then
        sha256sum "$1" | cut -d' ' -f1
    elif command -v shasum &> /dev/null; then
        shasum -a 256 "$1" | cut -d' ' -f1
    elif command -v sha256 &> /dev/null; then
        sha256 -q "$1"
    else
        return 1
    fi
}

# Check the archive against the release's checksums.txt. A release without the
# file, or an archive it does not list, stops the install: the archive is
# about to be run, and an unchecked download is the one thing not to run.
verify_checksum() {
    ARCHIVE=$1
    NAME=$2
    SUMS="$TMP_DIR/checksums.txt"

    if ! download_file "https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt" "$SUMS"; then
        print_error "Failed to download checksums.txt for ${VERSION}"
        exit 1
    fi

    EXPECTED=$(awk -v name="$NAME" '$2 == name { print $1 }' "$SUMS")
    if [ -z "$EXPECTED" ]; then
        print_error "checksums.txt does not list $NAME"
        exit 1
    fi

    if ! ACTUAL=$(sha256_of "$ARCHIVE"); then
        print_warning "No sha256sum, shasum or sha256 found, so the download was not verified"
        return 0
    fi

    if [ "$EXPECTED" != "$ACTUAL" ]; then
        print_error "Checksum mismatch for $NAME"
        print_info "Expected: $EXPECTED"
        print_info "Got:      $ACTUAL"
        exit 1
    fi
    print_success "Checksum verified"
}

# Main installation
main() {
    print_info "Installing tuios-web (Web Terminal Server)..."
    echo ""

    # Detect system
    OS=$(detect_os)
    ARCH=$(detect_arch)

    if [ "$OS" = "Linux" ] && launched_from_windows; then
        OS="Windows"
        print_info "This bash is WSL, started from Windows, so the Windows build is installed."
        print_info "To install into WSL instead, run this command from a WSL shell."
    fi

    print_info "Detected OS: $OS"
    print_info "Detected Architecture: $ARCH"

    # Check if OS/arch is supported
    if [ "$OS" = "UNKNOWN" ] || [ "$ARCH" = "unknown" ]; then
        print_error "Unsupported OS or architecture: $OS/$ARCH"
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

    verify_checksum "$TMP_DIR/$ARCHIVE_NAME" "$ARCHIVE_NAME"

    # Extract archive
    print_info "Extracting archive..."
    tar -xzf "$TMP_DIR/$ARCHIVE_NAME" -C "$TMP_DIR"
    print_success "Extracted successfully"

    if [ "$OS" = "Windows" ]; then
        install_windows
    else
        install_unix
    fi

    echo ""
    print_info "To start the web server, run: $BINARY_NAME"
    print_info "Then open http://localhost:7681 in your browser"
    print_info "For help, run: $BINARY_NAME --help"
}

install_unix() {
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
}

# Run main installation
main
