#!/bin/sh
# Install the alror CLI from GitHub releases.
#   curl -fsSL https://raw.githubusercontent.com/manaskumar3003/alror-cli/main/scripts/install.sh | sh
# Env: ALROR_VERSION (default: latest), ALROR_INSTALL_DIR (default: /usr/local/bin or ~/.local/bin)
set -eu

REPO="manaskumar3003/alror-cli"
VERSION="${ALROR_VERSION:-latest}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in linux|darwin) ;; *) echo "unsupported OS: $os (use install.ps1 on Windows)" >&2; exit 1 ;; esac
arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "unsupported arch: $arch" >&2; exit 1 ;; esac

if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$VERSION" ] || { echo "could not determine the latest release" >&2; exit 1; }
fi
num="${VERSION#v}"
archive="alror_${num}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$VERSION"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading alror $VERSION for $os/$arch…"
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
(cd "$tmp" && grep " $archive\$" checksums.txt | { command -v sha256sum >/dev/null && sha256sum -c - || shasum -a 256 -c -; })
tar -xzf "$tmp/$archive" -C "$tmp"

dir="${ALROR_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; mkdir -p "$dir"; fi
fi
install -m 0755 "$tmp/alror" "$dir/alror"
[ -f "$tmp/alror-docs" ] && install -m 0755 "$tmp/alror-docs" "$dir/alror-docs"
echo "Installed alror to $dir/alror"
case ":$PATH:" in *":$dir:"*) ;; *) echo "Add $dir to your PATH to use it." ;; esac
"$dir/alror" version || true
