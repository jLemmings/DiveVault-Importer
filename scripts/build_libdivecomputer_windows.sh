#!/usr/bin/env bash
set -euo pipefail

ARCH="${1:-x86_64}"
case "$ARCH" in
  i686|x86_64) ;;
  *)
    echo "Unsupported arch: $ARCH" >&2
    exit 1
    ;;
esac

ROOT_DIR="${DIVEVAULT_IMPORTER_ROOT:-$(pwd)}"
ROOT_DIR="$(cd "$ROOT_DIR" && pwd)"
LIBDIVECOMPUTER_DIR="$ROOT_DIR/vendor/libdivecomputer-0.9.0"
RUNTIME_DIR="$LIBDIVECOMPUTER_DIR/runtime/windows"
HOST="${ARCH}-w64-mingw32"
LIBUSB_VERSION="1.0.29"
HIDAPI_VERSION="0.15.0"
JOBS="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

for cmd in bash autoreconf make tar cp find pkg-config; do
  require_cmd "$cmd"
done

if command -v curl >/dev/null 2>&1; then
  fetch() {
    curl -L --retry 3 --fail -o "$1" "$2"
  }
elif command -v wget >/dev/null 2>&1; then
  fetch() {
    wget -O "$1" "$2"
  }
else
  echo "Missing required downloader: curl or wget" >&2
  exit 1
fi

require_cmd "${HOST}-gcc"

if [ ! -d "$LIBDIVECOMPUTER_DIR" ]; then
  echo "Missing libdivecomputer source tree at $LIBDIVECOMPUTER_DIR" >&2
  echo "Run python scripts/fetch_libdivecomputer.py --source-only first." >&2
  exit 1
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/libdivecomputer-win-build-XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

STAGE_DIR="$TMP_DIR/artifacts"
DOWNLOAD_DIR="$TMP_DIR/downloads"
mkdir -p "$STAGE_DIR" "$DOWNLOAD_DIR" "$RUNTIME_DIR"

build_from_tarball() {
  local archive_name="$1"
  local url="$2"
  local source_dir="$3"
  local configure_args="$4"

  pushd "$DOWNLOAD_DIR" >/dev/null
  fetch "$archive_name" "$url"
  tar xzf "$archive_name"
  popd >/dev/null

  pushd "$DOWNLOAD_DIR/$source_dir" >/dev/null
  autoreconf --install --force
  ./configure $configure_args
  make -j"$JOBS"
  make install DESTDIR="$STAGE_DIR"
  popd >/dev/null
}

build_from_tarball \
  "libusb-${LIBUSB_VERSION}.tar.gz" \
  "https://github.com/libusb/libusb/archive/refs/tags/v${LIBUSB_VERSION}.tar.gz" \
  "libusb-${LIBUSB_VERSION}" \
  "--host=${HOST}"

build_from_tarball \
  "hidapi-${HIDAPI_VERSION}.tar.gz" \
  "https://github.com/libusb/hidapi/archive/refs/tags/hidapi-${HIDAPI_VERSION}.tar.gz" \
  "hidapi-hidapi-${HIDAPI_VERSION}" \
  "--host=${HOST}"

LOCAL_SOURCE="$TMP_DIR/libdivecomputer"
cp -R "$LIBDIVECOMPUTER_DIR" "$LOCAL_SOURCE"

pushd "$LOCAL_SOURCE" >/dev/null
autoreconf --install --force
env \
  PKG_CONFIG_LIBDIR="$STAGE_DIR/usr/local/lib/pkgconfig" \
  PKG_CONFIG_SYSROOT_DIR="$STAGE_DIR" \
  PKG_CONFIG_ALLOW_SYSTEM_CFLAGS=1 \
  PKG_CONFIG_ALLOW_SYSTEM_LIBS=1 \
  ./configure --host="${HOST}"
env \
  PKG_CONFIG_LIBDIR="$STAGE_DIR/usr/local/lib/pkgconfig" \
  PKG_CONFIG_SYSROOT_DIR="$STAGE_DIR" \
  PKG_CONFIG_ALLOW_SYSTEM_CFLAGS=1 \
  PKG_CONFIG_ALLOW_SYSTEM_LIBS=1 \
  make -j"$JOBS"
env \
  PKG_CONFIG_LIBDIR="$STAGE_DIR/usr/local/lib/pkgconfig" \
  PKG_CONFIG_SYSROOT_DIR="$STAGE_DIR" \
  PKG_CONFIG_ALLOW_SYSTEM_CFLAGS=1 \
  PKG_CONFIG_ALLOW_SYSTEM_LIBS=1 \
  make install DESTDIR="$STAGE_DIR"
popd >/dev/null

find_stage_file() {
  local pattern="$1"
  find "$STAGE_DIR" -type f -name "$pattern" | head -n 1
}

LIBDIVECOMPUTER_DLL="$(find_stage_file 'libdivecomputer-*.dll')"
LIBUSB_DLL="$(find_stage_file 'libusb-1.0.dll')"
HIDAPI_DLL="$(find_stage_file 'libhidapi-*.dll')"

if [ -z "$LIBDIVECOMPUTER_DLL" ] || [ ! -f "$LIBDIVECOMPUTER_DLL" ]; then
  echo "Failed to build libdivecomputer DLL." >&2
  exit 1
fi
if [ ! -f "$LIBUSB_DLL" ]; then
  echo "Failed to build libusb-1.0.dll." >&2
  exit 1
fi
if [ -z "$HIDAPI_DLL" ] || [ ! -f "$HIDAPI_DLL" ]; then
  echo "Failed to build hidapi DLL." >&2
  exit 1
fi

cp "$LIBDIVECOMPUTER_DLL" "$RUNTIME_DIR/libdivecomputer.dll"
cp "$LIBUSB_DLL" "$RUNTIME_DIR/libusb-1.0.dll"
cp "$HIDAPI_DLL" "$RUNTIME_DIR/libhidapi-0.dll"

echo "Prepared Windows runtime in $RUNTIME_DIR"
ls -l "$RUNTIME_DIR"
