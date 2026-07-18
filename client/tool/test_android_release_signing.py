#!/usr/bin/env python3
"""Exercise Android release-signing failures and a temporary signed build."""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import shutil
import ssl
import subprocess
import sys
import tempfile
from contextlib import contextmanager
from pathlib import Path
from typing import Iterable, Iterator


EXPECTED_APPLICATION_ID = "com.mnemonas.app"
TEST_PASSWORD = "MnemoNASReleaseTestOnly2026"
WRONG_KEY_PASSWORD = "WrongTestKeyPassword2026"
TEST_ALIAS = "mnemonas-release-test"


class GateError(RuntimeError):
    """Raised when the release-signing gate does not behave as expected."""


def _local_property(path: Path, name: str) -> str | None:
    if not path.is_file():
        return None
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError:
        return None
    for line in lines:
        key, separator, value = line.partition("=")
        if separator and key.strip() == name:
            candidate = value.strip()
            return candidate or None
    return None


def _resolve_flutter_sdk(
    local_properties: Path,
    configured: Path | None = None,
) -> Path:
    candidates: list[Path] = []
    if configured is not None:
        candidates.append(configured)
    environment = os.environ.get("FLUTTER_ROOT")
    if environment:
        candidates.append(Path(environment))
    local_value = _local_property(local_properties, "flutter.sdk")
    if local_value:
        candidates.append(Path(local_value))
    executable = shutil.which("flutter")
    if executable:
        candidates.append(Path(executable).resolve().parent.parent)

    for candidate in candidates:
        resolved = candidate.expanduser().resolve()
        if (resolved / "packages" / "flutter_tools" / "gradle").is_dir():
            return resolved
    raise GateError("Flutter SDK is unavailable for Android build bootstrap")


def _property_path(path: Path) -> str:
    value = str(path.resolve())
    if "\n" in value or "\r" in value:
        raise GateError("Android build path contains unsupported characters")
    return value.replace("\\", "\\\\")


def _create_owned_file(path: Path, contents: bytes, executable: bool = False) -> None:
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("xb") as output:
            output.write(contents)
        if executable:
            path.chmod(path.stat().st_mode | 0o100)
    except FileExistsError as error:
        raise GateError("Android build bootstrap encountered a concurrent file change") from error
    except OSError as error:
        raise GateError("Android build bootstrap could not create a required file") from error


def _remove_owned_file(path: Path, contents: bytes) -> None:
    try:
        if path.is_file() and path.read_bytes() == contents:
            path.unlink()
    except OSError:
        pass


@contextmanager
def _temporary_android_build_files(
    client_root: Path,
    *,
    android_sdk: Path | None = None,
    flutter_sdk: Path | None = None,
) -> Iterator[None]:
    android_root = client_root / "android"
    local_properties = android_root / "local.properties"
    gradlew = android_root / "gradlew"
    wrapper_jar = android_root / "gradle" / "wrapper" / "gradle-wrapper.jar"
    created: list[tuple[Path, bytes]] = []

    if local_properties.exists() and not local_properties.is_file():
        raise GateError("Android local properties path is not a regular file")
    if gradlew.exists() and not gradlew.is_file():
        raise GateError("Android Gradle wrapper path is not a regular file")
    if wrapper_jar.exists() and not wrapper_jar.is_file():
        raise GateError("Android Gradle wrapper JAR path is not a regular file")

    try:
        resolved_flutter_sdk: Path | None = None
        if not local_properties.exists() or not gradlew.exists() or not wrapper_jar.exists():
            resolved_flutter_sdk = _resolve_flutter_sdk(
                local_properties,
                configured=flutter_sdk,
            )

        if not local_properties.exists():
            resolved_android_sdk = android_sdk or _android_sdk(client_root)
            version_name, version_code = _declared_flutter_version(client_root)
            contents = (
                "sdk.dir="
                + _property_path(resolved_android_sdk)
                + "\nflutter.sdk="
                + _property_path(resolved_flutter_sdk)
                + "\nflutter.versionName="
                + version_name
                + "\nflutter.versionCode="
                + version_code
                + "\n"
            ).encode("utf-8")
            _create_owned_file(local_properties, contents)
            created.append((local_properties, contents))

        if not gradlew.exists() or not wrapper_jar.exists():
            artifact_root = (
                resolved_flutter_sdk / "bin" / "cache" / "artifacts" / "gradle_wrapper"
            )
            source_gradlew = artifact_root / "gradlew"
            source_wrapper_jar = artifact_root / "gradle" / "wrapper" / "gradle-wrapper.jar"
            if not source_gradlew.is_file() or not source_wrapper_jar.is_file():
                raise GateError("Flutter Gradle wrapper artifacts are unavailable")
            if not gradlew.exists():
                contents = source_gradlew.read_bytes()
                _create_owned_file(gradlew, contents, executable=True)
                created.append((gradlew, contents))
            if not wrapper_jar.exists():
                contents = source_wrapper_jar.read_bytes()
                _create_owned_file(wrapper_jar, contents)
                created.append((wrapper_jar, contents))

        if not os.access(gradlew, os.X_OK):
            raise GateError("Android Gradle wrapper is not executable")
        yield
    finally:
        for path, contents in reversed(created):
            _remove_owned_file(path, contents)


def _sanitized_tail(output: str, secrets: Iterable[str], lines: int = 40) -> str:
    sanitized = output
    for secret in secrets:
        sanitized = sanitized.replace(secret, "<redacted>")
    return "\n".join(sanitized.splitlines()[-lines:])


def _run(
    command: list[str],
    *,
    cwd: Path,
    env: dict[str, str],
    secrets: Iterable[str] = (),
) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        command,
        cwd=cwd,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        check=False,
    )
    if any(secret and secret in result.stdout for secret in secrets):
        raise GateError("command output exposed a configured secret")
    if result.returncode != 0:
        raise GateError(
            f"command failed with exit status {result.returncode}:\n"
            f"{_sanitized_tail(result.stdout, secrets)}"
        )
    return result


def _run_gradle(
    android_root: Path,
    java_home: Path,
    tasks: list[str],
    key_properties: Path | None,
    *,
    expect_success: bool,
    expected_error: str | None = None,
    use_gradle_property: bool = False,
    extra_gradle_properties: dict[str, str] | None = None,
    extra_system_properties: dict[str, str] | None = None,
    extra_environment: dict[str, str] | None = None,
    configuration_cache: bool = False,
) -> str:
    env = os.environ.copy()
    env["JAVA_HOME"] = str(java_home)
    env["PATH"] = f"{java_home / 'bin'}{os.pathsep}{env.get('PATH', '')}"
    env.pop("MNEMONAS_ANDROID_KEY_PROPERTIES", None)
    env.update(extra_environment or {})
    command = [
        str(android_root / "gradlew"),
        *tasks,
        "--console=plain",
        "--configuration-cache"
        if configuration_cache
        else "--no-configuration-cache",
    ]
    if use_gradle_property:
        if key_properties is None:
            raise GateError("Gradle key-properties injection requires a file")
        command.append(f"-Pmnemonas.android.keyProperties={key_properties}")
    elif key_properties is not None:
        env["MNEMONAS_ANDROID_KEY_PROPERTIES"] = str(key_properties)
    for name, value in sorted((extra_gradle_properties or {}).items()):
        command.append(f"-P{name}={value}")
    for name, value in sorted((extra_system_properties or {}).items()):
        command.append(f"-D{name}={value}")
    result = subprocess.run(
        command,
        cwd=android_root,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        check=False,
    )
    output = result.stdout
    if any(secret in output for secret in (TEST_PASSWORD, WRONG_KEY_PASSWORD)):
        raise GateError("Gradle output exposed a configured signing password")
    if expect_success:
        if result.returncode != 0:
            raise GateError(
                "Gradle signing check unexpectedly failed:\n"
                f"{_sanitized_tail(output, (TEST_PASSWORD,))}"
            )
    else:
        if result.returncode == 0:
            raise GateError(
                f"Gradle tasks unexpectedly accepted invalid signing input: {tasks!r}"
            )
        if expected_error is not None and expected_error not in output:
            raise GateError(
                f"Gradle failure did not contain {expected_error!r}:\n"
                f"{_sanitized_tail(output, (TEST_PASSWORD,))}"
            )
    return output


def _tool(java_home: Path, name: str) -> str:
    path = java_home / "bin" / name
    if not path.is_file():
        raise GateError(f"required JDK tool is unavailable: {name}")
    return str(path)


def _keytool_environment() -> dict[str, str]:
    env = os.environ.copy()
    env["MNEMONAS_TEST_STORE_PASSWORD"] = TEST_PASSWORD
    env["MNEMONAS_TEST_KEY_PASSWORD"] = TEST_PASSWORD
    return env


def _generate_key(
    java_home: Path,
    keystore: Path,
    *,
    alias: str,
    distinguished_name: str,
    start_date: str | None = None,
    validity_days: int = 3650,
) -> None:
    command = [
        _tool(java_home, "keytool"),
        "-genkeypair",
        "-noprompt",
        "-storetype",
        "PKCS12",
        "-keystore",
        str(keystore),
        "-storepass:env",
        "MNEMONAS_TEST_STORE_PASSWORD",
        "-keypass:env",
        "MNEMONAS_TEST_KEY_PASSWORD",
        "-alias",
        alias,
        "-keyalg",
        "RSA",
        "-keysize",
        "2048",
        "-sigalg",
        "SHA256withRSA",
        "-validity",
        str(validity_days),
        "-dname",
        distinguished_name,
    ]
    if start_date is not None:
        command.extend(["-startdate", start_date])
    _run(
        command,
        cwd=keystore.parent,
        env=_keytool_environment(),
        secrets=(TEST_PASSWORD,),
    )


def _export_certificate(
    java_home: Path,
    keystore: Path,
    alias: str,
) -> bytes:
    result = _run(
        [
            _tool(java_home, "keytool"),
            "-exportcert",
            "-rfc",
            "-storetype",
            "PKCS12",
            "-keystore",
            str(keystore),
            "-storepass:env",
            "MNEMONAS_TEST_STORE_PASSWORD",
            "-alias",
            alias,
        ],
        cwd=keystore.parent,
        env=_keytool_environment(),
        secrets=(TEST_PASSWORD,),
    )
    match = re.search(
        r"-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----",
        result.stdout,
        flags=re.DOTALL,
    )
    if match is None:
        raise GateError("keytool did not return a PEM certificate")
    return match.group(0).encode("ascii")


def _certificate_fingerprint(pem: bytes) -> str:
    match = re.search(
        rb"-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----",
        pem,
        flags=re.DOTALL,
    )
    if match is None:
        raise GateError("certificate output did not contain a PEM certificate")
    der = ssl.PEM_cert_to_DER_cert(match.group(0).decode("ascii"))
    return hashlib.sha256(der).hexdigest()


def _write_properties(
    path: Path,
    *,
    store_file: Path | str,
    alias: str,
    fingerprint: str,
    store_password: str = TEST_PASSWORD,
    key_password: str = TEST_PASSWORD,
) -> None:
    path.write_text(
        "\n".join(
            [
                f"storeFile={store_file}",
                f"storePassword={store_password}",
                f"keyAlias={alias}",
                f"keyPassword={key_password}",
                f"certificateSha256={fingerprint}",
                "storeType=PKCS12",
                "",
            ]
        ),
        encoding="utf-8",
    )


def _create_trusted_certificate_store(
    java_home: Path,
    directory: Path,
    certificate: bytes,
) -> tuple[Path, str]:
    certificate_file = directory / "trusted-certificate.pem"
    certificate_file.write_bytes(certificate)
    truststore = directory / "trusted-only.p12"
    alias = "trusted-only"
    _run(
        [
            _tool(java_home, "keytool"),
            "-importcert",
            "-noprompt",
            "-storetype",
            "PKCS12",
            "-keystore",
            str(truststore),
            "-storepass:env",
            "MNEMONAS_TEST_STORE_PASSWORD",
            "-alias",
            alias,
            "-file",
            str(certificate_file),
        ],
        cwd=directory,
        env=_keytool_environment(),
        secrets=(TEST_PASSWORD,),
    )
    return truststore, alias


def _android_sdk(client_root: Path) -> Path:
    configured = os.environ.get("ANDROID_SDK_ROOT") or os.environ.get("ANDROID_HOME")
    if configured:
        path = Path(configured).expanduser()
        if path.is_dir():
            return path
    local_properties = client_root / "android" / "local.properties"
    if local_properties.is_file():
        for line in local_properties.read_text(encoding="utf-8").splitlines():
            if line.startswith("sdk.dir="):
                path = Path(line.removeprefix("sdk.dir=")).expanduser()
                if path.is_dir():
                    return path
    raise GateError("Android SDK path is unavailable")


def _build_tool(android_sdk: Path, name: str) -> Path:
    candidates = [
        path
        for path in (android_sdk / "build-tools").glob(f"*/{name}")
        if path.is_file()
    ]
    if not candidates:
        raise GateError(f"Android build tool is unavailable: {name}")

    def version_key(path: Path) -> tuple[int, ...]:
        values = re.findall(r"\d+", path.parent.name)
        return tuple(int(value) for value in values)

    return max(candidates, key=version_key)


def _verify_apk(
    client_root: Path,
    apk: Path,
    expected_fingerprint: str,
    expected_version_code: str,
    expected_version_name: str,
) -> None:
    android_sdk = _android_sdk(client_root)
    apksigner = _build_tool(android_sdk, "apksigner")
    verification = _run(
        [str(apksigner), "verify", "--verbose", "--print-certs", str(apk)],
        cwd=client_root,
        env=os.environ.copy(),
    ).stdout
    fingerprint_match = re.search(
        r"Signer #1 certificate SHA-256 digest:\s*([0-9A-Fa-f]+)",
        verification,
    )
    if fingerprint_match is None:
        raise GateError("apksigner did not report a signer fingerprint")
    if fingerprint_match.group(1).lower() != expected_fingerprint:
        raise GateError("APK signer fingerprint does not match the test certificate")

    aapt = _build_tool(android_sdk, "aapt")
    badging = _run(
        [str(aapt), "dump", "badging", str(apk)],
        cwd=client_root,
        env=os.environ.copy(),
    ).stdout
    package = re.search(
        r"package: name='([^']+)' versionCode='([^']+)' versionName='([^']+)'",
        badging,
    )
    if package is None:
        raise GateError("aapt did not report APK identity")
    if package.groups() != (
        EXPECTED_APPLICATION_ID,
        expected_version_code,
        expected_version_name,
    ):
        raise GateError(f"unexpected release APK identity: {package.groups()!r}")
    if "application-label:'MnemoNAS'" not in badging:
        raise GateError("release APK label is not MnemoNAS")


def _verify_bundle(
    java_home: Path,
    bundle: Path,
    expected_fingerprint: str,
) -> None:
    _run(
        [_tool(java_home, "jarsigner"), "-verify", str(bundle)],
        cwd=bundle.parent,
        env=os.environ.copy(),
    )
    certificate = _run(
        [
            _tool(java_home, "keytool"),
            "-printcert",
            "-rfc",
            "-jarfile",
            str(bundle),
        ],
        cwd=bundle.parent,
        env=os.environ.copy(),
    ).stdout.encode("ascii")
    if _certificate_fingerprint(certificate) != expected_fingerprint:
        raise GateError("AAB signer fingerprint does not match the test certificate")


def _release_outputs(client_root: Path) -> tuple[Path, Path]:
    return (
        client_root / "build" / "app" / "outputs" / "apk" / "release" / "app-release.apk",
        client_root
        / "build"
        / "app"
        / "outputs"
        / "bundle"
        / "release"
        / "app-release.aab",
    )


def _cleanup_release_outputs(client_root: Path) -> None:
    for path in (
        client_root / "build" / "app" / "outputs" / "apk" / "release",
        client_root / "build" / "app" / "outputs" / "bundle" / "release",
        client_root
        / "build"
        / "app"
        / "intermediates"
        / "intermediary_bundle"
        / "release",
    ):
        shutil.rmtree(path, ignore_errors=True)
    flutter_apk = client_root / "build" / "app" / "outputs" / "flutter-apk"
    for path in flutter_apk.glob("app-release.apk*"):
        path.unlink(missing_ok=True)


def _repository_key_files(client_root: Path) -> set[Path]:
    patterns = ("key.properties", "*.jks", "*.keystore", "*.p12", "*.pfx", "*.pkcs12")
    return {
        path.resolve()
        for pattern in patterns
        for path in client_root.rglob(pattern)
        if ".dart_tool" not in path.parts and "build" not in path.parts
    }


def _declared_flutter_version(client_root: Path) -> tuple[str, str]:
    pubspec = client_root / "pubspec.yaml"
    try:
        contents = pubspec.read_text(encoding="utf-8")
    except OSError as error:
        raise GateError(f"unable to read Flutter version: {error}") from error
    match = re.search(
        r"^version:\s*([^+\s]+)\+([1-9][0-9]*)\s*$",
        contents,
        flags=re.MULTILINE,
    )
    if match is None:
        raise GateError("pubspec version must include a positive Android build number")
    return match.group(1), match.group(2)


def run_gate(client_root: Path, java_home: Path) -> None:
    if not (java_home / "bin" / "javac").is_file():
        raise GateError("a complete JDK is required")
    with _temporary_android_build_files(client_root):
        _run_prepared_gate(client_root, java_home)


def _run_prepared_gate(client_root: Path, java_home: Path) -> None:
    android_root = client_root / "android"
    gradlew = android_root / "gradlew"
    if not gradlew.is_file() or not os.access(gradlew, os.X_OK):
        raise GateError("Android Gradle wrapper is unavailable")

    expected_version_name, expected_version_code = _declared_flutter_version(
        client_root
    )
    initial_key_files = _repository_key_files(client_root)
    _cleanup_release_outputs(client_root)
    try:
        with tempfile.TemporaryDirectory(prefix="mnemonas-release-signing-") as temp:
            temporary = Path(temp)
            missing = temporary / "missing.properties"
            _run_gradle(
                android_root,
                java_home,
                [":app:assembleRelease"],
                None,
                expect_success=False,
                expected_error="an explicit key properties path is required",
            )
            print("android-release-signing: missing APK signing properties rejected")
            _run_gradle(
                android_root,
                java_home,
                [":app:bundleRelease"],
                None,
                expect_success=False,
                expected_error="an explicit key properties path is required",
            )
            print("android-release-signing: missing AAB signing properties rejected")
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                missing,
                expect_success=False,
                expected_error="key properties file is missing or unreadable",
            )
            print("android-release-signing: missing explicit properties file rejected")

            blank = temporary / "blank.properties"
            blank.write_text(
                "storeFile=\nstorePassword=\nkeyAlias=\nkeyPassword=\ncertificateSha256=\n",
                encoding="utf-8",
            )
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                blank,
                expect_success=False,
                expected_error="missing or blank fields",
            )
            print("android-release-signing: blank signing fields rejected")

            unreadable = temporary / "unreadable.properties"
            unreadable.mkdir()
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                unreadable,
                expect_success=False,
                expected_error="key properties file is missing or unreadable",
            )
            print("android-release-signing: unreadable signing properties rejected")

            valid_store = temporary / "release-test.p12"
            _generate_key(
                java_home,
                valid_store,
                alias=TEST_ALIAS,
                distinguished_name=(
                    "CN=MnemoNAS Release Test,OU=Client,O=MnemoNAS Test,C=CN"
                ),
            )
            valid_certificate = _export_certificate(
                java_home,
                valid_store,
                TEST_ALIAS,
            )
            valid_fingerprint = _certificate_fingerprint(valid_certificate)
            valid = temporary / "valid.properties"
            _write_properties(
                valid,
                store_file="release-test.p12",
                alias=TEST_ALIAS,
                fingerprint=valid_fingerprint,
            )
            with tempfile.TemporaryDirectory(
                prefix=".mnemonas-signing-path-policy-",
                dir=client_root,
            ) as checkout_temp:
                checkout_directory = Path(checkout_temp)
                inside_properties = checkout_directory / "release.properties"
                inside_properties.write_text("", encoding="utf-8")
                _run_gradle(
                    android_root,
                    java_home,
                    [":app:validateReleaseSigning"],
                    inside_properties,
                    expect_success=False,
                    expected_error=(
                        "key properties must be outside the source checkout"
                    ),
                )
                print(
                    "android-release-signing: source-checkout properties rejected"
                )

                inside_store = checkout_directory / "release-test.p12"
                inside_store.symlink_to(valid_store)
                inside_store_properties = temporary / "inside-store.properties"
                _write_properties(
                    inside_store_properties,
                    store_file=inside_store,
                    alias=TEST_ALIAS,
                    fingerprint=valid_fingerprint,
                )
                _run_gradle(
                    android_root,
                    java_home,
                    [":app:validateReleaseSigning"],
                    inside_store_properties,
                    expect_success=False,
                    expected_error="keystore must be outside the source checkout",
                )
                print(
                    "android-release-signing: source-checkout keystore link rejected"
                )

            malformed_fingerprint = temporary / "malformed-fingerprint.properties"
            _write_properties(
                malformed_fingerprint,
                store_file=valid_store,
                alias=TEST_ALIAS,
                fingerprint="not-a-fingerprint",
            )
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                malformed_fingerprint,
                expect_success=False,
                expected_error="exactly 64 hexadecimal characters",
            )
            print("android-release-signing: malformed certificate fingerprint rejected")

            missing_alias = temporary / "missing-alias.properties"
            _write_properties(
                missing_alias,
                store_file=valid_store,
                alias="missing-release-key",
                fingerprint=valid_fingerprint,
            )
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                missing_alias,
                expect_success=False,
                expected_error="configured key alias was not found",
            )
            print("android-release-signing: missing key alias rejected")

            truststore, trusted_alias = _create_trusted_certificate_store(
                java_home,
                temporary,
                valid_certificate,
            )
            trusted_entry = temporary / "trusted-entry.properties"
            _write_properties(
                trusted_entry,
                store_file=truststore,
                alias=trusted_alias,
                fingerprint=valid_fingerprint,
            )
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                trusted_entry,
                expect_success=False,
                expected_error="configured alias is not a key entry",
            )
            print("android-release-signing: certificate-only alias rejected")

            wrong_key_password = temporary / "wrong-key-password.properties"
            _write_properties(
                wrong_key_password,
                store_file=valid_store,
                alias=TEST_ALIAS,
                fingerprint=valid_fingerprint,
                key_password=WRONG_KEY_PASSWORD,
            )
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                wrong_key_password,
                expect_success=False,
                expected_error="key entry or key credentials are invalid",
            )
            print("android-release-signing: invalid key credentials rejected")

            expired_store = temporary / "expired-test.p12"
            _generate_key(
                java_home,
                expired_store,
                alias="expired-release-test",
                distinguished_name="CN=Expired Release Test,O=MnemoNAS Test,C=CN",
                start_date="2020/01/01 00:00:00",
                validity_days=1,
            )
            expired = temporary / "expired.properties"
            _write_properties(
                expired,
                store_file=expired_store,
                alias="expired-release-test",
                fingerprint=_certificate_fingerprint(
                    _export_certificate(
                        java_home,
                        expired_store,
                        "expired-release-test",
                    )
                ),
            )
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                expired,
                expect_success=False,
                expected_error="signing certificate has expired",
            )
            print("android-release-signing: expired certificate rejected")

            future_store = temporary / "future-test.p12"
            _generate_key(
                java_home,
                future_store,
                alias="future-release-test",
                distinguished_name="CN=Future Release Test,O=MnemoNAS Test,C=CN",
                start_date="2099/01/01 00:00:00",
            )
            future = temporary / "future.properties"
            _write_properties(
                future,
                store_file=future_store,
                alias="future-release-test",
                fingerprint=_certificate_fingerprint(
                    _export_certificate(
                        java_home,
                        future_store,
                        "future-release-test",
                    )
                ),
            )
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                future,
                expect_success=False,
                expected_error="signing certificate is not yet valid",
            )
            print("android-release-signing: not-yet-valid certificate rejected")

            debug_store = temporary / "android-debug-test.p12"
            _generate_key(
                java_home,
                debug_store,
                alias="custom-debug-alias",
                distinguished_name="CN=Android Debug,O=Android,C=US",
            )
            debug_certificate = temporary / "android-debug.properties"
            _write_properties(
                debug_certificate,
                store_file=debug_store,
                alias="custom-debug-alias",
                fingerprint=_certificate_fingerprint(
                    _export_certificate(
                        java_home,
                        debug_store,
                        "custom-debug-alias",
                    )
                ),
            )
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                debug_certificate,
                expect_success=False,
                expected_error="Android Debug certificates are forbidden",
            )
            print("android-release-signing: Android Debug certificate rejected")

            _run_gradle(
                android_root,
                java_home,
                [":app:aR", "-x", "validateReleaseSigning"],
                debug_certificate,
                expect_success=False,
                expected_error="Android Debug certificates are forbidden",
            )
            print(
                "android-release-signing: abbreviated release task exclusion bypass rejected"
            )

            for aggregate_task in (":app:assemble", ":app:bundle"):
                _run_gradle(
                    android_root,
                    java_home,
                    [aggregate_task, "-x", "validateReleaseSigning"],
                    debug_certificate,
                    expect_success=False,
                    expected_error="Android Debug certificates are forbidden",
                )
            print(
                "android-release-signing: aggregate release task exclusions rejected"
            )

            _run_gradle(
                android_root,
                java_home,
                [":app:assembleRelease"],
                valid,
                expect_success=False,
                expected_error="injected signing properties are forbidden",
                extra_gradle_properties={
                    "android.injected.signing.store.file": str(debug_store),
                    "android.injected.signing.store.password": TEST_PASSWORD,
                    "android.injected.signing.key.alias": "custom-debug-alias",
                    "android.injected.signing.key.password": TEST_PASSWORD,
                    "android.injected.signing.store.type": "PKCS12",
                    "android.injected.signing.v1-enabled": "true",
                    "android.injected.signing.v2-enabled": "true",
                    "android.injected.signing.unrecognized-test-option": "true",
                },
            )
            print("android-release-signing: injected signing override rejected")

            _run_gradle(
                android_root,
                java_home,
                [":app:assembleRelease"],
                valid,
                expect_success=False,
                expected_error="injected signing properties are forbidden",
                extra_system_properties={
                    "org.gradle.project.android.injected.signing.store.file": str(
                        debug_store
                    ),
                },
            )
            _run_gradle(
                android_root,
                java_home,
                [":app:assembleRelease"],
                valid,
                expect_success=False,
                expected_error="injected signing properties are forbidden",
                extra_environment={
                    "ORG_GRADLE_PROJECT_android.injected.signing.store.file": str(
                        debug_store
                    ),
                },
            )
            print(
                "android-release-signing: system and environment signing injection rejected"
            )

            wrong_fingerprint = temporary / "wrong-fingerprint.properties"
            _write_properties(
                wrong_fingerprint,
                store_file=valid_store,
                alias=TEST_ALIAS,
                fingerprint="0" * 64,
            )
            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                wrong_fingerprint,
                expect_success=False,
                expected_error="certificate SHA-256 fingerprint does not match",
            )
            print("android-release-signing: mismatched certificate fingerprint rejected")

            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                valid,
                expect_success=True,
            )
            print("android-release-signing: temporary key validation passed")

            _run_gradle(
                android_root,
                java_home,
                [":app:validateReleaseSigning"],
                valid,
                expect_success=True,
                use_gradle_property=True,
            )
            print("android-release-signing: Gradle property path injection passed")

            cache_outputs = [
                _run_gradle(
                    android_root,
                    java_home,
                    [":app:validateReleaseSigning"],
                    valid,
                    expect_success=True,
                    configuration_cache=True,
                )
                for _ in range(2)
            ]
            if any("Reusing configuration cache" in output for output in cache_outputs):
                raise GateError(
                    "release signing validation reused the configuration cache"
                )
            if any("Configuration cache entry stored" in output for output in cache_outputs):
                raise GateError(
                    "release signing validation stored a configuration cache entry"
                )
            print(
                "android-release-signing: configuration-cache reuse rejected"
            )

            debug_cache_outputs = [
                _run_gradle(
                    android_root,
                    java_home,
                    [":app:assembleDebug"],
                    valid,
                    expect_success=True,
                    configuration_cache=True,
                )
                for _ in range(2)
            ]
            if any(
                "Reusing configuration cache" in output
                or "Configuration cache entry stored" in output
                for output in debug_cache_outputs
            ):
                raise GateError(
                    "debug build cached configured release signing credentials"
                )
            print(
                "android-release-signing: debug signing credentials cache rejected"
            )

            _run_gradle(
                android_root,
                java_home,
                [":app:assembleRelease", ":app:bundleRelease"],
                valid,
                expect_success=True,
            )
            apk, bundle = _release_outputs(client_root)
            if not apk.is_file() or not bundle.is_file():
                raise GateError("signed release APK or AAB was not produced")
            _verify_apk(
                client_root,
                apk,
                valid_fingerprint,
                expected_version_code,
                expected_version_name,
            )
            _verify_bundle(java_home, bundle, valid_fingerprint)
            print("android-release-signing: signed APK and AAB verification passed")
    finally:
        _cleanup_release_outputs(client_root)

    if _repository_key_files(client_root) != initial_key_files:
        raise GateError("the release-signing gate left key material in the repository")


def main() -> int:
    default_client_root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--client-root",
        type=Path,
        default=default_client_root,
    )
    parser.add_argument(
        "--java-home",
        type=Path,
        default=os.environ.get("JAVA_HOME"),
        required=os.environ.get("JAVA_HOME") is None,
    )
    args = parser.parse_args()

    try:
        run_gate(args.client_root.resolve(), args.java_home.resolve())
    except GateError as error:
        print(f"android-release-signing: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
