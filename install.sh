#!/bin/sh
# skills-manager installer.
#
#   curl -fsSL https://raw.githubusercontent.com/Flow-Forge-Lab-Team/skills-manager/main/install.sh | sh
#
# Downloads the latest released binary for your OS/arch from GitHub Releases and
# installs it to ~/.local/bin (override with SKILLS_MANAGER_INSTALL_DIR).
set -eu

REPO="Flow-Forge-Lab-Team/skills-manager"
INSTALL_DIR="${SKILLS_MANAGER_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${SKILLS_MANAGER_VERSION:-latest}"

err() { printf 'install: %s\n' "$1" >&2; exit 1; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in
  linux|darwin) ;;
  *) err "unsupported OS: $os (use the Windows zip from the Releases page)" ;;
esac
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) err "unsupported architecture: $arch" ;;
esac

command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar >/dev/null 2>&1 || err "tar is required"

if [ "$VERSION" = "latest" ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

# Archive names follow GoReleaser defaults: skills-manager_<os>_<arch>.tar.gz
asset="skills-manager_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

printf 'Downloading %s/%s ...\n' "$base" "$asset"
if ! curl -fsSL "$base/$asset" -o "$tmp/$asset"; then
  err "download failed (no release asset for ${os}/${arch}?). See https://github.com/$REPO/releases"
fi

tar -xzf "$tmp/$asset" -C "$tmp"
[ -f "$tmp/skills-manager" ] || err "archive did not contain the skills-manager binary"

mkdir -p "$INSTALL_DIR"
mv "$tmp/skills-manager" "$INSTALL_DIR/skills-manager"
chmod +x "$INSTALL_DIR/skills-manager"

printf 'Installed skills-manager to %s\n' "$INSTALL_DIR/skills-manager"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf 'Add it to your PATH:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" ;;
esac
