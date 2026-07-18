#!/usr/bin/env python3
"""Validate Android release identity and signing-policy source invariants."""

from __future__ import annotations

import argparse
import re
import sys
import xml.etree.ElementTree as ET
from pathlib import Path


ANDROID_NAMESPACE = "http://schemas.android.com/apk/res/android"
ANDROID_ATTRIBUTE = f"{{{ANDROID_NAMESPACE}}}"
EXPECTED_APPLICATION_ID = "com.mnemonas.app"
EXPECTED_LABELS = {
    "main": "MnemoNAS",
    "debug": "MnemoNAS Dev",
    "profile": "MnemoNAS Profile",
}


class PolicyError(ValueError):
    """Raised when the Android release policy is incomplete or unsafe."""


def _read(path: Path) -> str:
    try:
        return path.read_text(encoding="utf-8")
    except OSError as error:
        raise PolicyError(f"cannot read {path}: {error}") from error


def _parse_xml(path: Path) -> ET.Element:
    try:
        return ET.parse(path).getroot()
    except (OSError, ET.ParseError) as error:
        raise PolicyError(f"cannot parse {path}: {error}") from error


def _require_once(contents: str, pattern: str, description: str) -> None:
    matches = re.findall(pattern, contents, flags=re.MULTILINE)
    if len(matches) != 1:
        raise PolicyError(
            f"{description} must appear exactly once; found {len(matches)}"
        )


def _validate_gradle(path: Path) -> None:
    contents = _read(path)
    _require_once(
        contents,
        rf'namespace\s*=\s*"{re.escape(EXPECTED_APPLICATION_ID)}"',
        "release namespace",
    )
    _require_once(
        contents,
        rf'applicationId\s*=\s*"{re.escape(EXPECTED_APPLICATION_ID)}"',
        "release applicationId",
    )
    _require_once(
        contents,
        r'applicationIdSuffix\s*=\s*"[.]debug"',
        "debug applicationId suffix",
    )
    _require_once(
        contents,
        r'applicationIdSuffix\s*=\s*"[.]profile"',
        "profile applicationId suffix",
    )

    required_tokens = {
        "external key-properties Gradle property": '"mnemonas.android.keyProperties"',
        "external key-properties environment variable": (
            '"MNEMONAS_ANDROID_KEY_PROPERTIES"'
        ),
        "source-checkout path detection": "isInsideSourceCheckout",
        "source-checkout root marker fallback": "isMnemoNASSourceRoot",
        "external key-properties enforcement": (
            '"Android release key properties must be outside the source checkout"'
        ),
        "external keystore enforcement": (
            '"Android release keystore must be outside the source checkout"'
        ),
        "certificate fingerprint field": '"certificateSha256"',
        "release signing validation task": (
            'tasks.register("validateReleaseSigning")'
        ),
        "debug certificate rejection": '"Android Debug"',
        "injected signing prefix rejection": '"android.injected.signing."',
        "injected signing generic enumeration": (
            "startsWith(injectedSigningPropertyPrefix, ignoreCase = true)"
        ),
        "injected signing rejection error": "injected signing properties are forbidden",
        "resolved release task graph guard": "gradle.taskGraph.whenReady",
        "release configuration-cache exclusion": (
            "notCompatibleWithConfigurationCache"
        ),
        "release APK task guard": '"assembleRelease"',
        "release bundle task guard": '"bundleRelease"',
        "release package task guard": '"packageRelease"',
        "release bundle signing guard": '"signReleaseBundle"',
    }
    for description, token in required_tokens.items():
        if token not in contents:
            raise PolicyError(f"{description} is missing")
    if 'rootProject.file("key.properties")' in contents:
        raise PolicyError("repository-local key.properties fallback is forbidden")


def _validate_manifest(path: Path) -> None:
    root = _parse_xml(path)
    applications = root.findall("application")
    if len(applications) != 1:
        raise PolicyError(f"{path}: expected exactly one application element")
    application = applications[0]
    label = application.get(f"{ANDROID_ATTRIBUTE}label")
    if label != "@string/app_name":
        raise PolicyError(
            f"{path}: android:label must be '@string/app_name', found {label!r}"
        )


def _validate_label(path: Path, expected: str) -> None:
    root = _parse_xml(path)
    labels = [
        element
        for element in root.findall("string")
        if element.get("name") == "app_name"
    ]
    values = [element.text for element in labels]
    if values != [expected]:
        raise PolicyError(
            f"{path}: app_name must be exactly {expected!r}, found {values!r}"
        )
    if labels[0].get("translatable") != "false":
        raise PolicyError(f"{path}: app_name must be non-translatable")


def _validate_gitignore(path: Path) -> None:
    lines = {
        line.strip()
        for line in _read(path).splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    }
    required = {
        "key.properties",
        "**/*.keystore",
        "**/*.jks",
        "**/*.p12",
        "**/*.pfx",
        "**/*.pkcs12",
    }
    missing = required - lines
    if missing:
        raise PolicyError(
            f"{path}: release secret ignore rules are incomplete: {sorted(missing)!r}"
        )


def validate_policy(client_root: Path) -> None:
    android_root = client_root / "android"
    app_root = android_root / "app"
    _validate_gradle(app_root / "build.gradle.kts")
    _validate_manifest(app_root / "src" / "main" / "AndroidManifest.xml")
    for variant, expected in EXPECTED_LABELS.items():
        _validate_label(
            app_root / "src" / variant / "res" / "values" / "strings.xml",
            expected,
        )
    _validate_gitignore(android_root / ".gitignore")


def main() -> int:
    default_client_root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--client-root",
        type=Path,
        default=default_client_root,
        help="Flutter client root, defaults to the parent of this tool directory",
    )
    args = parser.parse_args()

    try:
        validate_policy(args.client_root.resolve())
    except PolicyError as error:
        print(f"android-release-policy: {error}", file=sys.stderr)
        return 1

    print("android-release-policy: ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
