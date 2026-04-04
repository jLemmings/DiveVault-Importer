#!/usr/bin/env python3
"""
Download dives from a Mares Smart Air using libdivecomputer and send them to a backend API.

Tested logic-wise against the libdivecomputer v0.9.0 public headers/API layout,
but you should still expect to do a little hardware-specific debugging the first time.

Usage:
    python divevault-importer.py --port COM3 --backend-url http://localhost:8000
    python divevault-importer.py --port COM3 --backend-url http://localhost:8000 --backend-auth-token <desktop_sync_token_or_session_token>
    python divevault-importer.py --gui

Notes:
- This example uses SERIAL transport (clip/cable). The Mares Smart Air also supports BLE,
  but serial is simpler for a minimal first version.
- It stores a per-device fingerprint so subsequent runs only import newer dives.
- The Windows GUI can open your browser, let you sign in to the backend, and receive a short-lived
  desktop sync token without using Clerk API keys.
"""

from __future__ import annotations

import argparse
import base64
import ctypes
from ctypes import (
    POINTER,
    Structure,
    Union,
    byref,
    c_char_p,
    c_double,
    c_int,
    c_longlong,
    c_size_t,
    c_uint,
    c_ubyte,
    c_void_p,
    py_object,
    cast,
)
import ctypes.util
import hashlib
import json
import sys
import os
import subprocess
import tempfile
import threading
import time
import webbrowser
from datetime import datetime, timezone
from queue import Empty, Queue
from typing import Callable
from urllib import error, parse, request

try:
    import tkinter as tk
    from tkinter import messagebox, ttk
except ImportError:  # pragma: no cover - tkinter availability depends on local Python install
    tk = None
    messagebox = None
    ttk = None

try:
    from serial.tools import list_ports
except ImportError:  # pragma: no cover - optional dependency
    list_ports = None

from dotenv import load_dotenv


_DLL_SEARCH_HANDLES: list[object] = []


def executable_dir() -> str:
    if getattr(sys, "frozen", False):
        return os.path.dirname(os.path.abspath(sys.executable))
    return os.path.dirname(os.path.abspath(__file__))


def resource_dir() -> str:
    return getattr(sys, "_MEIPASS", os.path.dirname(os.path.abspath(__file__)))


def vendored_runtime_dirs() -> list[str]:
    project_dir = os.path.dirname(os.path.abspath(__file__))
    runtime_root = os.path.join(project_dir, "libdivecomputer-0.9.0", "runtime")
    if os.name == "nt":
        platform_dir = os.path.join(runtime_root, "windows")
    elif sys.platform == "darwin":
        platform_dir = os.path.join(runtime_root, "macos")
    else:
        platform_dir = os.path.join(runtime_root, "linux")
    return [platform_dir, runtime_root]


def set_windows_appusermodel_id() -> None:
    if os.name != "nt":
        return
    try:
        ctypes.windll.shell32.SetCurrentProcessExplicitAppUserModelID("DiveVault.DiveSync")
    except Exception:
        return


def convert_png_to_ico(source_png: str, target_ico: str) -> bool:
    if os.name != "nt":
        return False

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
    try:
        subprocess.run(
            ["powershell", "-NoProfile", "-Command", script, source_png, target_ico],
            check=True,
            capture_output=True,
            text=True,
        )
    except Exception:
        return False
    return True


def ensure_runtime_icon_path() -> str | None:
    png_path = os.path.join(resource_dir(), "logo.png")
    if not os.path.exists(png_path):
        return None

    ico_path = os.path.join(tempfile.gettempdir(), "dive_sync_runtime_icon.ico")
    if not convert_png_to_ico(png_path, ico_path):
        return None
    return ico_path


def load_optional_dotenv() -> None:
    candidates = [
        os.path.join(executable_dir(), ".env"),
        os.path.join(os.path.dirname(os.path.abspath(__file__)), ".env"),
        os.path.join(os.getcwd(), ".env"),
        os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), ".env"),
    ]

    seen: set[str] = set()
    for candidate in candidates:
        normalized = os.path.normcase(os.path.abspath(candidate))
        if normalized in seen:
            continue
        seen.add(normalized)
        if os.path.exists(candidate):
            load_dotenv(dotenv_path=candidate)
            return


load_optional_dotenv()


# ----------------------------
# Constants from public headers
# ----------------------------

DC_STATUS_SUCCESS = 0
DC_STATUS_DONE = 1  # iterator end / not-an-error in libdivecomputer docs

DC_FIELD_DIVETIME = 0
DC_FIELD_MAXDEPTH = 1
DC_FIELD_AVGDEPTH = 2
DC_FIELD_GASMIX_COUNT = 3
DC_FIELD_GASMIX = 4
DC_FIELD_SALINITY = 5
DC_FIELD_ATMOSPHERIC = 6
DC_FIELD_TEMPERATURE_SURFACE = 7
DC_FIELD_TEMPERATURE_MINIMUM = 8
DC_FIELD_TEMPERATURE_MAXIMUM = 9
DC_FIELD_TANK_COUNT = 10
DC_FIELD_TANK = 11
DC_FIELD_DIVEMODE = 12

DC_SAMPLE_TIME = 0
DC_SAMPLE_DEPTH = 1
DC_SAMPLE_PRESSURE = 2
DC_SAMPLE_TEMPERATURE = 3
DC_SAMPLE_EVENT = 4
DC_SAMPLE_RBT = 5
DC_SAMPLE_HEARTBEAT = 6
DC_SAMPLE_BEARING = 7
DC_SAMPLE_VENDOR = 8
DC_SAMPLE_SETPOINT = 9
DC_SAMPLE_PPO2 = 10
DC_SAMPLE_CNS = 11
DC_SAMPLE_DECO = 12
DC_SAMPLE_GASMIX = 13

OFFICIAL_SUPPORTED_BRANDS = [
    "Aeris",
    "Apeks",
    "Aqualung",
    "Atomic Aquatics",
    "Beuchat",
    "Citizen",
    "Cochran",
    "Cressi",
    "Crest",
    "Deepblu",
    "Deep Six",
    "Dive Rite",
    "Divesoft",
    "DiveSystem",
    "Genesis",
    "Halcyon",
    "Heinrichs Weikamp",
    "Hollis",
    "Liquivision",
    "Mares",
    "McLean",
    "Oceanic",
    "Oceans",
    "Ratio",
    "Reefnet",
    "Scorpena",
    "Scubapro",
    "Seac",
    "Seemann",
    "Shearwater",
    "Sherwood",
    "Sporasub",
    "Subgear",
    "Suunto",
    "Tecdiving",
    "Tusa",
    "Uwatec",
    "Zeagle",
]

APP_VERSION = "v0.1.0"


# ----------------------------
# ctypes type declarations
# ----------------------------

dc_ticks_t = c_longlong


class dc_context_t(Structure):
    pass


class dc_descriptor_t(Structure):
    pass


class dc_iterator_t(Structure):
    pass


class dc_iostream_t(Structure):
    pass


class dc_device_t(Structure):
    pass


class dc_parser_t(Structure):
    pass


class dc_datetime_t(Structure):
    _fields_ = [
        ("year", c_int),
        ("month", c_int),
        ("day", c_int),
        ("hour", c_int),
        ("minute", c_int),
        ("second", c_int),
        ("timezone", c_int),
    ]


class dc_gasmix_t(Structure):
    _fields_ = [
        ("oxygen", c_double),
        ("helium", c_double),
        ("nitrogen", c_double),
    ]


class dc_salinity_t(Structure):
    _fields_ = [
        ("type", c_uint),
        ("density", c_double),
    ]


class dc_tank_t(Structure):
    _fields_ = [
        ("gasmix", c_uint),
        ("type", c_uint),
        ("volume", c_double),
        ("workpressure", c_double),
        ("beginpressure", c_double),
        ("endpressure", c_double),
    ]


class PressureValue(Structure):
    _fields_ = [
        ("tank", c_uint),
        ("value", c_double),
    ]


class PPO2Value(Structure):
    _fields_ = [
        ("sensor", c_uint),
        ("value", c_double),
    ]


class DecoValue(Structure):
    _fields_ = [
        ("type", c_uint),
        ("time", c_uint),
        ("depth", c_double),
        ("tts", c_uint),
    ]


class VendorValue(Structure):
    _fields_ = [
        ("type", c_uint),
        ("size", c_uint),
        ("data", c_void_p),
    ]


class EventValue(Structure):
    _fields_ = [
        ("type", c_uint),
        ("time", c_uint),
        ("flags", c_uint),
        ("value", c_uint),
    ]


class dc_sample_value_t(Union):
    _fields_ = [
        ("time", c_uint),
        ("depth", c_double),
        ("pressure", PressureValue),
        ("temperature", c_double),
        ("event", EventValue),
        ("rbt", c_uint),
        ("heartbeat", c_uint),
        ("bearing", c_uint),
        ("vendor", VendorValue),
        ("setpoint", c_double),
        ("ppo2", PPO2Value),
        ("cns", c_double),
        ("deco", DecoValue),
        ("gasmix", c_uint),
    ]


# Callback types
DC_DIVE_CALLBACK = ctypes.CFUNCTYPE(
    c_int, POINTER(c_ubyte), c_uint, POINTER(c_ubyte), c_uint, c_void_p
)
DC_SAMPLE_CALLBACK = ctypes.CFUNCTYPE(
    None, c_int, POINTER(dc_sample_value_t), c_void_p
)


# ----------------------------
# Load library
# ----------------------------

def load_lib() -> ctypes.CDLL:
    if os.name == "nt":
        library_names = ["libdivecomputer.dll"]
    elif sys.platform == "darwin":
        library_names = ["libdivecomputer.dylib", "libdivecomputer.0.dylib"]
    else:
        library_names = ["libdivecomputer.so", "libdivecomputer.so.0"]

    search_dirs = [resource_dir(), executable_dir(), os.getcwd(), *vendored_runtime_dirs()]

    if os.name == "nt" and hasattr(os, "add_dll_directory"):
        seen_dirs: set[str] = set()
        for directory in search_dirs:
            normalized = os.path.normcase(os.path.abspath(directory))
            if normalized in seen_dirs or not os.path.isdir(directory):
                continue
            seen_dirs.add(normalized)
            _DLL_SEARCH_HANDLES.append(os.add_dll_directory(directory))
    elif os.name != "nt":
        seen_files: set[str] = set()
        preload_mode = getattr(ctypes, "RTLD_GLOBAL", 0)
        for directory in search_dirs:
            if not os.path.isdir(directory):
                continue
            for name in os.listdir(directory):
                if name in library_names:
                    continue
                if ".so" not in name and not name.endswith(".dylib"):
                    continue
                candidate = os.path.abspath(os.path.join(directory, name))
                if candidate in seen_files or not os.path.isfile(candidate):
                    continue
                seen_files.add(candidate)
                try:
                    ctypes.CDLL(candidate, mode=preload_mode)
                except OSError:
                    continue

    tried_paths: list[str] = []
    for directory in search_dirs:
        for library_name in library_names:
            candidate = os.path.join(directory, library_name)
            tried_paths.append(candidate)
            if os.path.exists(candidate):
                return ctypes.CDLL(candidate)

    find_library_name = ctypes.util.find_library("divecomputer")
    if find_library_name:
        tried_paths.append(find_library_name)
        try:
            return ctypes.CDLL(find_library_name)
        except OSError:
            pass

    for library_name in library_names:
        tried_paths.append(library_name)
        try:
            return ctypes.CDLL(library_name)
        except OSError:
            continue

    raise RuntimeError(
        "Could not load the libdivecomputer shared library.\n"
        "Make sure it is bundled with the app or installed on the system.\n"
        "Tried:\n- " + "\n- ".join(tried_paths)
    )


LIB = load_lib()


# ----------------------------
# Function signatures
# ----------------------------

LIB.dc_context_new.argtypes = [POINTER(POINTER(dc_context_t))]
LIB.dc_context_new.restype = c_int

LIB.dc_context_free.argtypes = [POINTER(dc_context_t)]
LIB.dc_context_free.restype = c_int

LIB.dc_descriptor_iterator_new.argtypes = [POINTER(POINTER(dc_iterator_t)), POINTER(dc_context_t)]
LIB.dc_descriptor_iterator_new.restype = c_int

LIB.dc_iterator_next.argtypes = [POINTER(dc_iterator_t), c_void_p]
LIB.dc_iterator_next.restype = c_int

LIB.dc_iterator_free.argtypes = [POINTER(dc_iterator_t)]
LIB.dc_iterator_free.restype = c_int

LIB.dc_descriptor_get_vendor.argtypes = [POINTER(dc_descriptor_t)]
LIB.dc_descriptor_get_vendor.restype = c_char_p

LIB.dc_descriptor_get_product.argtypes = [POINTER(dc_descriptor_t)]
LIB.dc_descriptor_get_product.restype = c_char_p

LIB.dc_descriptor_free.argtypes = [POINTER(dc_descriptor_t)]
LIB.dc_descriptor_free.restype = None

LIB.dc_serial_open.argtypes = [POINTER(POINTER(dc_iostream_t)), POINTER(dc_context_t), c_char_p]
LIB.dc_serial_open.restype = c_int

LIB.dc_iostream_close.argtypes = [POINTER(dc_iostream_t)]
LIB.dc_iostream_close.restype = c_int

LIB.dc_device_open.argtypes = [
    POINTER(POINTER(dc_device_t)),
    POINTER(dc_context_t),
    POINTER(dc_descriptor_t),
    POINTER(dc_iostream_t),
]
LIB.dc_device_open.restype = c_int

LIB.dc_device_set_fingerprint.argtypes = [POINTER(dc_device_t), POINTER(c_ubyte), c_uint]
LIB.dc_device_set_fingerprint.restype = c_int

LIB.dc_device_foreach.argtypes = [POINTER(dc_device_t), DC_DIVE_CALLBACK, c_void_p]
LIB.dc_device_foreach.restype = c_int

LIB.dc_device_close.argtypes = [POINTER(dc_device_t)]
LIB.dc_device_close.restype = c_int

LIB.dc_parser_new.argtypes = [
    POINTER(POINTER(dc_parser_t)),
    POINTER(dc_device_t),
    POINTER(c_ubyte),
    c_size_t,
]
LIB.dc_parser_new.restype = c_int

LIB.dc_parser_destroy.argtypes = [POINTER(dc_parser_t)]
LIB.dc_parser_destroy.restype = c_int

LIB.dc_parser_get_datetime.argtypes = [POINTER(dc_parser_t), POINTER(dc_datetime_t)]
LIB.dc_parser_get_datetime.restype = c_int

LIB.dc_parser_get_field.argtypes = [POINTER(dc_parser_t), c_int, c_uint, c_void_p]
LIB.dc_parser_get_field.restype = c_int

LIB.dc_parser_samples_foreach.argtypes = [POINTER(dc_parser_t), DC_SAMPLE_CALLBACK, c_void_p]
LIB.dc_parser_samples_foreach.restype = c_int


def build_dive_record(
    vendor: str,
    product: str,
    fingerprint: bytes | None,
    started_at: str | None,
    duration_seconds: int | None,
    max_depth_m: float | None,
    avg_depth_m: float | None,
    fields: dict,
    raw_data: bytes,
    samples: list[dict],
) -> dict:
    fingerprint_hex = fingerprint.hex() if fingerprint else None
    raw_sha256 = hashlib.sha256(raw_data).hexdigest()
    return {
        "vendor": vendor,
        "product": product,
        "fingerprint_hex": fingerprint_hex,
        "dive_uid": f"{vendor}:{product}:{fingerprint_hex or raw_sha256}",
        "started_at": started_at,
        "duration_seconds": duration_seconds,
        "max_depth_m": max_depth_m,
        "avg_depth_m": avg_depth_m,
        "fields": fields,
        "raw_sha256": raw_sha256,
        "raw_data_b64": base64.b64encode(raw_data).decode("ascii"),
        "samples": samples,
    }


class BackendDiveStore:
    def __init__(self, base_url: str, auth_token: str | None = None) -> None:
        self.base_url = base_url.rstrip("/")
        self.auth_token = normalize_bearer_token(auth_token)

    def _request_json(self, method: str, path: str, payload: dict | None = None, query: dict | None = None) -> dict:
        url = f"{self.base_url}{path}"
        if query:
            url = f"{url}?{parse.urlencode(query)}"

        data = None
        headers = {
            "Accept": "application/json",
            "User-Agent": "mares-smart-air-sync/1.0",
        }
        if self.auth_token:
            headers["Authorization"] = f"Bearer {self.auth_token}"
        if payload is not None:
            data = json.dumps(payload).encode("utf-8")
            headers["Content-Type"] = "application/json"

        req = request.Request(url, data=data, method=method, headers=headers)
        try:
            with request.urlopen(req, timeout=30) as response:
                body = response.read()
        except error.HTTPError as exc:
            details = exc.read().decode("utf-8", errors="replace")
            if exc.code in {401, 403}:
                raise RuntimeError(
                    f"Backend authentication failed: {method} {url} -> {exc.code} {details}. "
                    "Provide a valid desktop sync token or Clerk session token with --backend-auth-token, "
                    "--backend-auth-token-file, or BACKEND_AUTH_TOKEN."
                ) from exc
            raise RuntimeError(f"Backend request failed: {method} {url} -> {exc.code} {details}") from exc
        except error.URLError as exc:
            raise RuntimeError(f"Could not reach backend at {self.base_url}: {exc.reason}") from exc

        if not body:
            return {}
        return json.loads(body.decode("utf-8"))

    def get_saved_fingerprint(self, vendor: str, product: str) -> bytes | None:
        payload = self._request_json(
            "GET",
            "/api/device-state",
            query={"vendor": vendor, "product": product},
        )
        fingerprint_hex = payload.get("fingerprint_hex")
        return bytes.fromhex(fingerprint_hex) if fingerprint_hex else None

    def save_fingerprint(self, vendor: str, product: str, fp: bytes | None) -> None:
        self._request_json(
            "PUT",
            "/api/device-state",
            payload={
                "vendor": vendor,
                "product": product,
                "fingerprint_hex": fp.hex() if fp else None,
            },
        )

    def insert_dive_record(self, record: dict) -> bool:
        payload = self._request_json("POST", "/api/dives", payload=record)
        return bool(payload.get("inserted"))

    def count_dives(self) -> int | None:
        try:
            payload = self._request_json(
                "GET",
                "/api/dives",
                query={
                    "limit": 1,
                    "offset": 0,
                    "include_samples": "false",
                    "include_raw_data": "false",
                },
            )
        except RuntimeError:
            return None

        total = payload.get("total")
        return total if isinstance(total, int) and total >= 0 else None

    def close(self) -> None:
        return None


def load_auth_token(token: str | None = None, token_file: str | None = None) -> str | None:
    if token:
        return normalize_bearer_token(token)
    if token_file:
        with open(token_file, "r", encoding="utf-8") as handle:
            return normalize_bearer_token(handle.read())
    env_token = os.getenv("BACKEND_AUTH_TOKEN") or os.getenv("CLERK_SESSION_TOKEN")
    return normalize_bearer_token(env_token)


def normalize_bearer_token(token: str | None) -> str | None:
    if not token:
        return None
    normalized = token.strip()
    if len(normalized) >= 2 and normalized[0] == normalized[-1] and normalized[0] in {'"', "'"}:
        normalized = normalized[1:-1].strip()
    if normalized.lower().startswith("bearer "):
        normalized = normalized.split(" ", 1)[1].strip()
    return normalized or None


def request_backend_json(
    base_url: str,
    method: str,
    path: str,
    *,
    payload: dict | None = None,
    query: dict | None = None,
    auth_token: str | None = None,
    timeout: int = 30,
) -> dict:
    url = f"{base_url.rstrip('/')}{path}"
    if query:
        url = f"{url}?{parse.urlencode(query)}"

    data = None
    headers = {
        "Accept": "application/json",
        "User-Agent": "mares-smart-air-sync/1.0",
    }
    token = normalize_bearer_token(auth_token)
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"

    req = request.Request(url, data=data, method=method, headers=headers)
    try:
        with request.urlopen(req, timeout=timeout) as response:
            body = response.read()
    except error.HTTPError as exc:
        details = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"Backend request failed: {method} {url} -> {exc.code} {details}") from exc
    except error.URLError as exc:
        raise RuntimeError(f"Could not reach backend at {base_url}: {exc.reason}") from exc

    if not body:
        return {}
    return json.loads(body.decode("utf-8"))


def create_cli_auth_request(base_url: str) -> dict:
    return request_backend_json(base_url, "POST", "/api/cli-auth/request")


def poll_cli_auth_request(base_url: str, code: str) -> dict:
    return request_backend_json(base_url, "GET", "/api/cli-auth/request", query={"code": code})


def build_cli_auth_url(base_url: str, code: str) -> str:
    return f"{base_url.rstrip('/')}/#settings/cli-auth/{parse.quote(code)}"


def list_serial_port_infos() -> list[dict[str, str]]:
    if list_ports is not None:
        ports = []
        for port in list_ports.comports():
            summary_parts = [part.strip() for part in (port.description, port.manufacturer) if part and part.strip()]
            ports.append(
                {
                    "device": port.device,
                    "summary": " | ".join(summary_parts),
                }
            )
        return ports
    if os.name == "nt":
        return [{"device": f"COM{index}", "summary": ""} for index in range(1, 17)]
    return []


def list_serial_ports() -> list[str]:
    return [port["device"] for port in list_serial_port_infos()]


def descriptor_strings(descriptor: POINTER(dc_descriptor_t)) -> tuple[str, str]:
    vendor_ptr = LIB.dc_descriptor_get_vendor(descriptor)
    product_ptr = LIB.dc_descriptor_get_product(descriptor)
    vendor = vendor_ptr.decode("utf-8") if vendor_ptr else ""
    product = product_ptr.decode("utf-8") if product_ptr else ""
    return vendor, product


def format_dive_computer_name(vendor: str, product: str) -> str:
    parts = [part.strip() for part in (vendor, product) if part and part.strip()]
    return " ".join(parts) or "Unknown device"


def load_supported_dive_computers() -> dict[str, list[str]]:
    grouped: dict[str, list[str]] = {}
    context = POINTER(dc_context_t)()
    status = LIB.dc_context_new(byref(context))
    if status != DC_STATUS_SUCCESS:
        raise RuntimeError(f"dc_context_new failed with libdivecomputer status={status}")

    iterator = POINTER(dc_iterator_t)()
    status = LIB.dc_descriptor_iterator_new(byref(iterator), context)
    if status != DC_STATUS_SUCCESS:
        if context:
            LIB.dc_context_free(context)
        raise RuntimeError(f"dc_descriptor_iterator_new failed with libdivecomputer status={status}")

    try:
        while True:
            descriptor = POINTER(dc_descriptor_t)()
            rc = LIB.dc_iterator_next(iterator, byref(descriptor))
            if rc == DC_STATUS_DONE:
                break
            if rc != DC_STATUS_SUCCESS:
                raise RuntimeError(f"dc_iterator_next(descriptor) failed with libdivecomputer status={rc}")

            try:
                vendor, product = descriptor_strings(descriptor)
                if not vendor or not product:
                    continue
                models = grouped.setdefault(vendor, [])
                if product not in models:
                    models.append(product)
            finally:
                LIB.dc_descriptor_free(descriptor)
    finally:
        LIB.dc_iterator_free(iterator)
        if context:
            LIB.dc_context_free(context)

    supported: dict[str, list[str]] = {}
    for vendor in OFFICIAL_SUPPORTED_BRANDS:
        models = grouped.get(vendor)
        if models:
            supported[vendor] = models
    return supported


SUPPORTED_DIVE_COMPUTERS = load_supported_dive_computers()


def probe_descriptor_on_port(
    context: POINTER(dc_context_t),
    descriptor: POINTER(dc_descriptor_t),
    port: str,
    *,
    require_download: bool = False,
) -> bool:
    iostream = POINTER(dc_iostream_t)()
    device = POINTER(dc_device_t)()

    try:
        serial_status = LIB.dc_serial_open(byref(iostream), context, port.encode("utf-8"))
        if serial_status != DC_STATUS_SUCCESS:
            return False

        device_status = LIB.dc_device_open(byref(device), context, descriptor, iostream)
        if device_status != DC_STATUS_SUCCESS:
            return False

        if not require_download:
            return True

        def stop_after_first_dive(
            _data: POINTER(c_ubyte),
            _size: int,
            _fingerprint: POINTER(c_ubyte),
            _fsize: int,
            _userdata: int,
        ) -> int:
            return 0

        dive_cb = DC_DIVE_CALLBACK(stop_after_first_dive)
        foreach_status = LIB.dc_device_foreach(device, dive_cb, None)
        return foreach_status == DC_STATUS_SUCCESS
    finally:
        if device:
            LIB.dc_device_close(device)
        if iostream:
            LIB.dc_iostream_close(iostream)


def scan_supported_serial_ports(
    vendor: str,
    product: str,
    candidate_ports: list[str] | None = None,
) -> list[str]:
    ports = candidate_ports or list_serial_ports()
    if not ports:
        return []

    context = POINTER(dc_context_t)()
    check(LIB.dc_context_new(byref(context)), "dc_context_new")
    descriptor = None

    try:
        descriptor = find_descriptor(context, vendor, product)
        return [
            port
            for port in ports
            if probe_descriptor_on_port(context, descriptor, port, require_download=True)
        ]
    finally:
        if descriptor:
            LIB.dc_descriptor_free(descriptor)
        if context:
            LIB.dc_context_free(context)


def auto_detect_port(vendor: str, product: str) -> str:
    ports = list_serial_ports()
    if not ports:
        raise RuntimeError("No serial ports found while scanning for a supported dive computer.")

    supported_ports = scan_supported_serial_ports(vendor, product, candidate_ports=ports)
    if not supported_ports:
        raise RuntimeError(f"No supported {vendor} {product} dive computer detected on any serial port.")
    if len(supported_ports) > 1:
        raise RuntimeError(
            f"Multiple supported {vendor} {product} dive computers detected on: {', '.join(supported_ports)}. "
            "Specify --port explicitly."
        )
    return supported_ports[0]


# ----------------------------
# Helpers
# ----------------------------

def check(status: int, what: str) -> None:
    if status != DC_STATUS_SUCCESS:
        raise RuntimeError(f"{what} failed with libdivecomputer status={status}")


def get_parser_field(parser: POINTER(dc_parser_t), field_type: int, value, flags: int = 0):
    status = LIB.dc_parser_get_field(parser, field_type, flags, byref(value))
    if status == DC_STATUS_SUCCESS:
        return value
    return None


def get_uint_parser_field(parser: POINTER(dc_parser_t), field_type: int, flags: int = 0) -> int | None:
    value = get_parser_field(parser, field_type, c_uint(), flags)
    return int(value.value) if value is not None else None


def get_double_parser_field(parser: POINTER(dc_parser_t), field_type: int, flags: int = 0) -> float | None:
    value = get_parser_field(parser, field_type, c_double(), flags)
    return float(value.value) if value is not None else None


def extract_dive_fields(parser: POINTER(dc_parser_t)) -> dict:
    fields = {
        "divetime_seconds": get_uint_parser_field(parser, DC_FIELD_DIVETIME),
        "max_depth_m": get_double_parser_field(parser, DC_FIELD_MAXDEPTH),
        "avg_depth_m": get_double_parser_field(parser, DC_FIELD_AVGDEPTH),
        "gasmix_count": get_uint_parser_field(parser, DC_FIELD_GASMIX_COUNT),
        "gasmixes": None,
        "salinity": None,
        "atmospheric_bar": get_double_parser_field(parser, DC_FIELD_ATMOSPHERIC),
        "temperature_surface_c": get_double_parser_field(parser, DC_FIELD_TEMPERATURE_SURFACE),
        "temperature_minimum_c": get_double_parser_field(parser, DC_FIELD_TEMPERATURE_MINIMUM),
        "temperature_maximum_c": get_double_parser_field(parser, DC_FIELD_TEMPERATURE_MAXIMUM),
        "tank_count": get_uint_parser_field(parser, DC_FIELD_TANK_COUNT),
        "tanks": None,
        "dive_mode_code": get_uint_parser_field(parser, DC_FIELD_DIVEMODE),
    }

    if fields["gasmix_count"] is not None:
        gasmixes = []
        for index in range(fields["gasmix_count"]):
            gasmix = get_parser_field(parser, DC_FIELD_GASMIX, dc_gasmix_t(), index)
            if gasmix is None:
                gasmixes.append(None)
                continue
            gasmixes.append(
                {
                    "index": index,
                    "oxygen_fraction": float(gasmix.oxygen),
                    "helium_fraction": float(gasmix.helium),
                    "nitrogen_fraction": float(gasmix.nitrogen),
                }
            )
        fields["gasmixes"] = gasmixes

    salinity = get_parser_field(parser, DC_FIELD_SALINITY, dc_salinity_t())
    if salinity is not None:
        fields["salinity"] = {
            "type_code": int(salinity.type),
            "density": float(salinity.density),
        }

    if fields["tank_count"] is not None:
        tanks = []
        for index in range(fields["tank_count"]):
            tank = get_parser_field(parser, DC_FIELD_TANK, dc_tank_t(), index)
            if tank is None:
                tanks.append(None)
                continue
            tanks.append(
                {
                    "index": index,
                    "gasmix_index": int(tank.gasmix),
                    "type_code": int(tank.type),
                    "volume": float(tank.volume),
                    "workpressure_bar": float(tank.workpressure),
                    "beginpressure_bar": float(tank.beginpressure),
                    "endpressure_bar": float(tank.endpressure),
                }
            )
        fields["tanks"] = tanks

    return fields


def find_descriptor(context: POINTER(dc_context_t), vendor: str, product: str) -> POINTER(dc_descriptor_t):
    it = POINTER(dc_iterator_t)()
    check(LIB.dc_descriptor_iterator_new(byref(it), context), "dc_descriptor_iterator_new")

    try:
        while True:
            desc = POINTER(dc_descriptor_t)()
            rc = LIB.dc_iterator_next(it, byref(desc))
            if rc == DC_STATUS_DONE:
                break
            check(rc, "dc_iterator_next(descriptor)")

            v_str, p_str = descriptor_strings(desc)

            if v_str == vendor and p_str == product:
                return desc  # caller owns this descriptor

            LIB.dc_descriptor_free(desc)

    finally:
        LIB.dc_iterator_free(it)

    raise RuntimeError(f"Could not find descriptor for {vendor} {product}")


def dt_to_iso(dt: dc_datetime_t) -> str:
    # timezone may be unknown / absent depending on device.
    # Store naive local-style timestamp string if timezone is unavailable.
    return f"{dt.year:04d}-{dt.month:02d}-{dt.day:02d}T{dt.hour:02d}:{dt.minute:02d}:{dt.second:02d}"


# ----------------------------
# Import state passed through callback
# ----------------------------

class ImportState:
    def __init__(
        self,
        store,
        device: POINTER(dc_device_t),
        vendor: str,
        product: str,
        progress_callback: Callable[[int, int], None] | None = None,
    ) -> None:
        self.store = store
        self.device = device
        self.vendor = vendor
        self.product = product
        self.progress_callback = progress_callback
        self.first_fingerprint: bytes | None = None
        self.imported = 0
        self.skipped = 0

    def report_progress(self) -> None:
        if self.progress_callback is not None:
            self.progress_callback(self.imported, self.skipped)


# Keep callback objects alive
_SAMPLE_CBS: list = []
_DIVE_CBS: list = []


def new_sample_row() -> dict:
    return {
        "time_seconds": None,
        "depth_m": None,
        "temperature_c": None,
        "tank_pressure_bar": {},
        "events": [],
        "rbt_seconds": None,
        "heartbeat_bpm": None,
        "bearing_degrees": None,
        "vendor_samples": [],
        "setpoint_bar": None,
        "ppo2_bar": {},
        "cns_fraction": None,
        "deco": None,
        "gasmix_index": None,
    }


def make_sample_collector(samples: list[dict]):
    current = new_sample_row()

    def flush_current() -> None:
        if any(v not in (None, {}, []) for v in current.values()):
            row = {
                "time_seconds": current["time_seconds"],
                "depth_m": current["depth_m"],
                "temperature_c": current["temperature_c"],
                "tank_pressure_bar": current["tank_pressure_bar"] or None,
                "events": current["events"] or None,
                "rbt_seconds": current["rbt_seconds"],
                "heartbeat_bpm": current["heartbeat_bpm"],
                "bearing_degrees": current["bearing_degrees"],
                "vendor_samples": current["vendor_samples"] or None,
                "setpoint_bar": current["setpoint_bar"],
                "ppo2_bar": current["ppo2_bar"] or None,
                "cns_fraction": current["cns_fraction"],
                "deco": current["deco"],
                "gasmix_index": current["gasmix_index"],
            }
            samples.append(row)

    def sample_cb(sample_type: int, value_ptr: POINTER(dc_sample_value_t), _userdata: c_void_p) -> None:
        nonlocal current
        val = value_ptr.contents

        if sample_type == DC_SAMPLE_TIME:
            # Start a new row when time advances.
            if current["time_seconds"] is not None:
                flush_current()
                current = new_sample_row()
            current["time_seconds"] = int(val.time)

        elif sample_type == DC_SAMPLE_DEPTH:
            current["depth_m"] = float(val.depth)

        elif sample_type == DC_SAMPLE_TEMPERATURE:
            current["temperature_c"] = float(val.temperature)

        elif sample_type == DC_SAMPLE_PRESSURE:
            current["tank_pressure_bar"][str(int(val.pressure.tank))] = float(val.pressure.value)

        elif sample_type == DC_SAMPLE_EVENT:
            current["events"].append(
                {
                    "type_code": int(val.event.type),
                    "time_seconds": int(val.event.time),
                    "flags": int(val.event.flags),
                    "value": int(val.event.value),
                }
            )

        elif sample_type == DC_SAMPLE_RBT:
            current["rbt_seconds"] = int(val.rbt)

        elif sample_type == DC_SAMPLE_HEARTBEAT:
            current["heartbeat_bpm"] = int(val.heartbeat)

        elif sample_type == DC_SAMPLE_BEARING:
            current["bearing_degrees"] = int(val.bearing)

        elif sample_type == DC_SAMPLE_VENDOR:
            size = int(val.vendor.size)
            data_hex = None
            if val.vendor.data and size > 0:
                data_hex = ctypes.string_at(val.vendor.data, size).hex()
            current["vendor_samples"].append(
                {
                    "type_code": int(val.vendor.type),
                    "size": size,
                    "data_hex": data_hex,
                }
            )

        elif sample_type == DC_SAMPLE_SETPOINT:
            current["setpoint_bar"] = float(val.setpoint)

        elif sample_type == DC_SAMPLE_PPO2:
            current["ppo2_bar"][str(int(val.ppo2.sensor))] = float(val.ppo2.value)

        elif sample_type == DC_SAMPLE_CNS:
            current["cns_fraction"] = float(val.cns)

        elif sample_type == DC_SAMPLE_DECO:
            current["deco"] = {
                "type_code": int(val.deco.type),
                "time_seconds": int(val.deco.time),
                "depth_m": float(val.deco.depth),
                "tts_seconds": int(val.deco.tts),
            }

        elif sample_type == DC_SAMPLE_GASMIX:
            current["gasmix_index"] = int(val.gasmix)

    cb = DC_SAMPLE_CALLBACK(sample_cb)
    _SAMPLE_CBS.append(cb)
    return cb, flush_current


def make_dive_callback() -> DC_DIVE_CALLBACK:
    def dive_cb(
        data_ptr: POINTER(c_ubyte),
        size: int,
        fingerprint_ptr: POINTER(c_ubyte),
        fsize: int,
        userdata: c_void_p,
    ) -> int:
        try:
            state = cast(userdata, POINTER(py_object)).contents.value

            raw_data = ctypes.string_at(data_ptr, size)
            fingerprint = ctypes.string_at(fingerprint_ptr, fsize) if fingerprint_ptr and fsize > 0 else None

            # Per libdivecomputer docs, save the fingerprint from the first (newest) downloaded dive.
            if state.first_fingerprint is None and fingerprint:
                state.first_fingerprint = fingerprint

            parser = POINTER(dc_parser_t)()
            raw_array = (c_ubyte * len(raw_data)).from_buffer_copy(raw_data)
            check(
                LIB.dc_parser_new(byref(parser), state.device, raw_array, len(raw_data)),
                "dc_parser_new",
            )

            try:
                dt = dc_datetime_t()
                started_at = None
                if LIB.dc_parser_get_datetime(parser, byref(dt)) == DC_STATUS_SUCCESS:
                    started_at = dt_to_iso(dt)

                fields = extract_dive_fields(parser)
                duration_seconds = fields["divetime_seconds"]
                max_depth_m = fields["max_depth_m"]
                avg_depth_m = fields["avg_depth_m"]

                samples: list[dict] = []
                sample_cb, finalize_samples = make_sample_collector(samples)
                check(LIB.dc_parser_samples_foreach(parser, sample_cb, None), "dc_parser_samples_foreach")
                finalize_samples()

                record = build_dive_record(
                    state.vendor,
                    state.product,
                    fingerprint,
                    started_at,
                    duration_seconds,
                    max_depth_m,
                    avg_depth_m,
                    fields,
                    raw_data,
                    samples,
                )
                inserted = state.store.insert_dive_record(record)
                if inserted:
                    state.imported += 1
                else:
                    state.skipped += 1
                state.report_progress()

            finally:
                LIB.dc_parser_destroy(parser)

            return 1  # continue iteration

        except Exception as exc:
            print(f"ERROR in dive callback: {exc}", file=sys.stderr)
            return 0  # stop iteration

    cb = DC_DIVE_CALLBACK(dive_cb)
    _DIVE_CBS.append(cb)
    return cb


# ----------------------------
# Main sync routine
# ----------------------------

def sync_dives(
    port: str,
    vendor: str = "Mares",
    product: str = "Smart Air",
    backend_url: str | None = None,
    backend_auth_token: str | None = None,
    progress_callback: Callable[[int, int], None] | None = None,
) -> dict[str, int]:
    if not backend_url:
        raise ValueError("Provide --backend-url.")

    store = BackendDiveStore(backend_url, auth_token=backend_auth_token)

    existing_total = store.count_dives() if hasattr(store, "count_dives") else None

    context = POINTER(dc_context_t)()
    check(LIB.dc_context_new(byref(context)), "dc_context_new")

    descriptor = None
    iostream = POINTER(dc_iostream_t)()
    device = POINTER(dc_device_t)()

    try:
        descriptor = find_descriptor(context, vendor, product)

        check(LIB.dc_serial_open(byref(iostream), context, port.encode("utf-8")), "dc_serial_open")
        check(LIB.dc_device_open(byref(device), context, descriptor, iostream), "dc_device_open")

        saved_fp = store.get_saved_fingerprint(vendor, product)
        if saved_fp:
            fp_buf = (c_ubyte * len(saved_fp)).from_buffer_copy(saved_fp)
            check(
                LIB.dc_device_set_fingerprint(device, fp_buf, len(saved_fp)),
                "dc_device_set_fingerprint",
            )

        state = ImportState(store, device, vendor, product, progress_callback=progress_callback)
        state_box = py_object(state)
        state_ptr = ctypes.pointer(state_box)

        dive_cb = make_dive_callback()
        check(LIB.dc_device_foreach(device, dive_cb, cast(state_ptr, c_void_p)), "dc_device_foreach")

        # Save newest fingerprint for next run.
        if state.first_fingerprint is not None:
            store.save_fingerprint(vendor, product, state.first_fingerprint)

        result = {"imported": state.imported, "skipped": state.skipped}
        if isinstance(existing_total, int):
            result["existing_total"] = existing_total
        return result

    finally:
        if device:
            LIB.dc_device_close(device)
        if iostream:
            LIB.dc_iostream_close(iostream)
        if descriptor:
            LIB.dc_descriptor_free(descriptor)
        if context:
            LIB.dc_context_free(context)
        store.close()


class SyncDesktopApp:
    def __init__(self, root: tk.Tk, defaults: dict[str, str]) -> None:
        self.root = root
        self.root.title("Dive Sync")
        self.root.geometry("1180x820")
        self.root.resizable(False, False)
        self._icon_image: tk.PhotoImage | None = None
        self._runtime_icon_path: str | None = None
        self._configure_window_icon()

        self.events: Queue[tuple[str, object]] = Queue()
        self.auth_token: str | None = load_auth_token()
        self.auth_token_expires_at: int | None = None
        self.current_code: str | None = None

        self.backend_url_var = tk.StringVar(value=defaults.get("backend_url") or "http://localhost:8000")
        default_vendor = defaults.get("vendor") or next(iter(SUPPORTED_DIVE_COMPUTERS))
        default_models = SUPPORTED_DIVE_COMPUTERS.get(default_vendor, [])
        default_product = defaults.get("product") or (default_models[0] if default_models else "")
        self.vendor_var = tk.StringVar(value=default_vendor)
        self.product_var = tk.StringVar(value=default_product)
        self.port_var = tk.StringVar(value=defaults.get("port") or "")
        self.detected_device_var = tk.StringVar(value="Not detected")
        self.status_var = tk.StringVar(value="Ready. Sign in to the backend to start syncing.")
        self.auth_var = tk.StringVar(value="Desktop sync token already loaded." if self.auth_token else "Not signed in")
        self.step1_var = tk.StringVar(value="Choose your dive computer model and scan for it.")
        self.step2_var = tk.StringVar(value="Sign in after a dive computer has been detected.")
        self.step3_var = tk.StringVar(value="Start sync after detection and backend login are complete.")
        self.scan_in_progress = False
        self.login_in_progress = False
        self.sync_in_progress = False
        self.sync_imported = 0
        self.sync_skipped = 0
        self.sync_existing_total: int | None = None
        self.detected_devices_by_port: dict[str, dict[str, str]] = {}
        self.ui_ready = False

        self._configure_theme()
        self._build_ui()
        self._sync_model_options()
        self._update_ui_state()
        self.vendor_var.trace_add("write", self._handle_vendor_change)
        self.product_var.trace_add("write", self._handle_product_change)
        self.port_var.trace_add("write", self._handle_port_change)
        self.ui_ready = True
        self.root.after(150, self._pump_events)

    def _configure_window_icon(self) -> None:
        icon_path = os.path.join(resource_dir(), "logo.png")
        if os.path.exists(icon_path):
            try:
                self._icon_image = tk.PhotoImage(file=icon_path)
                self.root.iconphoto(True, self._icon_image)
            except tk.TclError:
                self._icon_image = None

        if os.name == "nt":
            self._runtime_icon_path = ensure_runtime_icon_path()
            self._apply_windows_titlebar_icon()
            self.root.after_idle(self._apply_windows_titlebar_icon)
            self.root.after(250, self._apply_windows_titlebar_icon)

    def _apply_windows_titlebar_icon(self) -> None:
        if os.name != "nt" or not self._runtime_icon_path:
            return
        try:
            self.root.iconbitmap(self._runtime_icon_path)
        except tk.TclError:
            return

    def _configure_theme(self) -> None:
        self.colors = {
            "bg": "#071f31",
            "panel": "#0b2840",
            "panel_alt": "#173148",
            "panel_muted": "#16324a",
            "panel_dark": "#06192a",
            "border": "#173752",
            "text": "#d8e8ff",
            "muted": "#8ea8c2",
            "accent": "#8db8ef",
            "accent_soft": "#253f59",
            "accent_strong": "#92bdf1",
            "divider": "#2a4762",
            "warning": "#ffbc79",
            "disabled": "#47627f",
            "success": "#9cc8ff",
        }
        self.root.configure(bg=self.colors["bg"])

        style = ttk.Style(self.root)
        style.theme_use("clam")

        self.root.option_add("*TCombobox*Listbox.Background", self.colors["panel_muted"])
        self.root.option_add("*TCombobox*Listbox.Foreground", self.colors["text"])
        self.root.option_add("*TCombobox*Listbox.Font", ("Segoe UI", 12))

        style.configure("Shell.TFrame", background=self.colors["bg"])
        style.configure("Panel.TFrame", background=self.colors["panel"], borderwidth=0, relief="flat")
        style.configure("PanelAlt.TFrame", background=self.colors["panel_alt"], borderwidth=0, relief="flat")
        style.configure("Stat.TFrame", background=self.colors["panel"])
        style.configure("Divider.TFrame", background=self.colors["divider"])
        style.configure("Title.TLabel", background=self.colors["bg"], foreground=self.colors["accent"], font=("Segoe UI Semibold", 42))
        style.configure("Brand.TLabel", background=self.colors["bg"], foreground=self.colors["text"], font=("Segoe UI Semibold", 18))
        style.configure("Subtitle.TLabel", background=self.colors["bg"], foreground="#c7d7ea", font=("Segoe UI", 15))
        style.configure("Section.TLabel", background=self.colors["panel"], foreground=self.colors["text"], font=("Segoe UI Semibold", 17))
        style.configure("SectionAlt.TLabel", background=self.colors["panel_alt"], foreground=self.colors["text"], font=("Segoe UI Semibold", 17))
        style.configure("Field.TLabel", background=self.colors["panel"], foreground=self.colors["muted"], font=("Consolas", 10))
        style.configure("FieldAlt.TLabel", background=self.colors["panel_alt"], foreground=self.colors["muted"], font=("Consolas", 10))
        style.configure("Body.TLabel", background=self.colors["panel_alt"], foreground=self.colors["text"], font=("Segoe UI", 12))
        style.configure("Muted.TLabel", background=self.colors["bg"], foreground=self.colors["muted"], font=("Consolas", 10))
        style.configure("Status.TLabel", background=self.colors["bg"], foreground=self.colors["muted"], font=("Segoe UI", 10))
        style.configure("MetricTitle.TLabel", background=self.colors["panel"], foreground=self.colors["muted"], font=("Consolas", 9))
        style.configure("MetricValue.TLabel", background=self.colors["panel"], foreground=self.colors["accent"], font=("Segoe UI Light", 18))
        style.configure(
            "Dive.TCombobox",
            foreground=self.colors["text"],
            fieldbackground=self.colors["panel_muted"],
            background=self.colors["panel_muted"],
            borderwidth=0,
            bordercolor=self.colors["panel_muted"],
            lightcolor=self.colors["panel_muted"],
            darkcolor=self.colors["panel_muted"],
            arrowsize=18,
            padding=(14, 12),
        )
        style.map(
            "Dive.TCombobox",
            fieldbackground=[("readonly", self.colors["panel_muted"]), ("disabled", self.colors["panel_dark"])],
            foreground=[("readonly", self.colors["text"]), ("disabled", self.colors["disabled"])],
            arrowcolor=[("readonly", self.colors["text"]), ("disabled", self.colors["disabled"])],
            selectbackground=[("readonly", self.colors["panel_muted"])],
            selectforeground=[("readonly", self.colors["text"])],
        )
        style.configure(
            "Dive.TEntry",
            foreground=self.colors["accent"],
            fieldbackground=self.colors["panel_dark"],
            background=self.colors["panel_dark"],
            borderwidth=0,
            bordercolor=self.colors["panel_dark"],
            lightcolor=self.colors["panel_dark"],
            darkcolor=self.colors["panel_dark"],
            padding=(14, 12),
        )
        style.map(
            "Dive.TEntry",
            fieldbackground=[("readonly", self.colors["panel_dark"]), ("disabled", self.colors["panel_dark"])],
            foreground=[("readonly", self.colors["accent"]), ("disabled", self.colors["disabled"])],
        )
        style.configure(
            "Dark.TEntry",
            foreground=self.colors["accent"],
            fieldbackground=self.colors["panel_dark"],
            background=self.colors["panel_dark"],
            borderwidth=0,
            bordercolor=self.colors["panel_dark"],
            lightcolor=self.colors["panel_dark"],
            darkcolor=self.colors["panel_dark"],
            padding=(14, 12),
        )
        style.configure(
            "Primary.TButton",
            font=("Consolas", 12, "bold"),
            foreground=self.colors["panel_dark"],
            background=self.colors["accent_strong"],
            borderwidth=0,
            bordercolor=self.colors["accent_strong"],
            lightcolor=self.colors["accent_strong"],
            darkcolor=self.colors["accent_strong"],
            padding=(18, 14),
        )
        style.map(
            "Primary.TButton",
            background=[("disabled", self.colors["accent_soft"]), ("active", "#a9cbf5")],
            foreground=[("disabled", "#6d87a2"), ("active", self.colors["panel_dark"])],
        )
        style.configure(
            "Secondary.TButton",
            font=("Consolas", 11, "bold"),
            foreground=self.colors["text"],
            background=self.colors["accent_soft"],
            borderwidth=0,
            bordercolor=self.colors["accent_soft"],
            lightcolor=self.colors["accent_soft"],
            darkcolor=self.colors["accent_soft"],
            padding=(16, 12),
        )
        style.map(
            "Secondary.TButton",
            background=[("disabled", self.colors["panel_muted"]), ("active", "#34506d")],
            foreground=[("disabled", self.colors["disabled"]), ("active", self.colors["text"])],
        )

    def _build_ui(self) -> None:
        frame = ttk.Frame(self.root, padding=(28, 26, 28, 20), style="Shell.TFrame")
        frame.pack(fill="both", expand=True)
        frame.columnconfigure(0, weight=3)
        frame.columnconfigure(1, weight=2)

        header = ttk.Frame(frame, style="Shell.TFrame")
        header.grid(row=0, column=0, columnspan=2, sticky="ew")
        header.columnconfigure(0, weight=1)
        ttk.Label(header, text="DIVEVAULT", style="Brand.TLabel").grid(row=0, column=0, sticky="w")
        ttk.Label(
            header,
            text="Upload Dive Entries to DiveVault.",
            style="Subtitle.TLabel",
            justify="left",
        ).grid(row=2, column=0, sticky="w", pady=(0, 28))

        step1_panel = ttk.Frame(frame, padding=24, style="Panel.TFrame")
        step1_panel.grid(row=1, column=0, sticky="nsew", padx=(0, 18))
        step1_panel.columnconfigure(0, weight=1)
        step1_panel.columnconfigure(1, weight=1)
        header1 = ttk.Frame(step1_panel, style="Panel.TFrame")
        header1.grid(row=0, column=0, columnspan=2, sticky="ew", pady=(0, 20))
        header1.columnconfigure(0, weight=1)
        ttk.Label(header1, text="1. Connect Dive Computer", style="Section.TLabel").grid(row=0, column=0, sticky="w")
        ttk.Frame(header1, style="Divider.TFrame", height=1).grid(row=1, column=0, sticky="ew", pady=(10, 0), padx=(0, 18))
        self.detect_badge = tk.Label(
            header1,
            text="DIVE_COMPUTER_NOT_DETECTED",
            bg=self.colors["accent_soft"],
            fg=self.colors["muted"],
            font=("Consolas", 10, "bold"),
            padx=16,
            pady=9,
            bd=0,
        )
        self.detect_badge.grid(row=0, column=1, rowspan=2, sticky="e")
        ttk.Label(step1_panel, textvariable=self.step1_var, style="Muted.TLabel", wraplength=560).grid(
            row=1, column=0, columnspan=2, sticky="w", pady=(0, 18)
        )

        ttk.Label(step1_panel, text="BRAND", style="Field.TLabel").grid(row=2, column=0, sticky="w", pady=(0, 6))
        self.vendor_combo = ttk.Combobox(
            step1_panel,
            textvariable=self.vendor_var,
            values=list(SUPPORTED_DIVE_COMPUTERS.keys()),
            state="readonly",
            style="Dive.TCombobox",
        )
        self.vendor_combo.grid(row=3, column=0, sticky="ew", pady=(0, 18), padx=(0, 14))

        ttk.Label(step1_panel, text="MODEL", style="Field.TLabel").grid(row=2, column=1, sticky="w", pady=(0, 6))
        self.product_combo = ttk.Combobox(step1_panel, textvariable=self.product_var, values=[], state="readonly", style="Dive.TCombobox")
        self.product_combo.grid(row=3, column=1, sticky="ew", pady=(0, 18))

        ttk.Label(step1_panel, text="SERIAL PORT", style="Field.TLabel").grid(row=4, column=0, columnspan=2, sticky="w", pady=(0, 6))
        self.port_combo = ttk.Combobox(step1_panel, textvariable=self.port_var, values=[], state="readonly", style="Dive.TCombobox")
        self.port_combo.grid(row=5, column=0, columnspan=2, sticky="ew", pady=(0, 18))

        self.scan_button = ttk.Button(step1_panel, text="Scan for Dive Computer", command=self.refresh_ports, style="Secondary.TButton")
        self.scan_button.grid(row=6, column=0, sticky="w", pady=(0, 22))

        ttk.Label(step1_panel, text="Detected Dive Computer", style="Field.TLabel").grid(row=7, column=0, columnspan=2, sticky="w", pady=(0, 6))
        ttk.Entry(step1_panel, textvariable=self.detected_device_var, state="readonly", style="Dive.TEntry").grid(
            row=8, column=0, columnspan=2, sticky="ew"
        )

        step2_panel = ttk.Frame(frame, padding=24, style="PanelAlt.TFrame")
        step2_panel.grid(row=1, column=1, sticky="nsew")
        step2_panel.columnconfigure(0, weight=1)
        header2 = ttk.Frame(step2_panel, style="PanelAlt.TFrame")
        header2.grid(row=0, column=0, sticky="ew", pady=(0, 18))
        header2.columnconfigure(0, weight=1)
        ttk.Label(header2, text="2. Authentication", style="SectionAlt.TLabel").grid(row=0, column=0, sticky="w")
        ttk.Frame(header2, style="Divider.TFrame", height=1).grid(row=1, column=0, sticky="ew", pady=(10, 0), padx=(0, 28))
        self.auth_badge = tk.Label(
            header2,
            text="NOT_AUTHENTICATED",
            bg=self.colors["panel_alt"],
            fg=self.colors["warning"],
            font=("Consolas", 10, "bold"),
            padx=4,
            pady=2,
            bd=0,
        )
        self.auth_badge.grid(row=1, column=0, sticky="w", pady=(14, 0))
        ttk.Label(step2_panel, text="URL", style="FieldAlt.TLabel").grid(row=2, column=0, sticky="w", pady=(18, 6))
        self.backend_url_entry = ttk.Entry(step2_panel, textvariable=self.backend_url_var, style="Dark.TEntry")
        self.backend_url_entry.grid(row=3, column=0, sticky="ew")
        auth_note = tk.Frame(step2_panel, bg=self.colors["panel_dark"], bd=0, highlightthickness=0)
        auth_note.grid(row=4, column=0, sticky="ew", pady=(20, 20))
        tk.Frame(auth_note, bg="#8f6d58", width=2).pack(side="left", fill="y")
        tk.Label(
            auth_note,
            text="Authentication is required to synchronize localized dive\ntelemetry with the primary cloud server.",
            bg=self.colors["panel_dark"],
            fg=self.colors["text"],
            justify="left",
            font=("Segoe UI", 12),
            padx=16,
            pady=16,
        ).pack(anchor="w")
        ttk.Label(step2_panel, textvariable=self.step2_var, style="Body.TLabel", wraplength=420, justify="left").grid(
            row=5, column=0, sticky="w", pady=(0, 18)
        )
        self.login_button = ttk.Button(step2_panel, text="Sign In", command=self.start_login, style="Primary.TButton")
        self.login_button.grid(row=6, column=0, sticky="ew")

        step3_panel = ttk.Frame(frame, padding=24, style="Panel.TFrame")
        step3_panel.grid(row=2, column=0, columnspan=2, sticky="ew", pady=(18, 18))
        step3_panel.columnconfigure(0, weight=0)
        step3_panel.columnconfigure(1, weight=1)
        step3_panel.columnconfigure(2, weight=0)
        step3_panel.columnconfigure(3, weight=0)
        sync_icon = tk.Label(
            step3_panel,
            text="\u27f3",
            bg=self.colors["accent_soft"],
            fg=self.colors["muted"],
            font=("Segoe UI Symbol", 27),
            width=3,
            height=2,
            bd=0,
        )
        sync_icon.grid(row=0, column=0, rowspan=2, sticky="w", padx=(0, 18))
        ttk.Label(step3_panel, text="3. Sync", style="Section.TLabel").grid(row=0, column=1, pady=(10, 0), sticky="w")
        ttk.Label(step3_panel, textvariable=self.step3_var, style="Muted.TLabel", wraplength=520).grid(row=1, column=1, sticky="w")
        self.close_button = ttk.Button(step3_panel, text="Close", command=self.root.destroy, style="Secondary.TButton")
        self.close_button.grid(row=0, column=2, rowspan=2, sticky="e", padx=(18, 12))
        self.sync_button = ttk.Button(step3_panel, text="Sync", command=self.start_sync, style="Primary.TButton")
        self.sync_button.grid(row=0, column=3, rowspan=2, sticky="e")

        status_row = ttk.Frame(frame, style="Shell.TFrame")
        status_row.grid(row=3, column=0, columnspan=2, sticky="ew", pady=(14, 0))
        status_row.columnconfigure(0, weight=1)
        ttk.Label(status_row, text=f"Version {APP_VERSION}", style="Status.TLabel").grid(row=0, column=0, sticky="w")
        ttk.Label(status_row, textvariable=self.status_var, style="Status.TLabel", wraplength=960).grid(row=0, column=1, sticky="e")

        self.log_text = None

    def log(self, message: str) -> None:
        self.status_var.set(message)
        if self.log_text is not None:
            self.log_text.configure(state="normal")
            self.log_text.insert("end", f"{message}\n")
            self.log_text.see("end")
            self.log_text.configure(state="disabled")

    def _handle_port_change(self, *_args) -> None:
        if not self.ui_ready:
            return
        self._update_detected_device_field()

    def _handle_vendor_change(self, *_args) -> None:
        if not self.ui_ready:
            return
        self._sync_model_options()
        self._clear_detected_ports()

    def _handle_product_change(self, *_args) -> None:
        if not self.ui_ready:
            return
        self._clear_detected_ports()

    def _clear_detected_ports(self) -> None:
        self.detected_devices_by_port = {}
        self.port_combo["values"] = []
        self.port_var.set("")
        self.detected_device_var.set("Not detected")
        self._update_ui_state()

    def _sync_model_options(self) -> None:
        vendor = self.vendor_var.get().strip()
        models = SUPPORTED_DIVE_COMPUTERS.get(vendor, [])
        self.product_combo["values"] = models
        current_product = self.product_var.get().strip()
        if current_product not in models:
            self.product_var.set(models[0] if models else "")

    def _update_detected_device_field(self) -> None:
        current_port = self.port_var.get().strip()
        detection = self.detected_devices_by_port.get(current_port)
        if detection is None:
            self.detected_device_var.set("Not detected")
        else:
            self.detected_device_var.set(detection["label"])
        self._update_ui_state()

    def _has_detected_device(self) -> bool:
        current_port = self.port_var.get().strip()
        detection = self.detected_devices_by_port.get(current_port)
        return detection is not None and detection.get("confirmed") == "true"

    def _update_ui_state(self) -> None:
        step1_complete = self._has_detected_device()
        step2_complete = bool(self.auth_token)

        if self.scan_in_progress:
            self.step1_var.set("Scanning serial ports for the selected dive computer...")
        elif step1_complete:
            self.step1_var.set("Dive computer detected. Continue to backend login.")
        else:
            self.step1_var.set("Choose your dive computer model and scan until it is detected on a COM port.")

        if self.login_in_progress:
            self.step2_var.set("Browser login in progress. Finish approval in the opened browser tab.")
        elif step2_complete:
            self.step2_var.set("Backend login completed. You can start the sync.")
        elif step1_complete:
            self.step2_var.set("Dive computer detected. Sign in to the backend to continue.")
        else:
            self.step2_var.set("Complete Step 1 before backend login is enabled.")

        if self.sync_in_progress:
            if self.sync_imported or self.sync_skipped:
                self.step3_var.set(
                    f"Sync in progress. {self.sync_imported} dives synced, {self.sync_skipped} already present."
                )
            else:
                self.step3_var.set("Sync in progress. 0 dives synced.")
        elif self.sync_existing_total is not None and self.sync_imported == 0 and self.sync_skipped == 0:
            self.step3_var.set(f"No new dives to sync. {self.sync_existing_total} dives already present in the backend.")
        elif step1_complete and step2_complete:
            self.step3_var.set("Detection and login are complete. Start the sync when ready.")
        elif step1_complete:
            self.step3_var.set("Complete backend login before starting the sync.")
        else:
            self.step3_var.set("Complete Steps 1 and 2 before starting the sync.")

        if self.scan_in_progress:
            self.detect_badge.configure(
                text="SCANNING_PORTS",
                bg=self.colors["accent_soft"],
                fg=self.colors["accent"],
            )
        elif step1_complete:
            self.detect_badge.configure(
                text="DIVE_COMPUTER_DETECTED",
                bg=self.colors["accent_soft"],
                fg=self.colors["success"],
            )
        else:
            self.detect_badge.configure(
                text="AWAITING_DEVICE",
                bg=self.colors["accent_soft"],
                fg=self.colors["muted"],
            )

        if self.login_in_progress:
            self.auth_badge.configure(
                text="AUTH_IN_PROGRESS",
                bg=self.colors["panel_alt"],
                fg=self.colors["accent"],
            )
        elif step2_complete:
            self.auth_badge.configure(
                text="AUTHENTICATED",
                bg=self.colors["panel_alt"],
                fg=self.colors["success"],
            )
        else:
            self.auth_badge.configure(
                text="NOT_AUTHENTICATED",
                bg=self.colors["panel_alt"],
                fg=self.colors["warning"],
            )

        self.vendor_combo.configure(state="readonly" if not self.scan_in_progress else "disabled")
        self.product_combo.configure(state="readonly" if not self.scan_in_progress else "disabled")
        self.port_combo.configure(state="readonly" if step1_complete and not self.scan_in_progress else "disabled")
        self.scan_button.configure(state="disabled" if self.scan_in_progress else "normal")
        self.backend_url_entry.configure(state="normal" if not self.login_in_progress and not self.sync_in_progress else "disabled")
        self.login_button.configure(state="normal" if step1_complete and not self.login_in_progress and not self.sync_in_progress else "disabled")
        self.sync_button.configure(
            state="normal" if step1_complete and step2_complete and not self.sync_in_progress else "disabled"
        )

    def refresh_ports(self) -> None:
        if self.scan_in_progress:
            self.log("Serial port scan already in progress.")
            return

        vendor = self.vendor_var.get().strip()
        product = self.product_var.get().strip()
        if not vendor or not product:
            self.log("Choose a brand and model before scanning.")
            return

        self.scan_in_progress = True
        self.detected_device_var.set("Scanning...")
        self.log(f"Scanning serial ports for {format_dive_computer_name(vendor, product)}...")
        self._update_ui_state()
        thread = threading.Thread(target=self._scan_ports_worker, args=(vendor, product), daemon=True)
        thread.start()

    def _scan_ports_worker(self, vendor: str, product: str) -> None:
        try:
            port_infos = list_serial_port_infos()
            ports = [info["device"] for info in port_infos]
            detected_ports = scan_supported_serial_ports(vendor, product, candidate_ports=ports)
            label = format_dive_computer_name(vendor, product)
            detections = [
                {
                    "port": port,
                    "vendor": vendor,
                    "product": product,
                    "label": label,
                    "confirmed": "true",
                }
                for port in detected_ports
            ]
            self.events.put(
                (
                    "ports_scanned",
                    {
                        "vendor": vendor,
                        "product": product,
                        "port_infos": port_infos,
                        "detections": detections,
                    },
                )
            )
        except Exception as exc:
            self.events.put(("ports_scan_failed", str(exc)))

    def _queue_sync_progress(self, imported: int, skipped: int) -> None:
        self.events.put(("sync_progress", {"imported": imported, "skipped": skipped}))

    def start_login(self) -> None:
        backend_url = self.backend_url_var.get().strip()
        if not backend_url:
            messagebox.showerror("Missing backend URL", "Enter the backend URL before signing in.")
            return

        self.login_in_progress = True
        self._update_ui_state()
        self.log("Creating desktop login request...")
        thread = threading.Thread(target=self._login_worker, daemon=True)
        thread.start()

    def _login_worker(self) -> None:
        try:
            backend_url = self.backend_url_var.get().strip()
            auth_request = create_cli_auth_request(backend_url)
            code = auth_request["code"]
            approval_url = build_cli_auth_url(backend_url, code)
            self.events.put(("login_started", {"code": code, "approval_url": approval_url}))

            while True:
                time.sleep(2)
                status = poll_cli_auth_request(backend_url, code)
                if status.get("status") == "approved" and status.get("token"):
                    self.events.put(("login_approved", status))
                    return
        except Exception as exc:
            self.events.put(("error", f"Desktop login failed: {exc}"))

    def start_sync(self) -> None:
        port = self.port_var.get().strip()
        if not port:
            messagebox.showerror("Missing serial port", "Choose a detected dive computer serial port.")
            return
        if not self.auth_token:
            messagebox.showerror("Not signed in", "Sign in to the backend first.")
            return
        detection = self.detected_devices_by_port.get(port)
        if detection is None:
            messagebox.showerror("Device not detected", "Run Scan Ports and choose a serial port with a detected dive computer.")
            return
        if detection.get("confirmed") != "true":
            messagebox.showerror(
                "Device not confirmed",
                "The scan found a possible dive computer on this port, but could not confirm the exact model. "
                "Sync is blocked to avoid using the wrong device descriptor.",
            )
            return

        self.sync_imported = 0
        self.sync_skipped = 0
        self.sync_existing_total = None
        self.sync_in_progress = True
        self._update_ui_state()
        self.log("Starting sync...")
        thread = threading.Thread(target=self._sync_worker, args=(detection,), daemon=True)
        thread.start()

    def _sync_worker(self, detection: dict[str, str]) -> None:
        try:
            result = sync_dives(
                port=detection["port"],
                vendor=detection["vendor"],
                product=detection["product"],
                backend_url=self.backend_url_var.get().strip(),
                backend_auth_token=self.auth_token,
                progress_callback=self._queue_sync_progress,
            )
            self.events.put(("sync_complete", result))
        except Exception as exc:
            self.events.put(("error", f"Sync failed: {exc}"))

    def _pump_events(self) -> None:
        try:
            while True:
                event, payload = self.events.get_nowait()
                if event == "login_started":
                    self.current_code = payload["code"]
                    self.auth_var.set("Browser approval pending. Finish login in the opened browser tab.")
                    self.log(f"Opened browser for backend login approval: {payload['approval_url']}")
                    webbrowser.open(payload["approval_url"])
                elif event == "login_approved":
                    self.login_in_progress = False
                    self.auth_token = payload.get("token")
                    self.auth_token_expires_at = payload.get("token_expires_at")
                    email = payload.get("email") or "signed-in user"
                    self.auth_var.set(f"Signed in as {email}. Desktop sync token ready.")
                    self.log("Desktop login approved. You can start syncing now.")
                elif event == "sync_progress":
                    self.sync_imported = int(payload.get("imported", 0))
                    self.sync_skipped = int(payload.get("skipped", 0))
                    self.status_var.set(f"Syncing dives: {self.sync_imported} synced")
                elif event == "sync_complete":
                    self.sync_in_progress = False
                    self.sync_imported = int(payload.get("imported", 0))
                    self.sync_skipped = int(payload.get("skipped", 0))
                    existing_total = payload.get("existing_total")
                    self.sync_existing_total = existing_total if isinstance(existing_total, int) and existing_total >= 0 else None
                    if self.sync_imported == 0 and self.sync_skipped == 0 and self.sync_existing_total is not None:
                        self.log(f"No new dives to sync. {self.sync_existing_total} dives already present in the backend.")
                    else:
                        self.log(
                            f"Sync completed successfully. {self.sync_imported} dives synced, {self.sync_skipped} already present."
                        )
                elif event == "ports_scanned":
                    self.scan_in_progress = False
                    vendor = payload["vendor"]
                    product = payload["product"]
                    port_infos = payload["port_infos"]
                    ports = [info["device"] for info in port_infos]
                    detections = payload["detections"]
                    self.detected_devices_by_port = {detection["port"]: detection for detection in detections}
                    detected_ports = [detection["port"] for detection in detections]

                    self.port_combo["values"] = detected_ports

                    current_port = self.port_var.get().strip()
                    if detected_ports:
                        if current_port not in detected_ports:
                            self.port_var.set(detected_ports[0])
                        self._update_detected_device_field()
                        if len(detections) == 1:
                            detection = detections[0]
                            self.log(f"Detected {detection['label']} on {detection['port']}.")
                        else:
                            summary = ", ".join(f"{detection['port']} ({detection['label']})" for detection in detections)
                            self.log(f"Detected dive computers on: {summary}.")
                    else:
                        self.detected_devices_by_port = {}
                        self.port_combo["values"] = []
                        if current_port:
                            self.port_var.set("")
                        self._update_detected_device_field()
                        if ports:
                            self.log(
                                f"No {format_dive_computer_name(vendor, product)} detected. "
                                f"Available serial ports: {', '.join(ports)}."
                            )
                        else:
                            self.port_combo["values"] = []
                            self.port_var.set("")
                            self.detected_device_var.set("Not detected")
                            self.log("No serial ports found.")
                elif event == "ports_scan_failed":
                    self.scan_in_progress = False
                    self.detected_devices_by_port = {}
                    self._update_detected_device_field()
                    self.log(str(payload))
                elif event == "error":
                    self.login_in_progress = False
                    self.sync_in_progress = False
                    self.log(str(payload))
                    messagebox.showerror("Dive Sync", str(payload))
        except Empty:
            pass
        finally:
            self._update_ui_state()
            self.root.after(150, self._pump_events)


def run_gui(defaults: dict[str, str]) -> None:
    if tk is None or ttk is None or messagebox is None:
        raise RuntimeError("Tkinter is not available in this Python installation. Install tkinter or run the CLI mode instead.")

    set_windows_appusermodel_id()
    root = tk.Tk()
    app = SyncDesktopApp(root, defaults)
    app.log("Desktop UI ready.")
    try:
        root.mainloop()
    except KeyboardInterrupt:
        if root.winfo_exists():
            root.destroy()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--gui", action="store_true", help="Launch the Windows desktop UI")
    parser.add_argument("--port", help="Serial port, e.g. /dev/ttyUSB0 or COM3. Use 'auto' to scan detected serial ports.")
    parser.add_argument("--backend-url", default=os.getenv("BACKEND_URL"), help="Backend base URL, e.g. http://localhost:8000")
    parser.add_argument("--backend-auth-token", help="Desktop sync token or Clerk session token for authenticated backend API access")
    parser.add_argument("--backend-auth-token-file", help="Path to a file containing the backend desktop sync token or session token")
    parser.add_argument("--vendor", default="Mares")
    parser.add_argument("--product", default="Smart Air")
    args = parser.parse_args()

    launch_gui = args.gui or (getattr(sys, "frozen", False) and len(sys.argv) == 1)

    if launch_gui:
        run_gui(
            {
                "port": args.port or "",
                "backend_url": args.backend_url or "https://divevault.local.joshuahemmings.ch",
                "vendor": args.vendor,
                "product": args.product,
            }
        )
        return

    port = (args.port or "").strip()
    if not port or port.lower() == "auto":
        port = auto_detect_port(args.vendor, args.product)
        print(f"Detected {args.vendor} {args.product} on {port}")
    if not args.backend_url:
        parser.error("Provide --backend-url.")

    backend_auth_token = load_auth_token(args.backend_auth_token, args.backend_auth_token_file)

    sync_dives(
        port=port,
        vendor=args.vendor,
        product=args.product,
        backend_url=args.backend_url,
        backend_auth_token=backend_auth_token,
    )


if __name__ == "__main__":
    main()
