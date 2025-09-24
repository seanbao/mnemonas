#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[doc-links-test] ERROR: %s\n' "$*" >&2
	exit 1
}

assert_file_contains() {
	local path="$1"
	local expected="$2"
	grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

new_repo() {
	local name="$1"
	local repo="$TMP_ROOT/$name"
	mkdir -p "$repo/scripts"
	cp "$REPO_ROOT/scripts/check-doc-links.sh" "$repo/scripts/check-doc-links.sh"
	cp "$REPO_ROOT/go.mod" "$REPO_ROOT/go.sum" "$repo/"
	chmod +x "$repo/scripts/check-doc-links.sh"
	git -C "$repo" init -q
	printf '%s\n' "$repo"
}

write_root_readme_pair() {
	local repo="$1"
	cat > "$repo/README.md" <<'EOF'
# Project Guide

English guide: [English](README.en.md)
EOF
	cat > "$repo/README.en.md" <<'EOF'
# Project Guide

Chinese guide: [简体中文](README.md)
EOF
	git -C "$repo" add README.md README.en.md
}

run_accepts() {
	local name="$1"
	local repo
	repo="$(new_repo "$name")"
	shift
	"$@" "$repo"
	(cd "$repo" && ./scripts/check-doc-links.sh) > "$TMP_ROOT/$name.log" 2>&1
}

run_rejects() {
	local name="$1"
	local expected="$2"
	local repo
	local status
	repo="$(new_repo "$name")"
	shift 2
	"$@" "$repo"

	set +e
	(cd "$repo" && ./scripts/check-doc-links.sh) > "$TMP_ROOT/$name.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "$name was accepted"
	assert_file_contains "$TMP_ROOT/$name.log" "$expected"
}

write_valid_docs() {
	local repo="$1"
	mkdir -p "$repo/docs"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

See [install docs](docs/install.md), [usage with title](docs/usage.md "Usage"), and [API](docs/api.md#api-reference).
EOF
	cat > "$repo/docs/install.md" <<'EOF'
# Install
EOF
	cat > "$repo/docs/install.en.md" <<'EOF'
# Install
EOF
	cat > "$repo/docs/usage.md" <<'EOF'
# Usage
EOF
	cat > "$repo/docs/usage.en.md" <<'EOF'
# Usage
EOF
	cat > "$repo/docs/api.md" <<'EOF'
# API Reference
EOF
	cat > "$repo/docs/api.en.md" <<'EOF'
# API Reference
EOF
	cat > "$repo/docs/README.md" <<'EOF'
# 文档

| 中文 | English |
|------|---------|
| [Install](install.md) | [Install](install.en.md) |
| [Usage](usage.md) | [Usage](usage.en.md) |
| [API](api.md) | [API](api.en.md) |
EOF
	cat > "$repo/docs/README.en.md" <<'EOF'
# Documentation

| Document | Description |
| --- | --- |
| [Install](install.en.md) | Install guide |
| [Usage](usage.en.md) | Usage guide |
| [API](api.en.md) | API reference |
EOF
	git -C "$repo" add README.md docs/README.md docs/README.en.md docs/install.md docs/install.en.md docs/usage.md docs/usage.en.md docs/api.md docs/api.en.md
}

write_untracked_valid_doc() {
	local repo="$1"
	mkdir -p "$repo/docs"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

See [draft](docs/draft.md).
EOF
	cat > "$repo/docs/draft.md" <<'EOF'
# Draft
EOF
	cat > "$repo/docs/draft.en.md" <<'EOF'
# Draft
EOF
	git -C "$repo" add README.md
}

write_missing_file_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

See [missing](docs/missing.md).
EOF
	git -C "$repo" add README.md
}

write_escaping_link_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

See [outside](../outside.md).
EOF
	git -C "$repo" add README.md
}

write_missing_anchor_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

See [missing anchor](#missing-section).

## Existing Section
EOF
	git -C "$repo" add README.md
}

write_missing_english_doc_pair() {
	local repo="$1"
	mkdir -p "$repo/docs"
	write_root_readme_pair "$repo"
	cat > "$repo/docs/guide.md" <<'EOF'
# 指南

[English](guide.en.md) | 简体中文
EOF
	git -C "$repo" add docs/guide.md
}

write_missing_chinese_doc_pair() {
	local repo="$1"
	mkdir -p "$repo/docs"
	write_root_readme_pair "$repo"
	cat > "$repo/docs/guide.en.md" <<'EOF'
# Guide

English | [简体中文](guide.md)
EOF
	git -C "$repo" add docs/guide.en.md
}

write_missing_chinese_index_entry() {
	local repo="$1"
	mkdir -p "$repo/docs"
	write_root_readme_pair "$repo"
	cat > "$repo/docs/README.md" <<'EOF'
# 文档

| 中文 | English |
|------|---------|
| [Guide](guide.md) | [Guide](guide.en.md) |
EOF
	cat > "$repo/docs/README.en.md" <<'EOF'
# Documentation

| Document | Description |
| --- | --- |
| [Guide](guide.en.md) | Guide |
| [Operations](operations.en.md) | Operations |
EOF
	cat > "$repo/docs/guide.md" <<'EOF'
# Guide
EOF
	cat > "$repo/docs/guide.en.md" <<'EOF'
# Guide
EOF
	cat > "$repo/docs/operations.md" <<'EOF'
# Operations
EOF
	cat > "$repo/docs/operations.en.md" <<'EOF'
# Operations
EOF
	git -C "$repo" add docs/README.md docs/README.en.md docs/guide.md docs/guide.en.md docs/operations.md docs/operations.en.md
}

write_missing_english_index_entry() {
	local repo="$1"
	mkdir -p "$repo/docs"
	write_root_readme_pair "$repo"
	cat > "$repo/docs/README.md" <<'EOF'
# 文档

| 中文 | English |
|------|---------|
| [Guide](guide.md) | [Guide](guide.en.md) |
| [Operations](operations.md) | [Operations](operations.en.md) |
EOF
	cat > "$repo/docs/README.en.md" <<'EOF'
# Documentation

| Document | Description |
| --- | --- |
| [Guide](guide.en.md) | Guide |
EOF
	cat > "$repo/docs/guide.md" <<'EOF'
# Guide
EOF
	cat > "$repo/docs/guide.en.md" <<'EOF'
# Guide
EOF
	cat > "$repo/docs/operations.md" <<'EOF'
# Operations
EOF
	cat > "$repo/docs/operations.en.md" <<'EOF'
# Operations
EOF
	git -C "$repo" add docs/README.md docs/README.en.md docs/guide.md docs/guide.en.md docs/operations.md docs/operations.en.md
}

write_missing_root_english_pair() {
	local repo="$1"
	cat > "$repo/README.md" <<'EOF'
# Project Guide
EOF
	git -C "$repo" add README.md
}

write_missing_root_chinese_pair() {
	local repo="$1"
	cat > "$repo/README.en.md" <<'EOF'
# Project Guide
EOF
	git -C "$repo" add README.en.md
}

write_valid_json_fence_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```json
{
  "success": true,
  "data": {
    "id": "example"
  }
}
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_invalid_json_fence_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```json
{
  "success": true,
  "data": { ... }
}
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_valid_toml_fence_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```toml
[server]
host = "127.0.0.1"
port = 8080
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_invalid_toml_fence_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```toml
[server]
port = 8080
port = 8081
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_decorative_heading_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

## 🚀 快速开始
EOF
	git -C "$repo" add README.md README.en.md
}

write_promotional_wording_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

> Your files. Your control.
EOF
	git -C "$repo" add README.md README.en.md
}

write_security_check_docs_base() {
	local repo="$1"
	local english_ids="${2-}"
	local chinese_ids="${3-}"
	if [[ -z "$english_ids" ]]; then
		english_ids="\`auth_enabled\`,
\`config_file_access\`"
	fi
	if [[ -z "$chinese_ids" ]]; then
		chinese_ids="\`auth_enabled\`、
\`config_file_access\`"
	fi
	mkdir -p "$repo/docs" "$repo/internal/api"
	write_root_readme_pair "$repo"
	cat > "$repo/internal/api/server.go" <<'EOF'
package api

type securityCheckItem struct {
	ID string
}

type auditMetadata struct {
	ID string
}

func securityExampleChecks() []securityCheckItem {
	_ = "ID: \"ghost_string\" { }"
	// const checkID = "ghost_comment"
	_ = auditMetadata{ID: "ghost_metadata"}
	const unusedCheckID = "ghost_unused"
	_ = unusedCheckID
	const checkID = "config_file_access"
	return []securityCheckItem{
		{ID: "auth_enabled"},
		{ID: checkID},
	}
}

func unrelatedRouteMetadata() securityCheckItem {
	return securityCheckItem{ID: "background_job"}
}
EOF
	cat > "$repo/docs/README.md" <<'EOF'
# 文档

| 中文 | English |
|------|---------|
| [API Reference](api-reference.md) | [API Reference](api-reference.en.md) |
EOF
	cat > "$repo/docs/README.en.md" <<'EOF'
# Documentation

| Document | Description |
| --- | --- |
| [API Reference](api-reference.en.md) | API reference |
EOF
	cat > "$repo/docs/api-reference.md" <<EOF
# API Reference

- \`checks[].id\` 当前包含 $chinese_ids
EOF
	cat > "$repo/docs/api-reference.en.md" <<EOF
# API Reference

Current check IDs include $english_ids.
EOF
	git -C "$repo" add README.md README.en.md docs/README.md docs/README.en.md docs/api-reference.md docs/api-reference.en.md internal/api/server.go
}

write_security_check_docs_valid() {
	local repo="$1"
	write_security_check_docs_base "$repo"
}

write_security_check_docs_missing_english_id() {
	local repo="$1"
	write_security_check_docs_base "$repo" "\`auth_enabled\`"
}

write_security_check_docs_missing_chinese_id() {
	local repo="$1"
	write_security_check_docs_base "$repo" "\`auth_enabled\` and \`config_file_access\`" "\`auth_enabled\`"
}

write_security_check_docs_unknown_english_id() {
	local repo="$1"
	write_security_check_docs_base "$repo" "\`auth_enabled\`, \`config_file_access\`, and \`ghost_probe\`"
}

write_security_check_docs_unknown_chinese_id() {
	local repo="$1"
	write_security_check_docs_base "$repo" "\`auth_enabled\` and \`config_file_access\`" "\`auth_enabled\`、\`config_file_access\`、\`ghost_probe\`"
}

run_accepts "valid-links" write_valid_docs
run_accepts "untracked-valid-link-target" write_untracked_valid_doc
run_accepts "valid-json-fence" write_valid_json_fence_doc
run_accepts "valid-toml-fence" write_valid_toml_fence_doc
run_accepts "security-check-doc-ids" write_security_check_docs_valid
run_rejects "missing-file" "missing link target: docs/missing.md" write_missing_file_doc
run_rejects "escaping-link" "link escapes repository: ../outside.md" write_escaping_link_doc
run_rejects "missing-anchor" "missing heading anchor: #missing-section" write_missing_anchor_doc
run_rejects "missing-english-doc-pair" "missing English documentation pair: docs/guide.en.md" write_missing_english_doc_pair
run_rejects "missing-chinese-doc-pair" "missing Chinese documentation pair: docs/guide.md" write_missing_chinese_doc_pair
run_rejects "missing-chinese-index-entry" "docs/README.md: missing documentation index entry: docs/operations.md" write_missing_chinese_index_entry
run_rejects "missing-english-index-entry" "docs/README.en.md: missing documentation index entry: docs/operations.en.md" write_missing_english_index_entry
run_rejects "missing-root-english-pair" "missing English documentation pair: README.en.md" write_missing_root_english_pair
run_rejects "missing-root-chinese-pair" "missing Chinese documentation pair: README.md" write_missing_root_chinese_pair
run_rejects "invalid-json-fence" "invalid json code fence" write_invalid_json_fence_doc
run_rejects "invalid-toml-fence" "invalid toml code fence" write_invalid_toml_fence_doc
run_rejects "decorative-heading" "avoid decorative emoji in markdown headings" write_decorative_heading_doc
run_rejects "promotional-wording" "avoid promotional wording in project documentation: Your files. Your control." write_promotional_wording_doc
run_rejects "security-check-doc-missing-id" "docs/api-reference.en.md: security-check documentation is missing ID: config_file_access" write_security_check_docs_missing_english_id
run_rejects "security-check-doc-missing-chinese-id" "docs/api-reference.md: security-check documentation is missing ID: config_file_access" write_security_check_docs_missing_chinese_id
run_rejects "security-check-doc-unknown-english-id" "docs/api-reference.en.md: security-check documentation lists unknown ID: ghost_probe" write_security_check_docs_unknown_english_id
run_rejects "security-check-doc-unknown-chinese-id" "docs/api-reference.md: security-check documentation lists unknown ID: ghost_probe" write_security_check_docs_unknown_chinese_id

printf '[doc-links-test] all checks passed\n'
