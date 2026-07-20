#!/bin/sh
set -eu

repository="iivankin/platformd"

usage() {
  cat >&2 <<'EOF'
usage: install.sh platformd|forward

  platformd  Install the server on a supported VPS.
  forward    Install the local port-forward helper.
EOF
}

if [ "$#" -ne 1 ]; then
  usage
  exit 2
fi

mode="$1"

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) printf '%s is not supported on this operating system\n' "$mode" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) printf '%s is not supported on this CPU architecture\n' "$mode" >&2; exit 1 ;;
esac

case "$mode" in
  platformd)
    if [ "$os/$arch" != "linux/amd64" ]; then
      printf 'platformd requires a Linux amd64 VPS\n' >&2
      exit 1
    fi
    binary="platformd"
    asset="platformd-linux-amd64"
    install_dir="${PLATFORMD_INSTALL_DIR:-/usr/local/bin}"
    version="${PLATFORMD_VERSION:-}"
    ;;
  forward)
    binary="platformd-forward"
    asset="platformd-forward-$os-$arch"
    install_dir="${PLATFORMD_FORWARD_INSTALL_DIR:-$HOME/.local/bin}"
    version="${PLATFORMD_FORWARD_VERSION:-}"
    ;;
  *)
    usage
    exit 2
    ;;
esac

if [ -n "$version" ]; then
  version="${version#v}"
  release_url="https://github.com/$repository/releases/download/v$version"
else
  release_url="https://github.com/$repository/releases/latest/download"
fi

temporary_directory="$(mktemp -d)"
temporary_install=""
trap 'rm -rf "$temporary_directory"; if [ -n "$temporary_install" ]; then rm -f "$temporary_install"; fi' EXIT HUP INT TERM

download() {
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "$1" --output "$2"
}

download "$release_url/$asset" "$temporary_directory/$asset"
download "$release_url/SHA256SUMS" "$temporary_directory/SHA256SUMS"
expected="$(awk -v asset="$asset" '$2 == asset { print $1 }' "$temporary_directory/SHA256SUMS")"
if [ -z "$expected" ]; then
  printf 'The release checksum manifest does not contain %s\n' "$asset" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$temporary_directory/$asset" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$temporary_directory/$asset" | awk '{ print $1 }')"
else
  printf 'sha256sum or shasum is required to verify the download\n' >&2
  exit 1
fi
if [ "$actual" != "$expected" ]; then
  printf 'Checksum verification failed for %s\n' "$asset" >&2
  exit 1
fi

mkdir -p "$install_dir"
temporary_install="$install_dir/.$binary.$$"
cp "$temporary_directory/$asset" "$temporary_install"
chmod 0755 "$temporary_install"
mv "$temporary_install" "$install_dir/$binary"
temporary_install=""
printf 'Installed %s to %s/%s\n' "$binary" "$install_dir" "$binary"
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) printf 'Add %s to PATH before running %s.\n' "$install_dir" "$binary" >&2 ;;
esac
