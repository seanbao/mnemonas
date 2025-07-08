import { describe, expect, it } from 'vitest'
import {
  getDiskHealthDeviceDisplayMessage,
  getDiskHealthGenericMessage,
  getDiskHealthReportDisplayMessage,
  getDiskHealthStatusLabel,
} from './diskHealthMessages'

describe('diskHealthMessages', () => {
  it('maps disk health statuses to stable labels', () => {
    expect(getDiskHealthStatusLabel('ok')).toBe('正常')
    expect(getDiskHealthStatusLabel('critical')).toBe('严重异常')
    expect(getDiskHealthStatusLabel('warning')).toBe('提醒')
    expect(getDiskHealthStatusLabel('unavailable')).toBe('不可用')
    expect(getDiskHealthStatusLabel(' disabled ')).toBe('未启用')
    expect(getDiskHealthStatusLabel('UNKNOWN')).toBe('未知')
    expect(getDiskHealthStatusLabel('backend raw status')).toBe('未知')
  })

  it('maps report-level backend messages without exposing raw diagnostics', () => {
    expect(getDiskHealthReportDisplayMessage('one or more disks require immediate attention', 'critical'))
      .toBe('磁盘健康严重异常，请尽快备份并检查 SMART、温度、磨损和设备连接状态。')
    expect(getDiskHealthReportDisplayMessage('disk health status is unavailable', 'unavailable'))
      .toBe('磁盘健康状态暂不可用，请检查 smartctl、设备路径和权限。')
    expect(getDiskHealthReportDisplayMessage('backend raw report message', 'warning'))
      .toBe(getDiskHealthGenericMessage('warning'))
  })

  it('maps device-level backend messages and falls back by status', () => {
    expect(getDiskHealthDeviceDisplayMessage('SMART self-assessment failed', 'critical'))
      .toBe('SMART 自检未通过，请尽快备份并检查磁盘。')
    expect(getDiskHealthDeviceDisplayMessage('temperature 61 C reached critical threshold 60 C', 'critical'))
      .toBe('磁盘温度 61 C 已达到严重阈值 60 C。')
    expect(getDiskHealthDeviceDisplayMessage('media wear used 98% reached warning threshold 90%', 'warning'))
      .toBe('介质磨损 98% 已达到提醒阈值 90%。')
    expect(getDiskHealthDeviceDisplayMessage('smart probe failed: permission denied', 'unavailable'))
      .toBe('SMART 检测命令执行失败，请检查 smartctl 权限和设备路径。')
    expect(getDiskHealthDeviceDisplayMessage('backend raw device detail', 'warning'))
      .toBe(getDiskHealthGenericMessage('warning'))
  })

  it('returns undefined when a device message is missing', () => {
    expect(getDiskHealthDeviceDisplayMessage(undefined, 'ok')).toBeUndefined()
    expect(getDiskHealthDeviceDisplayMessage('   ', 'ok')).toBeUndefined()
  })
})
