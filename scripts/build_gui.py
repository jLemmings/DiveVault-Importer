from __future__ import annotations

import os
import platform
import subprocess
import sys
from pathlib import Path

import PyInstaller.__main__


ROOT = Path(__file__).resolve().parent.parent
ENTRYPOINT = ROOT / "divevault-importer.py"
DIST_DIR = ROOT / "dist"
BUILD_DIR = ROOT / "build"
BUILD_ASSETS_DIR = ROOT / ".pyinstaller-assets"
LIBDIVECOMPUTER_DIR = ROOT / "libdivecomputer-0.9.0"
RUNTIME_DEPS_DIR = ROOT / "libdivecomputer-0.9.0" / "runtime"
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


def platform_runtime_dir() -> Path:
    if os.name == "nt":
        platform_name = "windows"
    elif sys.platform.startswith("linux"):
        platform_name = "linux"
    else:
        platform_name = "macos"
    return RUNTIME_DEPS_DIR / platform_name


def candidate_dependency_roots() -> list[Path]:
    runtime_dir = platform_runtime_dir()
    roots = [runtime_dir]
    platform_dir = LIBDIVECOMPUTER_DIR / runtime_dir.name
    if platform_dir != runtime_dir:
        roots.append(platform_dir)
    roots.append(LIBDIVECOMPUTER_DIR)
    return roots


def collect_runtime_files(patterns: tuple[str, ...]) -> list[Path]:
    files: dict[str, Path] = {}
    for root in candidate_dependency_roots():
        if not root.exists():
            continue
        for pattern in patterns:
            for path in sorted(root.rglob(pattern)):
                if path.is_file():
                    files[str(path.resolve())] = path.resolve()

    return sorted(files.values(), key=str)


def require_runtime_match(files: list[Path], expected: tuple[str, ...], description: str) -> None:
    if any(file.name.startswith(prefix) for file in files for prefix in expected):
        return
    expected_names = ", ".join(expected)
    searched = ", ".join(str(path) for path in candidate_dependency_roots())
    raise FileNotFoundError(
        f"Missing {description}. Expected a file starting with one of: {expected_names}. "
        f"Searched under: {searched}"
    )


def bundled_runtime_binaries() -> list[str]:
    if os.name == "nt":
        files = collect_runtime_files(("*.dll",))
        require_runtime_match(files, ("libdivecomputer.dll",), "libdivecomputer runtime")
        require_runtime_match(files, ("libusb-1.0.dll",), "libusb runtime")
        require_runtime_match(files, ("libhidapi-0.dll",), "hidapi runtime")
    elif sys.platform.startswith("linux"):
        files = collect_runtime_files(("*.so", "*.so.*"))
        require_runtime_match(files, ("libdivecomputer.so",), "libdivecomputer runtime")
        require_runtime_match(files, ("libusb-",), "libusb runtime")
        require_runtime_match(files, ("libhidapi-",), "hidapi runtime")
    else:
        files = []

    return [add_binary_arg(path) for path in files]


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
        for binary in bundled_runtime_binaries():
            args.extend(["--add-binary", binary])
    elif sys.platform.startswith("linux"):
        for binary in bundled_runtime_binaries():
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
