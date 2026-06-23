import type { BackupJob } from '@/api/files'

type BackupJobPollingState = Pick<BackupJob, 'disabled' | 'running' | 'schedule_interval'> & { last_run?: unknown }

function hasAutomaticBackupSchedule(job: BackupJobPollingState): boolean {
  const interval = job.schedule_interval?.trim()
  if (!interval) {
    return false
  }
  return !/^(?:0+(?:\.0+)?(?:ns|us|µs|ms|s|m|h))+$/u.test(interval)
}

export function shouldPollBackupJobs(jobs: BackupJobPollingState[] | undefined): boolean {
  return jobs?.some((job) => (
    job.running
    || (!job.disabled && hasAutomaticBackupSchedule(job) && !job.last_run)
  )) ?? false
}
