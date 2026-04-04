from __future__ import annotations

import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path

import PyInstaller.__main__


ROOT = Path(__file__).resolve().parent.parent
ENTRYPOINT = ROOT / "divevault-importer.py"
DIST_DIR = ROOT / "dist"
BUILD_DIR = ROOT / "build"
LIBDIVECOMPUTER_DIR = ROOT / "libdivecomputer-0.9.0"
RUNTIME_DEPS_DIR = ROOT / "libdivecomputer-0.9.0" / "runtime"
LOGO_PNG = ROOT / "logo.png"
LOGO_ICO = ROOT / "logo.ico"
VERSION_FILE = ROOT / "VERSION"
SPEC_FILE = ROOT / "DiveSync.spec"


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


def build_linux_runtime_from_source() -> list[Path]:
    runtime_dir = RUNTIME_DEPS_DIR / "linux"
    runtime_dir.mkdir(parents=True, exist_ok=True)

    configure_cmd = [
        "bash",
        "configure",
        "--without-libusb",
        "--without-hidapi",
        "--without-bluez",
        "--disable-examples",
        "--disable-doc",
    ]
    subprocess.run(
        configure_cmd,
        cwd=LIBDIVECOMPUTER_DIR,
        check=True,
    )
    subprocess.run(
        ["make", "-C", str(LIBDIVECOMPUTER_DIR / "src"), "-j2"],
        check=True,
    )

    built_files = sorted((LIBDIVECOMPUTER_DIR / "src" / ".libs").glob("libdivecomputer.so*"))
    if not built_files:
        raise FileNotFoundError(
            f"libdivecomputer source build completed but produced no shared libraries under "
            f"{LIBDIVECOMPUTER_DIR / 'src' / '.libs'}"
        )

    copied: list[Path] = []
    for path in built_files:
        if not path.is_file():
            continue
        target = runtime_dir / path.name
        shutil.copy2(path, target)
        copied.append(target.resolve())

    return copied


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
        if not any(file.name.startswith("libdivecomputer.so") for file in files):
            files = build_linux_runtime_from_source()
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
    DIST_DIR.mkdir(exist_ok=True)
    BUILD_DIR.mkdir(exist_ok=True)
    if SPEC_FILE.exists():
        SPEC_FILE.unlink()
    print(f"Building GUI for {platform.system()} with Python {platform.python_version()}")
    PyInstaller.__main__.run(pyinstaller_args())


if __name__ == "__main__":
    main()
