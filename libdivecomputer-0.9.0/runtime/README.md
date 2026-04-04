Vendored runtime libraries live here.

Layout:
- `windows/`: `libdivecomputer.dll`, `libusb-1.0.dll`, `libhidapi-0.dll`
- `linux/`: `libdivecomputer.so*`, `libusb-*.so*`, `libhidapi-*.so*`
- `macos/`: optional future location for `libdivecomputer*.dylib`

The GUI build script reads platform dependencies from this tree instead of system package managers.
