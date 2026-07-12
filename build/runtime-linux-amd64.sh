#!/usr/bin/env bash

set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
lock_file="$repository_root/build.lock.json"
work_dir=${PLATFORMD_BUILD_DIR:-$repository_root/.build/runtime-linux-amd64}
output_dir=${PLATFORMD_RUNTIME_OUTPUT:-$repository_root/dist/runtime}
jobs=${PLATFORMD_BUILD_JOBS:-$(nproc)}

if [[ $(uname -s) != "Linux" || $(uname -m) != "x86_64" ]]; then
	echo "runtime payload can only be built on Linux/amd64" >&2
	exit 1
fi

lock_value() {
	python3 - "$lock_file" "$1" <<'PY'
import json
import sys

value = json.load(open(sys.argv[1], encoding="utf-8"))
for component in sys.argv[2].split("."):
    value = value[component]
print(value)
PY
}

verify_version() {
	local name=$1
	local actual=$2
	local expected=$3
	if [[ "$actual" != "$expected" ]]; then
		echo "$name version mismatch: got $actual, expected $expected" >&2
		exit 1
	fi
}

verify_version rust "$(rustc --version | awk '{print $2}')" "$(lock_value toolchain.rust)"
verify_version protoc "$(protoc --version | awk '{print $2}')" "$(lock_value toolchain.protoc)"
if [[ ${PLATFORMD_STRICT_TOOLCHAIN:-0} == "1" ]]; then
	cc_version=$(gcc -dumpfullversion)
	verify_version cc "gcc-$cc_version" "$(lock_value toolchain.ccByArchitecture.amd64)"
fi

rm -rf "$work_dir" "$output_dir"
mkdir -p "$work_dir/archives" "$work_dir/src" "$output_dir"

download() {
	local name=$1
	local url=$2
	local expected=${3#sha256:}
	local destination="$work_dir/archives/$name"
	curl --fail --location --retry 3 --silent --show-error --output "$destination" "$url"
	printf '%s  %s\n' "$expected" "$destination" | sha256sum --check --status
}

conmon_revision=$(lock_value helpers.conmon.revision)
netavark_revision=$(lock_value helpers.netavark.revision)
catatonit_revision=$(lock_value helpers.catatonit.revision)

download crun.tar.gz \
	"https://github.com/containers/crun/releases/download/1.28/crun-1.28.tar.gz" \
	"$(lock_value helpers.crun.sourceSha256)"
download conmon.tar.gz \
	"https://codeload.github.com/containers/conmon/tar.gz/$conmon_revision" \
	"$(lock_value helpers.conmon.sourceSha256)"
download netavark.tar.gz \
	"https://codeload.github.com/containers/netavark/tar.gz/$netavark_revision" \
	"$(lock_value helpers.netavark.sourceSha256)"
download catatonit.tar.gz \
	"https://codeload.github.com/openSUSE/catatonit/tar.gz/$catatonit_revision" \
	"$(lock_value helpers.catatonit.sourceSha256)"

for archive in "$work_dir"/archives/*.tar.gz; do
	tar -xf "$archive" -C "$work_dir/src"
done

common_cflags="-O2 -ffile-prefix-map=$work_dir=. -fdebug-prefix-map=$work_dir=."
common_ldflags="-Wl,--as-needed -Wl,--build-id=none -Wl,-z,relro,-z,now"

crun_source="$work_dir/src/crun-1.28"
crun_revision=$(lock_value helpers.crun.revision)
if ! grep -Fq "\"$crun_revision\"" "$crun_source/.tarball-git-version.h"; then
	echo "crun release archive revision does not match build.lock.json" >&2
	exit 1
fi
(
	cd "$crun_source"
	CFLAGS="$common_cflags" LDFLAGS="$common_ldflags" ./configure \
		--disable-criu \
		--disable-dl \
		--disable-libcrun \
		--disable-systemd \
		--enable-crun
	make -j"$jobs"
	install -m 0755 crun "$output_dir/crun"
)

conmon_source="$work_dir/src/conmon-$conmon_revision"
CFLAGS="$common_cflags" LDFLAGS="$common_ldflags" meson setup \
	"$work_dir/conmon" "$conmon_source" \
	--buildtype=release \
	-Db_lto=true \
	-Db_ndebug=true
meson compile -C "$work_dir/conmon" -j "$jobs"
install -m 0755 "$work_dir/conmon/conmon" "$output_dir/conmon"

netavark_source="$work_dir/src/netavark-$netavark_revision"
(
	cd "$netavark_source"
	export RUSTFLAGS="-C panic=abort -C strip=symbols --remap-path-prefix=$work_dir=."
	cargo build --locked --release --bin netavark -j "$jobs"
	install -m 0755 target/release/netavark "$output_dir/netavark"
)

catatonit_source="$work_dir/src/catatonit-$catatonit_revision"
(
	cd "$catatonit_source"
	./autogen.sh
	CFLAGS="$common_cflags" LDFLAGS="$common_ldflags" ./configure
	make -j"$jobs"
	install -m 0755 catatonit "$output_dir/catatonit"
)

strip "$output_dir/crun" "$output_dir/conmon" "$output_dir/catatonit"
for configuration in containers.conf mounts.conf policy.json registries.conf seccomp.json storage.conf; do
	install -m 0644 "$repository_root/runtime/$configuration" "$output_dir/$configuration"
done

"$repository_root/build/verify-elf.sh" "$output_dir/crun" "$output_dir/conmon" \
	"$output_dir/netavark" "$output_dir/catatonit"
sha256sum "$output_dir"/*
