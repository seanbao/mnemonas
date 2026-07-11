package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
)

func TestManagerReadinessSnapshotAvailabilityAndJobCounts(t *testing.T) {
	var unavailable *Manager
	if got := unavailable.ReadinessSnapshot(); got != (ReadinessSnapshot{}) {
		t.Fatalf("nil manager snapshot = %+v, want zero value", got)
	}

	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	empty := newReadinessTestManager(t, now, nil)
	if got := empty.ReadinessSnapshot(); got != (ReadinessSnapshot{Available: true}) {
		t.Fatalf("empty manager snapshot = %+v, want available empty snapshot", got)
	}

	manager := newReadinessTestManager(t, now, []config.BackupJobConfig{
		{ID: "scheduled", Type: JobTypeLocal, ScheduleInterval: 24 * time.Hour},
		{ID: "manual", Type: JobTypeLocal},
		{ID: "disabled", Type: JobTypeLocal, ScheduleInterval: time.Hour, Disabled: true},
	})
	disabledSuccess := now.Add(-time.Hour)
	setReadinessJobState(t, manager, "disabled", JobState{
		LastSuccessfulRun: completedReadinessRun("disabled", disabledSuccess),
		LastRestoreDrill:  readinessDrill("disabled", StatusCompleted, disabledSuccess, false),
	})

	got := manager.ReadinessSnapshot()
	if !got.Available || got.EnabledJobCount != 2 || got.EnabledScheduledJobCount != 1 {
		t.Fatalf("snapshot availability/counts = %+v, want available with 2 enabled and 1 scheduled", got)
	}
	if got.LastSuccessfulBackupAt != nil || got.HasCurrentHealthyBackup {
		t.Fatalf("disabled evidence leaked into snapshot: %+v", got)
	}
	if got.LastValidRestoreEvidenceAt != nil || got.HasCurrentRestoreEvidence {
		t.Fatalf("disabled restore evidence leaked into snapshot: %+v", got)
	}
}

func TestManagerReadinessSnapshotBackupFreshness(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	const staleAfter = 48 * time.Hour
	tests := []struct {
		name        string
		job         config.BackupJobConfig
		state       JobState
		wantTime    *time.Time
		wantCurrent bool
	}{
		{
			name: "no successful backup",
			job:  config.BackupJobConfig{ID: "job", Type: JobTypeLocal, ScheduleInterval: 24 * time.Hour},
		},
		{
			name: "fresh scheduled backup",
			job:  config.BackupJobConfig{ID: "job", Type: JobTypeLocal, ScheduleInterval: 24 * time.Hour, StaleAfter: staleAfter},
			state: JobState{
				LastRun:           completedReadinessRun("job", now.Add(-time.Hour)),
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
			},
			wantTime:    readinessTimePtr(now.Add(-time.Hour)),
			wantCurrent: true,
		},
		{
			name: "backup exactly at stale threshold",
			job:  config.BackupJobConfig{ID: "job", Type: JobTypeLocal, ScheduleInterval: 24 * time.Hour, StaleAfter: staleAfter},
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-staleAfter)),
			},
			wantTime:    readinessTimePtr(now.Add(-staleAfter)),
			wantCurrent: true,
		},
		{
			name: "scheduled backup uses two intervals as default threshold",
			job:  config.BackupJobConfig{ID: "job", Type: JobTypeLocal, ScheduleInterval: 24 * time.Hour},
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-staleAfter)),
			},
			wantTime:    readinessTimePtr(now.Add(-staleAfter)),
			wantCurrent: true,
		},
		{
			name: "stale scheduled backup",
			job:  config.BackupJobConfig{ID: "job", Type: JobTypeLocal, ScheduleInterval: 24 * time.Hour, StaleAfter: staleAfter},
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-staleAfter-time.Nanosecond)),
			},
			wantTime: readinessTimePtr(now.Add(-staleAfter - time.Nanosecond)),
		},
		{
			name: "latest failure overrides previous success",
			job:  config.BackupJobConfig{ID: "job", Type: JobTypeLocal, ScheduleInterval: 24 * time.Hour, StaleAfter: staleAfter},
			state: JobState{
				LastRun:           failedReadinessRun("job", now.Add(-time.Hour)),
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-2*time.Hour)),
			},
			wantTime: readinessTimePtr(now.Add(-2 * time.Hour)),
		},
		{
			name: "manual backup without threshold remains current",
			job:  config.BackupJobConfig{ID: "job", Type: JobTypeLocal},
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-365*24*time.Hour)),
			},
			wantTime:    readinessTimePtr(now.Add(-365 * 24 * time.Hour)),
			wantCurrent: true,
		},
		{
			name: "manual backup honors explicit threshold",
			job:  config.BackupJobConfig{ID: "job", Type: JobTypeLocal, StaleAfter: staleAfter},
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-staleAfter-time.Nanosecond)),
			},
			wantTime: readinessTimePtr(now.Add(-staleAfter - time.Nanosecond)),
		},
		{
			name: "future backup completion is not evidence",
			job:  config.BackupJobConfig{ID: "job", Type: JobTypeLocal},
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(time.Nanosecond)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newReadinessTestManager(t, now, []config.BackupJobConfig{tt.job})
			setReadinessJobState(t, manager, tt.job.ID, tt.state)

			got := manager.ReadinessSnapshot()
			assertReadinessTime(t, "LastSuccessfulBackupAt", got.LastSuccessfulBackupAt, tt.wantTime)
			if got.HasCurrentHealthyBackup != tt.wantCurrent {
				t.Fatalf("HasCurrentHealthyBackup = %v, want %v; snapshot=%+v", got.HasCurrentHealthyBackup, tt.wantCurrent, got)
			}
		})
	}

	t.Run("restore drill cannot be current without a completed backup timestamp", func(t *testing.T) {
		job := config.BackupJobConfig{ID: "job", Type: JobTypeLocal}
		manager := newReadinessTestManager(t, now, []config.BackupJobConfig{job})
		run := completedReadinessRun(job.ID, now.Add(-2*time.Hour))
		run.FinishedAt = nil
		drillAt := now.Add(-time.Hour)
		setReadinessJobState(t, manager, job.ID, JobState{
			LastSuccessfulRun: run,
			LastRestoreDrill:  readinessDrill(job.ID, StatusCompleted, drillAt, false),
		})

		got := manager.ReadinessSnapshot()
		assertReadinessTime(t, "LastValidRestoreEvidenceAt", got.LastValidRestoreEvidenceAt, readinessTimePtr(drillAt))
		if got.HasCurrentHealthyBackup || got.HasCurrentRestoreEvidence {
			t.Fatalf("incomplete backup timestamp supported current readiness: %+v", got)
		}
	})
}

func TestManagerReadinessSnapshotUsesLatestSuccessfulBackup(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	manager := newReadinessTestManager(t, now, []config.BackupJobConfig{
		{ID: "older", Type: JobTypeLocal, ScheduleInterval: 24 * time.Hour},
		{ID: "newer", Type: JobTypeLocal, ScheduleInterval: 24 * time.Hour},
	})
	setReadinessJobState(t, manager, "older", JobState{LastSuccessfulRun: completedReadinessRun("older", now.Add(-4*time.Hour))})
	setReadinessJobState(t, manager, "newer", JobState{LastSuccessfulRun: completedReadinessRun("newer", now.Add(-time.Hour))})

	got := manager.ReadinessSnapshot()
	assertReadinessTime(t, "LastSuccessfulBackupAt", got.LastSuccessfulBackupAt, readinessTimePtr(now.Add(-time.Hour)))
	if !got.HasCurrentHealthyBackup {
		t.Fatalf("snapshot = %+v, want current healthy backup", got)
	}
}

func TestManagerReadinessSnapshotRejectsStaleBackupBindingAndMissingManifest(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*testing.T, *Manager)
	}{
		{
			name: "job id mismatch",
			mutate: func(_ *testing.T, manager *Manager) {
				manager.state.Jobs["job"].LastSuccessfulRun.JobID = "other-job"
			},
		},
		{
			name: "stored config binding missing",
			mutate: func(_ *testing.T, manager *Manager) {
				manager.state.Jobs["job"].LastSuccessfulRun.JobConfigBinding = ""
			},
		},
		{
			name: "source changed under same job id",
			mutate: func(t *testing.T, manager *Manager) {
				job := manager.jobs["job"]
				job.Source = filepath.Join(secureBackupTestTempDir(t), "new-source")
				manager.jobs["job"] = job
			},
		},
		{
			name: "destination changed under same job id",
			mutate: func(t *testing.T, manager *Manager) {
				job := manager.jobs["job"]
				job.Destination = filepath.Join(secureBackupTestTempDir(t), "new-destination")
				manager.jobs["job"] = job
			},
		},
		{
			name: "local manifest removed",
			mutate: func(t *testing.T, manager *Manager) {
				manifestPath := manager.state.Jobs["job"].LastSuccessfulRun.ManifestPath
				if err := os.Remove(manifestPath); err != nil {
					t.Fatalf("Remove(manifest) error: %v", err)
				}
			},
		},
		{
			name: "local manifest becomes truncated after first check",
			mutate: func(t *testing.T, manager *Manager) {
				if got := manager.ReadinessSnapshot(); !got.HasCurrentHealthyBackup {
					t.Fatalf("baseline readiness = %+v, want current backup", got)
				}
				manifestPath := manager.state.Jobs["job"].LastSuccessfulRun.ManifestPath
				if err := os.WriteFile(manifestPath, []byte(`{"version":`), 0o600); err != nil {
					t.Fatalf("WriteFile(truncated manifest) error: %v", err)
				}
			},
		},
		{
			name: "same-size same-mtime manifest tampering is revalidated",
			mutate: func(t *testing.T, manager *Manager) {
				if got := manager.ReadinessSnapshot(); !got.HasCurrentHealthyBackup {
					t.Fatalf("baseline readiness = %+v, want current backup", got)
				}
				manifestPath := manager.state.Jobs["job"].LastSuccessfulRun.ManifestPath
				originalInfo, err := os.Stat(manifestPath)
				if err != nil {
					t.Fatalf("Stat(manifest) error: %v", err)
				}
				original, err := os.ReadFile(manifestPath)
				if err != nil {
					t.Fatalf("ReadFile(manifest) error: %v", err)
				}
				tampered := strings.Replace(string(original), `"job_id": "job"`, `"job_id": "bad"`, 1)
				if tampered == string(original) || len(tampered) != len(original) {
					t.Fatalf("failed to build same-size tampered manifest")
				}
				if err := os.WriteFile(manifestPath, []byte(tampered), 0o600); err != nil {
					t.Fatalf("WriteFile(tampered manifest) error: %v", err)
				}
				if err := os.Chtimes(manifestPath, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
					t.Fatalf("Chtimes(tampered manifest) error: %v", err)
				}
				tamperedInfo, err := os.Stat(manifestPath)
				if err != nil {
					t.Fatalf("Stat(tampered manifest) error: %v", err)
				}
				if tamperedInfo.Size() != originalInfo.Size() || !tamperedInfo.ModTime().Equal(originalInfo.ModTime()) {
					t.Fatalf("tampered manifest metadata = %d/%v, want %d/%v", tamperedInfo.Size(), tamperedInfo.ModTime(), originalInfo.Size(), originalInfo.ModTime())
				}
			},
		},
		{
			name: "local manifest identity changed",
			mutate: func(t *testing.T, manager *Manager) {
				run := manager.state.Jobs["job"].LastSuccessfulRun
				createdAt, err := parseRunID(run.ID)
				if err != nil {
					t.Fatalf("parseRunID() error: %v", err)
				}
				manifest := Manifest{
					Version:     manifestVersion,
					JobID:       "other-job",
					RunID:       run.ID,
					Source:      run.Source,
					CreatedAt:   createdAt,
					Entries:     []ManifestEntry{},
					Directories: testManifestDirectories(),
				}
				if err := writeJSONFile(run.ManifestPath, manifest, 0o600); err != nil {
					t.Fatalf("writeJSONFile(identity mismatch) error: %v", err)
				}
			},
		},
		{
			name: "manifest summary no longer matches persisted run",
			mutate: func(_ *testing.T, manager *Manager) {
				run := manager.state.Jobs["job"].LastSuccessfulRun
				run.FileCount = 1
				run.TotalBytes = 4096
			},
		},
		{
			name: "manifest config inclusion no longer matches persisted run",
			mutate: func(_ *testing.T, manager *Manager) {
				manager.state.Jobs["job"].LastSuccessfulRun.ConfigIncluded = true
			},
		},
		{
			name: "local manifest is not a regular file",
			mutate: func(t *testing.T, manager *Manager) {
				manifestPath := manager.state.Jobs["job"].LastSuccessfulRun.ManifestPath
				if err := os.Remove(manifestPath); err != nil {
					t.Fatalf("Remove(manifest) error: %v", err)
				}
				if err := os.Mkdir(manifestPath, 0o700); err != nil {
					t.Fatalf("Mkdir(manifest path) error: %v", err)
				}
			},
		},
		{
			name: "local manifest is a symlink",
			mutate: func(t *testing.T, manager *Manager) {
				manifestPath := manager.state.Jobs["job"].LastSuccessfulRun.ManifestPath
				if err := os.Remove(manifestPath); err != nil {
					t.Fatalf("Remove(manifest) error: %v", err)
				}
				target := filepath.Join(secureBackupTestTempDir(t), "outside-manifest.json")
				if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
					t.Fatalf("WriteFile(outside manifest) error: %v", err)
				}
				if err := os.Symlink(target, manifestPath); err != nil {
					t.Fatalf("Symlink(manifest) error: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newReadinessTestManager(t, now, []config.BackupJobConfig{{ID: "job", Type: JobTypeLocal}})
			setReadinessJobState(t, manager, "job", JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
			})
			tt.mutate(t, manager)

			got := manager.ReadinessSnapshot()
			if got.LastSuccessfulBackupAt != nil || got.HasCurrentHealthyBackup {
				t.Fatalf("stale backup evidence remained current: %+v", got)
			}
		})
	}
}

func TestManagerReadinessSnapshotRejectsCredentialFileIdentityDrift(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name        string
		initial     string
		replacement string
		job         func(string) config.BackupJobConfig
	}{
		{
			name:        "restic password file changed in place",
			initial:     "alpha-secret\n",
			replacement: "bravo-secret\n",
			job: func(path string) config.BackupJobConfig {
				return config.BackupJobConfig{ID: "job", Type: JobTypeRestic, Repository: "/backup/repository", PasswordFile: path}
			},
		},
		{
			name:        "rclone config changed in place",
			initial:     "[archive]\ntype = s3\nregion = east\n",
			replacement: "[archive]\ntype = s3\nregion = west\n",
			job: func(path string) config.BackupJobConfig {
				return config.BackupJobConfig{ID: "job", Type: JobTypeRclone, Remote: "archive:current", ConfigFile: path}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.initial) != len(tt.replacement) {
				t.Fatalf("credential fixtures must have equal length")
			}
			credentialPath := filepath.Join(secureBackupTestTempDir(t), "credential.conf")
			if err := os.WriteFile(credentialPath, []byte(tt.initial), 0o600); err != nil {
				t.Fatalf("WriteFile(credential) error: %v", err)
			}
			job := tt.job(credentialPath)
			manager := newReadinessTestManager(t, now, []config.BackupJobConfig{job})
			setReadinessJobState(t, manager, job.ID, JobState{
				LastSuccessfulRun: completedReadinessRun(job.ID, now.Add(-time.Hour)),
			})
			if got := manager.ReadinessSnapshot(); !got.HasCurrentHealthyBackup {
				t.Fatalf("baseline readiness = %+v, want current backup", got)
			}
			originalBinding := manager.state.Jobs[job.ID].LastSuccessfulRun.JobConfigBinding
			originalInfo, err := os.Stat(credentialPath)
			if err != nil {
				t.Fatalf("Stat(credential) error: %v", err)
			}

			if err := os.WriteFile(credentialPath, []byte(tt.replacement), 0o600); err != nil {
				t.Fatalf("WriteFile(replacement credential) error: %v", err)
			}
			if err := os.Chtimes(credentialPath, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
				t.Fatalf("Chtimes(credential) error: %v", err)
			}
			currentBinding, err := jobConfigEvidenceBinding(manager.jobs[job.ID], manager.storageRoot, manager.configPath)
			if err != nil {
				t.Fatalf("jobConfigEvidenceBinding() error: %v", err)
			}
			if currentBinding == originalBinding {
				t.Fatal("in-place credential change did not alter evidence binding")
			}

			got := manager.ReadinessSnapshot()
			if got.LastSuccessfulBackupAt != nil || got.HasCurrentHealthyBackup {
				t.Fatalf("credential-drifted backup evidence remained valid: %+v", got)
			}
		})
	}
}

func TestManagerReadinessSnapshotRejectsIncompleteCompletionTimestamps(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

	for _, tt := range []struct {
		name   string
		mutate func(*RunResult)
	}{
		{
			name: "backup missing finished time",
			mutate: func(result *RunResult) {
				result.FinishedAt = nil
			},
		},
		{
			name: "backup finishes before it starts",
			mutate: func(result *RunResult) {
				finishedAt := result.StartedAt.Add(-time.Nanosecond)
				result.FinishedAt = &finishedAt
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			job := config.BackupJobConfig{ID: "job", Type: JobTypeRclone, Remote: "archive:current"}
			manager := newReadinessTestManager(t, now, []config.BackupJobConfig{job})
			run := completedReadinessRun(job.ID, now.Add(-time.Hour))
			tt.mutate(run)
			setReadinessJobState(t, manager, job.ID, JobState{LastSuccessfulRun: run})

			got := manager.ReadinessSnapshot()
			if got.LastSuccessfulBackupAt != nil || got.HasCurrentHealthyBackup {
				t.Fatalf("incomplete backup evidence remained valid: %+v", got)
			}
		})
	}

	for _, tt := range []struct {
		name   string
		mutate func(*RestoreDrillResult)
	}{
		{
			name: "restore drill missing finished time",
			mutate: func(result *RestoreDrillResult) {
				result.FinishedAt = nil
			},
		},
		{
			name: "restore drill finishes before it starts",
			mutate: func(result *RestoreDrillResult) {
				finishedAt := result.StartedAt.Add(-time.Nanosecond)
				result.FinishedAt = &finishedAt
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			job := config.BackupJobConfig{ID: "job", Type: JobTypeRclone, Remote: "archive:current"}
			manager := newReadinessTestManager(t, now, []config.BackupJobConfig{job})
			drill := readinessDrill(job.ID, StatusCompleted, now.Add(-time.Hour), false)
			tt.mutate(drill)
			setReadinessJobState(t, manager, job.ID, JobState{LastRestoreDrill: drill})

			got := manager.ReadinessSnapshot()
			if got.LastValidRestoreEvidenceAt != nil || got.HasCurrentRestoreEvidence {
				t.Fatalf("incomplete restore drill evidence remained valid: %+v", got)
			}
		})
	}

	for _, tt := range []struct {
		name   string
		mutate func(*RestoreResult, *RestoreVerifyResult)
	}{
		{
			name: "restore missing finished time",
			mutate: func(restore *RestoreResult, _ *RestoreVerifyResult) {
				restore.FinishedAt = nil
			},
		},
		{
			name: "restore finishes before it starts",
			mutate: func(restore *RestoreResult, _ *RestoreVerifyResult) {
				finishedAt := restore.StartedAt.Add(-time.Nanosecond)
				restore.FinishedAt = &finishedAt
			},
		},
		{
			name: "restore verification missing finished time",
			mutate: func(_ *RestoreResult, verify *RestoreVerifyResult) {
				verify.FinishedAt = nil
			},
		},
		{
			name: "restore verification finishes before it starts",
			mutate: func(_ *RestoreResult, verify *RestoreVerifyResult) {
				finishedAt := verify.StartedAt.Add(-time.Nanosecond)
				verify.FinishedAt = &finishedAt
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			job := config.BackupJobConfig{ID: "job", Type: JobTypeRclone, Remote: "archive:current"}
			manager := newReadinessTestManager(t, now, []config.BackupJobConfig{job})
			restoreFinishedAt := now.Add(-2 * time.Hour)
			restore := &RestoreResult{
				ID:         "restore",
				Status:     StatusCompleted,
				StartedAt:  restoreFinishedAt.Add(-time.Minute),
				FinishedAt: readinessTimePtr(restoreFinishedAt),
				TargetPath: "/restore/current",
			}
			verify := &RestoreVerifyResult{
				ID:         "verify",
				Status:     StatusCompleted,
				StartedAt:  restoreFinishedAt.Add(time.Minute),
				FinishedAt: readinessTimePtr(now.Add(-time.Hour)),
				TargetPath: "/restore/current",
			}
			tt.mutate(restore, verify)
			setReadinessJobState(t, manager, job.ID, JobState{
				LastRestore:       restore,
				LastRestoreVerify: verify,
			})

			got := manager.ReadinessSnapshot()
			if got.LastValidRestoreEvidenceAt != nil || got.HasCurrentRestoreEvidence {
				t.Fatalf("incomplete explicit restore evidence remained valid: %+v", got)
			}
		})
	}
}

func TestManagerReadinessSnapshotUnavailableAfterStatePersistenceFailure(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	manager := newReadinessTestManager(t, now, []config.BackupJobConfig{{ID: "job", Type: JobTypeLocal}})
	setReadinessJobState(t, manager, "job", JobState{
		LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
	})
	run := cloneRunResultRaw(manager.state.Jobs["job"].LastSuccessfulRun)
	originalWriteBackupStateFile := writeBackupStateFile
	t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
	persistErr := errors.New("injected state persistence failure")
	failWrite := true
	writeBackupStateFile = func(lock *backupStateLock, path string, value any, perm os.FileMode) error {
		if failWrite {
			return persistErr
		}
		return originalWriteBackupStateFile(lock, path, value, perm)
	}
	if err := manager.updateLastRun(run); err == nil {
		t.Fatal("updateLastRun() succeeded with an unusable state root")
	}
	if got := manager.ReadinessSnapshot(); got != (ReadinessSnapshot{}) {
		t.Fatalf("readiness after persistence failure = %+v, want unavailable zero value", got)
	}

	failWrite = false
	if err := manager.updateLastRun(run); err != nil {
		t.Fatalf("updateLastRun() recovery error: %v", err)
	}
	got := manager.ReadinessSnapshot()
	if !got.Available || !got.HasCurrentHealthyBackup {
		t.Fatalf("readiness did not recover after persisted state save: %+v", got)
	}
}

func TestManagerReadinessSnapshotUnavailableAfterStateDirectorySyncFailure(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	manager := newReadinessTestManager(t, now, []config.BackupJobConfig{{ID: "job", Type: JobTypeLocal}})
	setReadinessJobState(t, manager, "job", JobState{
		LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
	})
	run := cloneRunResultRaw(manager.state.Jobs["job"].LastSuccessfulRun)

	originalSyncBackupJSONDir := syncBackupJSONDir
	t.Cleanup(func() { syncBackupJSONDir = originalSyncBackupJSONDir })
	syncErr := errors.New("injected directory sync failure")
	syncBackupJSONDir = func(*os.File, string) error { return syncErr }
	if err := manager.updateLastRun(run); !errors.Is(err, syncErr) {
		t.Fatalf("updateLastRun() error = %v, want %v", err, syncErr)
	}
	if got := manager.ReadinessSnapshot(); got != (ReadinessSnapshot{}) {
		t.Fatalf("readiness after directory sync failure = %+v, want unavailable zero value", got)
	}

	syncBackupJSONDir = originalSyncBackupJSONDir
	if err := manager.updateLastRun(run); err != nil {
		t.Fatalf("updateLastRun() recovery error: %v", err)
	}
	got := manager.ReadinessSnapshot()
	if !got.Available || !got.HasCurrentHealthyBackup {
		t.Fatalf("readiness did not recover after durable state save: %+v", got)
	}
}

func TestManagerReadinessSnapshotRestoreDrillEvidence(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	const staleAfter = 24 * time.Hour
	currentAt := now.Add(-time.Hour)
	thresholdAt := now.Add(-staleAfter)
	staleAt := thresholdAt.Add(-time.Nanosecond)
	historyAt := now.Add(-2 * time.Hour)
	tests := []struct {
		name        string
		state       JobState
		wantTime    *time.Time
		wantCurrent bool
	}{
		{name: "missing drill"},
		{
			name: "failed drill",
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
				LastRestoreDrill:  readinessDrill("job", StatusFailed, currentAt, false),
			},
		},
		{
			name: "running drill",
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
				LastRestoreDrill:  readinessDrill("job", StatusRunning, currentAt, false),
			},
		},
		{
			name: "current completed drill",
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
				LastRestoreDrill:  readinessDrill("job", StatusCompleted, currentAt, false),
			},
			wantTime:    readinessTimePtr(currentAt),
			wantCurrent: true,
		},
		{
			name: "completed drill with cleanup warning remains valid",
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
				LastRestoreDrill:  readinessDrill("job", StatusCompleted, currentAt, true),
			},
			wantTime:    readinessTimePtr(currentAt),
			wantCurrent: true,
		},
		{
			name: "drill exactly at stale threshold",
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
				LastRestoreDrill:  readinessDrill("job", StatusCompleted, thresholdAt, false),
			},
			wantTime:    readinessTimePtr(thresholdAt),
			wantCurrent: true,
		},
		{
			name: "stale completed drill retains evidence time",
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
				LastRestoreDrill:  readinessDrill("job", StatusCompleted, staleAt, false),
			},
			wantTime: readinessTimePtr(staleAt),
		},
		{
			name: "latest failed drill retains historical success without current conclusion",
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
				LastRestoreDrill:  readinessDrill("job", StatusFailed, currentAt, false),
				RestoreDrillHistory: []*RestoreDrillResult{
					readinessDrill("job", StatusFailed, currentAt, false),
					readinessDrill("job", StatusCompleted, historyAt, false),
				},
			},
			wantTime: readinessTimePtr(historyAt),
		},
		{
			name: "local drill without successful backup is not current",
			state: JobState{
				LastRestoreDrill: readinessDrill("job", StatusCompleted, currentAt, false),
			},
			wantTime: readinessTimePtr(currentAt),
		},
		{
			name: "future completed drill is not evidence",
			state: JobState{
				LastSuccessfulRun: completedReadinessRun("job", now.Add(-time.Hour)),
				LastRestoreDrill:  readinessDrill("job", StatusCompleted, now.Add(time.Nanosecond), false),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := config.BackupJobConfig{ID: "job", Type: JobTypeLocal, RestoreDrillStaleAfter: staleAfter}
			manager := newReadinessTestManager(t, now, []config.BackupJobConfig{job})
			setReadinessJobState(t, manager, job.ID, tt.state)

			got := manager.ReadinessSnapshot()
			assertReadinessTime(t, "LastValidRestoreEvidenceAt", got.LastValidRestoreEvidenceAt, tt.wantTime)
			if got.HasCurrentRestoreEvidence != tt.wantCurrent {
				t.Fatalf("HasCurrentRestoreEvidence = %v, want %v; snapshot=%+v", got.HasCurrentRestoreEvidence, tt.wantCurrent, got)
			}
		})
	}
}

func TestManagerReadinessSnapshotRejectsRestoreDrillBindingDrift(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*Manager)
	}{
		{
			name: "job id mismatch",
			mutate: func(manager *Manager) {
				manager.state.Jobs["job"].LastRestoreDrill.JobID = "other-job"
			},
		},
		{
			name: "stored binding mismatch",
			mutate: func(manager *Manager) {
				manager.state.Jobs["job"].LastRestoreDrill.JobConfigBinding = "stale-binding"
			},
		},
		{
			name: "stored binding missing",
			mutate: func(manager *Manager) {
				manager.state.Jobs["job"].LastRestoreDrill.JobConfigBinding = ""
			},
		},
		{
			name: "current job configuration changed",
			mutate: func(manager *Manager) {
				job := manager.jobs["job"]
				job.Remote = "archive:replacement"
				manager.jobs["job"] = job
			},
		},
		{
			name: "historical drill binding mismatch",
			mutate: func(manager *Manager) {
				state := manager.state.Jobs["job"]
				historical := cloneRestoreDrillResultRaw(state.LastRestoreDrill)
				historical.JobConfigBinding = "stale-binding"
				state.LastRestoreDrill.Status = StatusFailed
				state.RestoreDrillHistory = []*RestoreDrillResult{historical}
				manager.state.Jobs["job"] = state
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := config.BackupJobConfig{ID: "job", Type: JobTypeRclone, Remote: "archive:current"}
			manager := newReadinessTestManager(t, now, []config.BackupJobConfig{job})
			drill := readinessDrill(job.ID, StatusCompleted, now.Add(-time.Hour), false)
			setReadinessJobState(t, manager, job.ID, JobState{
				LastRestoreDrill: drill,
			})
			tt.mutate(manager)

			got := manager.ReadinessSnapshot()
			if got.LastValidRestoreEvidenceAt != nil || got.HasCurrentRestoreEvidence {
				t.Fatalf("drifted restore drill evidence remained valid: %+v", got)
			}
		})
	}
}

func TestManagerReadinessSnapshotRejectsExplicitRestoreBindingDrift(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*Manager)
	}{
		{
			name: "restore job id mismatch",
			mutate: func(manager *Manager) {
				manager.state.Jobs["job"].LastRestore.JobID = "other-job"
			},
		},
		{
			name: "verify job id mismatch",
			mutate: func(manager *Manager) {
				manager.state.Jobs["job"].LastRestoreVerify.JobID = "other-job"
			},
		},
		{
			name: "restore binding mismatch",
			mutate: func(manager *Manager) {
				manager.state.Jobs["job"].LastRestore.JobConfigBinding = "stale-binding"
			},
		},
		{
			name: "verify binding mismatch",
			mutate: func(manager *Manager) {
				manager.state.Jobs["job"].LastRestoreVerify.JobConfigBinding = "stale-binding"
			},
		},
		{
			name: "current job configuration changed",
			mutate: func(manager *Manager) {
				job := manager.jobs["job"]
				job.Remote = "archive:replacement"
				manager.jobs["job"] = job
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := config.BackupJobConfig{ID: "job", Type: JobTypeRclone, Remote: "archive:current"}
			manager := newReadinessTestManager(t, now, []config.BackupJobConfig{job})
			restoreFinished := now.Add(-2 * time.Hour)
			setReadinessJobState(t, manager, job.ID, JobState{
				LastRestore: &RestoreResult{
					ID:         "restore",
					Status:     StatusCompleted,
					StartedAt:  restoreFinished.Add(-time.Minute),
					FinishedAt: readinessTimePtr(restoreFinished),
					TargetPath: "/restore/current",
				},
				LastRestoreVerify: &RestoreVerifyResult{
					ID:         "verify",
					Status:     StatusCompleted,
					StartedAt:  restoreFinished.Add(time.Minute),
					FinishedAt: readinessTimePtr(now.Add(-time.Hour)),
					TargetPath: "/restore/current",
				},
			})
			tt.mutate(manager)

			got := manager.ReadinessSnapshot()
			if got.LastValidRestoreEvidenceAt != nil || got.HasCurrentRestoreEvidence {
				t.Fatalf("drifted explicit restore evidence remained valid: %+v", got)
			}
		})
	}
}

func TestManagerReadinessSnapshotKeepsRestoreEvidenceAcrossNewBackup(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	manager := newReadinessTestManager(t, now, []config.BackupJobConfig{{
		ID:                     "job",
		Type:                   JobTypeLocal,
		RestoreDrillStaleAfter: 24 * time.Hour,
	}})
	drillAt := now.Add(-2 * time.Hour)
	newBackupAt := now.Add(-time.Hour)
	drill := readinessDrill("job", StatusCompleted, drillAt, false)
	drill.SnapshotPath = filepath.Join(manager.jobs["job"].Destination, "job", "snapshots", "older-snapshot")
	setReadinessJobState(t, manager, "job", JobState{
		LastRun:           completedReadinessRun("job", newBackupAt),
		LastSuccessfulRun: completedReadinessRun("job", newBackupAt),
		LastRestoreDrill:  drill,
	})

	got := manager.ReadinessSnapshot()
	assertReadinessTime(t, "LastValidRestoreEvidenceAt", got.LastValidRestoreEvidenceAt, readinessTimePtr(drillAt))
	if !got.HasCurrentHealthyBackup || !got.HasCurrentRestoreEvidence {
		t.Fatalf("new backup invalidated same-config restore evidence: %+v", got)
	}
}

func TestManagerReadinessSnapshotMatchingRestoreVerifyEvidence(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	const staleAfter = 30 * 24 * time.Hour
	tests := []struct {
		name         string
		verifyStatus string
		verifyTarget string
		verifyStart  time.Time
		verifyFinish time.Time
		overlaps     bool
		warnings     []string
		errorMessage string
		wantTime     *time.Time
		wantCurrent  bool
	}{
		{
			name:         "wrong target",
			verifyStatus: StatusCompleted,
			verifyTarget: "/restore/other",
			verifyStart:  now.Add(-time.Hour),
			verifyFinish: now.Add(-time.Hour + time.Minute),
		},
		{
			name:         "verify overlaps restore",
			verifyStatus: StatusCompleted,
			verifyTarget: "/restore/current",
			verifyStart:  now.Add(-3 * time.Hour),
			verifyFinish: now.Add(-time.Hour),
			overlaps:     true,
		},
		{
			name:         "running verify",
			verifyStatus: StatusRunning,
			verifyTarget: "/restore/current",
			verifyStart:  now.Add(-time.Hour),
			verifyFinish: now.Add(-time.Hour + time.Minute),
		},
		{
			name:         "failed verify",
			verifyStatus: StatusFailed,
			verifyTarget: "/restore/current",
			verifyStart:  now.Add(-time.Hour),
			verifyFinish: now.Add(-time.Hour + time.Minute),
		},
		{
			name:         "completed verify with warning",
			verifyStatus: StatusCompleted,
			verifyTarget: "/restore/current",
			verifyStart:  now.Add(-time.Hour),
			verifyFinish: now.Add(-time.Hour + time.Minute),
			warnings:     []string{"restore target differs from manifest"},
		},
		{
			name:         "completed verify with error text",
			verifyStatus: StatusCompleted,
			verifyTarget: "/restore/current",
			verifyStart:  now.Add(-time.Hour),
			verifyFinish: now.Add(-time.Hour + time.Minute),
			errorMessage: "verify incomplete",
		},
		{
			name:         "normal matching verify",
			verifyStatus: StatusCompleted,
			verifyTarget: "/restore/current",
			verifyStart:  now.Add(-time.Hour),
			verifyFinish: now.Add(-time.Hour + time.Minute),
			wantTime:     readinessTimePtr(now.Add(-time.Hour + time.Minute)),
			wantCurrent:  true,
		},
		{
			name:         "verify exactly at stale threshold",
			verifyStatus: StatusCompleted,
			verifyTarget: "/restore/current",
			verifyStart:  now.Add(-staleAfter - time.Minute),
			verifyFinish: now.Add(-staleAfter),
			wantTime:     readinessTimePtr(now.Add(-staleAfter)),
			wantCurrent:  true,
		},
		{
			name:         "stale matching verify retains evidence time",
			verifyStatus: StatusCompleted,
			verifyTarget: "/restore/current",
			verifyStart:  now.Add(-staleAfter - 2*time.Minute),
			verifyFinish: now.Add(-staleAfter - time.Nanosecond),
			wantTime:     readinessTimePtr(now.Add(-staleAfter - time.Nanosecond)),
		},
		{
			name:         "future matching verify is not evidence",
			verifyStatus: StatusCompleted,
			verifyTarget: "/restore/current",
			verifyStart:  now.Add(-time.Minute),
			verifyFinish: now.Add(time.Nanosecond),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restoreFinished := tt.verifyStart.Add(-time.Minute)
			if tt.overlaps {
				restoreFinished = tt.verifyStart.Add(time.Minute)
			}
			restoreStarted := restoreFinished.Add(-time.Minute)
			state := JobState{
				LastRestore: &RestoreResult{
					ID:         "restore",
					JobID:      "job",
					Status:     StatusCompleted,
					StartedAt:  restoreStarted,
					FinishedAt: readinessTimePtr(restoreFinished),
					TargetPath: "/restore/current",
				},
				LastRestoreVerify: &RestoreVerifyResult{
					ID:           "verify",
					JobID:        "job",
					Status:       tt.verifyStatus,
					StartedAt:    tt.verifyStart,
					FinishedAt:   readinessTimePtr(tt.verifyFinish),
					TargetPath:   tt.verifyTarget,
					Warnings:     tt.warnings,
					ErrorMessage: tt.errorMessage,
				},
			}
			job := config.BackupJobConfig{ID: "job", Type: JobTypeLocal}
			manager := newReadinessTestManager(t, now, []config.BackupJobConfig{job})
			setReadinessJobState(t, manager, job.ID, state)

			got := manager.ReadinessSnapshot()
			assertReadinessTime(t, "LastValidRestoreEvidenceAt", got.LastValidRestoreEvidenceAt, tt.wantTime)
			if got.HasCurrentRestoreEvidence != tt.wantCurrent {
				t.Fatalf("HasCurrentRestoreEvidence = %v, want %v; snapshot=%+v", got.HasCurrentRestoreEvidence, tt.wantCurrent, got)
			}
		})
	}
}

func TestManagerReadinessSnapshotUsesLatestRestoreEvidenceAndReturnsCopies(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	drillAt := now.Add(-2 * time.Hour)
	verifyAt := now.Add(-time.Hour)
	restoreFinished := verifyAt.Add(-2 * time.Minute)
	manager := newReadinessTestManager(t, now, []config.BackupJobConfig{
		{ID: "drill", Type: JobTypeLocal, RestoreDrillStaleAfter: 24 * time.Hour},
		{ID: "verify", Type: JobTypeLocal, RestoreDrillStaleAfter: 24 * time.Hour},
	})
	setReadinessJobState(t, manager, "drill", JobState{
		LastSuccessfulRun: completedReadinessRun("drill", now.Add(-time.Hour)),
		LastRestoreDrill:  readinessDrill("drill", StatusCompleted, drillAt, false),
	})
	setReadinessJobState(t, manager, "verify", JobState{
		LastRestore: &RestoreResult{
			ID:         "restore",
			JobID:      "verify",
			Status:     StatusCompleted,
			StartedAt:  restoreFinished.Add(-time.Minute),
			FinishedAt: readinessTimePtr(restoreFinished),
			TargetPath: "/restore/current",
		},
		LastRestoreVerify: &RestoreVerifyResult{
			ID:         "verify",
			JobID:      "verify",
			Status:     StatusCompleted,
			StartedAt:  restoreFinished.Add(time.Minute),
			FinishedAt: readinessTimePtr(verifyAt),
			TargetPath: "/restore/current",
		},
	})

	got := manager.ReadinessSnapshot()
	assertReadinessTime(t, "LastValidRestoreEvidenceAt", got.LastValidRestoreEvidenceAt, readinessTimePtr(verifyAt))
	if !got.HasCurrentRestoreEvidence {
		t.Fatalf("snapshot = %+v, want current restore evidence", got)
	}
	*got.LastValidRestoreEvidenceAt = time.Time{}
	again := manager.ReadinessSnapshot()
	assertReadinessTime(t, "LastValidRestoreEvidenceAt after caller mutation", again.LastValidRestoreEvidenceAt, readinessTimePtr(verifyAt))
}

func newReadinessTestManager(t *testing.T, now time.Time, jobs []config.BackupJobConfig) *Manager {
	t.Helper()
	tmpDir := secureBackupTestTempDir(t)
	storageRoot := filepath.Join(tmpDir, "storage")
	for i := range jobs {
		if jobs[i].Source == "" {
			jobs[i].Source = storageRoot
		}
		if jobs[i].Destination == "" {
			jobs[i].Destination = filepath.Join(tmpDir, "backups", jobs[i].ID)
		}
		if jobs[i].Type == JobTypeRestic && jobs[i].PasswordFile == "" {
			jobs[i].PasswordFile = filepath.Join(tmpDir, jobs[i].ID+".password")
			if err := os.WriteFile(jobs[i].PasswordFile, []byte("test-password\n"), 0o600); err != nil {
				t.Fatalf("WriteFile(restic password) error: %v", err)
			}
		}
		if jobs[i].Type == JobTypeRclone && jobs[i].ConfigFile == "" {
			jobs[i].ConfigFile = filepath.Join(tmpDir, jobs[i].ID+".rclone.conf")
			remoteName, err := rcloneRemoteName(jobs[i].Remote)
			if err != nil {
				t.Fatalf("rcloneRemoteName(%q) error: %v", jobs[i].Remote, err)
			}
			if err := os.WriteFile(jobs[i].ConfigFile, []byte("["+remoteName+"]\ntype = local\n"), 0o600); err != nil {
				t.Fatalf("WriteFile(rclone config) error: %v", err)
			}
		}
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: storageRoot,
		Jobs:        jobs,
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	manager.now = func() time.Time { return now }
	return manager
}

func setReadinessJobState(t *testing.T, manager *Manager, jobID string, state JobState) {
	t.Helper()
	job, ok := manager.jobs[jobID]
	if !ok {
		t.Fatalf("readiness test job %q not found", jobID)
	}
	binding, err := jobConfigEvidenceBinding(job, manager.storageRoot, manager.configPath)
	if err != nil {
		t.Fatalf("jobConfigEvidenceBinding() error: %v", err)
	}
	bindRun := func(result *RunResult) {
		if result == nil {
			return
		}
		result.JobID = job.ID
		result.JobConfigBinding = binding
		result.Source = effectiveSource(job, manager.storageRoot)
		result.Destination = backupTarget(job)
		if job.Type != JobTypeLocal || result.Status != StatusCompleted {
			return
		}
		result.SnapshotPath = filepath.Join(job.Destination, job.ID, "snapshots", result.ID)
		result.ManifestPath = filepath.Join(result.SnapshotPath, manifestFileName)
		if err := os.MkdirAll(result.SnapshotPath, 0o700); err != nil {
			t.Fatalf("MkdirAll(snapshot) error: %v", err)
		}
		createdAt, err := parseRunID(result.ID)
		if err != nil {
			t.Fatalf("parseRunID(%q) error: %v", result.ID, err)
		}
		manifest := Manifest{
			Version:     manifestVersion,
			JobID:       job.ID,
			RunID:       result.ID,
			Source:      result.Source,
			CreatedAt:   createdAt,
			FileCount:   result.FileCount,
			TotalBytes:  result.TotalBytes,
			Entries:     []ManifestEntry{},
			Directories: testManifestDirectories(),
		}
		if result.ConfigIncluded {
			manifest.ConfigPath = "/config.toml"
		}
		if err := writeJSONFile(result.ManifestPath, manifest, 0o600); err != nil {
			t.Fatalf("writeJSONFile(manifest) error: %v", err)
		}
		digest, observation, err := manifestEvidenceDigest(context.Background(), result.ManifestPath, 0, result)
		if err != nil {
			t.Fatalf("manifestEvidenceDigest() error: %v", err)
		}
		result.ManifestSize = observation.size
		result.ManifestDigest = digest
	}
	bindRun(state.LastRun)
	bindRun(state.LastSuccessfulRun)
	bindDrill := func(result *RestoreDrillResult) {
		if result == nil {
			return
		}
		result.JobID = job.ID
		result.JobConfigBinding = binding
	}
	bindDrill(state.LastRestoreDrill)
	for _, result := range state.RestoreDrillHistory {
		bindDrill(result)
	}
	if state.LastRestore != nil {
		state.LastRestore.JobID = job.ID
		state.LastRestore.JobConfigBinding = binding
	}
	if state.LastRestoreVerify != nil {
		state.LastRestoreVerify.JobID = job.ID
		state.LastRestoreVerify.JobConfigBinding = binding
	}
	manager.state.Jobs[jobID] = state
}

func completedReadinessRun(jobID string, finishedAt time.Time) *RunResult {
	startedAt := finishedAt.Add(-time.Minute)
	return &RunResult{
		ID:         formatRunID(finishedAt),
		JobID:      jobID,
		Status:     StatusCompleted,
		StartedAt:  startedAt,
		FinishedAt: readinessTimePtr(finishedAt),
	}
}

func failedReadinessRun(jobID string, finishedAt time.Time) *RunResult {
	startedAt := finishedAt.Add(-time.Minute)
	return &RunResult{
		ID:         jobID + "-failed",
		JobID:      jobID,
		Status:     StatusFailed,
		StartedAt:  startedAt,
		FinishedAt: readinessTimePtr(finishedAt),
	}
}

func readinessDrill(jobID, status string, finishedAt time.Time, warning bool) *RestoreDrillResult {
	startedAt := finishedAt.Add(-time.Minute)
	result := &RestoreDrillResult{
		ID:         jobID + "-drill-" + status,
		JobID:      jobID,
		Status:     status,
		StartedAt:  startedAt,
		FinishedAt: readinessTimePtr(finishedAt),
		Warning:    warning,
	}
	if warning {
		result.Warnings = []string{restoreDrillCleanupWarning}
	}
	return result
}

func readinessTimePtr(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func assertReadinessTime(t *testing.T, field string, got, want *time.Time) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("%s = %v, want %v", field, got, want)
		}
		return
	}
	if !got.Equal(*want) {
		t.Fatalf("%s = %s, want %s", field, got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}
