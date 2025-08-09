#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
READINESS="$REPO_ROOT/scripts/release-readiness.sh"
PLANNER="$REPO_ROOT/scripts/plan-hardening-commits.sh"

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
	mkdir -p docs
	cat >docs/release-notes.md <<'EOF'
# 发布说明草稿

## 发布前验证

- `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- `make docs-check`
- `make scripts-check`
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

- `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- `make docs-check`
- `make scripts-check`
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
		.github/pull_request_template.md
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
chmod +x "$tmp/scripts/release-readiness.sh" "$tmp/scripts/plan-hardening-commits.sh"

cd "$tmp"
git init -q -b master
git config user.email "mnemonas@example.invalid"
git config user.name "MnemoNAS Test"

write_checklists
write_release_notes
write_community_files
git add .
git commit -q -m "docs: initial checklist"
validation_target="$(git rev-parse --short=12 HEAD)"
write_validation_docs "$validation_target"
git add docs
git commit -q -m "docs: record validation evidence"

./scripts/release-readiness.sh --base "$validation_target" >"$output_dir/evidence-only.out" 2>"$output_dir/evidence-only.err"
assert_file_contains "$output_dir/evidence-only.out" "[release-readiness] validation:"
assert_file_contains "$output_dir/evidence-only.out" "only validation evidence docs changed since target"
assert_file_contains "$output_dir/evidence-only.out" "[release-readiness] validation-diff:"
assert_file_contains "$output_dir/evidence-only.out" "release readiness summary completed"

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
assert_file_contains "$output_dir/pass.out" "[release-readiness] community:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] validation:"
assert_file_contains "$output_dir/pass.out" "full gate evidence at"
assert_file_contains "$output_dir/pass.out" "files changed since target"
assert_file_contains "$output_dir/pass.out" "[release-readiness] validation-diff:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] release-notes:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] checklist:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] status:"
assert_file_contains "$output_dir/pass.out" "release readiness summary completed"

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

touch .github/pull_request_template.md
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
