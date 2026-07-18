#!/usr/bin/env python3
"""Regression tests for the Android backup policy checker."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from check_android_backup_policy import PolicyError, validate_policy


CLIENT_ROOT = Path(__file__).resolve().parents[1]


class AndroidBackupPolicyTest(unittest.TestCase):
    def test_repository_policy_is_complete(self) -> None:
        validate_policy(CLIENT_ROOT)

    def test_backup_enabled_manifest_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            fixture = Path(directory)
            self._copy_policy(fixture)
            manifest = fixture / "android" / "app" / "src" / "main" / "AndroidManifest.xml"
            manifest.write_text(
                manifest.read_text(encoding="utf-8").replace(
                    'android:allowBackup="false"',
                    'android:allowBackup="true"',
                ),
                encoding="utf-8",
            )

            with self.assertRaisesRegex(PolicyError, "allowBackup"):
                validate_policy(fixture)

    def test_incomplete_device_transfer_exclusions_are_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            fixture = Path(directory)
            self._copy_policy(fixture)
            rules = (
                fixture
                / "android"
                / "app"
                / "src"
                / "main"
                / "res"
                / "xml"
                / "data_extraction_rules.xml"
            )
            contents = rules.read_text(encoding="utf-8")
            prefix, device_transfer = contents.split("<device-transfer>", 1)
            device_transfer = device_transfer.replace(
                '        <exclude domain="sharedpref" path="." />\n',
                "",
                1,
            )
            rules.write_text(
                f"{prefix}<device-transfer>{device_transfer}",
                encoding="utf-8",
            )

            with self.assertRaisesRegex(PolicyError, "exclusions are incomplete"):
                validate_policy(fixture)

    @staticmethod
    def _copy_policy(destination: Path) -> None:
        source_main = CLIENT_ROOT / "android" / "app" / "src" / "main"
        destination_main = destination / "android" / "app" / "src" / "main"
        destination_xml = destination_main / "res" / "xml"
        destination_xml.mkdir(parents=True)
        (destination_main / "AndroidManifest.xml").write_bytes(
            (source_main / "AndroidManifest.xml").read_bytes()
        )
        for name in ("backup_rules.xml", "data_extraction_rules.xml"):
            (destination_xml / name).write_bytes(
                (source_main / "res" / "xml" / name).read_bytes()
            )


if __name__ == "__main__":
    unittest.main()
