#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

if ! command -v python3 >/dev/null 2>&1; then
  printf 'check-yaml-configs: python3 is required to validate YAML configuration files\n' >&2
  exit 1
fi

if [[ "$#" -eq 0 ]]; then
  set -- \
    .github/dependabot.yml \
    .github/dependabot.yaml \
    codecov.yml \
    codecov.yaml \
    .pre-commit-config.yaml
fi

python3 - "$@" <<'PY'
import pathlib
import sys
from collections.abc import Hashable

try:
    import yaml
    from yaml.constructor import ConstructorError
    from yaml.nodes import MappingNode
except ModuleNotFoundError:
    print(
        "check-yaml-configs: PyYAML is required; install python3-yaml or run `python3 -m pip install PyYAML`",
        file=sys.stderr,
    )
    sys.exit(1)


class UniqueKeyLoader(yaml.SafeLoader):
    pass


def construct_mapping_without_duplicates(loader, node, deep=False):
    if not isinstance(node, MappingNode):
        raise ConstructorError(
            None,
            None,
            f"expected a mapping node, but found {node.id}",
            node.start_mark,
        )

    seen = {}
    for key_node, _ in node.value:
        # YAML merge keys can legitimately override inherited keys. This guard
        # catches repeated keys written in the same mapping without changing
        # standard merge-key semantics.
        if key_node.tag == "tag:yaml.org,2002:merge":
            continue

        key = loader.construct_object(key_node, deep=deep)
        if not isinstance(key, Hashable):
            raise ConstructorError(
                "while constructing a mapping",
                node.start_mark,
                "found unhashable key",
                key_node.start_mark,
            )
        if key in seen:
            raise ConstructorError(
                "while constructing a mapping",
                node.start_mark,
                f"found duplicate key {key!r}",
                key_node.start_mark,
            )
        seen[key] = key_node.start_mark

    return yaml.SafeLoader.construct_mapping(loader, node, deep=deep)


UniqueKeyLoader.add_constructor(
    yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG,
    construct_mapping_without_duplicates,
)

errors = []
checked = 0

for raw_path in sys.argv[1:]:
    path = pathlib.Path(raw_path)
    if not path.exists():
        continue
    if path.is_dir():
        errors.append(f"{path}: expected a YAML file, got a directory")
        continue

    try:
        yaml.load(path.read_text(encoding="utf-8"), Loader=UniqueKeyLoader)
    except UnicodeDecodeError as error:
        errors.append(f"{path}: invalid UTF-8: {error}")
        continue
    except yaml.YAMLError as error:
        errors.append(f"{path}: invalid YAML: {error}")
        continue

    checked += 1

if errors:
    for error in errors:
        print(error, file=sys.stderr)
    sys.exit(1)

print(f"[yaml-config-check] checked {checked} YAML file(s)")
PY
