#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="${TMPDIR:-/tmp}/mnemonas-release-go-live-check-test-$$"

fail() {
	echo "[release-go-live-check-test] ERROR: $*" >&2
	exit 1
}

assert_file_contains() {
	local file="$1"
	local expected="$2"
	if ! grep -Fq -- "$expected" "$file"; then
		echo "Expected to find: $expected" >&2
		echo "--- $file ---" >&2
		cat "$file" >&2
		fail "missing expected text"
	fi
}

assert_file_not_contains() {
	local file="$1"
	local unexpected="$2"
	if [[ -f "$file" ]] && grep -Fq -- "$unexpected" "$file"; then
		echo "Unexpected text: $unexpected" >&2
		echo "--- $file ---" >&2
		cat "$file" >&2
		fail "found unexpected text"
	fi
}

cleanup() {
	rm -rf -- "$TMP_ROOT"
}

make_fake_helper() {
	local path="$1"
	local label="$2"

	mkdir -p "$(dirname "$path")"
	cat >"$path" <<EOF
#!/usr/bin/env bash
set -euo pipefail

printf '%s %s\n' "$label" "\$*" >> "\$RELEASE_GO_LIVE_LOG"
if [[ "\${RELEASE_GO_LIVE_FAIL_LABEL:-}" == "$label" ]]; then
	exit 9
fi
if [[ "$label" == "backup-smoke" ]]; then
	printf 'backup-api=%s\n' "\${MNEMONAS_API_URL:-}" >> "\$RELEASE_GO_LIVE_LOG"
	printf 'backup-job=%s\n' "\${MNEMONAS_BACKUP_JOB_ID:-}" >> "\$RELEASE_GO_LIVE_LOG"
	printf 'backup-cookie=%s\n' "\${MNEMONAS_COOKIE_FILE:-}" >> "\$RELEASE_GO_LIVE_LOG"
	printf 'backup-keep=%s\n' "\${MNEMONAS_BACKUP_KEEP_ARTIFACT:-}" >> "\$RELEASE_GO_LIVE_LOG"
	printf 'backup-insecure=%s\n' "\${CURL_INSECURE:-}" >> "\$RELEASE_GO_LIVE_LOG"
fi
EOF
	chmod +x "$path"
}

make_fake_helpers() {
	local case_dir="$1"
	local helper_dir="$case_dir/helpers"

	make_fake_helper "$helper_dir/release-readiness" "release-readiness"
	make_fake_helper "$helper_dir/verify-published" "verify-published"
	make_fake_helper "$helper_dir/doctor" "doctor"
	make_fake_helper "$helper_dir/public-smoke" "public-smoke"
	make_fake_helper "$helper_dir/backup-smoke" "backup-smoke"
}

run_full_check_orchestrates_all_steps() {
	local case_dir="$TMP_ROOT/full"
	local helper_dir="$case_dir/helpers"
	local log="$case_dir/invocations.log"
	local out="$case_dir/out.log"
	local cookie_file="$case_dir/cookies.txt"

	mkdir -p "$case_dir"
	make_fake_helpers "$case_dir"
	: >"$cookie_file"

	RELEASE_GO_LIVE_LOG="$log" \
		MNEMONAS_RELEASE_READINESS_BIN="$helper_dir/release-readiness" \
		MNEMONAS_VERIFY_PUBLISHED_RELEASE_BIN="$helper_dir/verify-published" \
		MNEMONAS_DOCTOR_BIN="$helper_dir/doctor" \
		MNEMONAS_PUBLIC_GO_LIVE_SMOKE_BIN="$helper_dir/public-smoke" \
		MNEMONAS_BACKUP_RESTORE_DRILL_SMOKE_BIN="$helper_dir/backup-smoke" \
		bash "$REPO_ROOT/scripts/release-go-live-check.sh" \
			--version v1.2.3 \
			--domain nas.example.com \
			--repository seanbao/mnemonas \
			--artifact-dir "$case_dir/artifacts" \
			--backup-api-url https://nas.example.com/api/v1 \
			--backup-job-id external-disk \
			--cookie-file "$cookie_file" \
			--keep-backup-artifact \
			--curl-insecure >"$out"

	assert_file_contains "$log" "release-readiness "
	assert_file_contains "$log" "verify-published --version v1.2.3 --repository seanbao/mnemonas --artifact-dir $case_dir/artifacts"
	assert_file_contains "$log" "doctor --public-domain nas.example.com"
	assert_file_contains "$log" "public-smoke nas.example.com"
	assert_file_contains "$log" "backup-smoke "
	assert_file_contains "$log" "backup-api=https://nas.example.com/api/v1"
	assert_file_contains "$log" "backup-job=external-disk"
	assert_file_contains "$log" "backup-cookie=$cookie_file"
	assert_file_contains "$log" "backup-keep=1"
	assert_file_contains "$log" "backup-insecure=1"
	assert_file_contains "$out" "release go-live checks passed for v1.2.3 on nas.example.com"
}

run_skip_backup_requires_explicit_flag() {
	local case_dir="$TMP_ROOT/skip-backup"
	local helper_dir="$case_dir/helpers"
	local log="$case_dir/invocations.log"
	local out="$case_dir/out.log"

	mkdir -p "$case_dir"
	make_fake_helpers "$case_dir"

	RELEASE_GO_LIVE_LOG="$log" \
		MNEMONAS_RELEASE_READINESS_BIN="$helper_dir/release-readiness" \
		MNEMONAS_VERIFY_PUBLISHED_RELEASE_BIN="$helper_dir/verify-published" \
		MNEMONAS_DOCTOR_BIN="$helper_dir/doctor" \
		MNEMONAS_PUBLIC_GO_LIVE_SMOKE_BIN="$helper_dir/public-smoke" \
		MNEMONAS_BACKUP_RESTORE_DRILL_SMOKE_BIN="$helper_dir/backup-smoke" \
		bash "$REPO_ROOT/scripts/release-go-live-check.sh" \
			--version v1.2.3 \
			--domain nas.example.com \
			--skip-backup-restore-drill >"$out"

	assert_file_contains "$log" "release-readiness "
	assert_file_contains "$log" "verify-published --version v1.2.3 --repository seanbao/mnemonas"
	assert_file_contains "$log" "doctor --public-domain nas.example.com"
	assert_file_contains "$log" "public-smoke nas.example.com"
	assert_file_not_contains "$log" "backup-smoke"
	assert_file_contains "$out" "skipped backup restore-drill smoke by request"
}

run_missing_backup_args_fails_before_helpers() {
	local case_dir="$TMP_ROOT/missing-backup"
	local helper_dir="$case_dir/helpers"
	local log="$case_dir/invocations.log"
	local err="$case_dir/err.log"
	local status

	mkdir -p "$case_dir"
	make_fake_helpers "$case_dir"

	set +e
	RELEASE_GO_LIVE_LOG="$log" \
		MNEMONAS_RELEASE_READINESS_BIN="$helper_dir/release-readiness" \
		MNEMONAS_VERIFY_PUBLISHED_RELEASE_BIN="$helper_dir/verify-published" \
		MNEMONAS_DOCTOR_BIN="$helper_dir/doctor" \
		MNEMONAS_PUBLIC_GO_LIVE_SMOKE_BIN="$helper_dir/public-smoke" \
		MNEMONAS_BACKUP_RESTORE_DRILL_SMOKE_BIN="$helper_dir/backup-smoke" \
		bash "$REPO_ROOT/scripts/release-go-live-check.sh" \
			--version v1.2.3 \
			--domain nas.example.com >"$case_dir/out.log" 2>"$err"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release go-live check accepted missing backup smoke args"
	assert_file_contains "$err" "--backup-api-url and --backup-job-id are required"
	[[ ! -f "$log" ]] || fail "helpers ran before backup arguments were validated"
}

run_helper_failure_stops_later_steps() {
	local case_dir="$TMP_ROOT/helper-failure"
	local helper_dir="$case_dir/helpers"
	local log="$case_dir/invocations.log"
	local status

	mkdir -p "$case_dir"
	make_fake_helpers "$case_dir"

	set +e
	RELEASE_GO_LIVE_LOG="$log" \
		RELEASE_GO_LIVE_FAIL_LABEL="doctor" \
		MNEMONAS_RELEASE_READINESS_BIN="$helper_dir/release-readiness" \
		MNEMONAS_VERIFY_PUBLISHED_RELEASE_BIN="$helper_dir/verify-published" \
		MNEMONAS_DOCTOR_BIN="$helper_dir/doctor" \
		MNEMONAS_PUBLIC_GO_LIVE_SMOKE_BIN="$helper_dir/public-smoke" \
		MNEMONAS_BACKUP_RESTORE_DRILL_SMOKE_BIN="$helper_dir/backup-smoke" \
		bash "$REPO_ROOT/scripts/release-go-live-check.sh" \
			--version v1.2.3 \
			--domain nas.example.com \
			--backup-api-url https://nas.example.com/api/v1 \
			--backup-job-id external-disk >"$case_dir/out.log" 2>"$case_dir/err.log"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "release go-live check ignored helper failure"
	assert_file_contains "$log" "release-readiness "
	assert_file_contains "$log" "verify-published --version v1.2.3 --repository seanbao/mnemonas"
	assert_file_contains "$log" "doctor --public-domain nas.example.com"
	assert_file_not_contains "$log" "public-smoke"
	assert_file_not_contains "$log" "backup-smoke"
}

trap cleanup EXIT
rm -rf -- "$TMP_ROOT"
mkdir -p "$TMP_ROOT"

run_full_check_orchestrates_all_steps
run_skip_backup_requires_explicit_flag
run_missing_backup_args_fails_before_helpers
run_helper_failure_stops_later_steps

printf '[release-go-live-check-test] all checks passed\n'
