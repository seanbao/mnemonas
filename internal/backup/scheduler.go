package backup

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
)

// StartScheduler starts the background interval scheduler. It is idempotent.
func (m *Manager) StartScheduler(ctx context.Context) bool {
	if !m.hasScheduledWork() {
		return false
	}

	m.mu.Lock()
	if m.schedulerStarted || m.closed {
		m.mu.Unlock()
		return false
	}
	schedulerCtx, cancel := context.WithCancel(ctx)
	m.schedulerStarted = true
	pollInterval := m.schedulerPoll
	m.schedulerCancel = cancel
	m.schedulerDone = make(chan struct{})
	done := m.schedulerDone
	m.mu.Unlock()

	go func() {
		defer close(done)
		_ = m.RunDueJobs(schedulerCtx)
		_, _ = m.SendRestoreDrillReminders(schedulerCtx)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-schedulerCtx.Done():
				return
			case <-ticker.C:
				_ = m.RunDueJobs(schedulerCtx)
				_, _ = m.SendRestoreDrillReminders(schedulerCtx)
			}
		}
	}()
	return true
}

func (m *Manager) hasScheduledWork() bool {
	return m.hasScheduledJobs() || m.hasRestoreDrillReminderJobs()
}

func (m *Manager) hasScheduledJobs() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if !job.Disabled && job.ScheduleInterval > 0 {
			return true
		}
	}
	return false
}

func (m *Manager) hasRestoreDrillReminderJobs() bool {
	if m.notifier == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if !job.Disabled && effectiveRestoreDrillStaleAfter(job) > 0 {
			return true
		}
	}
	return false
}

// RunDueJobs runs scheduled jobs that are due at the manager's current time.
func (m *Manager) RunDueJobs(ctx context.Context) []ScheduledRunResult {
	if !m.beginManagerOperation() {
		return nil
	}
	defer m.endManagerOperation()

	now := m.now()
	dueJobs := m.dueJobs(now)
	results := make([]ScheduledRunResult, 0, len(dueJobs))

	for _, dueJob := range dueJobs {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		result := ScheduledRunResult{
			JobID: dueJob.job.ID,
			DueAt: dueJob.dueAt,
		}
		runResult, err := m.runJobWithTrigger(ctx, dueJob.job.ID, "scheduled")
		if err != nil {
			result.Error = sanitizeBackupMessageForAPI(err.Error())
			result.Result = runResult
		} else {
			result.Result = runResult
		}
		results = append(results, result)
		if errors.Is(err, ErrManagerClosed) || errors.Is(err, ErrBackupStateNamespaceChanged) {
			break
		}
	}

	return results
}

type dueBackupJob struct {
	job   config.BackupJobConfig
	dueAt time.Time
}

func (m *Manager) dueJobs(now time.Time) []dueBackupJob {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]string, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	jobs := make([]dueBackupJob, 0, len(ids))
	for _, id := range ids {
		job := m.jobs[id]
		if job.Disabled || job.ScheduleInterval <= 0 {
			continue
		}
		if !isWithinScheduleWindow(job, now) {
			continue
		}
		if _, running := m.running[id]; running {
			continue
		}
		nextRunAt := m.nextRunAtLocked(job, m.state.Jobs[id], now)
		if nextRunAt == nil || nextRunAt.After(now) {
			continue
		}
		jobs = append(jobs, dueBackupJob{job: cloneJob(job), dueAt: *nextRunAt})
	}
	return jobs
}

func (m *Manager) nextRunAt(jobID string) *time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[jobID]
	if !ok {
		return nil
	}
	return m.nextRunAtLocked(job, m.state.Jobs[jobID], m.now())
}

func (m *Manager) nextRunAtLocked(job config.BackupJobConfig, state JobState, now time.Time) *time.Time {
	if job.Disabled || job.ScheduleInterval <= 0 {
		return nil
	}
	base := state.LastScheduledRunAt
	if state.LastRun != nil {
		lastRunTime := state.LastRun.StartedAt
		if state.LastRun.FinishedAt != nil {
			lastRunTime = *state.LastRun.FinishedAt
		}
		if base == nil || lastRunTime.After(*base) {
			base = &lastRunTime
		}
	}
	if base == nil {
		next := now.UTC()
		return adjustNextRunForScheduleWindow(job, next, now)
	}
	next := base.UTC().Add(job.ScheduleInterval)
	return adjustNextRunForScheduleWindow(job, next, now)
}

func adjustNextRunForScheduleWindow(job config.BackupJobConfig, next time.Time, now time.Time) *time.Time {
	if !hasScheduleWindow(job) {
		return &next
	}
	if isWithinScheduleWindow(job, next) {
		return &next
	}
	nowUTC := now.UTC()
	if next.Before(nowUTC) {
		adjusted := nextScheduleWindowStart(job, now)
		return &adjusted
	}
	adjusted := nextScheduleWindowStart(job, next)
	return &adjusted
}

func hasScheduleWindow(job config.BackupJobConfig) bool {
	return strings.TrimSpace(job.ScheduleWindowStart) != "" && strings.TrimSpace(job.ScheduleWindowEnd) != ""
}

func isWithinScheduleWindow(job config.BackupJobConfig, when time.Time) bool {
	start, end, ok := scheduleWindowMinutes(job)
	if !ok {
		return true
	}
	minute := minuteOfDay(when)
	if start < end {
		return minute >= start && minute < end
	}
	return minute >= start || minute < end
}

func nextScheduleWindowStart(job config.BackupJobConfig, after time.Time) time.Time {
	start, end, ok := scheduleWindowMinutes(job)
	if !ok {
		return after.UTC()
	}
	local := after.In(time.Local)
	minute := minuteOfDay(local)
	startAt := time.Date(local.Year(), local.Month(), local.Day(), start/60, start%60, 0, 0, time.Local)
	if start < end {
		if minute < start {
			return startAt.UTC()
		}
		if minute >= end {
			return startAt.AddDate(0, 0, 1).UTC()
		}
		return startAt.UTC()
	}
	if minute >= start {
		return startAt.UTC()
	}
	if minute < end {
		return startAt.AddDate(0, 0, -1).UTC()
	}
	return startAt.UTC()
}

func scheduleWindowMinutes(job config.BackupJobConfig) (int, int, bool) {
	start, ok := parseScheduleWindowClock(job.ScheduleWindowStart)
	if !ok {
		return 0, 0, false
	}
	end, ok := parseScheduleWindowClock(job.ScheduleWindowEnd)
	if !ok || start == end {
		return 0, 0, false
	}
	return start, end, true
}

func parseScheduleWindowClock(value string) (int, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

func minuteOfDay(when time.Time) int {
	local := when.In(time.Local)
	return local.Hour()*60 + local.Minute()
}
