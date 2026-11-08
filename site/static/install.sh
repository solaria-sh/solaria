#!/bin/sh
# Install Solaria on macOS or Linux.
#
#   curl -fsSL https://solaria.sh/install.sh | sh
#
# It downloads the newest release from GitHub (prereleases included while
# Solaria is pre-alpha), checks the archive against the release's
# checksums.txt, and copies the binary into a folder you own. Nothing runs
# as root.
#
# Settings, all optional:
#   SOLARIA_VERSION      a version such as 0.1.0 (default: the newest release)
#   SOLARIA_INSTALL_DIR  where the binary goes (default: ~/.local/bin)
#   SOLARIA_DOWNLOAD_URL base URL of the release files, for mirrors
#                        (default: GitHub's download URL for that version)

set -eu

repo="solaria-sh/solaria"
install_dir="${SOLARIA_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf 'solaria: %s\n' "$*"; }
fail() { printf 'solaria: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "this script needs $1"; }
need uname
need tar
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 2 -o "$2" "$1"; }
  fetch_stdout() { curl -fsSL --retry 2 "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
  fetch_stdout() { wget -q -O - "$1"; }
else
  fail "this script needs curl or wget"
fi

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported system $(uname -s). On Windows, use install.ps1 in PowerShell." ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported processor $(uname -m). Releases are built for amd64 and arm64." ;;
esac

version="${SOLARIA_VERSION:-}"
if [ -z "$version" ]; then
  # The list endpoint, unlike /releases/latest, includes prereleases.
  version="$(fetch_stdout "https://api.github.com/repos/$repo/releases?per_page=1" |
    sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n 1)"
  [ -n "$version" ] || fail "couldn't find a release of $repo. Set SOLARIA_VERSION to pick one."
fi
version="${version#v}"

base="${SOLARIA_DOWNLOAD_URL:-https://github.com/$repo/releases/download/v$version}"
archive="solaria_${version}_${os}_${arch}.tar.gz"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "downloading $archive"
fetch "$base/$archive" "$tmp/$archive" || fail "couldn't download $base/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || fail "couldn't download $base/checksums.txt"

expected="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
[ -n "$expected" ] || fail "checksums.txt has no entry for $archive"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')"
else
  fail "this script needs sha256sum or shasum to check the download"
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive: expected $expected, got $actual"

tar -xzf "$tmp/$archive" -C "$tmp" solaria
mkdir -p "$install_dir"
mv "$tmp/solaria" "$install_dir/solaria"
chmod 755 "$install_dir/solaria"

say "installed $("$install_dir/solaria" version 2>/dev/null | head -n 1 || echo "$version") to $install_dir/solaria"
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) say "$install_dir isn't on your PATH. Add this line to your shell profile:"
     printf '\n  export PATH="%s:$PATH"\n\n' "$install_dir" ;;
esac
