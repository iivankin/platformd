#!/usr/bin/env bash

set -euo pipefail

profile=${1:-}
if [[ $profile != go && $profile != runtime && $profile != s3 ]]; then
	echo "usage: $0 {go|runtime|s3}" >&2
	exit 2
fi

packages=(
	build-essential
	libcap-dev
	libglib2.0-dev
	libjson-c-dev
	libseccomp-dev
	pkg-config
)

if [[ $profile == runtime ]]; then
	packages+=(
		autoconf
		automake
		curl
		gcc-13
		gperf
		libclang-dev
		libtool
		meson
		ninja-build
		protobuf-compiler
		python3
		python3-venv
	)
fi

if [[ $profile == s3 ]]; then
	packages+=(python3-venv)
fi

# GitHub ubuntu-24.04 lists azure.archive.ubuntu.com first in apt-mirrors.txt.
# That host hangs on apt-get update; drop it and fail over in seconds, not minutes.
if [[ -f /etc/apt/apt-mirrors.txt ]]; then
	{
		printf 'https://archive.ubuntu.com/ubuntu/\tpriority:1\n'
		printf 'https://security.ubuntu.com/ubuntu/\tpriority:2\n'
	} | sudo tee /etc/apt/apt-mirrors.txt >/dev/null
fi
sudo tee /etc/apt/apt.conf.d/99platformd-ci-acquire >/dev/null <<'EOF'
Acquire::Retries "2";
Acquire::http::Timeout "15";
Acquire::https::Timeout "15";
EOF

sudo apt-get update
sudo apt-get install --yes --no-install-recommends "${packages[@]}"

if [[ $profile == runtime ]]; then
	toolchain_dir="$RUNNER_TEMP/platformd-toolchain"
	mkdir -p "$toolchain_dir"
	ln -s /usr/bin/gcc-13 "$toolchain_dir/gcc"
	echo "$toolchain_dir" >> "$GITHUB_PATH"
fi
