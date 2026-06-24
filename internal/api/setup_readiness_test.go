package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/backup"
	"github.com/seanbao/mnemonas/internal/config"
)

func TestCountSetupChecksCountsCompletedStatusesOnce(t *testing.T) {
	checks := []setupReadinessCheck{
		{Requirement: setupRequirementRequired, Status: setupCheckComplete},
		{Requirement: setupRequirementRequired, Status: setupCheckNotApplicable},
		{Requirement: setupRequirementRequired, Status: setupCheckIncomplete},
		{Requirement: setupRequirementRecommended, Status: setupCheckComplete},
	}

	got := countSetupChecks(checks, setupRequirementRequired)
	want := setupReadinessCount{Completed: 2, Total: 3}
	if got != want {
		t.Fatalf("countSetupChecks() = %+v, want %+v", got, want)
	}
}

func TestEvaluateSetupReadinessLifecycleAndCounts(t *testing.T) {
	now := time.Date(2026, 7, 13, 9, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	nowUTC := now.UTC()
	deferredBeforeBoundary := nowUTC.Add(time.Nanosecond)
	deferredAtBoundary := nowUTC
	completedAt := nowUTC.Add(-time.Hour)

	tests := []struct {
		name                  string
		lifecycle             config.SetupLifecycleState
		mutate                func(*setupReadinessInputs)
		wantLifecycle         string
		wantPrompt            bool
		wantCanComplete       bool
		wantCanDefer          bool
		wantRequiredCompleted int
	}{
		{
			name:                  "pending",
			wantLifecycle:         setupLifecyclePending,
			wantPrompt:            true,
			wantCanComplete:       true,
			wantRequiredCompleted: 6,
		},
		{
			name:      "deferred before expiry boundary",
			lifecycle: config.SetupLifecycleState{DeferredUntil: &deferredBeforeBoundary},
			mutate: func(inputs *setupReadinessInputs) {
				inputs.Backup.HasCurrentHealthyBackup = false
			},
			wantLifecycle:         setupLifecycleDeferred,
			wantCanDefer:          true,
			wantRequiredCompleted: 5,
		},
		{
			name:      "deferred expires at boundary",
			lifecycle: config.SetupLifecycleState{DeferredUntil: &deferredAtBoundary},
			mutate: func(inputs *setupReadinessInputs) {
				inputs.Backup.HasCurrentHealthyBackup = false
			},
			wantLifecycle:         setupLifecyclePending,
			wantPrompt:            true,
			wantCanDefer:          true,
			wantRequiredCompleted: 5,
		},
		{
			name:                  "completed takes precedence",
			lifecycle:             config.SetupLifecycleState{CompletedAt: &completedAt, DeferredUntil: &deferredBeforeBoundary},
			wantLifecycle:         setupLifecycleCompleted,
			wantRequiredCompleted: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := readySetupReadinessInputs(nowUTC)
			inputs.Lifecycle = tt.lifecycle
			if tt.mutate != nil {
				tt.mutate(&inputs)
			}

			got := evaluateSetupReadiness(inputs, now)
			if got.Lifecycle != tt.wantLifecycle || got.Prompt != tt.wantPrompt {
				t.Fatalf("lifecycle/prompt = %q/%v, want %q/%v", got.Lifecycle, got.Prompt, tt.wantLifecycle, tt.wantPrompt)
			}
			if got.CanComplete != tt.wantCanComplete || got.CanDefer != tt.wantCanDefer {
				t.Fatalf("can_complete/can_defer = %v/%v, want %v/%v", got.CanComplete, got.CanDefer, tt.wantCanComplete, tt.wantCanDefer)
			}
			if got.Required != (setupReadinessCount{Completed: tt.wantRequiredCompleted, Total: 6}) {
				t.Fatalf("required count = %+v, want %d/6", got.Required, tt.wantRequiredCompleted)
			}
			if got.Recommended != (setupReadinessCount{Completed: 4, Total: 4}) {
				t.Fatalf("recommended count = %+v, want 4/4", got.Recommended)
			}
			for _, check := range got.Checks {
				wantDeferrable := check.ID == "backup_job" || check.ID == "backup_success"
				if check.Deferrable != wantDeferrable {
					t.Fatalf("check %q deferrable = %v, want %v", check.ID, check.Deferrable, wantDeferrable)
				}
			}
			if !got.GeneratedAt.Equal(nowUTC) || got.GeneratedAt.Location() != time.UTC {
				t.Fatalf("generated_at = %v, want UTC %v", got.GeneratedAt, nowUTC)
			}
		})
	}
}

func TestEvaluateSetupReadinessCompletingBackupEndsActiveDeferral(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	deferredUntil := now.Add(7 * 24 * time.Hour)
	inputs := readySetupReadinessInputs(now)
	inputs.Lifecycle.DeferredUntil = &deferredUntil

	got := evaluateSetupReadiness(inputs, now)
	if got.Lifecycle != setupLifecyclePending || !got.Prompt {
		t.Fatalf("lifecycle/prompt = %q/%v, want pending/true", got.Lifecycle, got.Prompt)
	}
	if !got.CanComplete || got.CanDefer || got.OverallStatus != setupOverallReady {
		t.Fatalf("overall/complete/defer = %q/%v/%v, want ready/true/false", got.OverallStatus, got.CanComplete, got.CanDefer)
	}
}

func TestEvaluateSetupReadinessActiveDeferralDoesNotHideNewRequiredIssue(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	deferredUntil := now.Add(7 * 24 * time.Hour)
	inputs := readySetupReadinessInputs(now)
	inputs.Lifecycle.DeferredUntil = &deferredUntil
	inputs.Users = []*auth.User{{ID: "admin", Role: auth.RoleAdmin, MustChangePassword: true}}

	got := evaluateSetupReadiness(inputs, now)
	if got.Lifecycle != setupLifecyclePending || !got.Prompt {
		t.Fatalf("lifecycle/prompt = %q/%v, want pending/true", got.Lifecycle, got.Prompt)
	}
	if got.CanComplete || got.CanDefer {
		t.Fatalf("can_complete/can_defer = %v/%v, want false/false", got.CanComplete, got.CanDefer)
	}
	if got.DeferredUntil == nil || !got.DeferredUntil.Equal(deferredUntil) {
		t.Fatalf("deferred_until = %v, want persisted deadline %v", got.DeferredUntil, deferredUntil)
	}
}

func TestEvaluateSetupReadinessAdminState(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name                  string
		mutate                func(*setupReadinessInputs)
		wantAdmin             string
		wantCredential        string
		wantActiveAdmins      int
		wantMustChangeAdmins  int
		wantOverall           string
		wantAdminRedundancy   string
		wantRequiredCompleted int
	}{
		{
			name: "authentication disabled",
			mutate: func(inputs *setupReadinessInputs) {
				inputs.AuthEnabled = false
			},
			wantAdmin:             setupCheckIncomplete,
			wantCredential:        setupCheckIncomplete,
			wantActiveAdmins:      2,
			wantOverall:           setupOverallActionRequired,
			wantAdminRedundancy:   setupCheckComplete,
			wantRequiredCompleted: 4,
		},
		{
			name: "user store unavailable",
			mutate: func(inputs *setupReadinessInputs) {
				inputs.UsersAvailable = false
				inputs.Users = nil
			},
			wantAdmin:             setupCheckUnavailable,
			wantCredential:        setupCheckUnavailable,
			wantOverall:           setupOverallUnavailable,
			wantAdminRedundancy:   setupCheckUnavailable,
			wantRequiredCompleted: 4,
		},
		{
			name: "no active administrator",
			mutate: func(inputs *setupReadinessInputs) {
				inputs.Users = []*auth.User{
					{ID: "disabled-admin", Role: auth.RoleAdmin, Disabled: true},
					{ID: "regular-user", Role: auth.RoleUser},
					nil,
				}
			},
			wantAdmin:             setupCheckIncomplete,
			wantCredential:        setupCheckIncomplete,
			wantOverall:           setupOverallActionRequired,
			wantAdminRedundancy:   setupCheckIncomplete,
			wantRequiredCompleted: 4,
		},
		{
			name: "bootstrap administrator must change password",
			mutate: func(inputs *setupReadinessInputs) {
				inputs.Users = []*auth.User{
					{ID: "bootstrap-admin", Role: auth.RoleAdmin, MustChangePassword: true},
					{ID: "disabled-admin", Role: auth.RoleAdmin, Disabled: true, MustChangePassword: true},
					{ID: "regular-user", Role: auth.RoleUser, MustChangePassword: true},
				}
			},
			wantAdmin:             setupCheckComplete,
			wantCredential:        setupCheckIncomplete,
			wantActiveAdmins:      1,
			wantMustChangeAdmins:  1,
			wantOverall:           setupOverallActionRequired,
			wantAdminRedundancy:   setupCheckIncomplete,
			wantRequiredCompleted: 5,
		},
		{
			name: "administrator password changed",
			mutate: func(inputs *setupReadinessInputs) {
				inputs.Users = []*auth.User{{ID: "admin", Role: auth.RoleAdmin}}
			},
			wantAdmin:             setupCheckComplete,
			wantCredential:        setupCheckComplete,
			wantActiveAdmins:      1,
			wantOverall:           setupOverallReady,
			wantAdminRedundancy:   setupCheckIncomplete,
			wantRequiredCompleted: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := readySetupReadinessInputs(now)
			tt.mutate(&inputs)

			got := evaluateSetupReadiness(inputs, now)
			assertSetupCheckStatus(t, got, "admin_access", tt.wantAdmin)
			assertSetupCheckStatus(t, got, "bootstrap_credential", tt.wantCredential)
			assertSetupCheckStatus(t, got, "admin_redundancy", tt.wantAdminRedundancy)
			if got.Summary.ActiveAdminCount != tt.wantActiveAdmins || got.Summary.PasswordChangeRequiredAdminCount != tt.wantMustChangeAdmins {
				t.Fatalf("admin summary = %d active/%d must-change, want %d/%d", got.Summary.ActiveAdminCount, got.Summary.PasswordChangeRequiredAdminCount, tt.wantActiveAdmins, tt.wantMustChangeAdmins)
			}
			if got.OverallStatus != tt.wantOverall {
				t.Fatalf("overall_status = %q, want %q", got.OverallStatus, tt.wantOverall)
			}
			if got.Required.Completed != tt.wantRequiredCompleted || got.Required.Total != 6 {
				t.Fatalf("required count = %+v, want %d/6", got.Required, tt.wantRequiredCompleted)
			}
		})
	}
}

func TestEvaluateSetupReadinessInitialPasswordFile(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name            string
		status          securityCheckStatus
		wantCheck       string
		wantSummary     string
		wantOverall     string
		wantCanComplete bool
	}{
		{
			name:            "pass means file missing",
			status:          securityCheckPass,
			wantCheck:       setupCheckComplete,
			wantSummary:     "missing",
			wantOverall:     setupOverallReady,
			wantCanComplete: true,
		},
		{
			name:        "block means file present",
			status:      securityCheckBlock,
			wantCheck:   setupCheckIncomplete,
			wantSummary: "present",
			wantOverall: setupOverallActionRequired,
		},
		{
			name:        "warning is unavailable evidence",
			status:      securityCheckWarning,
			wantCheck:   setupCheckUnavailable,
			wantSummary: "unavailable",
			wantOverall: setupOverallUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := readySetupReadinessInputs(now)
			inputs.Security.Status = tt.status
			inputs.Security.Checks = []securityCheckItem{{ID: "initial_password_file", Status: tt.status}}

			got := evaluateSetupReadiness(inputs, now)
			assertSetupCheckStatus(t, got, "initial_password_file", tt.wantCheck)
			if got.Summary.InitialPasswordFile != tt.wantSummary {
				t.Fatalf("initial_password_file summary = %q, want %q", got.Summary.InitialPasswordFile, tt.wantSummary)
			}
			if got.OverallStatus != tt.wantOverall || got.CanComplete != tt.wantCanComplete {
				t.Fatalf("overall/can_complete = %q/%v, want %q/%v", got.OverallStatus, got.CanComplete, tt.wantOverall, tt.wantCanComplete)
			}
		})
	}
}

func TestEvaluateSetupReadinessSecurityBlocksAndWarnings(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name               string
		status             securityCheckStatus
		checks             []securityCheckItem
		wantBlockingIDs    []string
		wantInitial        string
		wantBaseline       string
		wantRecommendation string
	}{
		{
			name:   "deduplicates dedicated blocks",
			status: securityCheckBlock,
			checks: []securityCheckItem{
				{ID: "initial_password_file", Status: securityCheckBlock},
				{ID: "initial_password_file", Status: securityCheckBlock},
				{ID: "admin_accounts", Status: securityCheckBlock},
				{ID: "admin_accounts", Status: securityCheckBlock},
			},
			wantBlockingIDs:    []string{"admin_accounts", "initial_password_file"},
			wantInitial:        setupCheckIncomplete,
			wantBaseline:       setupCheckComplete,
			wantRecommendation: setupCheckIncomplete,
		},
		{
			name:   "unrelated block fails baseline",
			status: securityCheckBlock,
			checks: []securityCheckItem{
				{ID: "initial_password_file", Status: securityCheckPass},
				{ID: "firewall", Status: securityCheckBlock},
			},
			wantBlockingIDs:    []string{"firewall"},
			wantInitial:        setupCheckComplete,
			wantBaseline:       setupCheckIncomplete,
			wantRecommendation: setupCheckIncomplete,
		},
		{
			name:   "warning affects recommendations only",
			status: securityCheckWarning,
			checks: []securityCheckItem{
				{ID: "initial_password_file", Status: securityCheckPass},
				{ID: "tls", Status: securityCheckWarning},
			},
			wantBlockingIDs:    []string{},
			wantInitial:        setupCheckComplete,
			wantBaseline:       setupCheckComplete,
			wantRecommendation: setupCheckIncomplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := readySetupReadinessInputs(now)
			inputs.Security.Status = tt.status
			inputs.Security.Checks = tt.checks

			got := evaluateSetupReadiness(inputs, now)
			assertSetupCheckStatus(t, got, "initial_password_file", tt.wantInitial)
			assertSetupCheckStatus(t, got, "security_baseline", tt.wantBaseline)
			assertSetupCheckStatus(t, got, "security_recommendations", tt.wantRecommendation)
			if !reflect.DeepEqual(got.Summary.SecurityBlockingCheckIDs, tt.wantBlockingIDs) {
				t.Fatalf("security blocking IDs = %#v, want %#v", got.Summary.SecurityBlockingCheckIDs, tt.wantBlockingIDs)
			}
		})
	}
}

func TestEvaluateSetupReadinessBackupState(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	backupAt := now.Add(-time.Hour)
	restoreAt := now.Add(-30 * time.Minute)
	tests := []struct {
		name                    string
		snapshot                backup.ReadinessSnapshot
		wantJob                 string
		wantSuccess             string
		wantSchedule            string
		wantRestore             string
		wantOverall             string
		wantRequiredCompleted   int
		wantRecommendedComplete int
		wantCanComplete         bool
		wantCanDefer            bool
	}{
		{
			name:                    "manager unavailable",
			wantJob:                 setupCheckUnavailable,
			wantSuccess:             setupCheckUnavailable,
			wantSchedule:            setupCheckUnavailable,
			wantRestore:             setupCheckUnavailable,
			wantOverall:             setupOverallUnavailable,
			wantRequiredCompleted:   4,
			wantRecommendedComplete: 2,
		},
		{
			name:                    "no enabled jobs",
			snapshot:                backup.ReadinessSnapshot{Available: true},
			wantJob:                 setupCheckIncomplete,
			wantSuccess:             setupCheckIncomplete,
			wantSchedule:            setupCheckIncomplete,
			wantRestore:             setupCheckIncomplete,
			wantOverall:             setupOverallActionRequired,
			wantRequiredCompleted:   4,
			wantRecommendedComplete: 2,
			wantCanDefer:            true,
		},
		{
			name:                    "enabled job without success",
			snapshot:                backup.ReadinessSnapshot{Available: true, EnabledJobCount: 1},
			wantJob:                 setupCheckComplete,
			wantSuccess:             setupCheckIncomplete,
			wantSchedule:            setupCheckIncomplete,
			wantRestore:             setupCheckIncomplete,
			wantOverall:             setupOverallActionRequired,
			wantRequiredCompleted:   5,
			wantRecommendedComplete: 2,
			wantCanDefer:            true,
		},
		{
			name: "current successful manual backup",
			snapshot: backup.ReadinessSnapshot{
				Available:               true,
				EnabledJobCount:         1,
				LastSuccessfulBackupAt:  &backupAt,
				HasCurrentHealthyBackup: true,
			},
			wantJob:                 setupCheckComplete,
			wantSuccess:             setupCheckComplete,
			wantSchedule:            setupCheckIncomplete,
			wantRestore:             setupCheckIncomplete,
			wantOverall:             setupOverallReady,
			wantRequiredCompleted:   6,
			wantRecommendedComplete: 2,
			wantCanComplete:         true,
		},
		{
			name: "scheduled successful backup",
			snapshot: backup.ReadinessSnapshot{
				Available:                true,
				EnabledJobCount:          1,
				EnabledScheduledJobCount: 1,
				LastSuccessfulBackupAt:   &backupAt,
				HasCurrentHealthyBackup:  true,
			},
			wantJob:                 setupCheckComplete,
			wantSuccess:             setupCheckComplete,
			wantSchedule:            setupCheckComplete,
			wantRestore:             setupCheckIncomplete,
			wantOverall:             setupOverallReady,
			wantRequiredCompleted:   6,
			wantRecommendedComplete: 3,
			wantCanComplete:         true,
		},
		{
			name: "scheduled backup with restore evidence",
			snapshot: backup.ReadinessSnapshot{
				Available:                  true,
				EnabledJobCount:            1,
				EnabledScheduledJobCount:   1,
				LastSuccessfulBackupAt:     &backupAt,
				HasCurrentHealthyBackup:    true,
				LastValidRestoreEvidenceAt: &restoreAt,
				HasCurrentRestoreEvidence:  true,
			},
			wantJob:                 setupCheckComplete,
			wantSuccess:             setupCheckComplete,
			wantSchedule:            setupCheckComplete,
			wantRestore:             setupCheckComplete,
			wantOverall:             setupOverallReady,
			wantRequiredCompleted:   6,
			wantRecommendedComplete: 4,
			wantCanComplete:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := readySetupReadinessInputs(now)
			inputs.Backup = tt.snapshot

			got := evaluateSetupReadiness(inputs, now)
			assertSetupCheckStatus(t, got, "backup_job", tt.wantJob)
			assertSetupCheckStatus(t, got, "backup_success", tt.wantSuccess)
			assertSetupCheckStatus(t, got, "backup_schedule", tt.wantSchedule)
			assertSetupCheckStatus(t, got, "restore_verification", tt.wantRestore)
			if got.OverallStatus != tt.wantOverall || got.CanComplete != tt.wantCanComplete || got.CanDefer != tt.wantCanDefer {
				t.Fatalf("overall/complete/defer = %q/%v/%v, want %q/%v/%v", got.OverallStatus, got.CanComplete, got.CanDefer, tt.wantOverall, tt.wantCanComplete, tt.wantCanDefer)
			}
			if got.Required != (setupReadinessCount{Completed: tt.wantRequiredCompleted, Total: 6}) {
				t.Fatalf("required count = %+v, want %d/6", got.Required, tt.wantRequiredCompleted)
			}
			if got.Recommended != (setupReadinessCount{Completed: tt.wantRecommendedComplete, Total: 4}) {
				t.Fatalf("recommended count = %+v, want %d/4", got.Recommended, tt.wantRecommendedComplete)
			}
			if got.Summary.EnabledBackupJobCount != tt.snapshot.EnabledJobCount {
				t.Fatalf("enabled backup jobs = %d, want %d", got.Summary.EnabledBackupJobCount, tt.snapshot.EnabledJobCount)
			}
			assertOptionalSetupTime(t, got.Summary.LatestBackupSuccessAt, tt.snapshot.LastSuccessfulBackupAt)
			assertOptionalSetupTime(t, got.Summary.LatestRestoreVerificationAt, tt.snapshot.LastValidRestoreEvidenceAt)
		})
	}
}

func TestEvaluateSetupReadinessCompletionAndDeferralGates(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name            string
		mutate          func(*setupReadinessInputs)
		wantCanComplete bool
		wantCanDefer    bool
	}{
		{
			name:            "all required checks complete",
			mutate:          func(*setupReadinessInputs) {},
			wantCanComplete: true,
		},
		{
			name: "only deferrable required checks incomplete",
			mutate: func(inputs *setupReadinessInputs) {
				inputs.Backup = backup.ReadinessSnapshot{Available: true}
			},
			wantCanDefer: true,
		},
		{
			name: "non-deferrable check incomplete",
			mutate: func(inputs *setupReadinessInputs) {
				inputs.Security.Status = securityCheckBlock
				inputs.Security.Checks = []securityCheckItem{{ID: "initial_password_file", Status: securityCheckBlock}}
				inputs.Backup = backup.ReadinessSnapshot{Available: true}
			},
		},
		{
			name: "deferrable check unavailable",
			mutate: func(inputs *setupReadinessInputs) {
				inputs.Backup = backup.ReadinessSnapshot{}
			},
		},
		{
			name: "completed lifecycle closes both actions",
			mutate: func(inputs *setupReadinessInputs) {
				completedAt := now.Add(-time.Minute)
				inputs.Lifecycle.CompletedAt = &completedAt
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := readySetupReadinessInputs(now)
			tt.mutate(&inputs)
			got := evaluateSetupReadiness(inputs, now)
			if got.CanComplete != tt.wantCanComplete || got.CanDefer != tt.wantCanDefer {
				t.Fatalf("can_complete/can_defer = %v/%v, want %v/%v", got.CanComplete, got.CanDefer, tt.wantCanComplete, tt.wantCanDefer)
			}
		})
	}
}

func TestEvaluateSetupReadinessResponseOmitsPathsAndUserIDs(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	inputs := readySetupReadinessInputs(now)
	inputs.Users[0].ID = "sensitive-admin-id"
	inputs.Users[0].PasswordHash = "sensitive-password-hash"
	inputs.Security.Request = map[string]interface{}{"path": "/srv/private/request"}
	inputs.Security.Config = map[string]interface{}{"users_file": "/srv/private/users.json"}
	inputs.Security.Checks[0].Details = map[string]interface{}{"path": "/srv/private/initial-password.txt"}

	payload, err := json.Marshal(evaluateSetupReadiness(inputs, now))
	if err != nil {
		t.Fatalf("Marshal(readiness) error: %v", err)
	}
	body := string(payload)
	for _, secret := range []string{
		"sensitive-admin-id",
		"sensitive-password-hash",
		"/srv/private/request",
		"/srv/private/users.json",
		"/srv/private/initial-password.txt",
	} {
		if strings.Contains(body, secret) {
			t.Fatalf("readiness response leaked %q: %s", secret, body)
		}
	}
}

func readySetupReadinessInputs(now time.Time) setupReadinessInputs {
	backupAt := now.Add(-time.Hour)
	restoreAt := now.Add(-30 * time.Minute)
	return setupReadinessInputs{
		LifecycleAvailable: true,
		AuthEnabled:        true,
		UsersAvailable:     true,
		Users: []*auth.User{
			{ID: "admin-primary", Role: auth.RoleAdmin},
			{ID: "admin-secondary", Role: auth.RoleAdmin},
		},
		Backup: backup.ReadinessSnapshot{
			Available:                  true,
			EnabledJobCount:            1,
			EnabledScheduledJobCount:   1,
			LastSuccessfulBackupAt:     &backupAt,
			HasCurrentHealthyBackup:    true,
			LastValidRestoreEvidenceAt: &restoreAt,
			HasCurrentRestoreEvidence:  true,
		},
		SecurityAvailable: true,
		Security: securityCheckResponse{
			Status: securityCheckPass,
			Checks: []securityCheckItem{{ID: "initial_password_file", Status: securityCheckPass}},
		},
	}
}

func setupReadinessCheckByID(t *testing.T, data setupReadinessData, id string) setupReadinessCheck {
	t.Helper()
	for _, check := range data.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("setup readiness check %q not found in %#v", id, data.Checks)
	return setupReadinessCheck{}
}

func assertSetupCheckStatus(t *testing.T, data setupReadinessData, id, want string) {
	t.Helper()
	if got := setupReadinessCheckByID(t, data, id).Status; got != want {
		t.Fatalf("check %q status = %q, want %q", id, got, want)
	}
}

func assertOptionalSetupTime(t *testing.T, got, want *time.Time) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("time = %v, want %v", got, want)
		}
		return
	}
	if !got.Equal(*want) {
		t.Fatalf("time = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

func TestSetupReadinessGetRequiresAdministrator(t *testing.T) {
	fixture := newSetupReadinessHTTPFixture(t, setupReadinessHTTPOptions{backupAvailable: true})

	unauthenticated := fixture.request(t, http.MethodGet, "/api/v1/setup/readiness", "", "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET status = %d, want %d: %s", unauthenticated.Code, http.StatusUnauthorized, unauthenticated.Body.String())
	}
	assertSetupReadCacheHeaders(t, unauthenticated, "/api/v1/setup/readiness")

	regularUser := fixture.request(t, http.MethodGet, "/api/v1/setup/readiness", "", fixture.userToken)
	if regularUser.Code != http.StatusForbidden {
		t.Fatalf("regular-user GET status = %d, want %d: %s", regularUser.Code, http.StatusForbidden, regularUser.Body.String())
	}

	admin := fixture.request(t, http.MethodGet, "/api/v1/setup/readiness", "", fixture.adminToken)
	if admin.Code != http.StatusOK {
		t.Fatalf("administrator GET status = %d, want %d: %s", admin.Code, http.StatusOK, admin.Body.String())
	}
	data := decodeSetupReadinessResponse(t, admin)
	if data.Lifecycle != setupLifecyclePending || data.Summary.ActiveAdminCount != 1 {
		t.Fatalf("GET readiness data = %+v, want pending with one active admin", data)
	}
	for _, secret := range []string{fixture.root, fixture.admin.ID, fixture.user.ID} {
		if strings.Contains(admin.Body.String(), secret) {
			t.Fatalf("GET readiness leaked %q: %s", secret, admin.Body.String())
		}
	}
}

func TestSetupReadEndpointsDisableCaching(t *testing.T) {
	fixture := newSetupReadinessHTTPFixture(t, setupReadinessHTTPOptions{backupAvailable: true})
	tests := []struct {
		name   string
		target string
		token  string
	}{
		{name: "public status", target: "/api/v1/setup/"},
		{name: "administrator readiness", target: "/api/v1/setup/readiness", token: fixture.adminToken},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.request(t, http.MethodGet, test.target, "", test.token)
			if response.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d: %s", test.target, response.Code, http.StatusOK, response.Body.String())
			}
			assertSetupReadCacheHeaders(t, response, test.target)
		})
	}
}

func assertSetupReadCacheHeaders(t *testing.T, response *httptest.ResponseRecorder, target string) {
	t.Helper()
	if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("GET %s Cache-Control = %q, want private, no-store", target, got)
	}
	for _, token := range []string{"Cookie", "Authorization"} {
		if !headerValueContainsToken(response.Header().Values("Vary"), token) {
			t.Fatalf("GET %s Vary = %q, want %s", target, response.Header().Values("Vary"), token)
		}
	}
}

func headerValueContainsToken(values []string, token string) bool {
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(candidate), token) {
				return true
			}
		}
	}
	return false
}

func TestSetupReadinessAcknowledgeStatusesAndIdempotency(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	restoreNow := replaceAPITimeNow(t, now)
	defer restoreNow()

	t.Run("incomplete required checks return conflict", func(t *testing.T) {
		fixture := newSetupReadinessHTTPFixture(t, setupReadinessHTTPOptions{backupAvailable: true})
		response := fixture.request(t, http.MethodPost, "/api/v1/setup/acknowledge", `{}`, fixture.adminToken)
		if response.Code != http.StatusConflict {
			t.Fatalf("acknowledge status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
		}
		assertSetupReadinessError(t, response, "SETUP_NOT_READY", []string{"backup_job", "backup_success"})
	})

	t.Run("unavailable required checks return service unavailable", func(t *testing.T) {
		fixture := newSetupReadinessHTTPFixture(t, setupReadinessHTTPOptions{})
		response := fixture.request(t, http.MethodPost, "/api/v1/setup/acknowledge", `{}`, fixture.adminToken)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("acknowledge status = %d, want %d: %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
		}
		assertSetupReadinessError(t, response, "SETUP_READINESS_UNAVAILABLE", []string{"backup_job", "backup_success"})
	})

	t.Run("ready setup completes once and remains idempotent", func(t *testing.T) {
		fixture := newSetupReadinessHTTPFixture(t, setupReadinessHTTPOptions{
			backupAvailable: true,
			backupJob:       true,
			backupSuccess:   true,
		})

		first := fixture.request(t, http.MethodPost, "/api/v1/setup/acknowledge", `{}`, fixture.adminToken)
		if first.Code != http.StatusOK {
			t.Fatalf("first acknowledge status = %d, want %d: %s", first.Code, http.StatusOK, first.Body.String())
		}
		firstData := decodeSetupReadinessResponse(t, first)
		if firstData.Lifecycle != setupLifecycleCompleted || firstData.CompletedAt == nil || !firstData.CompletedAt.Equal(now) {
			t.Fatalf("first acknowledge lifecycle = %+v, want completed at %s", firstData, now)
		}
		if firstData.CanComplete || firstData.CanDefer || firstData.Prompt {
			t.Fatalf("completed readiness still exposes actions: %+v", firstData)
		}

		persisted, err := config.LoadSecrets(fixture.storageRoot)
		if err != nil {
			t.Fatalf("LoadSecrets() error: %v", err)
		}
		if persisted == nil || persisted.SetupLifecycle.CompletedAt == nil || !persisted.SetupLifecycle.CompletedAt.Equal(now) {
			t.Fatalf("persisted lifecycle = %#v, want completed at %s", persisted, now)
		}
		publicStatus := decodeSetupStatusResponse(t, fixture.request(t, http.MethodGet, "/api/v1/setup/", "", ""))
		if publicStatus.IsFirstRun {
			t.Fatal("public setup status still reports first run after completion")
		}

		second := fixture.request(t, http.MethodPost, "/api/v1/setup/acknowledge", `{}`, fixture.adminToken)
		if second.Code != http.StatusOK {
			t.Fatalf("second acknowledge status = %d, want %d: %s", second.Code, http.StatusOK, second.Body.String())
		}
		secondData := decodeSetupReadinessResponse(t, second)
		if secondData.CompletedAt == nil || !secondData.CompletedAt.Equal(*firstData.CompletedAt) {
			t.Fatalf("idempotent completed_at = %v, want %v", secondData.CompletedAt, firstData.CompletedAt)
		}
		if message := decodeSetupReadinessMessage(t, second); message != "setup already completed" {
			t.Fatalf("idempotent response message = %q, want %q", message, "setup already completed")
		}
	})
}

func TestSetupReadinessDeferValidationAndSuccess(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	restoreNow := replaceAPITimeNow(t, now)
	defer restoreNow()

	incomplete := newSetupReadinessHTTPFixture(t, setupReadinessHTTPOptions{backupAvailable: true})
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "zero days", body: `{"remind_in_days":0}`},
		{name: "over maximum", body: `{"remind_in_days":31}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := incomplete.request(t, http.MethodPost, "/api/v1/setup/defer", tt.body, incomplete.adminToken)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("defer status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}

	t.Run("unknown field", func(t *testing.T) {
		response := incomplete.request(t, http.MethodPost, "/api/v1/setup/defer", `{"remind_in_days":7,"unexpected":true}`, incomplete.adminToken)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("defer status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
		}
	})

	t.Run("non-deferrable readiness", func(t *testing.T) {
		ready := newSetupReadinessHTTPFixture(t, setupReadinessHTTPOptions{
			backupAvailable: true,
			backupJob:       true,
			backupSuccess:   true,
		})
		response := ready.request(t, http.MethodPost, "/api/v1/setup/defer", `{"remind_in_days":7}`, ready.adminToken)
		if response.Code != http.StatusConflict {
			t.Fatalf("defer status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
		}
		assertSetupReadinessError(t, response, "SETUP_DEFER_FORBIDDEN", []string{})
	})

	t.Run("completed lifecycle takes precedence over unavailable evidence", func(t *testing.T) {
		completed := newSetupReadinessHTTPFixture(t, setupReadinessHTTPOptions{
			backupAvailable: true,
			backupJob:       true,
			backupSuccess:   true,
		})
		acknowledge := completed.request(t, http.MethodPost, "/api/v1/setup/acknowledge", `{}`, completed.adminToken)
		if acknowledge.Code != http.StatusOK {
			t.Fatalf("acknowledge status = %d, want %d: %s", acknowledge.Code, http.StatusOK, acknowledge.Body.String())
		}
		completed.server.backupManager = nil

		response := completed.request(t, http.MethodPost, "/api/v1/setup/defer", `{"remind_in_days":7}`, completed.adminToken)
		if response.Code != http.StatusConflict {
			t.Fatalf("defer status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
		}
		assertSetupReadinessError(t, response, "SETUP_DEFER_FORBIDDEN", []string{})
	})

	t.Run("successful deferral", func(t *testing.T) {
		response := incomplete.request(t, http.MethodPost, "/api/v1/setup/defer", `{"remind_in_days":7}`, incomplete.adminToken)
		if response.Code != http.StatusOK {
			t.Fatalf("defer status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
		}
		data := decodeSetupReadinessResponse(t, response)
		wantUntil := now.AddDate(0, 0, 7)
		if data.Lifecycle != setupLifecycleDeferred || data.Prompt || data.DeferredUntil == nil || !data.DeferredUntil.Equal(wantUntil) {
			t.Fatalf("deferred readiness = %+v, want deferred until %s without prompt", data, wantUntil)
		}

		persisted, err := config.LoadSecrets(incomplete.storageRoot)
		if err != nil {
			t.Fatalf("LoadSecrets() error: %v", err)
		}
		if persisted == nil || persisted.SetupLifecycle.DeferredAt == nil || !persisted.SetupLifecycle.DeferredAt.Equal(now) || persisted.SetupLifecycle.DeferredUntil == nil || !persisted.SetupLifecycle.DeferredUntil.Equal(wantUntil) {
			t.Fatalf("persisted deferral = %#v, want %s through %s", persisted, now, wantUntil)
		}
		publicStatus := decodeSetupStatusResponse(t, incomplete.request(t, http.MethodGet, "/api/v1/setup/", "", ""))
		if publicStatus.IsFirstRun {
			t.Fatal("public setup status reports first run during an effective deferral")
		}
	})
}

type setupReadinessHTTPOptions struct {
	backupAvailable bool
	backupJob       bool
	backupSuccess   bool
}

type setupReadinessHTTPFixture struct {
	server      *Server
	root        string
	storageRoot string
	admin       *auth.User
	user        *auth.User
	adminToken  string
	userToken   string
}

func newSetupReadinessHTTPFixture(t *testing.T, options setupReadinessHTTPOptions) *setupReadinessHTTPFixture {
	t.Helper()
	if options.backupSuccess && (!options.backupAvailable || !options.backupJob) {
		t.Fatal("backup success fixture requires an available manager and a configured job")
	}

	root := t.TempDir()
	storageRoot := filepath.Join(root, "storage")
	authRoot := filepath.Join(storageRoot, ".mnemonas")
	if err := os.MkdirAll(authRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(auth root) error: %v", err)
	}
	usersFile := filepath.Join(authRoot, "users.json")
	userStore, _, err := auth.NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}

	var admin *auth.User
	for _, user := range userStore.List() {
		if user.Role == auth.RoleAdmin && !user.Disabled {
			admin = user
			break
		}
	}
	if admin == nil {
		t.Fatal("bootstrap administrator not found")
	}
	if err := userStore.ResetOwnPassword(admin.ID, "changed-admin-password"); err != nil {
		t.Fatalf("ResetOwnPassword() error: %v", err)
	}
	admin, err = userStore.GetByID(admin.ID)
	if err != nil {
		t.Fatalf("GetByID(admin) error: %v", err)
	}
	initialPasswordFile := filepath.Join(authRoot, "initial-password.txt")
	if err := os.Remove(initialPasswordFile); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove(initial password) error: %v", err)
	}
	regularUser, err := userStore.Create("setup-user", "regular-user-password", "", auth.RoleUser)
	if err != nil {
		t.Fatalf("Create(regular user) error: %v", err)
	}

	settings := config.Default()
	settings.Server.Host = "127.0.0.1"
	settings.Storage.Root = storageRoot
	settings.Auth.Enabled = true
	settings.Auth.UsersFile = usersFile
	settings.WebDAV.Enabled = false
	settings.Share.Enabled = false
	settings.Favorites.Enabled = false
	settings.Security.AllowUnsafeNoAuth = false

	var jobs []config.BackupJobConfig
	if options.backupJob {
		jobs = []config.BackupJobConfig{{
			ID:          "setup-readiness",
			Name:        "Setup readiness",
			Type:        backup.JobTypeLocal,
			Source:      storageRoot,
			Destination: filepath.Join(root, "backup-target"),
		}}
	}
	settings.Backup.Jobs = append([]config.BackupJobConfig(nil), jobs...)
	configPath := filepath.Join(root, "config.toml")
	if err := settings.Save(configPath); err != nil {
		t.Fatalf("Save(config) error: %v", err)
	}
	if err := config.SaveSecrets(storageRoot, &config.Secrets{JWTSecret: "setup-readiness-jwt-secret"}); err != nil {
		t.Fatalf("SaveSecrets() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storageRoot, "readiness.txt"), []byte("ready"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	serverConfig := &ServerConfig{
		Config:         settings,
		ConfigPath:     configPath,
		StorageRoot:    storageRoot,
		BackupJobs:     jobs,
		AuthEnabled:    true,
		AuthUsersFile:  usersFile,
		AuthUserStore:  userStore,
		AuthJWTSecret:  "setup-readiness-test-secret-at-least-32-bytes",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	}
	if options.backupAvailable {
		serverConfig.BackupRoot = filepath.Join(root, "backup-state")
	}
	server, err := NewServer(zerolog.Nop(), serverConfig)
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	if options.backupSuccess {
		result, runErr := server.backupManager.RunJob(context.Background(), jobs[0].ID)
		if runErr != nil {
			t.Fatalf("RunJob() error: %v; result=%+v", runErr, result)
		}
		if result == nil || result.Status != backup.StatusCompleted {
			t.Fatalf("RunJob() result = %+v, want completed", result)
		}
	}

	adminTokens, err := server.tokenManager.GenerateTokenPair(admin)
	if err != nil {
		t.Fatalf("GenerateTokenPair(admin) error: %v", err)
	}
	userTokens, err := server.tokenManager.GenerateTokenPair(regularUser)
	if err != nil {
		t.Fatalf("GenerateTokenPair(user) error: %v", err)
	}

	return &setupReadinessHTTPFixture{
		server:      server,
		root:        root,
		storageRoot: storageRoot,
		admin:       admin,
		user:        regularUser,
		adminToken:  adminTokens.AccessToken,
		userToken:   userTokens.AccessToken,
	}
}

func (fixture *setupReadinessHTTPFixture) request(t *testing.T, method, target, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	request := httptest.NewRequest(method, target, reader)
	request.RemoteAddr = "127.0.0.1:12345"
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	fixture.server.Router().ServeHTTP(response, request)
	return response
}

func decodeSetupStatusResponse(t *testing.T, response *httptest.ResponseRecorder) SetupStatusResponse {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("setup status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var status SetupStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatalf("Unmarshal(setup status) error: %v; body=%s", err, response.Body.String())
	}
	return status
}

func decodeSetupReadinessResponse(t *testing.T, response *httptest.ResponseRecorder) setupReadinessData {
	t.Helper()
	var envelope struct {
		Data setupReadinessData `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("Unmarshal(readiness response) error: %v; body=%s", err, response.Body.String())
	}
	return envelope.Data
}

func decodeSetupReadinessMessage(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("Unmarshal(readiness message) error: %v; body=%s", err, response.Body.String())
	}
	return envelope.Message
}

func assertSetupReadinessError(t *testing.T, response *httptest.ResponseRecorder, wantCode string, wantIDs []string) {
	t.Helper()
	var payload struct {
		Code    string `json:"code"`
		Details struct {
			RequiredCheckIDs []string `json:"required_check_ids"`
		} `json:"details"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal(readiness error) error: %v; body=%s", err, response.Body.String())
	}
	if payload.Code != wantCode {
		t.Fatalf("error code = %q, want %q; body=%s", payload.Code, wantCode, response.Body.String())
	}
	if !reflect.DeepEqual(payload.Details.RequiredCheckIDs, wantIDs) {
		t.Fatalf("required_check_ids = %#v, want %#v", payload.Details.RequiredCheckIDs, wantIDs)
	}
}

func replaceAPITimeNow(t *testing.T, now time.Time) func() {
	t.Helper()
	original := apiTimeNow
	apiTimeNow = func() time.Time { return now }
	return func() { apiTimeNow = original }
}
