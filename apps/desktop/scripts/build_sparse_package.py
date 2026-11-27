#!/usr/bin/env python3
"""Build and sign the sparse identity package used by Windows Explorer."""

from __future__ import annotations

import base64
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

APP_ROOT = Path(__file__).resolve().parents[1]
TAURI_ROOT = APP_ROOT / "src-tauri"
PACKAGE_LAYOUT = APP_ROOT / "shell" / "identity" / "Package.appxmanifest.template"
SHELL_DLL = APP_ROOT / "shell" / "target" / "x86_64-pc-windows-msvc" / "release" / "privasync_shell.dll"
OUTPUT_DIR = TAURI_ROOT / "target" / "sparse-package"
PACKAGE_NAME = "privasync-identity.msix"
CERT_NAME = "privasync-package.cer"
PUBLISHER = "CN=PrivaSync"


def run_openappx(*args: str, password: str | None = None) -> None:
    env = os.environ.copy()
    if password is not None:
        env["OPENAPPX_PFX_PASSWORD"] = password
    subprocess.run([sys.executable, "-m", "openappx", *args], check=True, env=env)


def export_certificate(pfx_path: Path, password: str | None, output_path: Path) -> None:
    from cryptography.hazmat.primitives import serialization
    from cryptography.hazmat.primitives.serialization import pkcs12

    key, certificate, _ = pkcs12.load_key_and_certificates(
        pfx_path.read_bytes(),
        password.encode("utf-8") if password else None,
    )
    if key is None or certificate is None:
        raise RuntimeError("The package-signing PFX must contain a private key and certificate.")
    if certificate.subject.rfc4514_string() != PUBLISHER:
        raise RuntimeError(
            f"The package-signing certificate subject must be {PUBLISHER}; "
            f"got {certificate.subject.rfc4514_string()}."
        )
    output_path.write_bytes(certificate.public_bytes(serialization.Encoding.DER))


def main() -> int:
    platform = os.environ.get("TAURI_ENV_PLATFORM", "").lower()
    target = (
        os.environ.get("TAURI_ENV_TARGET_TRIPLE")
        or os.environ.get("TARGET")
        or ""
    ).lower()
    is_windows_target = (
        platform == "windows"
        or "windows" in target
        or (not platform and not target and os.name == "nt")
    )
    if not is_windows_target:
        print("[sparse-package] skipped for non-Windows target")
        return 0

    if not SHELL_DLL.is_file():
        raise FileNotFoundError(f"Windows shell extension was not built: {SHELL_DLL}")

    package_version = json.loads((APP_ROOT / "package.json").read_text(encoding="utf-8"))["version"]
    tauri_version = json.loads((TAURI_ROOT / "tauri.conf.json").read_text(encoding="utf-8"))["version"]
    if package_version != tauri_version:
        raise RuntimeError(
            f"Desktop package version {package_version} differs from Tauri version {tauri_version}."
        )
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:\.\d+)?", package_version):
        raise RuntimeError(f"Unsupported package version: {package_version}")
    identity_version = package_version if package_version.count(".") == 3 else f"{package_version}.0"

    pfx_base64 = os.environ.get("PRIVASYNC_APPX_SIGNING_PFX_BASE64")
    password = os.environ.get("PRIVASYNC_APPX_SIGNING_PASSWORD")
    if pfx_base64 and password is None:
        raise RuntimeError("PRIVASYNC_APPX_SIGNING_PASSWORD is required with the signing PFX.")
    if not pfx_base64 and os.environ.get("CI"):
        raise RuntimeError(
            "Release builds require PRIVASYNC_APPX_SIGNING_PFX_BASE64 and "
            "PRIVASYNC_APPX_SIGNING_PASSWORD."
        )

    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    package_path = OUTPUT_DIR / PACKAGE_NAME
    certificate_path = OUTPUT_DIR / CERT_NAME
    package_path.unlink(missing_ok=True)
    certificate_path.unlink(missing_ok=True)

    with tempfile.TemporaryDirectory(prefix="privasync-sparse-package-") as temp_dir:
        temp = Path(temp_dir)
        layout = temp / "layout"
        layout.mkdir()
        manifest = PACKAGE_LAYOUT.read_text(encoding="utf-8").replace("@VERSION@", identity_version)
        (layout / "AppxManifest.xml").write_text(manifest, encoding="utf-8", newline="\n")
        shutil.copy2(SHELL_DLL, layout / "privasync_shell.dll")
        for logo in ("StoreLogo.png", "Square150x150Logo.png", "Square44x44Logo.png"):
            shutil.copy2(TAURI_ROOT / "icons" / logo, layout / logo)

        pfx_path = temp / "signing.pfx"
        if pfx_base64:
            pfx_path.write_bytes(base64.b64decode(pfx_base64, validate=True))
            pfx_path.chmod(0o600)
        else:
            pfx_path = temp / "local-test"
            run_openappx(
                "sign",
                "--make-test-cert",
                PUBLISHER,
                "--cert-out",
                str(pfx_path),
            )
            pfx_path = pfx_path.with_suffix(".pfx")

        run_openappx("pack", "--root", str(layout), "--out", str(package_path), "--allow-missing")
        run_openappx(
            "sign",
            "--package",
            str(package_path),
            "--pfx",
            str(pfx_path),
            password=password if pfx_base64 else None,
        )
        export_certificate(pfx_path, password if pfx_base64 else None, certificate_path)
        run_openappx("inspect", "--package", str(package_path))

    print(f"[sparse-package] created {package_path.name} and its public certificate")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
