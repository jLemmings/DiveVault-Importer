# Developing DiveVault Importer

The application is written in Go. Device enumeration, serial communication, device protocols, fingerprints and telemetry parsing are provided by **libdivecomputer 0.9.0**, called through cgo and its official headers. There is no Go implementation of dive computer protocols.

Fyne replaces Tkinter. The Go standard library handles HTTP, JSON, browser-approval polling and settings. Serial enumeration also uses libdivecomputer, so no replacement for pyserial is required. PyJWT was not used by the Python application: desktop tokens remain opaque bearer tokens supplied by the backend. Python and PyInstaller are no longer required.

## Requirements

- Go 1.26 or later and a C compiler compatible with cgo.
- Autoconf, Automake, Libtool, Make and pkg-config.
- Linux: OpenGL/X11 development packages, libusb and hidapi development packages. On Ubuntu: `sudo apt-get install autoconf automake libtool pkg-config libusb-1.0-0-dev libhidapi-dev libgl1-mesa-dev xorg-dev`.
- macOS: Xcode command line tools; `brew install autoconf automake libtool pkg-config libusb hidapi`.
- Windows: MSYS2 MINGW64 with `autoconf automake libtool make mingw-w64-x86_64-gcc mingw-w64-x86_64-pkgconf mingw-w64-x86_64-libusb mingw-w64-x86_64-hidapi` installed through pacman.

The native `vendor/` directory is not Go module vendoring. Use `GOFLAGS=-mod=readonly` (or pass `-mod=readonly` to Go commands) to use the pinned dependencies in `go.mod` and `go.sum`.

## Windows

From PowerShell at the repository root:

```powershell
$env:GOFLAGS = '-mod=readonly'
$env:CGO_ENABLED = '1'
$env:CC = 'gcc'
$env:PATH = "C:\msys64\mingw64\bin;C:\msys64\usr\bin;$PWD\vendor\native\bin;$env:PATH"
go run ./scripts/fetch
powershell -ExecutionPolicy Bypass -File scripts/build_libdivecomputer_windows.ps1
go run .
```

## Linux and macOS

```bash
export GOFLAGS=-mod=readonly
export CGO_ENABLED=1
go run ./scripts/fetch
bash scripts/build_libdivecomputer.sh
export LD_LIBRARY_PATH="$PWD/vendor/native/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export DYLD_LIBRARY_PATH="$PWD/vendor/native/lib${DYLD_LIBRARY_PATH:+:$DYLD_LIBRARY_PATH}"
go run .
```

The fetch tool downloads the pinned source into `vendor/libdivecomputer-0.9.0`. The native build installs into `vendor/native`. Re-running the native build cleans compiled objects first. Installed libusb/hidapi are detected by upstream configure; the serial importer does not require USB/HID transports, but CI enables those native dependencies.

## Checks and packaging

With the environment above configured:

```bash
go test ./...
go vet ./...
go run ./scripts/package
```

Run the extracted executable with `-check-runtime` to verify that libdivecomputer loads and its serial device catalog is available without opening the GUI or contacting hardware.

The package command builds the real desktop application and produces `dist/DiveSync-<goos>-<goarch>-<version>.zip`. Windows additionally produces a standalone `.exe` with libdivecomputer and all its non-system DLL dependencies embedded inside it. macOS bundles include a `DiveSync.app` with relocated native libraries and an ad-hoc signature. Linux bundles include libdivecomputer beside the executable and use an executable-relative runtime search path. Linux users need system OpenGL/X11, libusb and hidapi runtimes. Official builds run on Windows, Ubuntu 22.04 and macOS in `.github/workflows/build-gui.yml`.

On Windows, run the standalone executable directly; no adjacent DLL or installed native runtime is needed. The Windows system loader requires DLL files, so the application automatically extracts its embedded libraries into a content-addressed directory under `%LOCALAPPDATA%\DiveVault Importer\runtime`. It verifies the cached bytes against the embedded payload on every startup and loads only from that directory and Windows system directories. Concurrent instances share the verified cache. No administrator access is required, and the executable's directory can be read-only. The DLLs remain ordinary upstream libdivecomputer binaries; device protocols are still entirely implemented by that library.

Windows source builds use `vendor/native/bin` and the configured development toolchain PATH. Release packaging generates the ignored `internal/divecomputer/runtime/` directory, compiles with `-tags=bundled`, and removes that generated directory after building. Do not run multiple package builds simultaneously in the same checkout.

Extract the whole archive on Linux/macOS. Keep Linux native libraries beside the executable. Packaging uses a fresh staging directory, embeds the logos, translations and version, and includes the application and libdivecomputer licenses in the ZIP. macOS distribution is not notarized, and Windows executables are not code-signed.

## Structure and compatibility

- `main.go`, `resources.go`: Fyne UI and embedded resources.
- `internal/divecomputer/`: cgo adapter and minimal C callback bridge; links the upstream library.
- `internal/importer/`: payload construction and transactional checkpoint coordination.
- `internal/backend/`: `/api/device-state`, `/api/dives` and `/api/cli-auth/request` clients.
- `internal/settings/`: the existing `DiveVault Importer/settings.json` under the OS configuration directory.
- `scripts/fetch/`, `scripts/package/`: Go bootstrap and packaging commands.
- `scripts/build_libdivecomputer.sh`: native build; the PowerShell wrapper selects MSYS2 on Windows.
- `scripts/build_libdivecomputer_windows.sh`: retained standalone Linux-to-MinGW runtime build with pinned libusb/hidapi sources.

Backend record keys, fingerprint-based IDs, raw hashes/base64, nullable metadata and sample fields are retained. Dive timestamps retain the previous local-time string format. Sample timestamps now correctly convert libdivecomputer 0.9.0 milliseconds to seconds. Gas and tank fields use the actual upstream struct layouts, correcting the old handwritten ctypes definitions. A failed read, parse, upload or cancellation never advances the checkpoint. Already-uploaded dives are deduplicated by the backend on retry.

The UI keeps authentication tokens only in memory, invalidates them when the backend changes, and requires device detection before sign-in and sync. Long operations run outside the UI thread. Cancel uses libdivecomputer's cancellation callback and HTTP contexts; an in-flight device open or native blocking I/O may finish before cancellation is observed.

Tests cover the backend HTTP contract, payload identity, duplicate handling, failure/cancellation checkpoint behavior, settings compatibility, native descriptor enumeration and sample collection. A supported physical device and live backend are still required for an end-to-end hardware test.

Upstream API reference: [libdivecomputer 0.9.0 headers](https://github.com/libdivecomputer/libdivecomputer/tree/v0.9.0/include/libdivecomputer). UI reference: [Fyne documentation](https://docs.fyne.io/).
