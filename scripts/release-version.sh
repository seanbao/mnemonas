#!/usr/bin/env bash

release_version_fail() {
	local message="$1"

	if declare -F fail >/dev/null 2>&1; then
		fail "$message"
	fi

	printf 'release-version: %s\n' "$message" >&2
	exit 1
}

release_version_contains_control_character() {
	local value="$1"

	LC_ALL=C printf '%s' "$value" | LC_ALL=C grep -q '[[:cntrl:]]'
}

release_version_contains_whitespace_character() {
	local value="$1"

	[[ "$value" == *[[:space:]]* ]]
}

release_version_with_optional_value() {
	local message="$1"
	local value="$2"
	local include_value="$3"

	if [[ "$include_value" == "1" ]]; then
		printf '%s: %s' "$message" "$value"
	else
		printf '%s' "$message"
	fi
}

validate_docker_release_version() {
	local value="$1"
	local label="$2"
	local empty_message="$3"
	local include_value_in_shape_errors="$4"
	local pattern
	local major
	local minor
	local patch
	local prerelease
	local docker_tag
	local component
	local identifier
	local message

	[[ -n "$value" ]] || release_version_fail "$empty_message"
	if release_version_contains_control_character "$value" || release_version_contains_whitespace_character "$value"; then
		release_version_fail "$label must not contain whitespace or control characters"
	fi
	if [[ "$value" == *+* ]]; then
		message="$(release_version_with_optional_value "$label must not include build metadata because Docker tags do not support '+'" "$value" "$include_value_in_shape_errors")"
		release_version_fail "$message"
	fi

	pattern='^v([0-9]+)\.([0-9]+)\.([0-9]+)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
	if [[ ! "$value" =~ $pattern ]]; then
		message="$(release_version_with_optional_value "$label must match vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-PRERELEASE" "$value" "$include_value_in_shape_errors")"
		release_version_fail "$message"
	fi

	major="${BASH_REMATCH[1]}"
	minor="${BASH_REMATCH[2]}"
	patch="${BASH_REMATCH[3]}"
	prerelease="${BASH_REMATCH[5]:-}"
	docker_tag="${value#v}"
	if ((${#docker_tag} > 128)); then
		release_version_fail "$label without the v prefix must be at most 128 characters for Docker image tags: $value"
	fi

	for component in "$major" "$minor" "$patch"; do
		if [[ "$component" =~ ^0[0-9]+$ ]]; then
			release_version_fail "$label numeric components must not contain leading zeroes: $value"
		fi
	done

	if [[ -n "$prerelease" ]]; then
		IFS='.' read -r -a identifiers <<<"$prerelease"
		for identifier in "${identifiers[@]}"; do
			if [[ "$identifier" =~ ^[0-9]+$ && "$identifier" =~ ^0[0-9]+$ ]]; then
				release_version_fail "$label numeric prerelease identifiers must not contain leading zeroes: $value"
			fi
		done
	fi
}
