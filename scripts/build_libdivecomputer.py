from __future__ import annotations

import argparse

from libdivecomputer_bootstrap import build_runtime_for_current_platform, platform_name


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Build libdivecomputer runtime files for the current platform."
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Re-download the source tree and rebuild runtime files from scratch.",
    )
    parser.add_argument(
        "--arch",
        default="x86_64",
        choices=("x86_64", "i686"),
        help="Windows MinGW target architecture. Ignored on non-Windows platforms.",
    )
    args = parser.parse_args()

    runtime_files = build_runtime_for_current_platform(force=args.force, arch=args.arch)
    if runtime_files:
        print(f"Built {platform_name()} runtime:")
        for path in runtime_files:
            print(f"- {path}")
        return

    print(f"No automated local runtime build is configured for {platform_name()}.")


if __name__ == "__main__":
    main()
