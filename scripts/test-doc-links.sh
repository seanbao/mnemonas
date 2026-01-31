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
	local status
	repo="$(new_repo "$name")"
	shift
	"$@" "$repo"

	set +e
	(cd "$repo" && ./scripts/check-doc-links.sh) > "$TMP_ROOT/$name.log" 2>&1
	status=$?
	set -e

	if [[ "$status" -ne 0 ]]; then
		sed 's/^/[doc-links-test] /' "$TMP_ROOT/$name.log" >&2
		fail "$name was rejected"
	fi
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

[English](install.en.md) | 简体中文
EOF
	cat > "$repo/docs/install.en.md" <<'EOF'
# Install

English | [简体中文](install.md)
EOF
	cat > "$repo/docs/usage.md" <<'EOF'
# Usage

[English](usage.en.md) | 简体中文
EOF
	cat > "$repo/docs/usage.en.md" <<'EOF'
# Usage

English | [简体中文](usage.md)
EOF
	cat > "$repo/docs/api.md" <<'EOF'
# API Reference

[English](api.en.md) | 简体中文
EOF
	cat > "$repo/docs/api.en.md" <<'EOF'
# API Reference

English | [简体中文](api.md)
EOF
	cat > "$repo/docs/README.md" <<'EOF'
# Docs

[English](README.en.md) | 简体中文

| 中文 | English |
|------|---------|
| [Install](install.md) | [Install](install.en.md) |
| [Usage](usage.md) | [Usage](usage.en.md) |
| [API](api.md) | [API](api.en.md) |
EOF
	cat > "$repo/docs/README.en.md" <<'EOF'
# Documentation

English | [简体中文](README.md)

| Document | Description |
| --- | --- |
| [Install](install.en.md) | Install guide |
| [Usage](usage.en.md) | Usage guide |
| [API](api.en.md) | API reference |
EOF
	git -C "$repo" add README.md docs/README.md docs/README.en.md docs/install.md docs/install.en.md docs/usage.md docs/usage.en.md docs/api.md docs/api.en.md
}

write_parenthesized_link_target_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

See [legacy release notes](release-notes-(legacy).md).
EOF
	cat > "$repo/release-notes-(legacy).md" <<'EOF'
# Legacy Release Notes
EOF
	git -C "$repo" add README.md README.en.md 'release-notes-(legacy).md'
}

write_markdown_code_fence_link_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```markdown
[example](docs/missing-example.md)
```
EOF
	git -C "$repo" add README.md README.en.md
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

[English](draft.en.md) | 简体中文
EOF
	cat > "$repo/docs/draft.en.md" <<'EOF'
# Draft

English | [简体中文](draft.md)
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
# Guide

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

write_heading_sequence_mismatch_doc_pair() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

## Install
EOF
	cat >> "$repo/README.en.md" <<'EOF'

### Install
EOF
	git -C "$repo" add README.md README.en.md
}

write_missing_english_language_link_doc_pair() {
	local repo="$1"
	cat > "$repo/README.md" <<'EOF'
# Project Guide

简体中文
EOF
	cat > "$repo/README.en.md" <<'EOF'
# Project Guide

English | [简体中文](README.md)
EOF
	git -C "$repo" add README.md README.en.md
}

write_missing_chinese_language_link_doc_pair() {
	local repo="$1"
	cat > "$repo/README.md" <<'EOF'
# Project Guide

[English](README.en.md) | 简体中文
EOF
	cat > "$repo/README.en.md" <<'EOF'
# Project Guide

English
EOF
	git -C "$repo" add README.md README.en.md
}

write_missing_chinese_index_entry() {
	local repo="$1"
	mkdir -p "$repo/docs"
	write_root_readme_pair "$repo"
	cat > "$repo/docs/README.md" <<'EOF'
# Docs

[English](README.en.md) | 简体中文

| 中文 | English |
|------|---------|
| [Guide](guide.md) | [Guide](guide.en.md) |
EOF
	cat > "$repo/docs/README.en.md" <<'EOF'
# Documentation

English | [简体中文](README.md)

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
# Docs

[English](README.en.md) | 简体中文

| 中文 | English |
|------|---------|
| [Guide](guide.md) | [Guide](guide.en.md) |
| [Operations](operations.md) | [Operations](operations.en.md) |
EOF
	cat > "$repo/docs/README.en.md" <<'EOF'
# Documentation

English | [简体中文](README.md)

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

write_missing_public_access_english_pair() {
	local repo="$1"
	mkdir -p "$repo/deploy/public-access"
	write_root_readme_pair "$repo"
	cat > "$repo/deploy/public-access/README.md" <<'EOF'
# Public Access Templates
EOF
	git -C "$repo" add README.md README.en.md deploy/public-access/README.md
}

write_missing_public_access_chinese_pair() {
	local repo="$1"
	mkdir -p "$repo/deploy/public-access"
	write_root_readme_pair "$repo"
	cat > "$repo/deploy/public-access/README.en.md" <<'EOF'
# Public Access Templates
EOF
	git -C "$repo" add README.md README.en.md deploy/public-access/README.en.md
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

write_duplicate_json_key_fence_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```json
{
  "success": true,
  "success": false
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

write_valid_yaml_fence_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```yaml
services:
  mnemonas:
    image: mnemonas:latest
    ports:
      - "127.0.0.1:8080:8080"
response:
  headers:
    Set-Cookie: !!regexp mnemonas_access=.*HttpOnly
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_invalid_yaml_fence_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```yaml
services:
  mnemonas:
    ports:
      - [
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_duplicate_yaml_key_fence_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```yaml
services:
  mnemonas:
    image: mnemonas:latest
    image: mnemonas:dev
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_decorative_heading_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

## 🚀 Quick Start
EOF
	git -C "$repo" add README.md README.en.md
}

write_promotional_wording_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

> Your files. Your control.

Avoid one-click claims in stable project docs.
EOF
	cat >> "$repo/README.md" <<'EOF'

避免在稳定项目文档中使用一键式表述。
EOF
	git -C "$repo" add README.md README.en.md
}

write_case_insensitive_promotional_wording_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

One-click restore is available.
EOF
	git -C "$repo" add README.md README.en.md
}

write_second_person_wording_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

You can run MnemoNAS with the local development script.
EOF
	git -C "$repo" add README.md README.en.md
}

write_copyable_placeholder_credentials_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```toml
[webdav]
password = "your-secure-password"
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_chinese_doc_english_phrase_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

SMB/Samba 仅 preview config，当前构建不启动 SMB runtime。
EOF
	git -C "$repo" add README.md README.en.md
}

write_english_doc_phrase_allowed_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

SMB/Samba keeps preview config only; this build does not start an SMB runtime.
EOF
	git -C "$repo" add README.md README.en.md
}

write_english_doc_chinese_text_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

恢复预览 response includes preflight checks.
EOF
	git -C "$repo" add README.md README.en.md
}

write_english_json_fence_chinese_text_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```json
{
  "title": "目标路径隔离"
}
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_chinese_code_fence_phrase_allowed_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```text
SMB/Samba 仅 preview config，当前构建不启动 SMB runtime。
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_status_emoji_code_fence_allowed_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```text
✅ Ready
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_status_emoji_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

| Status | Notes |
| --- | --- |
| ✅ Ready | Works |
EOF
	git -C "$repo" add README.md README.en.md
}

write_legacy_faq_marker_code_fence_allowed_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```text
Q: Legacy question
**A:** Legacy answer
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_legacy_faq_marker_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

Q: How does backup work?

**A:** Backup runs locally.
EOF
	git -C "$repo" add README.md README.en.md
}

write_remote_shell_pipe_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```bash
curl -fsSL https://example.com/install.sh | bash
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_direct_executable_script_reference_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat > "$repo/scripts/example.sh" <<'EOF'
#!/usr/bin/env bash
echo ok
EOF
	chmod +x "$repo/scripts/example.sh"
	cat >> "$repo/README.md" <<'EOF'

```bash
./scripts/example.sh
```
EOF
	git -C "$repo" add README.md README.en.md scripts/example.sh
}

write_interpreter_script_reference_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat > "$repo/scripts/helper.sh" <<'EOF'
#!/usr/bin/env bash
echo ok
EOF
	chmod 0644 "$repo/scripts/helper.sh"
	cat >> "$repo/README.md" <<'EOF'

```bash
bash ./scripts/helper.sh
```
EOF
	git -C "$repo" add README.md README.en.md scripts/helper.sh
}

write_encoded_restore_query_path_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```bash
curl -X POST \
  "http://localhost:8080/api/v1/versions/abc123/restore?path=%2Fdocuments%2Freport.txt"
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_raw_restore_query_path_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

```bash
curl -X POST \
  "http://localhost:8080/api/v1/versions/abc123/restore?path=/documents/report.txt"
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_encoded_api_path_query_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

Favorite check endpoint:

```text
GET /api/v1/favorites/check?path=%2Fdocuments%2Ffile.pdf
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_raw_api_path_query_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.md" <<'EOF'

Favorite check endpoint:

```text
GET /api/v1/favorites/check?path=/documents/file.pdf
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_storage_cdc_contract_valid_docs() {
	local repo="$1"
	write_valid_docs "$repo"
	cat >> "$repo/docs/README.md" <<'EOF'
| [Storage](storage-internals.md) | [Storage](storage-internals.en.md) |
EOF
	cat >> "$repo/docs/README.en.md" <<'EOF'
| [Storage](storage-internals.en.md) | Storage internals |
EOF
	cat > "$repo/docs/storage-internals.md" <<'EOF'
# 存储原理与运维建议

[English](storage-internals.en.md) | 简体中文

| 范围 | MnemoNAS |
| --- | --- |
| 去重 | BLAKE3 整对象版本；dataplane 中提供 CDC file API，但当前版本历史不会按 CDC 分块引用计数 |

当前版本历史使用整对象 CAS 快照；FastCDC API 属于数据面能力。
EOF
	cat > "$repo/docs/storage-internals.en.md" <<'EOF'
# Storage Internals and Operations Guidance

English | [简体中文](storage-internals.md)

| Area | MnemoNAS |
| --- | --- |
| Deduplication | BLAKE3 whole-object versions; CDC file APIs are available in dataplane, but current version history does not reference-count CDC chunks |

Current version history uses whole-object CAS snapshots; the FastCDC API is a dataplane capability.
EOF
	git -C "$repo" add docs/README.md docs/README.en.md docs/storage-internals.md docs/storage-internals.en.md
}

write_storage_cdc_contract_missing_boundary_doc() {
	local repo="$1"
	write_valid_docs "$repo"
	cat >> "$repo/docs/README.md" <<'EOF'
| [Storage](storage-internals.md) | [Storage](storage-internals.en.md) |
EOF
	cat >> "$repo/docs/README.en.md" <<'EOF'
| [Storage](storage-internals.en.md) | Storage internals |
EOF
	cat > "$repo/docs/storage-internals.md" <<'EOF'
# 存储原理与运维建议

[English](storage-internals.en.md) | 简体中文

| 范围 | MnemoNAS |
| --- | --- |
| 去重 | BLAKE3 整对象版本；dataplane 中提供 CDC file API，但当前版本历史不会按 CDC 分块引用计数 |

当前版本历史使用整对象 CAS 快照；FastCDC API 属于数据面能力。
EOF
	cat > "$repo/docs/storage-internals.en.md" <<'EOF'
# Storage Internals and Operations Guidance

English | [简体中文](storage-internals.md)

| Area | MnemoNAS |
| --- | --- |
| Deduplication | BLAKE3 whole-object versions; CDC file APIs are available in dataplane |

Current version history uses whole-object CAS snapshots.
EOF
	git -C "$repo" add docs/README.md docs/README.en.md docs/storage-internals.md docs/storage-internals.en.md
}

write_non_executable_script_reference_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat > "$repo/scripts/helper.sh" <<'EOF'
#!/usr/bin/env bash
echo ok
EOF
	chmod 0644 "$repo/scripts/helper.sh"
	cat >> "$repo/README.md" <<'EOF'

```bash
./scripts/helper.sh
```
EOF
	git -C "$repo" add README.md README.en.md scripts/helper.sh
}

write_non_executable_script_link_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat > "$repo/scripts/helper.sh" <<'EOF'
#!/usr/bin/env bash
echo ok
EOF
	chmod 0644 "$repo/scripts/helper.sh"
	cat >> "$repo/README.md" <<'EOF'

See [helper script](scripts/helper.sh).
EOF
	git -C "$repo" add README.md README.en.md scripts/helper.sh
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
# Docs

[English](README.en.md) | 简体中文

| 中文 | English |
|------|---------|
| [API Reference](api-reference.md) | [API Reference](api-reference.en.md) |
EOF
	cat > "$repo/docs/README.en.md" <<'EOF'
# Documentation

English | [简体中文](README.md)

| Document | Description |
| --- | --- |
| [API Reference](api-reference.en.md) | API reference |
EOF
cat > "$repo/docs/api-reference.md" <<EOF
# API Reference

[English](api-reference.en.md) | 简体中文

当前检查项 ID 包括 $chinese_ids。
EOF
	cat > "$repo/docs/api-reference.en.md" <<EOF
# API Reference

English | [简体中文](api-reference.md)

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
run_accepts "parenthesized-link-target" write_parenthesized_link_target_doc
run_accepts "markdown-code-fence-link-example" write_markdown_code_fence_link_doc
run_accepts "untracked-valid-link-target" write_untracked_valid_doc
run_accepts "valid-json-fence" write_valid_json_fence_doc
run_accepts "valid-toml-fence" write_valid_toml_fence_doc
run_accepts "valid-yaml-fence" write_valid_yaml_fence_doc
run_accepts "english-doc-phrase-allowed" write_english_doc_phrase_allowed_doc
run_accepts "chinese-code-fence-phrase-allowed" write_chinese_code_fence_phrase_allowed_doc
run_accepts "status-emoji-code-fence-allowed" write_status_emoji_code_fence_allowed_doc
run_accepts "legacy-faq-marker-code-fence-allowed" write_legacy_faq_marker_code_fence_allowed_doc
run_accepts "security-check-doc-ids" write_security_check_docs_valid
run_accepts "direct-executable-script-reference" write_direct_executable_script_reference_doc
run_accepts "interpreter-script-reference" write_interpreter_script_reference_doc
run_accepts "encoded-restore-query-path" write_encoded_restore_query_path_doc
run_accepts "encoded-api-path-query" write_encoded_api_path_query_doc
run_accepts "storage-cdc-contract" write_storage_cdc_contract_valid_docs
run_rejects "missing-file" "missing link target: docs/missing.md" write_missing_file_doc
run_rejects "escaping-link" "link escapes repository: ../outside.md" write_escaping_link_doc
run_rejects "missing-anchor" "missing heading anchor: #missing-section" write_missing_anchor_doc
run_rejects "missing-english-doc-pair" "missing English documentation pair: docs/guide.en.md" write_missing_english_doc_pair
run_rejects "missing-chinese-doc-pair" "missing Chinese documentation pair: docs/guide.md" write_missing_chinese_doc_pair
run_rejects "heading-sequence-mismatch" "README.md and README.en.md: heading level sequence differs" write_heading_sequence_mismatch_doc_pair
run_rejects "missing-english-language-link" "README.md: missing language switch link to README.en.md" write_missing_english_language_link_doc_pair
run_rejects "missing-chinese-language-link" "README.en.md: missing language switch link to README.md" write_missing_chinese_language_link_doc_pair
run_rejects "missing-chinese-index-entry" "docs/README.md: missing documentation index entry: docs/operations.md" write_missing_chinese_index_entry
run_rejects "missing-english-index-entry" "docs/README.en.md: missing documentation index entry: docs/operations.en.md" write_missing_english_index_entry
run_rejects "missing-root-english-pair" "missing English documentation pair: README.en.md" write_missing_root_english_pair
run_rejects "missing-root-chinese-pair" "missing Chinese documentation pair: README.md" write_missing_root_chinese_pair
run_rejects "missing-public-access-english-pair" "missing English documentation pair: deploy/public-access/README.en.md" write_missing_public_access_english_pair
run_rejects "missing-public-access-chinese-pair" "missing Chinese documentation pair: deploy/public-access/README.md" write_missing_public_access_chinese_pair
run_rejects "invalid-json-fence" "invalid json code fence" write_invalid_json_fence_doc
run_rejects "duplicate-json-key-fence" "found duplicate key 'success'" write_duplicate_json_key_fence_doc
run_rejects "invalid-toml-fence" "invalid toml code fence" write_invalid_toml_fence_doc
run_rejects "invalid-yaml-fence" "invalid yaml code fence" write_invalid_yaml_fence_doc
run_rejects "duplicate-yaml-key-fence" "found duplicate key 'image'" write_duplicate_yaml_key_fence_doc
run_rejects "decorative-heading" "avoid decorative emoji in markdown headings" write_decorative_heading_doc
run_rejects "status-emoji-doc" "avoid emoji status markers in project documentation" write_status_emoji_doc
run_rejects "legacy-faq-marker-doc" "avoid legacy Q:/A: FAQ markers in project documentation" write_legacy_faq_marker_doc
run_rejects "promotional-wording" "avoid promotional wording in project documentation: Your files. Your control." write_promotional_wording_doc
run_rejects "promotional-wording-case" "avoid promotional wording in project documentation: one-click" write_case_insensitive_promotional_wording_doc
run_rejects "second-person-wording" "avoid second-person wording in project documentation" write_second_person_wording_doc
run_rejects "copyable-placeholder-credentials" "avoid copyable placeholder credentials in project documentation: your-secure-password" write_copyable_placeholder_credentials_doc
run_rejects "chinese-doc-english-phrase" "avoid English phrasing in Chinese documentation: preview config" write_chinese_doc_english_phrase_doc
run_rejects "english-doc-chinese-text" "avoid non-English text outside language-navigation links in English documentation" write_english_doc_chinese_text_doc
run_rejects "english-json-fence-chinese-text" "avoid non-English text outside language-navigation links in English documentation" write_english_json_fence_chinese_text_doc
run_rejects "remote-shell-pipe" "avoid piping remote install scripts directly to a shell" write_remote_shell_pipe_doc
run_rejects "non-executable-script-reference" "script reference is not executable: ./scripts/helper.sh" write_non_executable_script_reference_doc
run_rejects "non-executable-script-link" "linked script is not executable: scripts/helper.sh" write_non_executable_script_link_doc
run_rejects "raw-restore-query-path" "URL-encode API path query values in documentation examples; use path=%2F..." write_raw_restore_query_path_doc
run_rejects "raw-api-path-query" "URL-encode API path query values in documentation examples; use path=%2F..." write_raw_api_path_query_doc
run_rejects "storage-cdc-contract-missing-boundary" "docs/storage-internals.en.md: missing storage CDC boundary text: current version history does not reference-count CDC chunks" write_storage_cdc_contract_missing_boundary_doc
run_rejects "security-check-doc-missing-id" "docs/api-reference.en.md: security-check documentation is missing ID: config_file_access" write_security_check_docs_missing_english_id
run_rejects "security-check-doc-missing-chinese-id" "docs/api-reference.md: security-check documentation is missing ID: config_file_access" write_security_check_docs_missing_chinese_id
run_rejects "security-check-doc-unknown-english-id" "docs/api-reference.en.md: security-check documentation lists unknown ID: ghost_probe" write_security_check_docs_unknown_english_id
run_rejects "security-check-doc-unknown-chinese-id" "docs/api-reference.md: security-check documentation lists unknown ID: ghost_probe" write_security_check_docs_unknown_chinese_id

printf '[doc-links-test] all checks passed\n'
