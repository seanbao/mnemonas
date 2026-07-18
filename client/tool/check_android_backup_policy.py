#!/usr/bin/env python3
"""Validate that the Android client cannot back up or transfer app data."""

from __future__ import annotations

import argparse
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

ANDROID_NAMESPACE = "http://schemas.android.com/apk/res/android"
ANDROID_ATTRIBUTE = f"{{{ANDROID_NAMESPACE}}}"
BACKUP_DOMAINS = frozenset(
    {
        "root",
        "file",
        "database",
        "sharedpref",
        "external",
        "device_root",
        "device_file",
        "device_database",
        "device_sharedpref",
    }
)


class PolicyError(ValueError):
    """Raised when the Android backup policy is incomplete or unsafe."""


def _parse_xml(path: Path) -> ET.Element:
    try:
        return ET.parse(path).getroot()
    except (OSError, ET.ParseError) as error:
        raise PolicyError(f"cannot parse {path}: {error}") from error


def _android_attribute(name: str) -> str:
    return f"{ANDROID_ATTRIBUTE}{name}"


def validate_manifest(path: Path) -> None:
    root = _parse_xml(path)
    applications = root.findall("application")
    if len(applications) != 1:
        raise PolicyError(f"{path}: expected exactly one application element")

    application = applications[0]
    required_attributes = {
        "allowBackup": "false",
        "fullBackupContent": "@xml/backup_rules",
        "dataExtractionRules": "@xml/data_extraction_rules",
    }
    for name, expected in required_attributes.items():
        actual = application.get(_android_attribute(name))
        if actual != expected:
            raise PolicyError(
                f"{path}: android:{name} must be {expected!r}, found {actual!r}"
            )

    if application.get(_android_attribute("backupAgent")) is not None:
        raise PolicyError(f"{path}: a custom android:backupAgent is not permitted")


def _validate_exclusions(
    path: Path,
    parent: ET.Element,
    context: str,
) -> None:
    includes = parent.findall("include")
    if includes:
        raise PolicyError(f"{path}: {context} must not contain include rules")

    rules = [
        (element.get("domain"), element.get("path"))
        for element in parent.findall("exclude")
    ]
    expected = {(domain, ".") for domain in BACKUP_DOMAINS}
    actual = set(rules)
    if actual != expected or len(rules) != len(expected):
        missing = sorted(expected - actual, key=repr)
        unexpected = sorted(actual - expected, key=repr)
        raise PolicyError(
            f"{path}: {context} exclusions are incomplete; "
            f"missing={missing!r}, unexpected={unexpected!r}"
        )


def validate_legacy_rules(path: Path) -> None:
    root = _parse_xml(path)
    if root.tag != "full-backup-content":
        raise PolicyError(f"{path}: root must be full-backup-content")
    _validate_exclusions(path, root, "full-backup-content")


def validate_extraction_rules(path: Path) -> None:
    root = _parse_xml(path)
    if root.tag != "data-extraction-rules":
        raise PolicyError(f"{path}: root must be data-extraction-rules")

    expected_sections = {"cloud-backup", "device-transfer"}
    actual_sections = {child.tag for child in root}
    if actual_sections != expected_sections or len(root) != len(expected_sections):
        raise PolicyError(
            f"{path}: expected exactly cloud-backup and device-transfer sections"
        )

    for section_name in sorted(expected_sections):
        section = root.find(section_name)
        if section is None:
            raise PolicyError(f"{path}: missing {section_name} section")
        _validate_exclusions(path, section, section_name)


def validate_policy(client_root: Path) -> None:
    android_main = client_root / "android" / "app" / "src" / "main"
    validate_manifest(android_main / "AndroidManifest.xml")
    validate_legacy_rules(android_main / "res" / "xml" / "backup_rules.xml")
    validate_extraction_rules(
        android_main / "res" / "xml" / "data_extraction_rules.xml"
    )


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
        print(f"android-backup-policy: {error}", file=sys.stderr)
        return 1

    print("android-backup-policy: ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
