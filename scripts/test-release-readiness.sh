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

mkdir -p "$tmp/scripts"
cp "$READINESS" "$tmp/scripts/release-readiness.sh"
cp "$PLANNER" "$tmp/scripts/plan-hardening-commits.sh"
chmod +x "$tmp/scripts/release-readiness.sh" "$tmp/scripts/plan-hardening-commits.sh"

cd "$tmp"
git init -q -b master
git config user.email "mnemonas@example.invalid"
git config user.name "MnemoNAS Test"

write_checklists
git add .
git commit -q -m "docs: initial checklist"

git checkout -q -b release-readiness
printf '# docs\n' >README.md
git add README.md
git commit -q -m "docs: update release docs"

./scripts/release-readiness.sh >"$output_dir/pass.out" 2>"$output_dir/pass.err"
assert_file_contains "$output_dir/pass.out" "[release-readiness] branch:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] base:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] commits:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] diff:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] worktree:          clean"
assert_file_contains "$output_dir/pass.out" "[release-readiness] planner          [hardening-commit-plan] no changed files detected"
assert_file_contains "$output_dir/pass.out" "[release-readiness] checklist:"
assert_file_contains "$output_dir/pass.out" "[release-readiness] status:"
assert_file_contains "$output_dir/pass.out" "release readiness summary completed"

printf '# dirty docs\n' >>README.md
if ./scripts/release-readiness.sh >"$output_dir/dirty.out" 2>"$output_dir/dirty.err"; then
	fail "release readiness accepted a dirty worktree by default"
fi
assert_file_contains "$output_dir/dirty.err" "worktree has uncommitted changes"

./scripts/release-readiness.sh --allow-dirty >"$output_dir/allow-dirty.out" 2>"$output_dir/allow-dirty.err"
assert_file_contains "$output_dir/allow-dirty.out" "[release-readiness] worktree:          dirty (draft summary)"

git checkout -q -- README.md
sed -i.bak '/release-readiness/d' CHANGELOG.en.md
rm -f CHANGELOG.en.md.bak
if ./scripts/release-readiness.sh --allow-dirty >"$output_dir/missing-checklist.out" 2>"$output_dir/missing-checklist.err"; then
	fail "release readiness accepted a missing checklist command"
fi
assert_file_contains "$output_dir/missing-checklist.err" "CHANGELOG.en.md is missing required text"

./scripts/release-readiness.sh --allow-dirty --skip-checklist >"$output_dir/skip-checklist.out" 2>"$output_dir/skip-checklist.err"
assert_file_contains "$output_dir/skip-checklist.out" "[release-readiness] status:"
assert_file_contains "$output_dir/skip-checklist.out" "release readiness summary completed"

printf 'test-release-readiness: ok\n'
