#!/usr/bin/env bash

set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
lock_file="$repository_root/build.lock.json"
temporary_dir=$(mktemp -d)
trap 'rm -rf "$temporary_dir"' EXIT

python3 - "$lock_file" >"$temporary_dir/allowed" <<'PY'
import json
import sys

lock = json.load(open(sys.argv[1], encoding="utf-8"))
for soname in lock["nativeDependencies"]["allowedHostSonamesByArchitecture"]["amd64"]:
    print(soname)
PY

python3 - "$lock_file" >"$temporary_dir/static" <<'PY'
import json
import sys

lock = json.load(open(sys.argv[1], encoding="utf-8"))
for output in lock["nativeDependencies"]["staticOutputsByArchitecture"]["amd64"]:
    print(output)
PY

: >"$temporary_dir/actual"
for executable in "$@"; do
	if [[ ! -x "$executable" ]]; then
		echo "ELF input is not executable: $executable" >&2
		exit 1
	fi
	if readelf -d "$executable" | grep -Eq '\((RPATH|RUNPATH)\)'; then
		echo "ELF contains forbidden RPATH/RUNPATH: $executable" >&2
		exit 1
	fi
	name=$(basename "$executable")
	if grep -Fxq "$name" "$temporary_dir/static"; then
		if readelf -l "$executable" | grep -q 'Requesting program interpreter'; then
			echo "ELF output must be static: $executable" >&2
			exit 1
		fi
		continue
	fi
	if ! readelf -l "$executable" | grep -q 'Requesting program interpreter'; then
		echo "unexpected static ELF output: $executable" >&2
		exit 1
	fi
	if ldd "$executable" | grep -q 'not found'; then
		echo "ELF has unresolved host libraries: $executable" >&2
		exit 1
	fi
	ldd "$executable" | awk '
		/^[[:space:]]*linux-vdso/ { next }
		/=>/ { print $1; next }
		$1 ~ /^\// { count = split($1, parts, "/"); print parts[count] }
	' >>"$temporary_dir/actual"
done

sort -u -o "$temporary_dir/actual" "$temporary_dir/actual"
if ! diff -u "$temporary_dir/allowed" "$temporary_dir/actual"; then
	echo "ELF dependency union differs from build.lock.json" >&2
	exit 1
fi
