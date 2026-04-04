Vendored runtime libraries live here.

Layout:
- `windows/`: `libdivecomputer.dll`, `libusb-1.0.dll`, `libhidapi-0.dll`
- `linux/`: optional location for prebuilt `libdivecomputer.so*`
- `macos/`: optional future location for `libdivecomputer*.dylib`

The GUI build script reads platform dependencies from this tree instead of system package managers.
On Linux, if no prebuilt `libdivecomputer.so*` is present, the build script compiles it from the vendored source tree with optional USB, HIDAPI, and BlueZ support disabled.
