from __future__ import annotations

import os
import platform
import sys
from pathlib import Path

import PyInstaller.__main__
from libdivecomputer_bootstrap import (
    LIBDIVECOMPUTER_DIR,
    candidate_dependency_roots,
    collect_runtime_files,
    ensure_libdivecomputer_source,
    ensure_runtime_for_current_platform,
)


ROOT = Path(__file__).resolve().parent.parent
ENTRYPOINT = ROOT / "divevault-importer.py"
DIST_DIR = ROOT / "dist"
BUILD_DIR = ROOT / "build"
RUNTIME_DEPS_DIR = LIBDIVECOMPUTER_DIR / "runtime"
ASSETS_DIR = ROOT / "assets"
LOGO_PNG = ASSETS_DIR / "logo.png"
LOGO_ICO = ASSETS_DIR / "logo.ico"
VERSION_FILE = ROOT / "VERSION"


def binary_separator() -> str:
    return ";" if os.name == "nt" else ":"


def add_binary_arg(source: Path, dest: str = ".") -> str:
    return f"{source}{binary_separator()}{dest}"


def add_data_arg(source: Path, dest: str = ".") -> str:
    return f"{source}{binary_separator()}{dest}"


def platform_runtime_dir() -> Path:
    if os.name == "nt":
        platform_name = "windows"
    elif sys.platform.startswith("linux"):
        platform_name = "linux"
    else:
        platform_name = "macos"
    return RUNTIME_DEPS_DIR / platform_name


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
        if not any(file.name == "libdivecomputer.dll" for file in files):
            files = ensure_runtime_for_current_platform()
        require_runtime_match(files, ("libdivecomputer.dll",), "libdivecomputer runtime")
    elif sys.platform.startswith("linux"):
        files = collect_runtime_files(("*.so", "*.so.*"))
        if not any(file.name.startswith("libdivecomputer.so") for file in files):
            files = ensure_runtime_for_current_platform()
        require_runtime_match(files, ("libdivecomputer.so",), "libdivecomputer runtime")
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
    if VERSION_FILE.exists():
        args.extend(["--add-data", add_data_arg(VERSION_FILE)])

    if os.name == "nt":
        if not LOGO_ICO.exists():
            raise FileNotFoundError(f"Missing Windows application icon: {LOGO_ICO}")
        args.extend(["--icon", str(LOGO_ICO)])
        for binary in bundled_runtime_binaries():
            args.extend(["--add-binary", binary])
    elif sys.platform.startswith("linux"):
        for binary in bundled_runtime_binaries():
            args.extend(["--add-binary", binary])

    return args


def main() -> None:
    ensure_libdivecomputer_source()
    DIST_DIR.mkdir(exist_ok=True)
    BUILD_DIR.mkdir(exist_ok=True)
    print(f"Building GUI for {platform.system()} with Python {platform.python_version()}")
    PyInstaller.__main__.run(pyinstaller_args())


if __name__ == "__main__":
    main()
