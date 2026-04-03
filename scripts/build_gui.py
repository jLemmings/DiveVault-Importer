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
BUILD_ASSETS_DIR = ROOT / ".pyinstaller-assets"
LOGO_PNG = ROOT / "logo.png"
LOGO_ICO = BUILD_ASSETS_DIR / "logo.ico"
SPEC_FILE = ROOT / "DiveSync.spec"


def binary_separator() -> str:
    return ";" if os.name == "nt" else ":"


def add_binary_arg(source: Path, dest: str = ".") -> str:
    return f"{source}{binary_separator()}{dest}"


def add_data_arg(source: Path, dest: str = ".") -> str:
    return f"{source}{binary_separator()}{dest}"


def convert_png_to_ico(source_png: Path, target_ico: Path) -> None:
    script = """
param([string]$src, [string]$dst)
Add-Type -AssemblyName System.Drawing
$bmp = [System.Drawing.Bitmap]::FromFile($src)
try {
    $icon = [System.Drawing.Icon]::FromHandle($bmp.GetHicon())
    try {
        $stream = [System.IO.File]::Open($dst, [System.IO.FileMode]::Create)
        try {
            $icon.Save($stream)
        } finally {
            $stream.Close()
        }
    } finally {
        $icon.Dispose()
    }
} finally {
    $bmp.Dispose()
}
"""
    subprocess.run(
        ["powershell", "-NoProfile", "-Command", script, str(source_png), str(target_ico)],
        check=True,
        capture_output=True,
        text=True,
    )


def ensure_windows_icon() -> Path:
    if not LOGO_PNG.exists():
        raise FileNotFoundError(f"Missing application icon source: {LOGO_PNG}")

    BUILD_ASSETS_DIR.mkdir(parents=True, exist_ok=True)
    convert_png_to_ico(LOGO_PNG, LOGO_ICO)
    return LOGO_ICO


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

    if LOGO_PNG.exists():
        args.extend(["--add-data", add_data_arg(LOGO_PNG)])

    if os.name == "nt":
        args.extend(["--icon", str(ensure_windows_icon())])
        for binary in windows_binaries():
            args.extend(["--add-binary", binary])
    elif sys.platform.startswith("linux"):
        for binary in linux_binaries():
            args.extend(["--add-binary", binary])

    return args


def main() -> None:
    DIST_DIR.mkdir(exist_ok=True)
    BUILD_DIR.mkdir(exist_ok=True)
    BUILD_ASSETS_DIR.mkdir(exist_ok=True)
    if SPEC_FILE.exists():
        SPEC_FILE.unlink()
    print(f"Building GUI for {platform.system()} with Python {platform.python_version()}")
    PyInstaller.__main__.run(pyinstaller_args())


if __name__ == "__main__":
    main()
