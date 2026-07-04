#!/bin/sh
# kindle-cli installer — downloads the latest release binary for this machine.
#
#   curl -fsSL https://raw.githubusercontent.com/seapy/kindle-cli/main/install.sh | sh
#
# Options (environment variables):
#   KINDLE_CLI_VERSION      install a specific version, e.g. "v0.0.1" (default: latest)
#   KINDLE_CLI_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
set -eu

REPO="seapy/kindle-cli"
INSTALL_DIR="${KINDLE_CLI_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${KINDLE_CLI_VERSION:-}"

err() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

# --- platform detection ---------------------------------------------------
case "$(uname -s)" in
    Linux)  os="linux" ;;
    Darwin) os="darwin" ;;
    *) err "unsupported OS: $(uname -s) — Windows는 릴리스 페이지의 zip을 받아주세요: https://github.com/$REPO/releases" ;;
esac
case "$(uname -m)" in
    x86_64 | amd64)  arch="amd64" ;;
    aarch64 | arm64) arch="arm64" ;;
    *) err "unsupported architecture: $(uname -m)" ;;
esac

command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar  >/dev/null 2>&1 || err "tar is required"

# --- resolve version ------------------------------------------------------
if [ -z "$VERSION" ]; then
    # the /releases/latest URL redirects to /releases/tag/vX.Y.Z
    VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
        "https://github.com/$REPO/releases/latest" | sed 's|.*/tag/||')
    [ -n "$VERSION" ] || err "could not determine the latest version"
fi
version_num=${VERSION#v}

archive="kindle-cli_${version_num}_${os}_${arch}.tar.gz"
base_url="https://github.com/$REPO/releases/download/$VERSION"

# --- download + checksum --------------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

printf '⬇ downloading kindle-cli %s (%s/%s)\n' "$VERSION" "$os" "$arch"
curl -fsSL -o "$tmp/$archive" "$base_url/$archive" \
    || err "download failed: $base_url/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base_url/checksums.txt" \
    || err "download failed: checksums.txt"

if command -v sha256sum >/dev/null 2>&1; then
    sumtool="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
    sumtool="shasum -a 256"
else
    sumtool=""
    printf '! sha256sum/shasum not found — skipping checksum verification\n' >&2
fi
if [ -n "$sumtool" ]; then
    want=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
    got=$($sumtool "$tmp/$archive" | cut -d' ' -f1)
    [ -n "$want" ] && [ "$want" = "$got" ] || err "checksum mismatch for $archive"
fi

# --- install ----------------------------------------------------------------
tar -xzf "$tmp/$archive" -C "$tmp" kindle-cli
mkdir -p "$INSTALL_DIR"
install -m 755 "$tmp/kindle-cli" "$INSTALL_DIR/kindle-cli"

printf '✓ installed %s → %s\n' "$("$INSTALL_DIR/kindle-cli" --version)" "$INSTALL_DIR/kindle-cli"

case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) printf '\n! %s is not in your PATH. Add this to your shell profile:\n    export PATH="%s:$PATH"\n' \
        "$INSTALL_DIR" "$INSTALL_DIR" ;;
esac
