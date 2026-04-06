from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path
from urllib import request


ROOT = Path(__file__).resolve().parent.parent
VENDOR_DIR = ROOT / "vendor"
LIBDIVECOMPUTER_VERSION = "0.9.0"
LIBDIVECOMPUTER_DIR = VENDOR_DIR / f"libdivecomputer-{LIBDIVECOMPUTER_VERSION}"
LIBDIVECOMPUTER_ARCHIVE_URL = (
    f"https://libdivecomputer.org/releases/libdivecomputer-{LIBDIVECOMPUTER_VERSION}.tar.gz"
)
RUNTIME_DEPS_DIR = LIBDIVECOMPUTER_DIR / "runtime"
WINDOWS_BUILD_SCRIPT = ROOT / "scripts" / "build_libdivecomputer_windows.sh"


def platform_name() -> str:
    if os.name == "nt":
        return "windows"
    if sys.platform.startswith("linux"):
        return "linux"
    return "macos"


def platform_runtime_dir() -> Path:
    return RUNTIME_DEPS_DIR / platform_name()


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


def _archive_root(extract_dir: Path) -> Path:
    matches = [path for path in extract_dir.iterdir() if path.is_dir()]
    if len(matches) != 1:
        raise RuntimeError(
            f"Expected exactly one extracted top-level directory in {extract_dir}, found {len(matches)}."
        )
    return matches[0]


def ensure_libdivecomputer_source(force: bool = False) -> Path:
    if LIBDIVECOMPUTER_DIR.exists() and not force:
        return LIBDIVECOMPUTER_DIR

    VENDOR_DIR.mkdir(parents=True, exist_ok=True)

    if LIBDIVECOMPUTER_DIR.exists():
        shutil.rmtree(LIBDIVECOMPUTER_DIR)

    with tempfile.TemporaryDirectory(prefix="libdivecomputer-download-") as temp_dir_name:
        temp_dir = Path(temp_dir_name)
        archive_path = temp_dir / f"libdivecomputer-{LIBDIVECOMPUTER_VERSION}.tar.gz"

        print(f"Downloading {LIBDIVECOMPUTER_ARCHIVE_URL}")
        with request.urlopen(LIBDIVECOMPUTER_ARCHIVE_URL, timeout=60) as response:
            archive_path.write_bytes(response.read())

        extract_dir = temp_dir / "extract"
        extract_dir.mkdir()
        with tarfile.open(archive_path, "r:gz") as archive:
            archive.extractall(extract_dir, filter="data")

        extracted_root = _archive_root(extract_dir)
        shutil.move(str(extracted_root), str(LIBDIVECOMPUTER_DIR))

    print(f"Prepared {LIBDIVECOMPUTER_DIR}")
    return LIBDIVECOMPUTER_DIR


def find_windows_build_shell() -> Path:
    env_override = os.environ.get("BASH_EXE")
    if env_override:
        candidate = Path(env_override).expanduser()
        if candidate.exists():
            return candidate
        raise FileNotFoundError(f"BASH_EXE is set but does not exist: {candidate}")

    known_paths = [
        Path(r"C:\msys64\usr\bin\bash.exe"),
        Path(r"C:\Program Files\Git\bin\bash.exe"),
        Path(r"C:\Program Files (x86)\Git\bin\bash.exe"),
    ]
    for candidate in known_paths:
        if candidate.exists():
            return candidate

    resolved = shutil.which("bash")
    if resolved:
        return Path(resolved)

    raise FileNotFoundError(
        "Could not find a bash executable for the Windows MinGW build. Install MSYS2 or Git Bash "
        "with the required autotools/mingw-w64 toolchain, or set BASH_EXE to the full path of bash.exe."
    )


def windows_build_environment() -> dict[str, str]:
    env = os.environ.copy()
    msys_root = Path(r"C:\msys64")
    msys_bash = msys_root / "usr" / "bin" / "bash.exe"
    mingw_bin = msys_root / "mingw64" / "bin"
    usr_bin = msys_root / "usr" / "bin"

    if msys_bash.exists():
        env["MSYSTEM"] = "MINGW64"
        env["CHERE_INVOKING"] = "1"
        path_parts = [str(mingw_bin), str(usr_bin), env.get("PATH", "")]
        env["PATH"] = os.pathsep.join(part for part in path_parts if part)

    return env


def build_windows_runtime_from_source(arch: str = "x86_64") -> list[Path]:
    ensure_libdivecomputer_source()
    bash_exe = find_windows_build_shell()
    script_path = WINDOWS_BUILD_SCRIPT.resolve().as_posix()
    env = windows_build_environment()
    env["DIVEVAULT_IMPORTER_ROOT"] = str(ROOT)
    subprocess.run(
        [str(bash_exe), "--login", script_path, arch],
        cwd=ROOT,
        check=True,
        env=env,
    )
    return collect_runtime_files(("*.dll",))


def build_linux_runtime_from_source() -> list[Path]:
    ensure_libdivecomputer_source()
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
    subprocess.run(configure_cmd, cwd=LIBDIVECOMPUTER_DIR, check=True)
    subprocess.run(["make", "-C", str(LIBDIVECOMPUTER_DIR / "src"), "-j2"], check=True)

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


def build_runtime_for_current_platform(force: bool = False, arch: str = "x86_64") -> list[Path]:
    ensure_libdivecomputer_source(force=force)

    runtime_dir = platform_runtime_dir()
    if force and runtime_dir.exists():
        shutil.rmtree(runtime_dir)

    if os.name == "nt":
        return build_windows_runtime_from_source(arch=arch)

    if sys.platform.startswith("linux"):
        return build_linux_runtime_from_source()

    return []


def ensure_runtime_for_current_platform() -> list[Path]:
    if os.name == "nt":
        files = collect_runtime_files(("*.dll",))
        if any(file.name == "libdivecomputer.dll" for file in files):
            return files
        return build_windows_runtime_from_source()

    if sys.platform.startswith("linux"):
        files = collect_runtime_files(("*.so", "*.so.*"))
        if any(file.name.startswith("libdivecomputer.so") for file in files):
            return files
        return build_linux_runtime_from_source()

    return []
