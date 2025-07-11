#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

if ! command -v python3 >/dev/null 2>&1; then
  printf 'check-toolchain-versions: python3 is required\n' >&2
  exit 1
fi

python3 - <<'PY'
import json
import pathlib
import re
import sys

ROOT = pathlib.Path.cwd()


def read_text(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def read_json(path: str):
    with (ROOT / path).open("r", encoding="utf-8") as handle:
        return json.load(handle)


def first_match(path: str, pattern: str, label: str) -> str:
    match = re.search(pattern, read_text(path), flags=re.MULTILINE)
    if not match:
        fail(f"{path}: missing {label}")
    return match.group(1)


def fail(message: str) -> None:
    print(f"check-toolchain-versions: {message}", file=sys.stderr)
    sys.exit(1)


def require_equal(label: str, got: str, expected: str) -> None:
    if got != expected:
        fail(f"{label} = {got!r}, expected {expected!r}")


def require_contains(path: str, needle: str) -> None:
    if needle not in read_text(path):
        fail(f"{path}: missing {needle!r}")


def require_json_object_equal(label: str, got, expected) -> None:
    if not isinstance(got, dict):
        fail(f"{label} is {type(got).__name__}, expected object")
    if not isinstance(expected, dict):
        fail(f"{label} expected value is {type(expected).__name__}, expected object")
    if got == expected:
        return

    got_keys = set(got)
    expected_keys = set(expected)
    missing = sorted(expected_keys - got_keys)
    extra = sorted(got_keys - expected_keys)
    changed = sorted(key for key in got_keys & expected_keys if got[key] != expected[key])
    details = []
    if missing:
        details.append(f"missing keys {missing[:5]}")
    if extra:
        details.append(f"extra keys {extra[:5]}")
    if changed:
        details.append(f"changed keys {changed[:5]}")
    fail(f"{label} does not match web/package.json ({'; '.join(details)})")


def version_major_minor(version: str) -> str:
    parts = version.split(".")
    if len(parts) < 2:
        fail(f"invalid version {version!r}")
    return ".".join(parts[:2])


go_version = read_text(".go-version").strip()
node_version = read_text(".nvmrc").strip()
web_node_version = read_text("web/.nvmrc").strip()
go_toolchain = first_match("go.mod", r"^toolchain\s+go([0-9][^\s]+)$", "Go toolchain directive")
go_language = first_match("go.mod", r"^go\s+([0-9][^\s]+)$", "Go language directive")
dataplane_rust = first_match("dataplane/Cargo.toml", r'^rust-version\s*=\s*"([^"]+)"$', "dataplane rust-version")
proto_gen_rust = first_match("tools/proto-gen/Cargo.toml", r'^rust-version\s*=\s*"([^"]+)"$', "proto generator rust-version")

require_equal(".nvmrc vs web/.nvmrc", web_node_version, node_version)
require_equal(".go-version vs go.mod toolchain", go_toolchain, go_version)
require_equal("go.mod go directive major.minor", version_major_minor(go_language), version_major_minor(go_version))
require_equal("dataplane vs proto generator rust-version", proto_gen_rust, dataplane_rust)

workflow_files = [
    ".github/workflows/ci.yml",
    ".github/workflows/release.yml",
    ".github/workflows/torture.yml",
]
for workflow in workflow_files:
    require_equal(
        f"{workflow} env.GO_VERSION",
        first_match(workflow, r"^\s*GO_VERSION:\s*'([^']+)'", "GO_VERSION"),
        go_version,
    )
    require_equal(
        f"{workflow} env.RUST_VERSION",
        first_match(workflow, r"^\s*RUST_VERSION:\s*'([^']+)'", "RUST_VERSION"),
        dataplane_rust,
    )
    require_equal(
        f"{workflow} env.NODE_VERSION",
        first_match(workflow, r"^\s*NODE_VERSION:\s*'([^']+)'", "NODE_VERSION"),
        node_version,
    )

go_image = f"golang:{go_version}-alpine"
rust_image = f"rust:{dataplane_rust}"
node_image = f"node:{node_version}-bookworm-slim"

require_contains("Dockerfile", f"ARG GO_IMAGE={go_image}")
require_contains("Dockerfile", f"ARG RUST_IMAGE={rust_image}")
require_contains("Dockerfile", f"ARG NODE_IMAGE={node_image}")
require_contains(".env.example", f"# GO_IMAGE={go_image}")
require_contains(".env.example", f"# RUST_IMAGE={rust_image}")
require_contains(".env.example", f"# NODE_IMAGE={node_image}")
require_contains("docker-compose.yml", f"GO_IMAGE: ${{GO_IMAGE:-{go_image}}}")
require_contains("docker-compose.yml", f"RUST_IMAGE: ${{RUST_IMAGE:-{rust_image}}}")
require_contains("docker-compose.yml", f"NODE_IMAGE: ${{NODE_IMAGE:-{node_image}}}")

package = read_json("web/package.json")
lockfile = read_json("web/package-lock.json")
lock_root = lockfile.get("packages", {}).get("", {})
expected_web_package_name = "mnemonas-web"
package_name = package.get("name", "")
lockfile_name = lockfile.get("name", "")
lock_root_name = lock_root.get("name", "")
engine = package.get("engines", {}).get("node", "")
lock_engine = lock_root.get("engines", {}).get("node", "")
require_equal("web/package.json name", package_name, expected_web_package_name)
require_equal("web/package-lock.json name", lockfile_name, package_name)
require_equal("web/package-lock.json root package name", lock_root_name, package_name)
require_equal("web/package-lock.json root node engine", lock_engine, engine)
for manifest_key in ("dependencies", "devDependencies", "optionalDependencies"):
    require_json_object_equal(
        f"web/package-lock.json root {manifest_key}",
        lock_root.get(manifest_key, {}),
        package.get(manifest_key, {}),
    )
require_contains("README.md", f"Go {go_version}+")
require_contains("README.md", f"Rust {dataplane_rust}+")
require_contains("README.md", f"推荐使用 `.nvmrc` 指定的 {node_version}.x")
require_contains("README.en.md", f"Go {go_version}+")
require_contains("README.en.md", f"Rust {dataplane_rust}+")
require_contains("README.en.md", f"Node {node_version} from `.nvmrc` is recommended")
require_contains("docs/development.md", f"| Go | {go_version} | {go_version}+ |")
require_contains("docs/development.md", f"| Rust | {dataplane_rust} | {dataplane_rust}.x |")
require_contains("docs/development.md", f"GO_VERSION={go_version}")
require_contains("docs/development.en.md", f"| Go | {go_version} | {go_version}+ |")
require_contains("docs/development.en.md", f"| Rust | {dataplane_rust} | {dataplane_rust}.x |")
require_contains("docs/development.en.md", f"GO_VERSION={go_version}")
require_contains("docs/docker-deployment.md", f"--build-arg GO_IMAGE={go_image}")
require_contains("docs/docker-deployment.md", f"--build-arg RUST_IMAGE={rust_image}")
require_contains("docs/docker-deployment.md", f"--build-arg NODE_IMAGE={node_image}")
require_contains("docs/docker-deployment.en.md", f"--build-arg GO_IMAGE={go_image}")
require_contains("docs/docker-deployment.en.md", f"--build-arg RUST_IMAGE={rust_image}")
require_contains("docs/docker-deployment.en.md", f"--build-arg NODE_IMAGE={node_image}")

print(
    "[toolchain-version-check] "
    f"Go {go_version}, Rust {dataplane_rust}, Node {node_version}, "
    f"Node engine {engine}, Web package {package_name}"
)
PY
