#!/usr/bin/env sh
set -eu

owner_repo="1001encore/wave"

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) asset_arch="amd64" ;;
  aarch64|arm64) asset_arch="arm64" ;;
  *)
    echo "Unsupported architecture: $arch" >&2
    exit 1
    ;;
esac

asset="wave_linux_${asset_arch}.tar.gz"
url="https://github.com/${owner_repo}/releases/latest/download/${asset}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

echo "Downloading ${url}"
curl -fsSL -o "${tmp_dir}/wave.tar.gz" "$url"
tar -xzf "${tmp_dir}/wave.tar.gz" -C "$tmp_dir"

install_dir="${HOME}/.local/bin"
mkdir -p "$install_dir"
install -m 0755 "${tmp_dir}/wave" "${install_dir}/wave"

echo "Installed wave to ${install_dir}/wave"
if command -v wave >/dev/null 2>&1; then
  echo "wave is already on PATH."
else
  echo "Add ${install_dir} to PATH:"
  echo "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.bashrc && . ~/.bashrc"
fi

"${install_dir}/wave" --help >/dev/null
echo "Install complete."
