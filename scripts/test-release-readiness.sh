#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
READINESS="$REPO_ROOT/scripts/release-readiness.sh"
PLANNER="$REPO_ROOT/scripts/plan-hardening-commits.sh"
COMMIT_MESSAGE_CHECK="$REPO_ROOT/scripts/check-commit-message.sh"

tmp="$(mktemp -d)"
output_dir="$(mktemp -d)"
trap 'rm -rf -- "$tmp" "$output_dir"' EXIT

fail() {
	printf 'test-release-readiness: %s\n' "$1" >&2
	exit 1
}

assert_file_contains() {
	local path="$1"
	local expected="$2"

	grep -Fq -- "$expected" "$path" || {
		cat "$path" >&2
		fail "$path does not contain: $expected"
	}
}

write_checklists() {
	cat >CHANGELOG.md <<'EOF'
# CHANGELOG

- [ ] 变更感知完整验证通过：`GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- [ ] 文档检查通过：`make docs-check`
- [ ] 脚本检查通过：`make scripts-check`
- [ ] 依赖安全检查通过：`make security-check NPM_AUDIT=1`
- [ ] Docker 构建和烟测通过：`make docker-check`
- [ ] 公网发布前在服务器运行：`sudo mnemonas-doctor --public-domain <domain>`，并按 [公网云防火墙复核清单](docs/cloud-firewall-checklist.md) 复核环境
- [ ] 公网发布前从外部网络运行：`./scripts/public-go-live-smoke.sh <domain>`
- [ ] 如本次发布包含备份恢复链路，运行恢复演练 smoke 入口：`./scripts/backup-restore-drill-smoke.sh`
- [ ] 发布前就绪摘要通过：`./scripts/release-readiness.sh`
- [ ] `./scripts/plan-hardening-commits.sh --fail-on-manual` 确认没有未归类路径
- [ ] 发布后下载 GitHub Release 产物，并运行 `./scripts/verify-release-artifacts.sh --version <tag> --repository seanbao/mnemonas --require-targets --check-image <artifact-dir>`，验证 release 产物。
EOF

	cat >CHANGELOG.en.md <<'EOF'
# CHANGELOG

- [ ] Run full change-aware validation: `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- [ ] Run documentation checks: `make docs-check`
- [ ] Run script checks: `make scripts-check`
- [ ] Run dependency security checks: `make security-check NPM_AUDIT=1`
- [ ] Run Docker build and smoke checks: `make docker-check`
- [ ] Before public release, run on the server: `sudo mnemonas-doctor --public-domain <domain>` and review the [Public cloud firewall checklist](docs/cloud-firewall-checklist.en.md)
- [ ] Before public release, run from an external network: `./scripts/public-go-live-smoke.sh <domain>`
- [ ] If this release includes the backup and restore path, run the restore-drill smoke entry point: `./scripts/backup-restore-drill-smoke.sh`
- [ ] Run release readiness summary: `./scripts/release-readiness.sh`
- [ ] Confirm `./scripts/plan-hardening-commits.sh --fail-on-manual` reports no unclassified paths
- [ ] After publication, download the GitHub Release artifacts and run `./scripts/verify-release-artifacts.sh --version <tag> --repository seanbao/mnemonas --require-targets --check-image <artifact-dir>` to verify release artifacts.
EOF
}

write_release_notes() {
	local target="$1"

	mkdir -p docs
	cat >docs/release-notes.md <<'EOF'
# 发布说明草稿

## 发布前验证
EOF
	cat >>docs/release-notes.md <<EOF
最近本地完整验证快照：验证目标 \`$target\`。
EOF
	cat >>docs/release-notes.md <<'EOF'

- `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- `make docs-check`
- `make scripts-check`
- `make security-check NPM_AUDIT=1`
- `make docker-check`
- `sudo mnemonas-doctor --public-domain <domain>`
- `./scripts/public-go-live-smoke.sh <domain>`
- `./scripts/backup-restore-drill-smoke.sh`
- `docs/cloud-firewall-checklist.md`
- `./scripts/test-release-tag.sh`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`

## 发布后核验

```bash
gh release download <tag> --repo seanbao/mnemonas --dir dist/release-check
./scripts/verify-release-artifacts.sh --version <tag> --repository seanbao/mnemonas --require-targets --check-image dist/release-check
```

## 已知限制

- 当前发布候选定位为已通过完整本地验证的 L1 私有文件云盘和 L1+ 公网安全入口基础，不应作为重要数据的唯一长期副本；生产使用仍应保留外部备份。
EOF

	cat >docs/release-notes.en.md <<'EOF'
# Release Notes Draft

## Pre-Release Validation
EOF
	cat >>docs/release-notes.en.md <<EOF
Latest local full-validation snapshot: validation target \`$target\`.
EOF
	cat >>docs/release-notes.en.md <<'EOF'

- `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- `make docs-check`
- `make scripts-check`
- `make security-check NPM_AUDIT=1`
- `make docker-check`
- `sudo mnemonas-doctor --public-domain <domain>`
- `./scripts/public-go-live-smoke.sh <domain>`
- `./scripts/backup-restore-drill-smoke.sh`
- `docs/cloud-firewall-checklist.en.md`
- `./scripts/test-release-tag.sh`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`

## Post-Publish Verification

```bash
gh release download <tag> --repo seanbao/mnemonas --dir dist/release-check
./scripts/verify-release-artifacts.sh --version <tag> --repository seanbao/mnemonas --require-targets --check-image dist/release-check
```

## Known Limitations

- This release candidate is positioned as a fully locally validated L1 private file cloud with an initial L1+ public-access safety baseline, not as the only long-term copy of important data. Production use should keep external backups.
EOF
}

write_community_files() {
	mkdir -p .github/ISSUE_TEMPLATE .github/workflows
	touch \
		README.md \
		README.en.md \
		LICENSE \
		CONTRIBUTING.md \
		CONTRIBUTING.en.md \
		CODE_OF_CONDUCT.md \
		CODE_OF_CONDUCT.zh-CN.md \
		SUPPORT.md \
		SUPPORT.en.md \
		SECURITY.md \
		SECURITY.zh-CN.md
	cat >Makefile <<'EOF'
.PHONY: go-packages workflows-check scripts-check toolchains-check docs-check security-check test test-torture docker docker-smoke docker-check check verify-changed quick-check lint

GO_TEST_TIMEOUT ?= 20m

go-packages:
	@true

workflows-check:
	@true

scripts-check:
	@true

toolchains-check:
	@true

docs-check:
	@true

security-check:
	@true

test:
	@true

test-torture:
	./scripts/torture-test.sh

docker:
	@true

docker-smoke:
	@true

docker-check: docker docker-smoke

lint:
	@true

check: workflows-check scripts-check toolchains-check docs-check lint test
	@true

verify-changed:
	./scripts/verify-changed.sh

quick-check:
	@true
EOF
	cat >.github/ISSUE_TEMPLATE/config.yml <<'EOF'
blank_issues_enabled: false
contact_links:
  - name: Security vulnerability / 安全漏洞
    url: https://github.com/seanbao/mnemonas/security/policy
    about: Report security vulnerabilities through the private reporting channel. 安全漏洞通过私密报告渠道提交。
  - name: Support boundary / 支持边界
    url: https://github.com/seanbao/mnemonas/blob/master/SUPPORT.md
    about: Review support channels and required diagnostics before filing operational questions. 提交运维问题前先查看支持渠道和诊断要求。
EOF
	cat >.github/dependabot.yml <<'EOF'
version: 2
updates:
  - package-ecosystem: "gomod"
    directory: "/"
    schedule:
      interval: "weekly"
      day: "monday"
    open-pull-requests-limit: 5
    labels:
      - "dependencies"
      - "go"

  - package-ecosystem: "cargo"
    directory: "/dataplane"
    schedule:
      interval: "weekly"
      day: "monday"
    open-pull-requests-limit: 5
    labels:
      - "dependencies"
      - "rust"

  - package-ecosystem: "cargo"
    directory: "/tools/proto-gen"
    schedule:
      interval: "weekly"
      day: "monday"
    open-pull-requests-limit: 3
    labels:
      - "dependencies"
      - "rust"
      - "proto"

  - package-ecosystem: "npm"
    directory: "/web"
    schedule:
      interval: "weekly"
      day: "monday"
    open-pull-requests-limit: 5
    labels:
      - "dependencies"
      - "frontend"

  - package-ecosystem: "github-actions"
    directory: "/"
    schedule:
      interval: "weekly"
      day: "monday"
    open-pull-requests-limit: 3
    labels:
      - "dependencies"
      - "ci"

  - package-ecosystem: "docker"
    directory: "/"
    schedule:
      interval: "weekly"
      day: "monday"
    labels:
      - "dependencies"
      - "docker"
EOF
	cat >.github/workflows/ci.yml <<'EOF'
name: CI

on:
  pull_request:
    branches: [main, master]

permissions:
  contents: read

jobs:
  workflows:
    steps:
      - uses: actions/checkout@v4
        with:
          persist-credentials: false
      - run: make workflows-check
  scripts:
    steps:
      - run: make scripts-check
  toolchains:
    steps:
      - run: make toolchains-check
  docs:
    steps:
      - run: make docs-check
  go:
    steps:
      - run: CGO_ENABLED=1 bash ./scripts/with-test-dataplane.sh go test -v -race -coverprofile=coverage.out ./...
  frontend:
    steps:
      - run: npm audit --audit-level="${{ env.NPM_AUDIT_LEVEL }}"
  e2e:
    steps:
      - run: npm run test:e2e
  docker:
    steps:
      - run: ./scripts/docker-smoke.sh mnemonas:test
EOF
	cat >.github/workflows/release.yml <<'EOF'
name: Release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: read

jobs:
  build:
    steps:
      - uses: actions/checkout@v4
        with:
          persist-credentials: false
      - run: ./scripts/check-release-tag.sh "$GITHUB_REF_NAME"
  docker:
    permissions:
      contents: read
      packages: write
    steps:
      - run: ./scripts/docker-smoke.sh mnemonas:release-smoke
  release:
    permissions:
      contents: write
      packages: read
    steps:
      - uses: docker/login-action@v3
      - run: |
          ./scripts/verify-release-artifacts.sh \
            --require-targets \
            --check-image \
            dist
      - uses: softprops/action-gh-release@v2
        with:
          prerelease: ${{ contains(github.ref_name, '-') }}
EOF
	cat >.github/workflows/torture.yml <<'EOF'
name: Torture

on:
  workflow_dispatch:
  schedule:
    - cron: '17 18 * * *'

permissions:
  contents: read

jobs:
  safe-torture:
    runs-on: ubuntu-latest
    steps:
      - name: Run non-destructive torture matrix
        env:
          RUN_LIVE_FAULTS: '0'
        run: make test-torture
EOF
	cat >SECURITY.md <<'EOF'
# Security Policy

**DO NOT** open a public GitHub issue for security vulnerabilities.

Use GitHub's **Private vulnerability reporting** feature for this repository when available.

A dedicated security email should only be added here after the mailbox is configured and monitored.

Dataplane gRPC/HTTP ports `9090/9091` should not be exposed to public or untrusted networks.

```bash
make security-check NPM_AUDIT=1
```

MnemoNAS is not designed for direct internet exposure without a hardened proxy/VPN layer.
EOF
	cat >SECURITY.zh-CN.md <<'EOF'
# 安全策略

**不要**为安全漏洞创建公开 GitHub Issue。

优先使用本仓库的 GitHub **Private vulnerability reporting** 功能。

只有在专用安全邮箱已经配置并持续监控后，才应把邮箱地址加入本文件。

dataplane gRPC/HTTP 端口 `9090/9091` 不应暴露到公网或不可信网络。

```bash
make security-check NPM_AUDIT=1
```

不建议在没有加固代理/VPN 的情况下直接暴露到公网。
EOF
	cat >.github/ISSUE_TEMPLATE/bug_report.yml <<'EOF'
name: Bug report / 缺陷报告
body:
  - type: markdown
    attributes:
      value: |
        Reports should include reproduction steps, deployment context, and diagnostics. Sensitive values such as passwords, tokens, cookies, private URLs, and internal addresses must be removed before posting logs.
  - type: textarea
    id: diagnostics
    attributes:
      description: Relevant sanitized logs, `mnemonas-doctor`, Docker preflight, browser console output, screenshots, or request IDs.
  - type: checkboxes
    id: safety
    attributes:
      options:
        - label: Security-sensitive exploit details are not posted publicly.
EOF
	cat >.github/ISSUE_TEMPLATE/feature_request.yml <<'EOF'
name: Feature request / 功能建议
body:
  - type: markdown
    attributes:
      value: |
        Feature requests should describe the workflow, user impact, and affected surfaces. Security, data, deployment, and compatibility implications should be called out explicitly.
  - type: textarea
    id: risks
    attributes:
      description: Data migration, security, deployment, performance, or client-compatibility concerns.
EOF
	cat >.github/ISSUE_TEMPLATE/question.yml <<'EOF'
name: Usage question / 使用问题
body:
  - type: markdown
    attributes:
      value: |
        Usage questions should include deployment context and diagnostics. Remove passwords, tokens, cookies, private URLs, internal addresses, and private file names before posting logs or configuration snippets.
  - type: textarea
    id: diagnostics
    attributes:
      description: Sanitized command output, logs, screenshots, or configuration excerpts.
  - type: checkboxes
    id: checklist
    attributes:
      options:
        - label: Logs and configuration snippets are sanitized.
EOF
	cat >.github/ISSUE_TEMPLATE/webdav_compatibility.yml <<'EOF'
name: WebDAV compatibility report / WebDAV 兼容性报告
body:
  - type: markdown
    attributes:
      value: |
        Use this form for WebDAV client compatibility reports, including successful validation, client-specific failures, and behavior that differs between clients. Remove passwords, tokens, cookies, private URLs, internal addresses, and private file names before posting logs or screenshots.
  - type: textarea
    id: diagnostics
    attributes:
      description: Sanitized `mnemonas-doctor`, client logs, server logs, request IDs, screenshots, or diagnostic bundle notes.
  - type: checkboxes
    id: checklist
    attributes:
      options:
        - label: Security-sensitive exploit details are not posted publicly.
EOF
	cat >.github/pull_request_template.md <<'EOF'
# Pull Request / 变更说明

## Scope / 范围

-

## User-Visible Behavior / 用户可见行为

-

## Data, Security, And Deployment Impact / 数据、安全与部署影响

-

## Validation / 验证

- [ ] `make verify-changed`
- [ ] `make docs-check`
- [ ] `make scripts-check`

## Residual Risk / 残余风险

-
EOF
	cat >SUPPORT.md <<'EOF'
# 支持

WebDAV 兼容性报告表单：https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml

| 安全漏洞 | GitHub Private Vulnerability Reporting | 不要公开提交漏洞细节，详见 [SECURITY.zh-CN.md](SECURITY.zh-CN.md) |
EOF
	cat >SUPPORT.en.md <<'EOF'
# Support

WebDAV compatibility report form: https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml

| Security vulnerability | GitHub Private Vulnerability Reporting | Do not post exploit details publicly; see [SECURITY.md](SECURITY.md) |
EOF
}

write_validation_docs() {
	local target="$1"

	mkdir -p docs
	cat >docs/hardening-progress.md <<EOF
# 硬化进度台账

| 日期 | 命令 | 结果 |
|------|------|------|
| 2026-06-18 | \`GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master\` | 通过。覆盖验证目标 \`$target\` 的分支范围。 |
EOF

	cat >docs/hardening-progress.en.md <<EOF
# Hardening Progress Ledger

| Date | Command | Result |
| --- | --- | --- |
| 2026-06-18 | \`GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master\` | Passed. Covered validation target \`$target\` across the branch range. |
EOF

	cat >docs/hardening-review-summary.md <<EOF
# 硬化审查摘要

| 项目 | 当前状态 |
|------|----------|
| 最近完整验证 | \`GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master\` 在验证目标 \`$target\` 通过。 |
EOF

	cat >docs/hardening-review-summary.en.md <<EOF
# Hardening Review Summary

| Item | Current status |
| --- | --- |
| Latest broad validation | \`GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master\` passed at validation target \`$target\`. |
EOF
}

mkdir -p "$tmp/scripts"
cp "$READINESS" "$tmp/scripts/release-readiness.sh"
cp "$PLANNER" "$tmp/scripts/plan-hardening-commits.sh"
cp "$COMMIT_MESSAGE_CHECK" "$tmp/scripts/check-commit-message.sh"
chmod +x "$tmp/scripts/release-readiness.sh" "$tmp/scripts/plan-hardening-commits.sh" "$tmp/scripts/check-commit-message.sh"

cd "$tmp"
git init -q -b master
git config user.email "mnemonas@example.invalid"
git config user.name "MnemoNAS Test"

write_checklists
write_community_files
git add .
git commit -q -m "docs: initial checklist"
validation_target="$(git rev-parse --short=12 HEAD)"
validation_target_full="$(git rev-parse "$validation_target^{commit}")"
validation_target_short="$(git rev-parse --short=7 "$validation_target")"
write_release_notes "$validation_target"
write_validation_docs "$validation_target"
git add docs
git commit -q -m "docs: record validation evidence"

./scripts/release-readiness.sh --base "$validation_target" >"$output_dir/evidence-only.out" 2>"$output_dir/evidence-only.err"
assert_file_contains "$output_dir/evidence-only.out" "[release-readiness] validation:"
assert_file_contains "$output_dir/evidence-only.out" "[release-readiness] commit-messages:"
assert_file_contains "$output_dir/evidence-only.out" "only release documentation changed since target"
assert_file_contains "$output_dir/evidence-only.out" "[release-readiness] validation-diff:"
assert_file_contains "$output_dir/evidence-only.out" "release readiness summary completed"

git checkout -q -b sibling-base "$validation_target"
printf '# sibling base\n' >sibling.md
git add sibling.md
git commit -q -m "docs: create sibling base"
git checkout -q master
if ./scripts/release-readiness.sh --base sibling-base >"$output_dir/non-ancestor-base.out" 2>"$output_dir/non-ancestor-base.err"; then
	fail "release readiness accepted a base ref that is not an ancestor of HEAD"
fi
assert_file_contains "$output_dir/non-ancestor-base.err" "base ref is not an ancestor of HEAD: sibling-base"

git checkout -q master
git checkout -q -b mixed-validation-target-lengths
sed -i.bak "s/${validation_target}/${validation_target_short}/" docs/hardening-progress.md
rm -f docs/hardening-progress.md.bak
sed -i.bak "s/${validation_target}/${validation_target_full}/" docs/hardening-review-summary.en.md
rm -f docs/hardening-review-summary.en.md.bak
git add docs/hardening-progress.md docs/hardening-review-summary.en.md
git commit -q -m "docs: mix validation target lengths"
./scripts/release-readiness.sh --base "$validation_target" >"$output_dir/mixed-validation-target-lengths.out" 2>"$output_dir/mixed-validation-target-lengths.err"
assert_file_contains "$output_dir/mixed-validation-target-lengths.out" "[release-readiness] validation:"
assert_file_contains "$output_dir/mixed-validation-target-lengths.out" "only release documentation changed since target"

git checkout -q master
git checkout -q -b missing-validation-evidence-file
rm -f docs/hardening-review-summary.en.md
git add -A docs/hardening-review-summary.en.md
git commit -q -m "docs: remove validation evidence"
if ./scripts/release-readiness.sh --base "$validation_target" >"$output_dir/missing-validation-evidence.out" 2>"$output_dir/missing-validation-evidence.err"; then
	fail "release readiness accepted a missing validation evidence document"
fi
assert_file_contains "$output_dir/missing-validation-evidence.err" "missing validation evidence file: docs/hardening-review-summary.en.md"

git checkout -q master
git checkout -q -b missing-validation-target
validation_tick="$(printf '\140')"
sed -i.bak "s/validation target ${validation_tick}[^${validation_tick}]*${validation_tick}/validation target missing/" docs/hardening-review-summary.en.md
rm -f docs/hardening-review-summary.en.md.bak
git add docs/hardening-review-summary.en.md
git commit -q -m "docs: remove validation target"
if ./scripts/release-readiness.sh --base "$validation_target" >"$output_dir/missing-validation-target.out" 2>"$output_dir/missing-validation-target.err"; then
	fail "release readiness accepted validation evidence without a target"
fi
assert_file_contains "$output_dir/missing-validation-target.err" "validation evidence target not recorded in: docs/hardening-review-summary.en.md"

git checkout -q master
git checkout -q -b missing-release-note-validation-target
sed -i.bak "s/${validation_target}/000000000000/" docs/release-notes.en.md
rm -f docs/release-notes.en.md.bak
git add docs/release-notes.en.md
git commit -q -m "docs: stale release note validation target"
if ./scripts/release-readiness.sh --base "$validation_target" >"$output_dir/missing-release-note-validation-target.out" 2>"$output_dir/missing-release-note-validation-target.err"; then
	fail "release readiness accepted release notes without the validation target"
fi
assert_file_contains "$output_dir/missing-release-note-validation-target.err" "docs/release-notes.en.md is missing required text"
assert_file_contains "$output_dir/missing-release-note-validation-target.err" "$validation_target"

git checkout -q master
git checkout -q -b release-docs
printf '\n- Release artifact verifier coverage updated.\n' >>CHANGELOG.en.md
printf '\n- Release artifact verifier coverage updated.\n' >>docs/release-notes.en.md
printf '\n- Release artifact verifier coverage updated.\n' >>CHANGELOG.md
printf '\n- Release artifact verifier coverage updated.\n' >>docs/release-notes.md
git add CHANGELOG.en.md CHANGELOG.md docs/release-notes.en.md docs/release-notes.md
git commit -q -m "docs: update release documentation"

./scripts/release-readiness.sh --base "$validation_target" >"$output_dir/release-docs-only.out" 2>"$output_dir/release-docs-only.err"
assert_file_contains "$output_dir/release-docs-only.out" "[release-readiness] validation:"
assert_file_contains "$output_dir/release-docs-only.out" "only release documentation changed since target"
assert_file_contains "$output_dir/release-docs-only.out" "[release-readiness] validation-diff:"

git checkout -q master
git checkout -q -b release-readiness
printf '# docs\n' >README.md
git add README.md
git commit -q -m "docs: update release docs"

if ./scripts/release-readiness.sh >"$output_dir/post-validation.out" 2>"$output_dir/post-validation.err"; then
	fail "release readiness accepted non-release-documentation changes after validation target"
fi
assert_file_contains "$output_dir/post-validation.out" "[release-readiness] validation:"
assert_file_contains "$output_dir/post-validation.out" "files changed since target"
assert_file_contains "$output_dir/post-validation.err" "non-release-documentation changes exist after validation target"

./scripts/release-readiness.sh --allow-post-validation-changes >"$output_dir/pass.out" 2>"$output_dir/pass.err"
assert_file_contains "$output_dir/pass.out" "[release-readiness] branch:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] base:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] commits:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] diff:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] worktree:          clean"
assert_file_contains "$output_dir/pass.out" "[release-readiness] planner          [hardening-commit-plan] no changed files detected"
assert_file_contains "$output_dir/pass.out" "[release-readiness] commit-messages:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] community:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] validation:"
assert_file_contains "$output_dir/pass.out" "full gate evidence at"
assert_file_contains "$output_dir/pass.out" "files changed since target"
assert_file_contains "$output_dir/pass.out" "[release-readiness] validation-diff:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] validation-warning:"
assert_file_contains "$output_dir/pass.out" "draft override allowed non-release-documentation changes after validation target"
assert_file_contains "$output_dir/pass.out" "[release-readiness] release-notes:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] checklist:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] status:"
assert_file_contains "$output_dir/pass.out" "release readiness summary completed"

git checkout -q master
git checkout -q -b bad-commit-message
printf '# docs\n' >README.md
git add README.md
git commit -q -m "Bad release docs"
if ./scripts/release-readiness.sh --allow-post-validation-changes >"$output_dir/bad-commit-message.out" 2>"$output_dir/bad-commit-message.err"; then
	fail "release readiness accepted a non-conventional branch commit"
fi
assert_file_contains "$output_dir/bad-commit-message.err" "commit message does not follow project convention"
assert_file_contains "$output_dir/bad-commit-message.err" "subject must use Conventional Commits"

git checkout -q master
git checkout -q -b autosquash-commit-message
printf '# docs\n' >README.md
git add README.md
git commit -q -m "fixup! docs: update release docs"
if ./scripts/release-readiness.sh --allow-post-validation-changes >"$output_dir/autosquash-commit-message.out" 2>"$output_dir/autosquash-commit-message.err"; then
	fail "release readiness accepted a temporary autosquash branch commit"
fi
assert_file_contains "$output_dir/autosquash-commit-message.err" "temporary autosquash commit remains on release branch"

git checkout -q release-readiness
printf '# dirty docs\n' >>README.md
if ./scripts/release-readiness.sh >"$output_dir/dirty.out" 2>"$output_dir/dirty.err"; then
	fail "release readiness accepted a dirty worktree by default"
fi
assert_file_contains "$output_dir/dirty.err" "worktree has uncommitted changes"

./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/allow-dirty.out" 2>"$output_dir/allow-dirty.err"
assert_file_contains "$output_dir/allow-dirty.out" "[release-readiness] worktree:          dirty (draft summary)"
assert_file_contains "$output_dir/allow-dirty.out" "[release-readiness] validation-warning:"

git checkout -q -- README.md
rm -f .github/pull_request_template.md
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-community.out" 2>"$output_dir/missing-community.err"; then
	fail "release readiness accepted missing community files"
fi
assert_file_contains "$output_dir/missing-community.err" "missing required community file: .github/pull_request_template.md"

git checkout -q -- .github/pull_request_template.md
sed -i.bak '/webdav_compatibility.yml/d' SUPPORT.en.md
rm -f SUPPORT.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-support-route.out" 2>"$output_dir/missing-support-route.err"; then
	fail "release readiness accepted a missing WebDAV support route"
fi
assert_file_contains "$output_dir/missing-support-route.err" "SUPPORT.en.md is missing required text"
git checkout -q -- SUPPORT.en.md

sed -i.bak '/SECURITY.zh-CN.md/d' SUPPORT.md
rm -f SUPPORT.md.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-support-security-link.out" 2>"$output_dir/missing-support-security-link.err"; then
	fail "release readiness accepted a missing localized security support link"
fi
assert_file_contains "$output_dir/missing-support-security-link.err" "SUPPORT.md is missing required text"
assert_file_contains "$output_dir/missing-support-security-link.err" "SECURITY.zh-CN.md"
git checkout -q -- SUPPORT.md

sed -i.bak 's/Do not post exploit details publicly/Public disclosure guidance missing/' SUPPORT.en.md
rm -f SUPPORT.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-support-security-warning.out" 2>"$output_dir/missing-support-security-warning.err"; then
	fail "release readiness accepted a support page without public-disclosure warning"
fi
assert_file_contains "$output_dir/missing-support-security-warning.err" "SUPPORT.en.md is missing required text"
assert_file_contains "$output_dir/missing-support-security-warning.err" "Do not post exploit details publicly"
git checkout -q -- SUPPORT.en.md

sed -i.bak '/Private vulnerability reporting/d' SECURITY.md
rm -f SECURITY.md.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-security-private-reporting.out" 2>"$output_dir/missing-security-private-reporting.err"; then
	fail "release readiness accepted a security policy without private reporting guidance"
fi
assert_file_contains "$output_dir/missing-security-private-reporting.err" "SECURITY.md is missing required text"
assert_file_contains "$output_dir/missing-security-private-reporting.err" "Private vulnerability reporting"
git checkout -q -- SECURITY.md

sed -i.bak '\#dataplane gRPC/HTTP#d' SECURITY.zh-CN.md
rm -f SECURITY.zh-CN.md.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-security-dataplane-boundary.out" 2>"$output_dir/missing-security-dataplane-boundary.err"; then
	fail "release readiness accepted a security policy without dataplane exposure guidance"
fi
assert_file_contains "$output_dir/missing-security-dataplane-boundary.err" "SECURITY.zh-CN.md is missing required text"
assert_file_contains "$output_dir/missing-security-dataplane-boundary.err" "dataplane gRPC/HTTP"
git checkout -q -- SECURITY.zh-CN.md

sed -i.bak '\#security/policy#d' .github/ISSUE_TEMPLATE/config.yml
rm -f .github/ISSUE_TEMPLATE/config.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-issue-config-link.out" 2>"$output_dir/missing-issue-config-link.err"; then
	fail "release readiness accepted a missing Issue config contact link"
fi
assert_file_contains "$output_dir/missing-issue-config-link.err" ".github/ISSUE_TEMPLATE/config.yml is missing required text"
git checkout -q -- .github/ISSUE_TEMPLATE/config.yml

sed -i.bak 's/blank_issues_enabled: false/blank_issues_enabled: true/' .github/ISSUE_TEMPLATE/config.yml
rm -f .github/ISSUE_TEMPLATE/config.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/blank-issues-enabled.out" 2>"$output_dir/blank-issues-enabled.err"; then
	fail "release readiness accepted enabled blank Issues"
fi
assert_file_contains "$output_dir/blank-issues-enabled.err" ".github/ISSUE_TEMPLATE/config.yml is missing required text"
assert_file_contains "$output_dir/blank-issues-enabled.err" "blank_issues_enabled: false"
git checkout -q -- .github/ISSUE_TEMPLATE/config.yml

sed -i.bak 's#/tools/proto-gen#/tools/proto-gen-missing#' .github/dependabot.yml
rm -f .github/dependabot.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-dependabot-proto.out" 2>"$output_dir/missing-dependabot-proto.err"; then
	fail "release readiness accepted a missing Dependabot proto generator update"
fi
assert_file_contains "$output_dir/missing-dependabot-proto.err" "missing required Dependabot update: cargo /tools/proto-gen"
git checkout -q -- .github/dependabot.yml

sed -i.bak '/docker-smoke.sh mnemonas:test/d' .github/workflows/ci.yml
rm -f .github/workflows/ci.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-ci-docker-smoke.out" 2>"$output_dir/missing-ci-docker-smoke.err"; then
	fail "release readiness accepted a CI workflow without Docker smoke coverage"
fi
assert_file_contains "$output_dir/missing-ci-docker-smoke.err" ".github/workflows/ci.yml is missing required text"
assert_file_contains "$output_dir/missing-ci-docker-smoke.err" "run: ./scripts/docker-smoke.sh mnemonas:test"
git checkout -q -- .github/workflows/ci.yml

sed -i.bak '/verify-release-artifacts/d' .github/workflows/release.yml
rm -f .github/workflows/release.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-release-artifact-verifier.out" 2>"$output_dir/missing-release-artifact-verifier.err"; then
	fail "release readiness accepted a Release workflow without artifact verification"
fi
assert_file_contains "$output_dir/missing-release-artifact-verifier.err" ".github/workflows/release.yml is missing required text"
assert_file_contains "$output_dir/missing-release-artifact-verifier.err" "./scripts/verify-release-artifacts.sh"
git checkout -q -- .github/workflows/release.yml

sed -i.bak '/--check-image/d' .github/workflows/release.yml
rm -f .github/workflows/release.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-release-image-check.out" 2>"$output_dir/missing-release-image-check.err"; then
	fail "release readiness accepted a Release workflow without pre-publish image verification"
fi
assert_file_contains "$output_dir/missing-release-image-check.err" ".github/workflows/release.yml job release is missing required text"
assert_file_contains "$output_dir/missing-release-image-check.err" "--check-image"
git checkout -q -- .github/workflows/release.yml

sed -i.bak '/docker\/login-action@v3/d' .github/workflows/release.yml
rm -f .github/workflows/release.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-release-ghcr-login.out" 2>"$output_dir/missing-release-ghcr-login.err"; then
	fail "release readiness accepted a Release workflow without release-job GHCR login"
fi
assert_file_contains "$output_dir/missing-release-ghcr-login.err" ".github/workflows/release.yml job release is missing required text"
assert_file_contains "$output_dir/missing-release-ghcr-login.err" "uses: docker/login-action@v3"
git checkout -q -- .github/workflows/release.yml

sed -i.bak '/workflow_dispatch:/d' .github/workflows/torture.yml
rm -f .github/workflows/torture.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-torture-dispatch.out" 2>"$output_dir/missing-torture-dispatch.err"; then
	fail "release readiness accepted a torture workflow without manual dispatch"
fi
assert_file_contains "$output_dir/missing-torture-dispatch.err" ".github/workflows/torture.yml is missing required text"
assert_file_contains "$output_dir/missing-torture-dispatch.err" "workflow_dispatch:"
git checkout -q -- .github/workflows/torture.yml

sed -i.bak "s/RUN_LIVE_FAULTS: '0'/RUN_LIVE_FAULTS: '1'/" .github/workflows/torture.yml
rm -f .github/workflows/torture.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-torture-safe-mode.out" 2>"$output_dir/missing-torture-safe-mode.err"; then
	fail "release readiness accepted a torture workflow without the non-destructive live-faults guard"
fi
assert_file_contains "$output_dir/missing-torture-safe-mode.err" ".github/workflows/torture.yml is missing required text"
assert_file_contains "$output_dir/missing-torture-safe-mode.err" "RUN_LIVE_FAULTS: '0'"
git checkout -q -- .github/workflows/torture.yml

sed -i.bak '/^docker-check:/d' Makefile
rm -f Makefile.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-makefile-docker-check.out" 2>"$output_dir/missing-makefile-docker-check.err"; then
	fail "release readiness accepted a Makefile without the Docker check target baseline"
fi
assert_file_contains "$output_dir/missing-makefile-docker-check.err" "Makefile is missing required text"
assert_file_contains "$output_dir/missing-makefile-docker-check.err" "docker-check: docker docker-smoke"
git checkout -q -- Makefile

sed -i.bak '/Sensitive values such as passwords/d' .github/ISSUE_TEMPLATE/bug_report.yml
rm -f .github/ISSUE_TEMPLATE/bug_report.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-bug-report-safety.out" 2>"$output_dir/missing-bug-report-safety.err"; then
	fail "release readiness accepted a bug report template without sensitive-value guidance"
fi
assert_file_contains "$output_dir/missing-bug-report-safety.err" ".github/ISSUE_TEMPLATE/bug_report.yml is missing required text"
assert_file_contains "$output_dir/missing-bug-report-safety.err" "Sensitive values such as passwords"
git checkout -q -- .github/ISSUE_TEMPLATE/bug_report.yml

sed -i.bak '/Security, data, deployment, and compatibility implications/d' .github/ISSUE_TEMPLATE/feature_request.yml
rm -f .github/ISSUE_TEMPLATE/feature_request.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-feature-request-impact.out" 2>"$output_dir/missing-feature-request-impact.err"; then
	fail "release readiness accepted a feature request template without impact guidance"
fi
assert_file_contains "$output_dir/missing-feature-request-impact.err" ".github/ISSUE_TEMPLATE/feature_request.yml is missing required text"
assert_file_contains "$output_dir/missing-feature-request-impact.err" "Security, data, deployment, and compatibility implications"
git checkout -q -- .github/ISSUE_TEMPLATE/feature_request.yml

sed -i.bak '/Remove passwords, tokens, cookies/d' .github/ISSUE_TEMPLATE/question.yml
rm -f .github/ISSUE_TEMPLATE/question.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-question-safety.out" 2>"$output_dir/missing-question-safety.err"; then
	fail "release readiness accepted a question template without sensitive-value guidance"
fi
assert_file_contains "$output_dir/missing-question-safety.err" ".github/ISSUE_TEMPLATE/question.yml is missing required text"
assert_file_contains "$output_dir/missing-question-safety.err" "Remove passwords, tokens, cookies"
git checkout -q -- .github/ISSUE_TEMPLATE/question.yml

sed -i.bak '/Remove passwords, tokens, cookies/d' .github/ISSUE_TEMPLATE/webdav_compatibility.yml
rm -f .github/ISSUE_TEMPLATE/webdav_compatibility.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-webdav-safety.out" 2>"$output_dir/missing-webdav-safety.err"; then
	fail "release readiness accepted a WebDAV template without sensitive-value guidance"
fi
assert_file_contains "$output_dir/missing-webdav-safety.err" ".github/ISSUE_TEMPLATE/webdav_compatibility.yml is missing required text"
assert_file_contains "$output_dir/missing-webdav-safety.err" "Remove passwords, tokens, cookies"
git checkout -q -- .github/ISSUE_TEMPLATE/webdav_compatibility.yml

sed -i.bak '/Data, Security, And Deployment Impact/d' .github/pull_request_template.md
rm -f .github/pull_request_template.md.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-pr-section.out" 2>"$output_dir/missing-pr-section.err"; then
	fail "release readiness accepted a missing PR template section"
fi
assert_file_contains "$output_dir/missing-pr-section.err" ".github/pull_request_template.md is missing required text"
git checkout -q -- .github/pull_request_template.md

sed -i.bak '/verify-release-artifacts/d' docs/release-notes.en.md
rm -f docs/release-notes.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-release-notes.out" 2>"$output_dir/missing-release-notes.err"; then
	fail "release readiness accepted a missing release-notes command"
fi
assert_file_contains "$output_dir/missing-release-notes.err" "docs/release-notes.en.md is missing required text"
git checkout -q -- docs/release-notes.en.md

sed -i.bak '/docs-check/d' CHANGELOG.en.md
rm -f CHANGELOG.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-docs-checklist.out" 2>"$output_dir/missing-docs-checklist.err"; then
	fail "release readiness accepted a missing documentation checklist command"
fi
assert_file_contains "$output_dir/missing-docs-checklist.err" "CHANGELOG.en.md is missing required text"
assert_file_contains "$output_dir/missing-docs-checklist.err" "make docs-check"
git checkout -q -- CHANGELOG.en.md

sed -i.bak '/security-check/d' CHANGELOG.en.md
rm -f CHANGELOG.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-security-checklist.out" 2>"$output_dir/missing-security-checklist.err"; then
	fail "release readiness accepted a missing dependency security checklist command"
fi
assert_file_contains "$output_dir/missing-security-checklist.err" "CHANGELOG.en.md is missing required text"
assert_file_contains "$output_dir/missing-security-checklist.err" "make security-check NPM_AUDIT=1"
git checkout -q -- CHANGELOG.en.md

sed -i.bak '/docker-check/d' CHANGELOG.en.md
rm -f CHANGELOG.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-docker-checklist.out" 2>"$output_dir/missing-docker-checklist.err"; then
	fail "release readiness accepted a missing Docker checklist command"
fi
assert_file_contains "$output_dir/missing-docker-checklist.err" "CHANGELOG.en.md is missing required text"
assert_file_contains "$output_dir/missing-docker-checklist.err" "make docker-check"
git checkout -q -- CHANGELOG.en.md

sed -i.bak '/public-go-live-smoke/d' CHANGELOG.en.md
rm -f CHANGELOG.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-public-smoke-checklist.out" 2>"$output_dir/missing-public-smoke-checklist.err"; then
	fail "release readiness accepted a missing public go-live smoke checklist command"
fi
assert_file_contains "$output_dir/missing-public-smoke-checklist.err" "CHANGELOG.en.md is missing required text"
assert_file_contains "$output_dir/missing-public-smoke-checklist.err" "./scripts/public-go-live-smoke.sh"
git checkout -q -- CHANGELOG.en.md

sed -i.bak '/backup-restore-drill-smoke/d' CHANGELOG.en.md
rm -f CHANGELOG.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-backup-restore-smoke-checklist.out" 2>"$output_dir/missing-backup-restore-smoke-checklist.err"; then
	fail "release readiness accepted a missing backup restore-drill smoke checklist command"
fi
assert_file_contains "$output_dir/missing-backup-restore-smoke-checklist.err" "CHANGELOG.en.md is missing required text"
assert_file_contains "$output_dir/missing-backup-restore-smoke-checklist.err" "./scripts/backup-restore-drill-smoke.sh"
git checkout -q -- CHANGELOG.en.md

sed -i.bak '/security-check/d' docs/release-notes.en.md
rm -f docs/release-notes.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-release-notes-security.out" 2>"$output_dir/missing-release-notes-security.err"; then
	fail "release readiness accepted release notes without the dependency security command"
fi
assert_file_contains "$output_dir/missing-release-notes-security.err" "docs/release-notes.en.md is missing required text"
assert_file_contains "$output_dir/missing-release-notes-security.err" "make security-check NPM_AUDIT=1"
git checkout -q -- docs/release-notes.en.md

sed -i.bak '/docker-check/d' docs/release-notes.en.md
rm -f docs/release-notes.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-release-notes-docker.out" 2>"$output_dir/missing-release-notes-docker.err"; then
	fail "release readiness accepted release notes without the Docker command"
fi
assert_file_contains "$output_dir/missing-release-notes-docker.err" "docs/release-notes.en.md is missing required text"
assert_file_contains "$output_dir/missing-release-notes-docker.err" "make docker-check"
git checkout -q -- docs/release-notes.en.md

sed -i.bak '/mnemonas-doctor --public-domain/d' docs/release-notes.en.md
rm -f docs/release-notes.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-release-notes-public-doctor.out" 2>"$output_dir/missing-release-notes-public-doctor.err"; then
	fail "release readiness accepted release notes without the public-domain doctor command"
fi
assert_file_contains "$output_dir/missing-release-notes-public-doctor.err" "docs/release-notes.en.md is missing required text"
assert_file_contains "$output_dir/missing-release-notes-public-doctor.err" "mnemonas-doctor --public-domain"
git checkout -q -- docs/release-notes.en.md

sed -i.bak '/backup-restore-drill-smoke/d' docs/release-notes.en.md
rm -f docs/release-notes.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-release-notes-backup-restore-smoke.out" 2>"$output_dir/missing-release-notes-backup-restore-smoke.err"; then
	fail "release readiness accepted release notes without the backup restore-drill smoke command"
fi
assert_file_contains "$output_dir/missing-release-notes-backup-restore-smoke.err" "docs/release-notes.en.md is missing required text"
assert_file_contains "$output_dir/missing-release-notes-backup-restore-smoke.err" "./scripts/backup-restore-drill-smoke.sh"
git checkout -q -- docs/release-notes.en.md

sed -i.bak '/cloud-firewall-checklist/d' docs/release-notes.en.md
rm -f docs/release-notes.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-release-notes-cloud-firewall.out" 2>"$output_dir/missing-release-notes-cloud-firewall.err"; then
	fail "release readiness accepted release notes without the cloud firewall checklist"
fi
assert_file_contains "$output_dir/missing-release-notes-cloud-firewall.err" "docs/release-notes.en.md is missing required text"
assert_file_contains "$output_dir/missing-release-notes-cloud-firewall.err" "cloud-firewall-checklist"
git checkout -q -- docs/release-notes.en.md

sed -i.bak 's/, not as the only long-term copy of important data//' docs/release-notes.en.md
rm -f docs/release-notes.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-release-notes-candidate-limit.out" 2>"$output_dir/missing-release-notes-candidate-limit.err"; then
	fail "release readiness accepted release notes without the release candidate data-copy limitation"
fi
assert_file_contains "$output_dir/missing-release-notes-candidate-limit.err" "docs/release-notes.en.md is missing required text"
assert_file_contains "$output_dir/missing-release-notes-candidate-limit.err" "not as the only long-term copy of important data"
git checkout -q -- docs/release-notes.en.md

sed -i.bak '/release-readiness/d' CHANGELOG.en.md
rm -f CHANGELOG.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes >"$output_dir/missing-checklist.out" 2>"$output_dir/missing-checklist.err"; then
	fail "release readiness accepted a missing checklist command"
fi
assert_file_contains "$output_dir/missing-checklist.err" "CHANGELOG.en.md is missing required text"

./scripts/release-readiness.sh --allow-dirty --allow-post-validation-changes --skip-checklist >"$output_dir/skip-checklist.out" 2>"$output_dir/skip-checklist.err"
assert_file_contains "$output_dir/skip-checklist.out" "[release-readiness] status:"
assert_file_contains "$output_dir/skip-checklist.out" "release readiness summary completed"

printf 'test-release-readiness: ok\n'
