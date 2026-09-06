#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/vendor/libdivecomputer-0.9.0"
if [ -f Makefile ]; then make clean; fi
# Uses installed libusb/hidapi when available. All device protocols stay upstream.
autoreconf --install --force
args=()
case "$(uname -s)" in MINGW*|MSYS*) args+=(--host="$(gcc -dumpmachine)");; esac
./configure --prefix="$ROOT/vendor/native" --disable-static "${args[@]}"
make -j"${JOBS:-4}"
make install
