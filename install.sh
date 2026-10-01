#!/bin/sh
set -e

REPO="kelvinzer0/zgo"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux*)  TARGET_OS="linux" ;;
  darwin*) TARGET_OS="darwin" ;;
  *)       echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect Architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) TARGET_ARCH="amd64" ;;
  arm64|aarch64) TARGET_ARCH="arm64" ;;
  *)            echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

BINARY_NAME="zgo-${TARGET_OS}-${TARGET_ARCH}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${BINARY_NAME}"

echo "🚀 Installing ZGo for ${TARGET_OS}/${TARGET_ARCH}..."

mkdir -p "$INSTALL_DIR"
TARGET_PATH="${INSTALL_DIR}/zgo"

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$DOWNLOAD_URL" -o "$TARGET_PATH"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$TARGET_PATH" "$DOWNLOAD_URL"
else
  echo "Error: curl or wget is required to install ZGo."
  exit 1
fi

chmod +x "$TARGET_PATH"

echo "✨ ZGo installed successfully to: $TARGET_PATH"

# PATH warning
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo ""
    echo "⚠️  Note: $INSTALL_DIR is not in your \$PATH."
    echo "   Add it by running:"
    echo "   export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac
