import { describe, expect, it } from 'vitest'
import {
  areDiskStatsAvailable,
  areStorageStatsAvailable,
  clampUsagePercent,
  formatFilesystemType,
  getFilesystemIntegrityStatus,
  formatUsagePercent,
  getDiskSpaceStatus,
} from './storageStats'

describe('storage stats helpers', () => {
  it('treats explicit CAS stats availability as authoritative', () => {
    expect(areStorageStatsAvailable({
      storageStatsAvailable: false,
      totalSize: 1024,
      totalObjects: 1,
    })).toBe(false)
    expect(areStorageStatsAvailable({
      storageStatsAvailable: true,
    })).toBe(true)
  })

  it('infers CAS stats availability from legacy numeric fields', () => {
    expect(areStorageStatsAvailable({ totalSize: 1024 })).toBe(true)
    expect(areStorageStatsAvailable({})).toBe(false)
    expect(areStorageStatsAvailable(undefined)).toBe(false)
  })

  it('treats explicit disk stats availability as authoritative', () => {
    expect(areDiskStatsAvailable({
      diskStatsAvailable: false,
      diskTotal: 2048,
      diskUsed: 1024,
    })).toBe(false)
    expect(areDiskStatsAvailable({
      diskStatsAvailable: true,
    })).toBe(true)
  })

  it('infers disk stats availability from legacy disk fields', () => {
    expect(areDiskStatsAvailable({ diskTotal: 2048 })).toBe(true)
    expect(areDiskStatsAvailable({ diskMountPoint: '/srv/mnemonas' })).toBe(true)
    expect(areDiskStatsAvailable({})).toBe(false)
    expect(areDiskStatsAvailable(undefined)).toBe(false)
  })

  it('clamps disk usage percentages for display', () => {
    expect(clampUsagePercent(0.25)).toBe(25)
    expect(clampUsagePercent(2)).toBe(100)
    expect(clampUsagePercent(-1)).toBe(0)
    expect(clampUsagePercent(undefined)).toBeUndefined()
    expect(formatUsagePercent(0.255)).toBe('25.5%')
    expect(formatUsagePercent(undefined)).toBe('--')
  })

  it('formats filesystem types for compact UI labels', () => {
    expect(formatFilesystemType('zfs')).toBe('ZFS')
    expect(formatFilesystemType('btrfs')).toBe('BTRFS')
    expect(formatFilesystemType('ext4')).toBe('EXT4')
    expect(formatFilesystemType('ext')).toBe('EXT 系列')
    expect(formatFilesystemType('fuse.sshfs')).toBe('fuse.sshfs')
    expect(formatFilesystemType('unknown')).toBe('未知')
    expect(formatFilesystemType(undefined)).toBe('未知')
  })

  it('classifies filesystem integrity support for storage safety hints', () => {
    expect(getFilesystemIntegrityStatus('zfs', true).level).toBe('supported')
    expect(getFilesystemIntegrityStatus('unknown', false).level).toBe('unknown')
    expect(getFilesystemIntegrityStatus('tmpfs', false).level).toBe('volatile')
    expect(getFilesystemIntegrityStatus('nfs4', false).level).toBe('remote')
    expect(getFilesystemIntegrityStatus('fuse.sshfs', false).level).toBe('remote')
    expect(getFilesystemIntegrityStatus('ext4', false).level).toBe('limited')
  })

  it('classifies disk space status from usage and available capacity', () => {
    expect(getDiskSpaceStatus({
      diskStatsAvailable: true,
      diskUsageRatio: 0.35,
      diskAvailable: 80 * 1024 * 1024 * 1024,
    }).level).toBe('normal')
    expect(getDiskSpaceStatus({
      diskStatsAvailable: true,
      diskUsageRatio: 0.91,
      diskAvailable: 20 * 1024 * 1024 * 1024,
    }).level).toBe('warning')
    expect(getDiskSpaceStatus({
      diskStatsAvailable: true,
      diskUsageRatio: 0.4,
      diskAvailable: 9 * 1024 * 1024 * 1024,
    }).level).toBe('warning')
    expect(getDiskSpaceStatus({
      diskStatsAvailable: true,
      diskUsageRatio: 0.96,
      diskAvailable: 20 * 1024 * 1024 * 1024,
    }).level).toBe('critical')
    expect(getDiskSpaceStatus({
      diskStatsAvailable: true,
      diskUsageRatio: 0.4,
      diskAvailable: 512 * 1024 * 1024,
    }).level).toBe('critical')
    expect(getDiskSpaceStatus({ diskStatsAvailable: false }).level).toBe('unknown')
    expect(getDiskSpaceStatus({ diskStatsAvailable: true }).level).toBe('unknown')
  })
})
