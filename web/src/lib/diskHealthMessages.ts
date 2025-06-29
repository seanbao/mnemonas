const diskHealthDeviceMessagesByBackendMessage: Record<string, string> = {
  'device is healthy': '设备健康。',
  'device is missing': '设备未找到，请检查设备路径或连接状态。',
  'device serial does not match configured serial': '设备序列号与配置不一致，请确认磁盘是否被替换。',
  'device serial is unavailable': '无法读取设备序列号，请确认 smartctl 权限和设备支持情况。',
  'smart self-assessment failed': 'SMART 自检未通过，请尽快备份并检查磁盘。',
  'smart self-assessment is unavailable': '无法读取 SMART 自检结果，请确认设备支持情况。',
  'smart probe returned no json': 'SMART 检测未返回 JSON，请确认 smartctl 支持 JSON 输出。',
}

const diskHealthReportMessagesByBackendMessage: Record<string, string> = {
  'all configured disks are healthy': '磁盘健康正常。',
  'one or more disks require immediate attention': '磁盘健康严重异常，请尽快备份并检查 SMART、温度、磨损和设备连接状态。',
  'one or more disks need attention': '磁盘健康存在提醒，请检查 SMART、温度、磨损和设备连接状态。',
  'disk health status is unavailable': '磁盘健康状态暂不可用，请检查 smartctl、设备路径和权限。',
  'disk health checks are disabled': '磁盘健康检查未启用。',
}

export function getDiskHealthStatusLabel(status: string): string {
  const normalized = status.trim().toLowerCase()
  const labels: Record<string, string> = {
    ok: '正常',
    critical: '严重异常',
    warning: '提醒',
    unavailable: '不可用',
    disabled: '未启用',
    unknown: '未知',
  }
  return labels[normalized] ?? status
}

export function getDiskHealthGenericMessage(status: string): string {
  switch (status.trim().toLowerCase()) {
    case 'critical':
      return '磁盘健康严重异常，请尽快备份并检查 SMART、温度、磨损和设备连接状态。'
    case 'warning':
    case 'unavailable':
      return '磁盘健康存在提醒，请检查 SMART、温度、磨损和设备连接状态。'
    case 'ok':
      return '磁盘健康正常。'
    default:
      return '磁盘健康状态已记录，请检查设备状态。'
  }
}

export function getDiskHealthReportDisplayMessage(value: string, status: string): string {
  const normalized = value.trim().toLowerCase()
  return diskHealthReportMessagesByBackendMessage[normalized] ?? getDiskHealthGenericMessage(status)
}

export function getDiskHealthDeviceDisplayMessage(value: string | undefined, status: string): string | undefined {
  const rawMessage = value?.trim()
  if (!rawMessage) {
    return undefined
  }

  const normalized = rawMessage.toLowerCase()
  const exactMessage = diskHealthDeviceMessagesByBackendMessage[normalized]
  if (exactMessage) {
    return exactMessage
  }

  let match = rawMessage.match(/^device stat failed:/i)
  if (match) {
    return '无法读取设备状态，请检查设备路径和权限。'
  }

  match = rawMessage.match(/^smart probe failed:/i)
  if (match) {
    return 'SMART 检测命令执行失败，请检查 smartctl 权限和设备路径。'
  }

  match = rawMessage.match(/^smart probe returned invalid JSON:/i)
  if (match) {
    return 'SMART 检测返回数据无效，请检查 smartctl 输出。'
  }

  match = rawMessage.match(/^smart probe returned warning status:/i)
  if (match) {
    return 'SMART 检测返回警告，请检查设备状态。'
  }

  match = rawMessage.match(/^NVMe critical warning bitmask 0x[0-9a-f]+ reported$/i)
  if (match) {
    return 'NVMe 报告严重健康警告，请尽快备份并检查磁盘。'
  }

  match = rawMessage.match(/^available spare (\d+)% is at or below threshold (\d+)%$/i)
  if (match) {
    return `NVMe 可用备用空间 ${match[1]}% 已低于或达到阈值 ${match[2]}%。`
  }

  match = rawMessage.match(/^temperature (\d+) C reached (critical|warning) threshold (\d+) C$/i)
  if (match) {
    const level = match[2].toLowerCase() === 'critical' ? '严重' : '提醒'
    return `磁盘温度 ${match[1]} C 已达到${level}阈值 ${match[3]} C。`
  }

  match = rawMessage.match(/^media wear used (\d+)% reached (critical|warning) threshold (\d+)%$/i)
  if (match) {
    const level = match[2].toLowerCase() === 'critical' ? '严重' : '提醒'
    return `介质磨损 ${match[1]}% 已达到${level}阈值 ${match[3]}%。`
  }

  match = rawMessage.match(/^media error count is (\d+)$/i)
  if (match) {
    return `发现 ${match[1]} 个介质错误，请尽快备份并检查磁盘。`
  }

  return getDiskHealthGenericMessage(status)
}
