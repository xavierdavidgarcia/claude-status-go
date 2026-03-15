#!/bin/bash
set -euo pipefail

REPO="xgarcia/claude-status-go"
BINARY="claude-status-go"
INSTALL_DIR="${HOME}/.claude"
SETTINGS="${INSTALL_DIR}/settings.json"

blue='\033[38;2;0;153;255m'
green='\033[38;2;0;175;80m'
yellow='\033[38;2;230;200;0m'
red='\033[38;2;255;85;85m'
dim='\033[2m'
reset='\033[0m'

info()    { echo -e "  ${blue}i${reset} $1"; }
success() { echo -e "  ${green}✓${reset} $1"; }
warn()    { echo -e "  ${yellow}!${reset} $1"; }
fail()    { echo -e "  ${red}✗${reset} $1"; exit 1; }

# ── Detect platform ──────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) fail "Unsupported architecture: $ARCH" ;;
esac

case "$OS" in
    darwin|linux) ;;
    *) fail "Unsupported OS: $OS" ;;
esac

ASSET_PATTERN="${BINARY}_*_${OS}_${ARCH}.tar.gz"

echo ""
echo -e "  ${blue}Claude Statusline Installer${reset}"
echo -e "  ${dim}──────────────────────────${reset}"
echo ""
info "Platform: ${OS}/${ARCH}"

# ── Check for gh CLI ─────────────────────────────────
if command -v gh >/dev/null 2>&1; then
    info "Downloading latest release via gh..."
    TMPDIR=$(mktemp -d)
    trap 'rm -rf "$TMPDIR"' EXIT

    gh release download --repo "$REPO" --pattern "$ASSET_PATTERN" --dir "$TMPDIR" --clobber
    tar -xzf "$TMPDIR"/*.tar.gz -C "$TMPDIR"
else
    # Fallback to curl
    info "Downloading latest release via curl..."
    LATEST_TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$LATEST_TAG" ]; then
        fail "Could not determine latest release"
    fi

    TMPDIR=$(mktemp -d)
    trap 'rm -rf "$TMPDIR"' EXIT

    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}/${BINARY}_${LATEST_TAG#v}_${OS}_${ARCH}.tar.gz"
    curl -fsSL "$DOWNLOAD_URL" -o "$TMPDIR/archive.tar.gz"
    tar -xzf "$TMPDIR/archive.tar.gz" -C "$TMPDIR"
fi

# ── Install binary ───────────────────────────────────
mkdir -p "$INSTALL_DIR"

if [ -f "${INSTALL_DIR}/${BINARY}" ]; then
    cp "${INSTALL_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}.bak"
    warn "Backed up existing binary to ${dim}${BINARY}.bak${reset}"
fi

cp "$TMPDIR/$BINARY" "${INSTALL_DIR}/${BINARY}"
chmod 755 "${INSTALL_DIR}/${BINARY}"
success "Installed binary to ${dim}${INSTALL_DIR}/${BINARY}${reset}"

# ── Update settings.json ────────────────────────────
STATUS_CMD="\$HOME/.claude/${BINARY}"

if command -v jq >/dev/null 2>&1; then
    if [ -f "$SETTINGS" ]; then
        tmp=$(mktemp)
        jq --arg cmd "$STATUS_CMD" '.statusLine = {"type": "command", "command": $cmd}' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
    else
        echo "{}" | jq --arg cmd "$STATUS_CMD" '.statusLine = {"type": "command", "command": $cmd}' > "$SETTINGS"
    fi
    success "Updated ${dim}settings.json${reset} with statusLine config"
else
    warn "jq not found — update ${dim}${SETTINGS}${reset} manually:"
    echo -e "    ${dim}{\"statusLine\":{\"type\":\"command\",\"command\":\"${STATUS_CMD}\"}}${reset}"
fi

echo ""
echo -e "  ${green}Done!${reset} Restart Claude Code to see your new status line."
echo -e "  ${dim}Run '${INSTALL_DIR}/${BINARY} --version' to verify.${reset}"
echo ""
