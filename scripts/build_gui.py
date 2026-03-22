from __future__ import annotations

import ctypes.util
import os
import platform
import subprocess
import sys
from pathlib import Path

import PyInstaller.__main__


ROOT = Path(__file__).resolve().parent.parent
ENTRYPOINT = ROOT / "mares_smart_air_sync.py"
DIST_DIR = ROOT / "dist"
BUILD_DIR = ROOT / "build"


def binary_separator() -> str:
    return ";" if os.name == "nt" else ":"


def add_binary_arg(source: Path, dest: str = ".") -> str:
    return f"{source}{binary_separator()}{dest}"


def windows_binaries() -> list[str]:
    binaries = []
    for name in ("libdivecomputer.dll", "libusb-1.0.dll", "libhidapi-0.dll"):
        path = ROOT / name
        if not path.exists():
            raise FileNotFoundError(f"Missing required Windows runtime dependency: {path}")
        binaries.append(add_binary_arg(path))
    return binaries


def locate_linux_library() -> Path:
    discovered = ctypes.util.find_library("divecomputer")
    candidates = [discovered] if discovered else []
    candidates.extend(
        [
            "/lib/x86_64-linux-gnu/libdivecomputer.so.0",
            "/usr/lib/x86_64-linux-gnu/libdivecomputer.so.0",
            "/usr/local/lib/libdivecomputer.so.0",
        ]
    )

    for candidate in candidates:
        if not candidate:
            continue
        path = Path(candidate)
        if not path.is_absolute():
            resolved = subprocess.run(
                ["bash", "-lc", f"ldconfig -p | awk '/{candidate}/ {{print $NF; exit}}'"],
                check=True,
                capture_output=True,
                text=True,
            ).stdout.strip()
            if resolved:
                path = Path(resolved)
        if path.exists():
            return path.resolve()

    raise FileNotFoundError("Could not locate libdivecomputer on this Linux runner.")


def linux_binaries() -> list[str]:
    primary = locate_linux_library()
    binaries = {primary}
    ldd = subprocess.run(["ldd", str(primary)], check=True, capture_output=True, text=True).stdout
    for line in ldd.splitlines():
        parts = line.strip().split()
        if len(parts) >= 3 and parts[1] == "=>" and parts[2].startswith("/"):
            dependency = Path(parts[2]).resolve()
            if dependency.name.startswith(("libusb-", "libhidapi-")):
                binaries.add(dependency)
    return [add_binary_arg(path) for path in sorted(binaries, key=str)]


def pyinstaller_args() -> list[str]:
    app_name = "DiveSync"
    args = [
        "--noconfirm",
        "--clean",
        "--onefile",
        "--windowed",
        "--name",
        app_name,
        "--distpath",
        str(DIST_DIR),
        "--workpath",
        str(BUILD_DIR),
        "--collect-submodules",
        "serial.tools",
        str(ENTRYPOINT),
    ]

    if os.name == "nt":
        for binary in windows_binaries():
            args.extend(["--add-binary", binary])
    elif sys.platform.startswith("linux"):
        for binary in linux_binaries():
            args.extend(["--add-binary", binary])

    return args


def main() -> None:
    DIST_DIR.mkdir(exist_ok=True)
    BUILD_DIR.mkdir(exist_ok=True)
    print(f"Building GUI for {platform.system()} with Python {platform.python_version()}")
    PyInstaller.__main__.run(pyinstaller_args())


if __name__ == "__main__":
    main()
