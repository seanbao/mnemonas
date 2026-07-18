#!/usr/bin/env python3
"""Regression tests for the Android release-policy checker."""

from __future__ import annotations

import tempfile
import unittest
from contextlib import contextmanager
from pathlib import Path
from typing import Iterator

from check_android_release_policy import PolicyError, validate_policy
from test_android_release_signing import _temporary_android_build_files


CLIENT_ROOT = Path(__file__).resolve().parents[1]


class AndroidReleasePolicyTest(unittest.TestCase):
    def test_repository_policy_is_complete(self) -> None:
        validate_policy(CLIENT_ROOT)

    def test_release_application_id_drift_is_rejected(self) -> None:
        with self._fixture() as fixture:
            gradle = fixture / "android" / "app" / "build.gradle.kts"
            gradle.write_text(
                gradle.read_text(encoding="utf-8").replace(
                    'applicationId = "com.mnemonas.app"',
                    'applicationId = "com.mnemonas.changed"',
                ),
                encoding="utf-8",
            )

            with self.assertRaisesRegex(PolicyError, "release applicationId"):
                validate_policy(fixture)

    def test_missing_debug_identity_suffix_is_rejected(self) -> None:
        with self._fixture() as fixture:
            gradle = fixture / "android" / "app" / "build.gradle.kts"
            gradle.write_text(
                gradle.read_text(encoding="utf-8").replace(
                    'applicationIdSuffix = ".debug"',
                    'applicationIdSuffix = ".profile"',
                ),
                encoding="utf-8",
            )

            with self.assertRaisesRegex(PolicyError, "debug applicationId suffix"):
                validate_policy(fixture)

    def test_variant_label_collision_is_rejected(self) -> None:
        with self._fixture() as fixture:
            label = (
                fixture
                / "android"
                / "app"
                / "src"
                / "debug"
                / "res"
                / "values"
                / "strings.xml"
            )
            label.write_text(
                label.read_text(encoding="utf-8").replace(
                    "MnemoNAS Dev",
                    "MnemoNAS",
                ),
                encoding="utf-8",
            )

            with self.assertRaisesRegex(PolicyError, "MnemoNAS Dev"):
                validate_policy(fixture)

    def test_manifest_label_resource_drift_is_rejected(self) -> None:
        with self._fixture() as fixture:
            manifest = (
                fixture
                / "android"
                / "app"
                / "src"
                / "main"
                / "AndroidManifest.xml"
            )
            manifest.write_text(
                manifest.read_text(encoding="utf-8").replace(
                    'android:label="@string/app_name"',
                    'android:label="MnemoNAS"',
                ),
                encoding="utf-8",
            )

            with self.assertRaisesRegex(PolicyError, "android:label"):
                validate_policy(fixture)

    @contextmanager
    def _fixture(self) -> Iterator[Path]:
        with tempfile.TemporaryDirectory() as directory:
            fixture = Path(directory)
            for relative in (
                Path("android/.gitignore"),
                Path("android/app/build.gradle.kts"),
                Path("android/app/src/main/AndroidManifest.xml"),
                Path("android/app/src/main/res/values/strings.xml"),
                Path("android/app/src/debug/res/values/strings.xml"),
                Path("android/app/src/profile/res/values/strings.xml"),
            ):
                source = CLIENT_ROOT / relative
                destination = fixture / relative
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(source.read_bytes())
            yield fixture


class AndroidReleaseSigningBootstrapTest(unittest.TestCase):
    def test_missing_android_build_files_are_temporary(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            client_root = root / "client"
            android_root = client_root / "android"
            android_sdk = root / "android-sdk"
            flutter_sdk = root / "flutter"
            client_root.mkdir()
            (client_root / "pubspec.yaml").write_text(
                "version: 0.1.0+1\n",
                encoding="utf-8",
            )
            android_sdk.mkdir()
            (flutter_sdk / "packages" / "flutter_tools" / "gradle").mkdir(
                parents=True
            )
            wrapper_artifacts = (
                flutter_sdk / "bin" / "cache" / "artifacts" / "gradle_wrapper"
            )
            (wrapper_artifacts / "gradle" / "wrapper").mkdir(parents=True)
            (wrapper_artifacts / "gradlew").write_bytes(b"#!/bin/sh\n")
            (
                wrapper_artifacts / "gradle" / "wrapper" / "gradle-wrapper.jar"
            ).write_bytes(b"temporary-wrapper")

            with _temporary_android_build_files(
                client_root,
                android_sdk=android_sdk,
                flutter_sdk=flutter_sdk,
            ):
                local_properties = android_root / "local.properties"
                gradlew = android_root / "gradlew"
                wrapper_jar = (
                    android_root / "gradle" / "wrapper" / "gradle-wrapper.jar"
                )
                self.assertTrue(local_properties.is_file())
                self.assertTrue(gradlew.is_file())
                self.assertTrue(wrapper_jar.is_file())
                self.assertTrue(gradlew.stat().st_mode & 0o100)
                local_contents = local_properties.read_text(encoding="utf-8")
                self.assertIn("flutter.versionName=0.1.0", local_contents)
                self.assertIn("flutter.versionCode=1", local_contents)

            self.assertFalse((android_root / "local.properties").exists())
            self.assertFalse((android_root / "gradlew").exists())
            self.assertFalse(
                (
                    android_root / "gradle" / "wrapper" / "gradle-wrapper.jar"
                ).exists()
            )

    def test_existing_android_build_files_are_preserved(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            client_root = Path(directory) / "client"
            android_root = client_root / "android"
            wrapper_root = android_root / "gradle" / "wrapper"
            wrapper_root.mkdir(parents=True)
            existing = {
                android_root / "local.properties": b"owned=true\n",
                android_root / "gradlew": b"#!/bin/sh\n# owned\n",
                wrapper_root / "gradle-wrapper.jar": b"owned-wrapper",
            }
            for path, contents in existing.items():
                path.write_bytes(contents)
            (android_root / "gradlew").chmod(0o700)

            with _temporary_android_build_files(client_root):
                for path, contents in existing.items():
                    self.assertEqual(path.read_bytes(), contents)

            for path, contents in existing.items():
                self.assertEqual(path.read_bytes(), contents)


if __name__ == "__main__":
    unittest.main()
