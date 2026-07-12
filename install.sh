#!/bin/sh
# Install redline from GitHub Releases.
#
#   curl -sSL https://raw.githubusercontent.com/abhyuday-fr/Redline/main/install.sh | sh
#
# Respects $INSTALL_DIR if set, otherwise installs to ~/.local/bin.
set -eu

REPO="abhyuday-fr/Redline"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

os="$(uname -s)"
if [ "$os" != "Linux" ]; then
    echo "error: redline currently supports Linux only (detected: $os)" >&2
    exit 1
fi

arch="$(uname -m)"
case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *)
        echo "error: unsupported architecture: $arch (redline ships amd64 and arm64 builds)" >&2
        exit 1
        ;;
esac

echo "Fetching latest release info..."
latest_url="https://api.github.com/repos/${REPO}/releases/latest"
tag="$(curl -sSL "$latest_url" | grep -m1 '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')"
if [ -z "$tag" ]; then
    echo "error: couldn't determine the latest release tag from $latest_url" >&2
    exit 1
fi

version="${tag#v}"
archive="redline_${version}_linux_${arch}.tar.gz"
download_url="https://github.com/${REPO}/releases/download/${tag}/${archive}"
checksums_url="https://github.com/${REPO}/releases/download/${tag}/checksums.txt"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

echo "Downloading redline ${tag} for linux/${arch}..."
curl -sSL -o "$tmpdir/$archive" "$download_url"
curl -sSL -o "$tmpdir/checksums.txt" "$checksums_url"

echo "Verifying checksum..."
(cd "$tmpdir" && grep "$archive" checksums.txt | sha256sum -c -) || {
    echo "error: checksum verification failed — aborting install" >&2
    exit 1
}

echo "Installing to $INSTALL_DIR..."
mkdir -p "$INSTALL_DIR"
tar -xzf "$tmpdir/$archive" -C "$tmpdir"
mv "$tmpdir/redline" "$INSTALL_DIR/redline"
chmod +x "$INSTALL_DIR/redline"

echo ""
echo "redline ${tag} installed to $INSTALL_DIR/redline"
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
        echo ""
        echo "$INSTALL_DIR is not on your PATH. Add this to your shell profile:"
        echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
        ;;
esac
echo ""
echo "Run 'redline doctor' to check for gdb, valgrind, perf, cmake, and ninja."
