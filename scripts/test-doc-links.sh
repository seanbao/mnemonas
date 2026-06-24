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

write_ellipsis_secret_placeholder_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```toml
[storage.s3]
access_key = "..."
secret_key = "..."
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_json_ellipsis_secret_placeholder_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```json
{
  "secret": "..."
}
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_yaml_ellipsis_secret_placeholder_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```yaml
storage:
  s3:
    access_key: "..."
    secret_key: ...
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_hyphenated_ellipsis_secret_placeholder_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```yaml
storage:
  s3:
    secret-key: "..."
    access-token: ...
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_dot_ellipsis_secret_placeholder_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```toml
[alerts]
api.key = "..."
bot.token = "..."
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_truncated_non_secret_ellipsis_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```text
go test ./...
https://github.com/example/project/compare/v0.1.0...HEAD
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

write_curl_basic_auth_guidance_allowed_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

Manual WebDAV checks should use temporary curl config files instead of `curl -u` command arguments.
API clients can still use `Authorization: Bearer <access-token>` outside copyable shell commands.

```bash
curl --config "$curl_auth_config" \
  -X PROPFIND \
  https://example.com/webdav/
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_curl_basic_auth_argument_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```bash
curl -u "$WEBDAV_USER:$WEBDAV_PASS" -X PROPFIND https://example.com/webdav/
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_multiline_curl_basic_auth_argument_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```bash
curl -X PROPFIND \
  --user "$WEBDAV_USER:$WEBDAV_PASS" \
  https://example.com/webdav/
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_curl_basic_authorization_header_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```bash
curl -H 'Authorization: Basic ZGVtbzpwYXNzd29yZA==' https://example.com/webdav/
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_reverse_proxy_webdav_contract_missing_chmod_doc() {
	local repo="$1"
	mkdir -p "$repo/docs"
	write_root_readme_pair "$repo"
	cat > "$repo/docs/reverse-proxy-setup.md" <<'EOF'
# 外网访问配置指南

[English](reverse-proxy-setup.en.md) | 简体中文

公网或生产 WebDAV 挂载建议优先使用 `auth_type=users`。
auth_type=basic 时使用 WebDAV 用户名和密码。
生成密码位于 /srv/mnemonas/secrets.json 的 webdav_password 字段。

```bash
curl_auth_config="$(mktemp -t mnemonas-webdav-curl-auth.XXXXXX)"
chmod 600 "$curl_auth_config"
curl --config "$curl_auth_config" -X PROPFIND https://nas.example.com/dav/ -H "Depth: 0"
```
EOF
	cat > "$repo/docs/reverse-proxy-setup.en.md" <<'EOF'
# Public HTTPS and Reverse Proxy Setup

English | [简体中文](reverse-proxy-setup.md)

Prefer `auth_type=users` for public or production WebDAV mounts.
Use the WebDAV username and password when auth_type=basic.
Custom Basic passwords are not echoed back; generated passwords use the webdav_password field in /srv/mnemonas/secrets.json.

```bash
curl_auth_config="$(mktemp -t mnemonas-webdav-curl-auth.XXXXXX)"
curl --config "$curl_auth_config" -X PROPFIND https://nas.example.com/dav/ -H "Depth: 0"
```
EOF
	git -C "$repo" add README.md README.en.md docs/reverse-proxy-setup.md docs/reverse-proxy-setup.en.md
}

write_curl_bearer_auth_header_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```bash
curl -H "Authorization: Bearer <access-token>" https://example.com/api/v1/files
```
EOF
	git -C "$repo" add README.md README.en.md
}

write_multiline_curl_bearer_auth_header_doc() {
	local repo="$1"
	write_root_readme_pair "$repo"
	cat >> "$repo/README.en.md" <<'EOF'

```bash
curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  https://example.com/api/v1/maintenance/gc
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
| [Configuration](configuration.md) | [Configuration](configuration.en.md) |
EOF
	cat >> "$repo/docs/README.en.md" <<'EOF'
| [Storage](storage-internals.en.md) | Storage internals |
| [Configuration](configuration.en.md) | Configuration reference |
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
	cat > "$repo/docs/configuration.md" <<'EOF'
# 配置参考

[English](configuration.en.md) | 简体中文

## `[dataplane.cdc]`

配置 Rust 数据面 FastCDC 文件 API 的算法参数。当前 Go 版本历史路径仍使用整对象 CAS 快照，因此这些参数只影响接入该数据面文件 API 的新写入，不表示当前版本历史已启用分块级去重。
EOF
	cat > "$repo/docs/configuration.en.md" <<'EOF'
# Configuration Reference

English | [简体中文](configuration.md)

## `[dataplane.cdc]`

Configure algorithm parameters for the Rust dataplane FastCDC file API. Current Go version history still uses whole-object CAS snapshots, so these settings only affect new writes that use that dataplane file API and do not mean version history has block-level deduplication enabled.
EOF
	git -C "$repo" add docs/README.md docs/README.en.md docs/storage-internals.md docs/storage-internals.en.md docs/configuration.md docs/configuration.en.md
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

write_configuration_cdc_contract_missing_boundary_doc() {
	local repo="$1"
	write_storage_cdc_contract_valid_docs "$repo"
	cat > "$repo/docs/configuration.md" <<'EOF'
# 配置参考

[English](configuration.en.md) | 简体中文

## `[dataplane.cdc]`

配置 Rust 数据面 FastCDC 文件 API 的算法参数。当前 Go 版本历史路径仍使用整对象 CAS 快照，因此这些参数只影响接入该数据面文件 API 的新写入，不表示当前版本历史已启用分块级去重。
EOF
	cat > "$repo/docs/configuration.en.md" <<'EOF'
# Configuration Reference

English | [简体中文](configuration.md)

## `[dataplane.cdc]`

Content-defined chunking settings affect deduplication and metadata overhead.
EOF
	git -C "$repo" add docs/configuration.md docs/configuration.en.md
}

write_security_checklist_contract_valid_docs() {
	local repo="$1"
	write_valid_docs "$repo"
	cat > "$repo/scripts/public-go-live-smoke.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
EOF
	chmod +x "$repo/scripts/public-go-live-smoke.sh"
	cat >> "$repo/docs/README.md" <<'EOF'
| [Security](security.md) | [Security](security.en.md) |
| [Cloud Firewall](cloud-firewall-checklist.md) | [Cloud Firewall](cloud-firewall-checklist.en.md) |
EOF
	cat >> "$repo/docs/README.en.md" <<'EOF'
| [Security](security.en.md) | Security hardening guide |
| [Cloud Firewall](cloud-firewall-checklist.en.md) | Public cloud firewall checklist |
EOF
	cat > "$repo/docs/security.md" <<'EOF'
# 安全加固指南

[English](security.en.md) | 简体中文

## 部署检查清单

- [ ] 已通过服务器端 `initial-password.txt` 完成首次 Web UI 登录，并已修改管理员密码。
- [ ] WebDAV 使用 `auth_type = "users"`，或已设置全局 Basic Auth 强密码。
- [ ] `auth_type` 不是 `none`。
- [ ] 公网部署时 `server.host = "127.0.0.1"`。
- [ ] 管理员首页的首次设置必需项已由服务端判定完成。
- [ ] dataplane gRPC/HTTP 端口保持在私有网络内。
- [ ] Web UI “安全自检”没有 `block` 项。
- [ ] 安全自检已覆盖 `allow_unsafe_no_auth` 和分享边界。
- [ ] 已运行 `sudo mnemonas-doctor --public-domain <domain>`。
- [ ] 已确认 HTTP 会跳转到同一域名的 HTTPS。
- [ ] 配置文件不是符号链接。
- [ ] 管理员账号冗余可用。
- [ ] `initial-password.txt` 路径不存在。
- [ ] 公开分享 JSON 响应边界已处理。
- [ ] 匿名 WebDAV `PROPFIND` 被拒绝。
- [ ] 没有 Web 后端直连。
- [ ] 没有 dataplane 端口暴露。
- [ ] 已从外部网络运行 `./scripts/public-go-live-smoke.sh <domain>`。
- [ ] 已按 [公网云防火墙复核清单](cloud-firewall-checklist.md) 确认只开放 `80/443`。
- [ ] 生产环境使用 HTTPS。

运行时检查：

```bash
curl --connect-timeout 3 --max-time 10 http://<domain>:8080/health
```

只要 TCP 可连接，即使没有 HTTP 状态码，也表示后端端口仍可从公网访问。
EOF
	cat > "$repo/docs/security.en.md" <<'EOF'
# Security Hardening Guide

English | [简体中文](security.md)

## Deployment Checklist

- [ ] First login completed using server-side `initial-password.txt`.
- [ ] WebDAV uses `auth_type = "users"` or strong global Basic Auth credentials.
- [ ] `webdav.auth_type` is not `none`.
- [ ] Public deployments use `server.host = "127.0.0.1"`.
- [ ] The administrator Dashboard first-run requirements are complete according to server-side evidence.
- [ ] Dataplane gRPC/HTTP ports are private.
- [ ] The Web UI security self-check has no `block` items.
- [ ] The security self-check covers `allow_unsafe_no_auth` and share boundaries.
- [ ] Run `sudo mnemonas-doctor --public-domain <domain>`.
- [ ] HTTP redirects to HTTPS on the same public domain.
- [ ] The non-symlink config file path has been verified.
- [ ] The administrator-account redundancy status is available.
- [ ] The absent `initial-password.txt` path has been verified.
- [ ] The public-share JSON response boundaries have been reviewed.
- [ ] Anonymous access is disabled and anonymous WebDAV `PROPFIND` is rejected.
- [ ] There is no direct backend exposure.
- [ ] There is no dataplane exposure.
- [ ] `./scripts/public-go-live-smoke.sh <domain>` has passed from an external network.
- [ ] The [Public cloud firewall checklist](cloud-firewall-checklist.en.md) has been applied and public rules expose only `80/443`.
- [ ] Public deployments use HTTPS.

Runtime checks:

```bash
curl --connect-timeout 3 --max-time 10 http://<domain>:8080/health
```

Any successful TCP connection means the backend port is still publicly reachable.
EOF
	cat > "$repo/docs/cloud-firewall-checklist.md" <<'EOF'
# 公网云防火墙复核清单

[English](cloud-firewall-checklist.en.md) | 简体中文
EOF
	cat > "$repo/docs/cloud-firewall-checklist.en.md" <<'EOF'
# Public Cloud Firewall Checklist

English | [简体中文](cloud-firewall-checklist.md)
EOF
	git -C "$repo" add scripts/public-go-live-smoke.sh docs/README.md docs/README.en.md docs/security.md docs/security.en.md docs/cloud-firewall-checklist.md docs/cloud-firewall-checklist.en.md
}

write_security_checklist_contract_missing_firewall_doc() {
	local repo="$1"
	write_security_checklist_contract_valid_docs "$repo"
	perl -0pi -e 's{- \[ \] The \[Public cloud firewall checklist\]\(cloud-firewall-checklist\.en\.md\) has been applied and public rules expose only `80/443`\.\n}{}' "$repo/docs/security.en.md"
	git -C "$repo" add docs/security.en.md
}

write_security_checklist_contract_missing_public_smoke_doc() {
	local repo="$1"
	write_security_checklist_contract_valid_docs "$repo"
	perl -0pi -e 's{- \[ \] `\.\/scripts\/public-go-live-smoke\.sh <domain>` has passed from an external network\.\n}{}' "$repo/docs/security.en.md"
	git -C "$repo" add docs/security.en.md
}

write_api_reference_webdav_auth_contract_valid_docs() {
	local repo="$1"
	write_valid_docs "$repo"
	cat >> "$repo/docs/README.md" <<'EOF'
| [API 参考](api-reference.md) | [API Reference](api-reference.en.md) |
EOF
	cat >> "$repo/docs/README.en.md" <<'EOF'
| [API Reference](api-reference.en.md) | API reference |
EOF
	cat > "$repo/docs/api-reference.md" <<'EOF'
# API 参考

[English](api-reference.en.md) | 简体中文

## WebDAV

- 日常或生产挂载建议设置 `webdav.auth_type = "users"`，使用 MnemoNAS 用户账户挂载。
- 根目录示例配置保留旧全局 Basic Auth 作为兼容基线；该模式使用 `[webdav]` 中的服务凭据。
EOF
	cat > "$repo/docs/api-reference.en.md" <<'EOF'
# API Reference

English | [简体中文](api-reference.md)

## WebDAV

- For day-to-day or production mounts, set `webdav.auth_type = "users"` to mount with MnemoNAS user accounts.
- The root example config keeps legacy global Basic Auth as a compatibility baseline; that mode uses service credentials from `[webdav]`.
EOF
	git -C "$repo" add docs/README.md docs/README.en.md docs/api-reference.md docs/api-reference.en.md
}

write_api_reference_webdav_auth_contract_missing_users_doc() {
	local repo="$1"
	write_api_reference_webdav_auth_contract_valid_docs "$repo"
	perl -0pi -e 's/For day-to-day or production mounts, set `webdav\.auth_type = "users"`/Choose an authentication mode/' "$repo/docs/api-reference.en.md"
	git -C "$repo" add docs/api-reference.en.md
}

write_api_reference_webdav_auth_contract_legacy_first_doc() {
	local repo="$1"
	write_api_reference_webdav_auth_contract_valid_docs "$repo"
	perl -0pi -e 's/- For day-to-day or production mounts, set `webdav\.auth_type = "users"` to mount with MnemoNAS user accounts\./- By default it uses the legacy global Basic Auth credentials from `[webdav]` or generated credentials in `secrets.json`.\n- For day-to-day or production mounts, set `webdav.auth_type = "users"` to mount with MnemoNAS user accounts./' "$repo/docs/api-reference.en.md"
	git -C "$repo" add docs/api-reference.en.md
}

write_backup_restore_drill_contract_valid_docs() {
	local repo="$1"
	write_valid_docs "$repo"
	cat >> "$repo/docs/README.md" <<'EOF'
| [Backup](backup-guide.md) | [Backup](backup-guide.en.md) |
EOF
	cat >> "$repo/docs/README.en.md" <<'EOF'
| [Backup](backup-guide.en.md) | Backup guide |
EOF
	cat > "$repo/docs/backup-guide.md" <<'EOF'
# 备份与恢复

[English](backup-guide.en.md) | 简体中文

## 恢复演练

```toml
restore_drill_stale_after = "720h"
```

恢复演练历史与显式恢复历史默认都保留最近 20 条。失败演练还会记录稳定的 `failure_category`。

```bash
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-drill \
  -H 'Content-Type: application/json' \
  -d '{"keep_artifact":false}'
```

`keep_artifact = true` 会保留临时恢复目录。`restic` 恢复演练当前执行 `restic check`；`rclone` 恢复演练当前执行 `rclone check --one-way`。

维护页提供“导出摘要”入口。从未恢复过的备份只是推测，不是已经验证过的恢复路径。
EOF
	cat > "$repo/docs/backup-guide.en.md" <<'EOF'
# Backup and Restore

English | [简体中文](backup-guide.md)

## Restore Drills

```toml
restore_drill_stale_after = "720h"
```

Restore-drill history and explicit restore history both keep the latest 20 entries. Failed drills also record a stable `failure_category`.

```bash
curl -X POST -b cookies.txt \
  http://localhost:8080/api/v1/maintenance/backups/external-disk/restore-drill \
  -H 'Content-Type: application/json' \
  -d '{"keep_artifact":false}'
```

Set `keep_artifact = true` to keep the temporary restored directory. For `restic`, the drill currently runs `restic check`; for `rclone`, it runs `rclone check --one-way`.

The Maintenance page provides an **Export summary** action. A backup that has never been restored is an assumption, not a proven recovery path.
EOF
	git -C "$repo" add docs/README.md docs/README.en.md docs/backup-guide.md docs/backup-guide.en.md
}

write_backup_restore_drill_contract_missing_history_doc() {
	local repo="$1"
	write_backup_restore_drill_contract_valid_docs "$repo"
	perl -0pi -e 's/Restore-drill history and explicit restore history both keep the latest 20 entries\. //' "$repo/docs/backup-guide.en.md"
	git -C "$repo" add docs/backup-guide.en.md
}

write_hardening_progress_release_readiness_contract_valid_docs() {
	local repo="$1"
	write_valid_docs "$repo"
	cat >> "$repo/docs/README.md" <<'EOF'
| [Hardening](hardening-progress.md) | [Hardening](hardening-progress.en.md) |
EOF
	cat >> "$repo/docs/README.en.md" <<'EOF'
| [Hardening](hardening-progress.en.md) | Hardening progress ledger |
EOF
	cat > "$repo/docs/hardening-progress.md" <<'EOF'
# 硬化进度台账

[English](hardening-progress.en.md) | 简体中文

| 区域 | 当前状态 | 验证证据 |
|------|----------|----------|
| 备份恢复演练 smoke 入口 | `scripts/backup-restore-drill-smoke.sh` 已纳入发布清单和双语 release notes 门禁，备份恢复演练 smoke 入口文档和发布门禁契约已记录。 | `make docs-check` |
| 发布后上线总核验 | 正式发布后运行 `./scripts/release-go-live-check.sh --version <tag> --domain <domain>` 统一串联 release-readiness、公网核验和备份恢复演练，再由 `./scripts/verify-published-release.sh --version <tag> --repository seanbao/mnemonas` 下载并核验 GitHub Release 产物、checksums 和容器镜像标签；以 `-` 开头的显式 artifact 目录会先规范化为本地路径，`--keep-published-artifacts` 可保留临时下载产物。 | `make docs-check` |
| 审查分组与发布前检查 | 发布就绪摘要要求发布清单和双语 release notes 保留公网部署 doctor、外部网络 smoke、备份恢复演练 smoke、发布后上线总核验和云防火墙复核入口。 | `./scripts/release-readiness.sh` |

## 整体状态边界

| 状态项 | 当前结论 | 进入下一状态所需证据 |
|------|----------|----------------------|
| 工程内发布候选 | 已成立。 | 非发布文档变更后重新执行完整验证。 |
| 最终可用目标 | 不能标记为最终完成。真实公网部署、正式 tag、Release workflow 结果和发布后产物核验仍缺少环境证据。 | 完成外部部署验证。 |
| 后续功能边界 | 已确认推迟的边缘功能不阻塞当前硬化收尾。 | 维护者重新确认范围。 |
EOF
	cat > "$repo/docs/hardening-progress.en.md" <<'EOF'
# Hardening Progress Ledger

English | [简体中文](hardening-progress.md)

| Area | Current status | Verification evidence |
| --- | --- | --- |
| Backup restore-drill smoke entry point | `scripts/backup-restore-drill-smoke.sh` is covered by the release checklist and bilingual release-notes gates, and the backup restore-drill smoke entry-point documentation and release-readiness contract is recorded. | `make docs-check` |
| Post-publication go-live verification | After publication, run `./scripts/release-go-live-check.sh --version <tag> --domain <domain>` to chain release-readiness, public verification, and backup restore-drill smoke, and use `./scripts/verify-published-release.sh --version <tag> --repository seanbao/mnemonas` to download and verify GitHub Release artifacts, checksums, and container image tags. Dash-prefixed explicit artifact directories are normalized as local paths, and `--keep-published-artifacts` can retain temporary downloaded artifacts. | `make docs-check` |
| Review grouping and pre-release checks | The release-readiness summary requires the release checklist and bilingual release notes to retain the public-deployment doctor, external-network smoke, backup restore-drill smoke, post-publication go-live verification, and cloud-firewall review entry points. | `./scripts/release-readiness.sh` |

## Overall Status Boundary

| Status item | Current conclusion | Evidence needed for the next state |
| --- | --- | --- |
| In-repository release candidate | Established. | Rerun validation after non-release-documentation changes. |
| Final usability objective | Not complete. Code, scripts, Web UI, Docker, local release-package fixtures, documentation, and the release-readiness summary have local evidence, but real public deployment, the official tag, Release workflow results, and post-publication artifact verification still lack environment evidence. | Complete external deployment verification. |
| Follow-up feature boundary | Confirmed deferred edge features do not block the current hardening closeout and should not be repeatedly reopened in this ledger. | Reconfirm scope before implementation. |
EOF
	git -C "$repo" add docs/README.md docs/README.en.md docs/hardening-progress.md docs/hardening-progress.en.md
}

write_hardening_progress_release_readiness_contract_missing_backup_smoke_doc() {
	local repo="$1"
	write_hardening_progress_release_readiness_contract_valid_docs "$repo"
	perl -0pi -e 's/, backup restore-drill smoke//' "$repo/docs/hardening-progress.en.md"
	git -C "$repo" add docs/hardening-progress.en.md
}

write_hardening_progress_release_readiness_contract_missing_published_release_verifier_doc() {
	local repo="$1"
	write_hardening_progress_release_readiness_contract_valid_docs "$repo"
	perl -0pi -e 's/`\.\/scripts\/verify-published-release\.sh --version <tag> --repository seanbao\/mnemonas`/`\.\/scripts\/verify-release-artifacts.sh --require-targets --check-image`/' "$repo/docs/hardening-progress.en.md"
	git -C "$repo" add docs/hardening-progress.en.md
}

write_hardening_progress_release_readiness_contract_missing_go_live_doc() {
	local repo="$1"
	write_hardening_progress_release_readiness_contract_valid_docs "$repo"
	perl -0pi -e 's/`\.\/scripts\/release-go-live-check\.sh --version <tag> --domain <domain>`/`\.\/scripts\/public-go-live-smoke.sh <domain>`/' "$repo/docs/hardening-progress.en.md"
	git -C "$repo" add docs/hardening-progress.en.md
}

write_hardening_progress_release_readiness_contract_missing_goal_boundary_doc() {
	local repo="$1"
	write_hardening_progress_release_readiness_contract_valid_docs "$repo"
	perl -0pi -e 's/Not complete\. //' "$repo/docs/hardening-progress.en.md"
	git -C "$repo" add docs/hardening-progress.en.md
}

write_docker_deployment_release_verification_contract_valid_docs() {
	local repo="$1"
	write_valid_docs "$repo"
	cat >> "$repo/docs/README.md" <<'EOF'
| [Docker](docker-deployment.md) | [Docker](docker-deployment.en.md) |
EOF
	cat >> "$repo/docs/README.en.md" <<'EOF'
| [Docker](docker-deployment.en.md) | Docker deployment guide |
EOF
	cat > "$repo/scripts/verify-published-release.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
EOF
	cat > "$repo/scripts/release-go-live-check.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
EOF
	chmod +x "$repo/scripts/verify-published-release.sh" "$repo/scripts/release-go-live-check.sh"
	cat > "$repo/docs/docker-deployment.md" <<'EOF'
# Docker 部署指南

[English](docker-deployment.en.md) | 简体中文

## 发布镜像

发布后可下载并核验 GitHub Release 归档、`checksums.txt` 和对应 GHCR 镜像标签：

```bash
mkdir -p dist/release-check
./scripts/verify-published-release.sh \
  --version v1.2.3 \
  --repository seanbao/mnemonas \
  --artifact-dir dist/release-check
```

`--version` 必须使用 `vMAJOR.MINOR.PATCH` 或语义化预发布形式。
默认会调用 Docker 检查 `ghcr.io/seanbao/mnemonas:1.2.3` 是否存在；需要调整时，可设置 `MNEMONAS_RELEASE_IMAGE_CHECK_RETRIES` 和 `MNEMONAS_RELEASE_IMAGE_CHECK_SLEEP_SECONDS`。
仅核验下载的归档和 checksums 时，可传入 `--skip-image-check`。
未设置 `--artifact-dir` 时，脚本会使用临时目录；显式目录必须为空或不存在。
需要保留临时下载目录用于排查失败时，可省略 `--artifact-dir` 并传入 `--keep-artifacts`。
统一上线核验入口需要保留临时下载产物用于排查失败时，可省略 `--artifact-dir` 并传入 `--keep-published-artifacts`。
显式目录可以是以 `-` 开头的相对路径；仓库名会在下载前校验为 GHCR 兼容的小写 `owner/repo`。

公网发布后运行统一上线核验：

```bash
./scripts/release-go-live-check.sh \
  --version v1.2.3 \
  --domain nas.example.com \
  --repository seanbao/mnemonas \
  --artifact-dir dist/release-check \
  --backup-api-url https://nas.example.com/api/v1 \
  --backup-job-id external-disk
```

仅在发布记录说明缺少完整恢复证据时，才传入 `--skip-backup-restore-drill`。
EOF
	cat > "$repo/docs/docker-deployment.en.md" <<'EOF'
# Docker Deployment Guide

English | [简体中文](docker-deployment.md)

## Release Images

After a release is published, download and verify the GitHub Release archives, `checksums.txt`, and matching GHCR image tag:

```bash
mkdir -p dist/release-check
./scripts/verify-published-release.sh \
  --version v1.2.3 \
  --repository seanbao/mnemonas \
  --artifact-dir dist/release-check
```

`--version` must use `vMAJOR.MINOR.PATCH` or a SemVer prerelease form.
By default, the script uses Docker to check that `ghcr.io/seanbao/mnemonas:1.2.3` exists; set `MNEMONAS_RELEASE_IMAGE_CHECK_RETRIES` and `MNEMONAS_RELEASE_IMAGE_CHECK_SLEEP_SECONDS` when different retry timing is required.
Pass `--skip-image-check` when only the downloaded archives and checksums need verification.
When `--artifact-dir` is omitted, the script uses a temporary directory. Explicit directories must be empty or absent.
To retain the temporary download directory for failure investigation, omit `--artifact-dir` and pass `--keep-artifacts`.
When the unified go-live check should retain temporary downloaded published artifacts for troubleshooting, omit `--artifact-dir` and pass `--keep-published-artifacts`.
Explicit directories may be dash-prefixed relative paths, and repository names are validated as GHCR-compatible lowercase `owner/repo` values before download.

After a public release, run the unified go-live check:

```bash
./scripts/release-go-live-check.sh \
  --version v1.2.3 \
  --domain nas.example.com \
  --repository seanbao/mnemonas \
  --artifact-dir dist/release-check \
  --backup-api-url https://nas.example.com/api/v1 \
  --backup-job-id external-disk
```

Pass `--skip-backup-restore-drill` only when the release notes record that complete restore evidence is missing.
EOF
	git -C "$repo" add scripts/verify-published-release.sh scripts/release-go-live-check.sh docs/README.md docs/README.en.md docs/docker-deployment.md docs/docker-deployment.en.md
}

write_docker_deployment_release_verification_contract_missing_retry_doc() {
	local repo="$1"
	write_docker_deployment_release_verification_contract_valid_docs "$repo"
	perl -0pi -e 's/ and `MNEMONAS_RELEASE_IMAGE_CHECK_SLEEP_SECONDS`//' "$repo/docs/docker-deployment.en.md"
	git -C "$repo" add docs/docker-deployment.en.md
}

write_docker_deployment_release_verification_contract_missing_dash_dir_doc() {
	local repo="$1"
	write_docker_deployment_release_verification_contract_valid_docs "$repo"
	perl -0pi -e 's/Explicit directories may be dash-prefixed relative paths, and //' "$repo/docs/docker-deployment.en.md"
	git -C "$repo" add docs/docker-deployment.en.md
}

write_docker_deployment_release_verification_contract_missing_keep_artifacts_doc() {
	local repo="$1"
	write_docker_deployment_release_verification_contract_valid_docs "$repo"
	grep -Fv -- '--keep-artifacts' "$repo/docs/docker-deployment.en.md" > "$repo/docs/docker-deployment.en.md.tmp"
	mv "$repo/docs/docker-deployment.en.md.tmp" "$repo/docs/docker-deployment.en.md"
	git -C "$repo" add docs/docker-deployment.en.md
}

write_docker_deployment_release_verification_contract_missing_go_live_keep_artifacts_doc() {
	local repo="$1"
	write_docker_deployment_release_verification_contract_valid_docs "$repo"
	grep -Fv -- '--keep-published-artifacts' "$repo/docs/docker-deployment.en.md" > "$repo/docs/docker-deployment.en.md.tmp"
	mv "$repo/docs/docker-deployment.en.md.tmp" "$repo/docs/docker-deployment.en.md"
	git -C "$repo" add docs/docker-deployment.en.md
}

write_release_notes_validation_evidence_contract_valid_docs() {
	local repo="$1"
	write_valid_docs "$repo"
	cat >> "$repo/docs/README.md" <<'EOF'
| [Hardening Review](hardening-review-summary.md) | [Hardening Review](hardening-review-summary.en.md) |
| [Release Notes](release-notes.md) | [Release Notes](release-notes.en.md) |
EOF
	cat >> "$repo/docs/README.en.md" <<'EOF'
| [Hardening Review](hardening-review-summary.en.md) | Hardening review summary |
| [Release Notes](release-notes.en.md) | Release notes draft |
EOF
	cat > "$repo/docs/hardening-review-summary.md" <<'EOF'
# 硬化审查摘要

[English](hardening-review-summary.en.md) | 简体中文

## 当前快照

| 项目 | 当前状态 |
|------|----------|
| 最近完整验证 | 验证目标 `abc1234` 已通过完整验证，覆盖前端单测 3115 个用例、Playwright 377 个 E2E 用例、Docker image `sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` 和 Docker smoke 使用 Docker 自动分配的 loopback 端口 `http://127.0.0.1:32779` |
EOF
	cat > "$repo/docs/hardening-review-summary.en.md" <<'EOF'
# Hardening Review Summary

English | [简体中文](hardening-review-summary.md)

## Current Snapshot

| Item | Current status |
| --- | --- |
| Latest broad validation | validation target `abc1234` passed full validation, covering 3115 frontend unit tests, 377 Playwright E2E cases, Docker image `sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`, and Docker smoke using Docker-assigned loopback port `http://127.0.0.1:32779` |
EOF
	cat > "$repo/docs/release-notes.md" <<'EOF'
# 发布说明草稿

[English](release-notes.en.md) | 简体中文

## 发布前验证

最近本地完整验证快照：验证目标 `abc1234`，完整验证通过，覆盖 Docker image `sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` 和 Docker smoke。Docker smoke 使用 Docker 自动分配的 loopback 端口 `http://127.0.0.1:32779`。

- Playwright E2E：`377 passed`
- 前端单测：`3115 passed`
EOF
	cat > "$repo/docs/release-notes.en.md" <<'EOF'
# Release Notes Draft

English | [简体中文](release-notes.md)

## Pre-Release Validation

Latest local full-validation snapshot: validation target `abc1234`; full validation passed, covering Docker image `sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` and Docker smoke. The Docker smoke used the Docker-assigned loopback port `http://127.0.0.1:32779`.

- Playwright E2E: `377 passed`
- Frontend unit tests: `3115 passed`
EOF
	git -C "$repo" add docs/README.md docs/README.en.md docs/hardening-review-summary.md docs/hardening-review-summary.en.md docs/release-notes.md docs/release-notes.en.md
}

write_release_notes_validation_evidence_contract_mismatch_doc() {
	local repo="$1"
	write_release_notes_validation_evidence_contract_valid_docs "$repo"
	perl -0pi -e 's/Frontend unit tests: `3115 passed`/Frontend unit tests: `3113 passed`/' "$repo/docs/release-notes.en.md"
	git -C "$repo" add docs/release-notes.en.md
}

write_release_notes_validation_evidence_contract_docker_image_mismatch_doc() {
	local repo="$1"
	write_release_notes_validation_evidence_contract_valid_docs "$repo"
	perl -0pi -e 's/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/' "$repo/docs/release-notes.en.md"
	git -C "$repo" add docs/release-notes.en.md
}

write_release_notes_validation_evidence_contract_docker_port_mismatch_doc() {
	local repo="$1"
	write_release_notes_validation_evidence_contract_valid_docs "$repo"
	perl -0pi -e 's/http:\/\/127\.0\.0\.1:32779/http:\/\/127.0.0.1:32780/' "$repo/docs/release-notes.en.md"
	git -C "$repo" add docs/release-notes.en.md
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

## WebDAV

- 日常或生产挂载建议设置 \`webdav.auth_type = "users"\`，使用 MnemoNAS 用户账户挂载。
- 根目录示例配置保留旧全局 Basic Auth 作为兼容基线；该模式使用 \`[webdav]\` 中的服务凭据。

当前检查项 ID 包括 $chinese_ids。
EOF
	cat > "$repo/docs/api-reference.en.md" <<EOF
# API Reference

English | [简体中文](api-reference.md)

## WebDAV

- For day-to-day or production mounts, set \`webdav.auth_type = "users"\` to mount with MnemoNAS user accounts.
- The root example config keeps legacy global Basic Auth as a compatibility baseline; that mode uses service credentials from \`[webdav]\`.

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
run_accepts "truncated-non-secret-ellipsis" write_truncated_non_secret_ellipsis_doc
run_accepts "security-check-doc-ids" write_security_check_docs_valid
run_accepts "direct-executable-script-reference" write_direct_executable_script_reference_doc
run_accepts "interpreter-script-reference" write_interpreter_script_reference_doc
run_accepts "curl-basic-auth-guidance-text" write_curl_basic_auth_guidance_allowed_doc
run_accepts "encoded-restore-query-path" write_encoded_restore_query_path_doc
run_accepts "encoded-api-path-query" write_encoded_api_path_query_doc
run_accepts "storage-cdc-contract" write_storage_cdc_contract_valid_docs
run_accepts "security-checklist-contract" write_security_checklist_contract_valid_docs
run_accepts "api-reference-webdav-auth-contract" write_api_reference_webdav_auth_contract_valid_docs
run_accepts "backup-restore-drill-contract" write_backup_restore_drill_contract_valid_docs
run_accepts "docker-deployment-release-verification-contract" write_docker_deployment_release_verification_contract_valid_docs
run_accepts "release-notes-validation-evidence-contract" write_release_notes_validation_evidence_contract_valid_docs
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
run_rejects "ellipsis-secret-placeholder" "avoid ellipsis-only secret placeholders in project documentation" write_ellipsis_secret_placeholder_doc
run_rejects "json-ellipsis-secret-placeholder" "avoid ellipsis-only secret placeholders in project documentation" write_json_ellipsis_secret_placeholder_doc
run_rejects "yaml-ellipsis-secret-placeholder" "avoid ellipsis-only secret placeholders in project documentation" write_yaml_ellipsis_secret_placeholder_doc
run_rejects "hyphenated-ellipsis-secret-placeholder" "avoid ellipsis-only secret placeholders in project documentation" write_hyphenated_ellipsis_secret_placeholder_doc
run_rejects "dot-ellipsis-secret-placeholder" "avoid ellipsis-only secret placeholders in project documentation" write_dot_ellipsis_secret_placeholder_doc
run_rejects "chinese-doc-english-phrase" "avoid English phrasing in Chinese documentation: preview config" write_chinese_doc_english_phrase_doc
run_rejects "english-doc-chinese-text" "avoid non-English text outside language-navigation links in English documentation" write_english_doc_chinese_text_doc
run_rejects "english-json-fence-chinese-text" "avoid non-English text outside language-navigation links in English documentation" write_english_json_fence_chinese_text_doc
run_rejects "remote-shell-pipe" "avoid piping remote install scripts directly to a shell" write_remote_shell_pipe_doc
run_rejects "curl-basic-auth-argument" "avoid putting Basic Auth credentials in curl command arguments" write_curl_basic_auth_argument_doc
run_rejects "multiline-curl-basic-auth-argument" "avoid putting Basic Auth credentials in curl command arguments" write_multiline_curl_basic_auth_argument_doc
run_rejects "curl-basic-authorization-header" "avoid putting Basic Auth credentials in curl command arguments" write_curl_basic_authorization_header_doc
run_rejects "reverse-proxy-webdav-contract-missing-chmod" "docs/reverse-proxy-setup.en.md: missing reverse-proxy WebDAV verification guidance text: chmod 600 \"\$curl_auth_config\"" write_reverse_proxy_webdav_contract_missing_chmod_doc
run_rejects "curl-bearer-auth-header" "avoid putting Bearer tokens in curl command arguments" write_curl_bearer_auth_header_doc
run_rejects "multiline-curl-bearer-auth-header" "avoid putting Bearer tokens in curl command arguments" write_multiline_curl_bearer_auth_header_doc
run_rejects "non-executable-script-reference" "script reference is not executable: ./scripts/helper.sh" write_non_executable_script_reference_doc
run_rejects "non-executable-script-link" "linked script is not executable: scripts/helper.sh" write_non_executable_script_link_doc
run_rejects "raw-restore-query-path" "URL-encode API path query values in documentation examples; use path=%2F..." write_raw_restore_query_path_doc
run_rejects "raw-api-path-query" "URL-encode API path query values in documentation examples; use path=%2F..." write_raw_api_path_query_doc
run_rejects "storage-cdc-contract-missing-boundary" "docs/storage-internals.en.md: missing storage CDC boundary text: current version history does not reference-count CDC chunks" write_storage_cdc_contract_missing_boundary_doc
run_rejects "configuration-cdc-contract-missing-boundary" "docs/configuration.en.md: missing storage CDC boundary text: Current Go version history still uses whole-object CAS snapshots" write_configuration_cdc_contract_missing_boundary_doc
run_rejects "security-checklist-contract-missing-firewall" "docs/security.en.md: missing public deployment security checklist text: [Public cloud firewall checklist](cloud-firewall-checklist.en.md)" write_security_checklist_contract_missing_firewall_doc
run_rejects "security-checklist-contract-missing-public-smoke" "docs/security.en.md: missing public deployment security checklist text: ./scripts/public-go-live-smoke.sh <domain>" write_security_checklist_contract_missing_public_smoke_doc
run_rejects "api-reference-webdav-auth-contract-missing-users" "docs/api-reference.en.md: missing WebDAV auth guidance text: For day-to-day or production mounts, set \`webdav.auth_type = \"users\"\`" write_api_reference_webdav_auth_contract_missing_users_doc
run_rejects "api-reference-webdav-auth-contract-legacy-first" "docs/api-reference.en.md: avoid leading WebDAV auth guidance with legacy Basic Auth: - By default it uses the legacy global Basic Auth credentials" write_api_reference_webdav_auth_contract_legacy_first_doc
run_rejects "backup-restore-drill-contract-missing-history" "docs/backup-guide.en.md: missing backup restore drill guidance text: Restore-drill history and explicit restore history both keep the latest 20 entries" write_backup_restore_drill_contract_missing_history_doc
run_rejects "hardening-progress-release-readiness-contract-missing-backup-smoke" "docs/hardening-progress.en.md: missing release-readiness hardening ledger text: release checklist and bilingual release notes to retain the public-deployment doctor, external-network smoke, backup restore-drill smoke, post-publication go-live verification, and cloud-firewall review entry points" write_hardening_progress_release_readiness_contract_missing_backup_smoke_doc
run_rejects "hardening-progress-release-readiness-contract-missing-published-release-verifier" "docs/hardening-progress.en.md: missing release-readiness hardening ledger text: \`./scripts/verify-published-release.sh --version <tag> --repository seanbao/mnemonas\`" write_hardening_progress_release_readiness_contract_missing_published_release_verifier_doc
run_rejects "hardening-progress-release-readiness-contract-missing-go-live" "docs/hardening-progress.en.md: missing release-readiness hardening ledger text: \`./scripts/release-go-live-check.sh --version <tag> --domain <domain>\`" write_hardening_progress_release_readiness_contract_missing_go_live_doc
run_rejects "hardening-progress-release-readiness-contract-missing-goal-boundary" "docs/hardening-progress.en.md: missing release-readiness hardening ledger text: Not complete." write_hardening_progress_release_readiness_contract_missing_goal_boundary_doc
run_rejects "docker-deployment-release-verification-contract-missing-retry" "docs/docker-deployment.en.md: missing Docker release verification guidance text: MNEMONAS_RELEASE_IMAGE_CHECK_SLEEP_SECONDS" write_docker_deployment_release_verification_contract_missing_retry_doc
run_rejects "docker-deployment-release-verification-contract-missing-dash-dir" "docs/docker-deployment.en.md: missing Docker release verification guidance text: dash-prefixed relative paths" write_docker_deployment_release_verification_contract_missing_dash_dir_doc
run_rejects "docker-deployment-release-verification-contract-missing-keep-artifacts" "docs/docker-deployment.en.md: missing Docker release verification guidance text: --keep-artifacts" write_docker_deployment_release_verification_contract_missing_keep_artifacts_doc
run_rejects "docker-deployment-release-verification-contract-missing-go-live-keep-artifacts" "docs/docker-deployment.en.md: missing Docker release verification guidance text: --keep-published-artifacts" write_docker_deployment_release_verification_contract_missing_go_live_keep_artifacts_doc
run_rejects "release-notes-validation-evidence-contract-mismatch" "docs/release-notes.en.md: frontend unit test count 3113 does not match docs/hardening-review-summary.en.md: 3115" write_release_notes_validation_evidence_contract_mismatch_doc
run_rejects "release-notes-validation-evidence-contract-docker-image-mismatch" "docs/release-notes.en.md: Docker image sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb does not match docs/hardening-review-summary.en.md: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" write_release_notes_validation_evidence_contract_docker_image_mismatch_doc
run_rejects "release-notes-validation-evidence-contract-docker-port-mismatch" "docs/release-notes.en.md: Docker smoke port http://127.0.0.1:32780 does not match docs/hardening-review-summary.en.md: http://127.0.0.1:32779" write_release_notes_validation_evidence_contract_docker_port_mismatch_doc
run_rejects "security-check-doc-missing-id" "docs/api-reference.en.md: security-check documentation is missing ID: config_file_access" write_security_check_docs_missing_english_id
run_rejects "security-check-doc-missing-chinese-id" "docs/api-reference.md: security-check documentation is missing ID: config_file_access" write_security_check_docs_missing_chinese_id
run_rejects "security-check-doc-unknown-english-id" "docs/api-reference.en.md: security-check documentation lists unknown ID: ghost_probe" write_security_check_docs_unknown_english_id
run_rejects "security-check-doc-unknown-chinese-id" "docs/api-reference.md: security-check documentation lists unknown ID: ghost_probe" write_security_check_docs_unknown_chinese_id

printf '[doc-links-test] all checks passed\n'
