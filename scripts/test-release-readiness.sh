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
- [ ] 脚本检查通过：`make scripts-check`
- [ ] 发布前就绪摘要通过：`./scripts/release-readiness.sh`
- [ ] `./scripts/plan-hardening-commits.sh --fail-on-manual` 确认没有未归类路径
- [ ] 发布后下载 GitHub Release 产物，并运行 `./scripts/verify-release-artifacts.sh --version <tag> --repository seanbao/mnemonas --require-targets --check-image <artifact-dir>`，验证 release 产物。
EOF

	cat >CHANGELOG.en.md <<'EOF'
# CHANGELOG

- [ ] Run full change-aware validation: `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- [ ] Run script checks: `make scripts-check`
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
- `./scripts/test-release-tag.sh`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`

## 发布后核验

```bash
gh release download v0.1.0 --repo seanbao/mnemonas --dir dist/release-check
./scripts/verify-release-artifacts.sh --version v0.1.0 --repository seanbao/mnemonas --require-targets --check-image dist/release-check
```
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
- `./scripts/test-release-tag.sh`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`

## Post-Publish Verification

```bash
gh release download v0.1.0 --repo seanbao/mnemonas --dir dist/release-check
./scripts/verify-release-artifacts.sh --version v0.1.0 --repository seanbao/mnemonas --require-targets --check-image dist/release-check
```
EOF
}

write_community_files() {
	mkdir -p .github/ISSUE_TEMPLATE
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
		SECURITY.zh-CN.md \
		.github/ISSUE_TEMPLATE/config.yml \
		.github/ISSUE_TEMPLATE/bug_report.yml \
		.github/ISSUE_TEMPLATE/feature_request.yml \
		.github/ISSUE_TEMPLATE/question.yml \
		.github/ISSUE_TEMPLATE/webdav_compatibility.yml
	cat >.github/ISSUE_TEMPLATE/config.yml <<'EOF'
blank_issues_enabled: true
contact_links:
  - name: Security vulnerability / 安全漏洞
    url: https://github.com/seanbao/mnemonas/security/policy
    about: Report security vulnerabilities through the private reporting channel. 安全漏洞通过私密报告渠道提交。
  - name: Support boundary / 支持边界
    url: https://github.com/seanbao/mnemonas/blob/master/SUPPORT.md
    about: Review support channels and required diagnostics before filing operational questions. 提交运维问题前先查看支持渠道和诊断要求。
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
EOF
	cat >SUPPORT.en.md <<'EOF'
# Support

WebDAV compatibility report form: https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml
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

sed -i.bak '\#security/policy#d' .github/ISSUE_TEMPLATE/config.yml
rm -f .github/ISSUE_TEMPLATE/config.yml.bak
if ./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/missing-issue-config-link.out" 2>"$output_dir/missing-issue-config-link.err"; then
	fail "release readiness accepted a missing Issue config contact link"
fi
assert_file_contains "$output_dir/missing-issue-config-link.err" ".github/ISSUE_TEMPLATE/config.yml is missing required text"
git checkout -q -- .github/ISSUE_TEMPLATE/config.yml

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
