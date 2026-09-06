<p align="center">
  <img src="assets/logo_header.png" alt="DiveVault" width="420">
</p>

<p align="center">
  Companion desktop importer for syncing dive computer telemetry into DiveVault.
</p>

<p align="center">
  <img src="https://img.shields.io/badge/libdivecomputer-0.9.0-0A66C2" alt="libdivecomputer 0.9.0">
  <img src="https://img.shields.io/badge/Go-desktop-00ADD8" alt="Go desktop application">
  <img src="https://img.shields.io/badge/Windows%20%7C%20Linux%20%7C%20macOS-builds-0078D4" alt="Windows, Linux, and macOS builds">
  <img src="https://img.shields.io/badge/GPL--3.0-licensed-A42E2B" alt="GPL 3.0 licensed">
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> |
  <a href="#what-the-importer-does">What It Does</a> |
  <a href="#using-the-importer">Using The Importer</a> |
  <a href="#developers">Developers</a> |
  <a href="#license">License</a>
</p>

---

DiveVault Importer is the desktop side of the DiveVault system. It detects a supported dive computer, reads dive telemetry through `libdivecomputer`, and uploads parsed dives to the DiveVault backend.

The GUI handles device selection, port scanning, browser-based backend sign-in, and sync progress.

## Quick Start

The easiest way to install DiveVault Importer is to download the latest release for your operating system.

1. Open the [DiveVault Importer releases page](https://github.com/jLemmings/DiveVault-Importer/releases).
2. Download the newest asset for your OS:
   - Windows: `DiveSync-windows-amd64-<version>.exe` (standalone; no DLL files needed)
   - macOS: `DiveSync-darwin-<architecture>-<version>.zip`
   - Linux: `DiveSync-linux-amd64-<version>.zip`
3. On Windows, run the downloaded `.exe` directly. On Linux/macOS, extract the complete archive and run `DiveSync` (or `DiveSync.app`). Keep the Linux native libraries beside the executable.
4. Select your dive computer brand, model, and serial port.
5. Sign in through the browser approval flow when prompted.
6. Start synchronization.

On Linux, you may need to mark the downloaded binary as executable:

```bash
cd DiveSync
chmod +x DiveSync
./DiveSync
```

On macOS, unzip the release asset first, then open `DiveSync.app`.

Linux requires the system OpenGL/X11, libusb and hidapi runtime libraries. See the [developer setup](./README-DEVELOPERS.md) for platform dependencies.

## What The Importer Does

- Detects compatible serial-connected dive computers
- Reads raw dive data, metadata, and samples through `libdivecomputer`
- Uploads parsed dive records to DiveVault
- Stores device sync state so later syncs only import newer dives
- Uses browser approval for desktop sync authentication
- Keeps backend URL, device model, serial port, and language settings between runs

## Using The Importer

Connect your dive computer before starting a sync. In the app, choose the matching brand and model, scan for the serial port, then sign in to the DiveVault backend through the browser window that opens.

After sign-in, start synchronization from the desktop app. Uploaded dives are available in DiveVault for review, completion, and logbook use.

## Developers

Developer setup, build instructions, project structure, backend API notes, and contribution checks live in [README-DEVELOPERS.md](./README-DEVELOPERS.md).

## Related Repositories

- Main DiveVault app and backend: <https://github.com/jLemmings/DiveVault>
- Helm chart repository: <https://github.com/jLemmings/helm-charts>

## License

This project is licensed under the GNU General Public License v3.0. See [`LICENSE`](./LICENSE) for details.
