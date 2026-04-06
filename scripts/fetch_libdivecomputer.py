from __future__ import annotations

import argparse

from libdivecomputer_bootstrap import (
    LIBDIVECOMPUTER_DIR,
    ensure_libdivecomputer_source,
    ensure_runtime_for_current_platform,
    platform_name,
)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Download libdivecomputer and prepare local runtime files for development."
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Delete any existing local libdivecomputer checkout and download it again.",
    )
    parser.add_argument(
        "--source-only",
        action="store_true",
        help="Download source only and skip building runtime files for the current platform.",
    )
    args = parser.parse_args()

    ensure_libdivecomputer_source(force=args.force)
    if args.source_only:
        print(f"Prepared source tree at {LIBDIVECOMPUTER_DIR}")
        return

    runtime_files = ensure_runtime_for_current_platform()
    if runtime_files:
        print(f"Prepared {platform_name()} runtime:")
        for path in runtime_files:
            print(f"- {path}")
    else:
        print(
            "Prepared source tree only. No automated runtime build is configured for "
            f"{platform_name()}."
        )


if __name__ == "__main__":
    main()
