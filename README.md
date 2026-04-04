# DiveVault Importer

DiveVault Importer is a Python desktop importer for downloading dive logs from a supported dive computer and sending them to the DiveVault backend.

At the moment, the code is centered around the Mares Smart Air over serial transport and includes a small GUI for device detection, backend sign-in, and sync progress.

## What This Project Does

- Detects compatible serial-connected dive computers.
- Uses `libdivecomputer` to read raw dive data and parse dive metadata and samples.
- Sends parsed dives to the DiveVault backend API.
- Stores and reuses a device fingerprint so later syncs only import newer dives.
- Supports a browser-based desktop sign-in flow for the backend.

## Project Structure

- `divevault-importer.py`: main application and GUI.
- `scripts/build_gui.py`: PyInstaller build script for packaging the desktop app.
- `libdivecomputer-0.9.0/`: vendored runtime/source dependency used to communicate with dive computers.
- `VERSION`: application version.

## Relationship to the DiveVault Backend

This repository is only the importer/client side. It does not contain the DiveVault backend implementation.

The importer sends data to the DiveVault backend, which lives in a separate repository:

- Backend repository: https://github.com/jLemmings/DiveVault

In practice, this app:

- fetches per-device sync state from `/api/device-state`
- updates the stored device fingerprint through `/api/device-state`
- uploads dive records to `/api/dives`
- uses `/api/cli-auth/request` for the desktop login flow

If you need to change backend storage, authentication, or API behavior, that work belongs in the DiveVault backend repository, not in this repository.

## Dependency on libdivecomputer

This project relies on the `libdivecomputer` project for low-level dive computer communication and parsing support.

- Official project: https://www.libdivecomputer.org/
- Upstream source: https://github.com/libdivecomputer/libdivecomputer
- Vendored copy used here: `libdivecomputer-0.9.0/`

Without `libdivecomputer`, this importer would not be able to open the device connection, iterate dives, parse dive fields, or read sample data from the supported hardware.

## Running Locally

Install dependencies:

```powershell
python -m pip install -r requirements.txt
```

Run the importer:

```powershell
python divevault-importer.py
```

Optional environment variables:

- `BACKEND_URL`: backend base URL used by the importer
- `BACKEND_AUTH_TOKEN`: bearer token for backend API access

## Building the GUI App

Install build dependencies:

```powershell
python -m pip install -r requirements.txt -r requirements-build.txt
```

Build the packaged GUI application:

```powershell
python scripts/build_gui.py
```

The GitHub Actions workflow in `.github/workflows/build-gui.yml` builds Windows, Linux, and macOS artifacts.
