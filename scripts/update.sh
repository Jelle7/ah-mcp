#!/usr/bin/env bash
# update.sh — Download and install the latest ah-mcp release from GitHub.
#
# Usage:
#   sudo ./update.sh
#
# Requirements:
#   - Store your GitHub PAT in /home/ah-mcp/.github_token (chmod 600)
#     Token needs: repo (classic) OR contents:read (fine-grained)
#   - Run as root (needed to write to /usr/local/bin and /etc/systemd)

set -euo pipefail

REPO="mrserzhan/ah-mcp"
ARCH="linux-amd64"          # change to linux-arm64 if needed
INSTALL_PATH="/usr/local/bin/ah-mcp"
SERVICE_NAME="ah-mcp"
SERVICE_PATH="/etc/systemd/system/ah-mcp.service"
TOKEN_FILE="/home/ah-mcp/.github_token"

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

# ── Auth ──────────────────────────────────────────────────────────────────────
if [[ ! -f "$TOKEN_FILE" ]]; then
  echo "ERROR: GitHub token not found at $TOKEN_FILE"
  echo "  Create it with: echo 'ghp_...' > $TOKEN_FILE && chmod 600 $TOKEN_FILE"
  exit 1
fi
GITHUB_TOKEN=$(cat "$TOKEN_FILE")

# The token is passed through a curl config on stdin rather than -H, so it
# never shows up in the process list for other users on the box.
gh_curl() {
  local accept="$1"; shift
  curl -fsSL -K - "$@" <<CURLRC
header = "Authorization: token ${GITHUB_TOKEN}"
header = "Accept: ${accept}"
CURLRC
}

gh_api() { gh_curl "application/vnd.github+json" "$@"; }

# asset_id NAME — resolve a release asset's numeric id from the cached metadata.
asset_id() {
  python3 -c "
import sys, json
name = sys.argv[1]
for a in json.load(sys.stdin).get('assets', []):
    if a['name'] == name:
        print(a['id'])
        break
" "$1" <<<"$RELEASE_JSON"
}

# download_asset NAME DEST — fetch a release asset by name.
download_asset() {
  local name="$1" dest="$2" id
  id=$(asset_id "$name")
  if [[ -z "$id" ]]; then
    return 1
  fi
  gh_curl "application/octet-stream" -o "$dest" \
    "https://api.github.com/repos/$REPO/releases/assets/$id"
}

# ── Latest release tag ────────────────────────────────────────────────────────
echo "Fetching latest release tag..."
LATEST=$(gh_api "https://api.github.com/repos/$REPO/releases/latest" \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('tag_name',''))")

if [[ -z "$LATEST" ]]; then
  echo "ERROR: Could not fetch latest release from GitHub API"
  exit 1
fi
echo "Latest: $LATEST"

# ── Current version check ─────────────────────────────────────────────────────
if [[ -x "$INSTALL_PATH" ]]; then
  CURRENT=$("$INSTALL_PATH" --version 2>/dev/null | awk '{print $2}' || echo "unknown")
  if [[ "$CURRENT" == "$LATEST" ]]; then
    echo "Already on $LATEST — nothing to do."
    exit 0
  fi
  echo "Current: $CURRENT → updating to $LATEST"
fi

# ── Fetch release metadata once ───────────────────────────────────────────────
echo "Fetching release metadata..."
RELEASE_JSON=$(gh_api "https://api.github.com/repos/$REPO/releases/tags/$LATEST")

# ── Download binary ───────────────────────────────────────────────────────────
BINARY_ASSET="ah-mcp-$ARCH"
echo "Downloading $BINARY_ASSET..."
if ! download_asset "$BINARY_ASSET" "$WORKDIR/$BINARY_ASSET"; then
  echo "ERROR: Asset $BINARY_ASSET not found in release $LATEST"
  echo "Available assets:"
  python3 -c "import sys,json; [print(' -', a['name']) for a in json.load(sys.stdin).get('assets',[])]" <<<"$RELEASE_JSON"
  exit 1
fi

# ── Verify checksum ───────────────────────────────────────────────────────────
# Without this the script installs whatever bytes it received, as root.
echo "Verifying checksum..."
if download_asset "SHA256SUMS" "$WORKDIR/SHA256SUMS"; then
  ( cd "$WORKDIR" && grep " ${BINARY_ASSET}\$" SHA256SUMS > expected.sha256 )
  if [[ ! -s "$WORKDIR/expected.sha256" ]]; then
    echo "ERROR: SHA256SUMS has no entry for $BINARY_ASSET"
    exit 1
  fi
  if ! ( cd "$WORKDIR" && sha256sum -c expected.sha256 ); then
    echo "ERROR: checksum mismatch — refusing to install $BINARY_ASSET"
    exit 1
  fi
  echo "Checksum OK."
else
  echo "ERROR: SHA256SUMS not published for release $LATEST — refusing to install unverified binary."
  echo "  Re-run the release workflow, or set ALLOW_UNVERIFIED=1 to override."
  [[ "${ALLOW_UNVERIFIED:-0}" == "1" ]] || exit 1
  echo "  ALLOW_UNVERIFIED=1 set — continuing without verification."
fi

# Quick sanity check
if ! file "$WORKDIR/$BINARY_ASSET" | grep -q "ELF"; then
  echo "ERROR: Downloaded file is not an ELF binary — check token permissions"
  exit 1
fi

# ── Download service file ─────────────────────────────────────────────────────
echo "Downloading service file..."
if ! download_asset "ah-mcp.service" "$WORKDIR/ah-mcp.service"; then
  # Fall back to raw content API (base64 decode)
  gh_api "https://api.github.com/repos/$REPO/contents/ah-mcp.service?ref=$LATEST" \
    | python3 -c "import sys,json,base64; print(base64.b64decode(json.load(sys.stdin)['content']).decode(),end='')" \
    > "$WORKDIR/ah-mcp.service"
fi

# ── Deploy ────────────────────────────────────────────────────────────────────
echo "Stopping $SERVICE_NAME..."
systemctl stop "$SERVICE_NAME" || true

echo "Installing binary to $INSTALL_PATH..."
install -o root -g root -m 755 "$WORKDIR/$BINARY_ASSET" "$INSTALL_PATH"

echo "Installing service file to $SERVICE_PATH..."
install -o root -g root -m 644 "$WORKDIR/ah-mcp.service" "$SERVICE_PATH"
systemctl daemon-reload

echo "Starting $SERVICE_NAME..."
systemctl start "$SERVICE_NAME"

echo ""
systemctl status "$SERVICE_NAME" --no-pager -l
echo ""
echo "Done — updated to $LATEST"
