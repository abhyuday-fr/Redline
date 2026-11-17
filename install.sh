#!/bin/sh
# Install redline from GitHub Releases.
#
#   curl -sSL https://raw.githubusercontent.com/abhyuday-fr/Redline/main/install.sh | sh
#
# Respects $INSTALL_DIR if set, otherwise installs to ~/.local/bin.
# Set $REDLINE_VERSION (e.g. 0.3.2 or v0.3.2) to install a specific release.
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

tag="${REDLINE_VERSION:-}"
if [ -n "$tag" ]; then
    case "$tag" in
        v*) ;;
        *) tag="v$tag" ;;
    esac
    echo "Using requested version $tag"
else
    echo "Fetching latest release info..."
    # Resolve the tag through the /releases/latest redirect instead of the
    # REST API: the API allows only 60 unauthenticated requests per hour per
    # IP, which shared networks use up, while this redirect isn't limited.
    latest_url="https://github.com/${REPO}/releases/latest"
    final_url="$(curl -sSLI -o /dev/null -w '%{url_effective}' "$latest_url" || true)"
    case "$final_url" in
        */releases/tag/*) tag="${final_url##*/}" ;;
        *) tag="" ;;
    esac
    if [ -z "$tag" ]; then
        echo "error: couldn't determine the latest release tag from $latest_url" >&2
        echo "       (is there a published release? see https://github.com/${REPO}/releases)" >&2
        exit 1
    fi
fi

version="${tag#v}"
archive="redline_${version}_linux_${arch}.tar.gz"
download_url="https://github.com/${REPO}/releases/download/${tag}/${archive}"
checksums_url="https://github.com/${REPO}/releases/download/${tag}/checksums.txt"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

echo "Downloading redline ${tag} for linux/${arch}..."
curl -fsSL -o "$tmpdir/$archive" "$download_url" || {
    echo "error: couldn't download $download_url" >&2
    exit 1
}
curl -fsSL -o "$tmpdir/checksums.txt" "$checksums_url" || {
    echo "error: couldn't download $checksums_url" >&2
    exit 1
}

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
