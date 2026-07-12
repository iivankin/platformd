# Release build inputs

`build.lock.json` is the only version/hash/ABI contract. The Linux runtime
builder downloads those exact helper sources, builds against the supported
host libraries, rejects a direct or transitive ELF SONAME mismatch, and
requires `catatonit` to be the only static output because it runs inside
arbitrary application root filesystems.

Ubuntu 24.04 release builders need GCC, pkg-config, libseccomp-dev,
libcap-dev, libjson-c-dev, libglib2.0-dev, autoconf, automake, libtool, gperf,
Meson, Ninja, Python 3, protobuf-compiler, curl, and the exact Rust toolchain
from the lock. These are build dependencies only; `platformd init` never runs
a package manager.

Run `PLATFORMD_STRICT_TOOLCHAIN=1 build/runtime-linux-amd64.sh` in release CI.
The output is written to `dist/runtime/` and is then appended to the Go
executable by the existing bundle tool.
