import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  acknowledgeSetup,
  deferSetup,
  getSetupReadiness,
  getSetupStatus,
  SetupError,
  type SetupReadiness,
} from './setup'

vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

const invalidResponseMessage = '服务器返回了无效的数据'

function createReadiness(overrides: Partial<SetupReadiness> = {}): SetupReadiness {
  return {
    lifecycle: 'pending',
    overall_status: 'action_required',
    prompt: true,
    generated_at: '2026-07-13T01:02:03Z',
    can_complete: false,
    can_defer: true,
    required: { completed: 4, total: 6 },
    recommended: { completed: 0, total: 4 },
    checks: [
      {
        id: 'admin_access',
        requirement: 'required',
        status: 'complete',
        deferrable: false,
        title: '管理员访问可用',
        message: '至少有一个启用中的管理员账号。',
        action: 'manage_users',
      },
      {
        id: 'bootstrap_credential',
        requirement: 'required',
        status: 'complete',
        deferrable: false,
        title: '修改初始密码',
        message: '所有管理员均已修改初始密码。',
        action: 'change_password',
      },
      {
        id: 'initial_password_file',
        requirement: 'required',
        status: 'complete',
        deferrable: false,
        title: '清理初始密码文件',
        message: '服务器上没有遗留初始密码文件。',
        action: 'change_password',
      },
      {
        id: 'security_baseline',
        requirement: 'required',
        status: 'complete',
        deferrable: false,
        title: '满足安全基线',
        message: '安全基线没有阻断项。',
        action: 'review_security',
      },
      {
        id: 'backup_job',
        requirement: 'required',
        status: 'incomplete',
        deferrable: true,
        title: '创建备份',
        message: '尚未创建已启用的备份任务。',
        action: 'create_backup',
      },
      {
        id: 'backup_success',
        requirement: 'required',
        status: 'incomplete',
        deferrable: true,
        title: '完成首次备份',
        message: '尚无当前有效的成功备份。',
        action: 'run_backup',
      },
      {
        id: 'admin_redundancy',
        requirement: 'recommended',
        status: 'incomplete',
        deferrable: false,
        title: '准备备用管理员',
        message: '建议再准备一个启用中的管理员账号。',
        action: 'manage_users',
      },
      {
        id: 'backup_schedule',
        requirement: 'recommended',
        status: 'incomplete',
        deferrable: false,
        title: '启用自动备份',
        message: '建议为备份任务启用自动计划。',
        action: 'create_backup',
      },
      {
        id: 'restore_verification',
        requirement: 'recommended',
        status: 'incomplete',
        deferrable: false,
        title: '验证恢复流程',
        message: '当前没有可用的恢复验证记录。',
        action: 'run_restore_drill',
      },
      {
        id: 'security_recommendations',
        requirement: 'recommended',
        status: 'incomplete',
        deferrable: false,
        title: '处理安全建议',
        message: '安全自检仍有建议处理的项目。',
        action: 'review_security',
      },
    ],
    summary: {
      auth_enabled: true,
      active_admin_count: 1,
      password_change_required_admin_count: 0,
      initial_password_file: 'missing',
      enabled_backup_job_count: 0,
      latest_backup_success_at: '2026-07-12T08:00:00+08:00',
      latest_restore_verification_at: '2026-07-12T09:00:00.123Z',
      security_status: 'warning',
      security_blocking_check_ids: [],
    },
    ...overrides,
  }
}

function successResponse(readiness = createReadiness()): Response {
  return {
    ok: true,
    status: 200,
    json: () => Promise.resolve({ success: true, data: readiness }),
  } as Response
}

describe('Setup API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    global.fetch = vi.fn() as typeof fetch
  })

  it('keeps the public first-run status contract', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        is_first_run: true,
        auth_enabled: true,
        share_enabled: false,
        webdav_enabled: true,
        webdav_auth_type: 'basic',
        allow_unsafe_no_auth: false,
      }),
    } as Response)

    await expect(getSetupStatus()).resolves.toMatchObject({
      is_first_run: true,
      share_enabled: false,
      webdav_auth_type: 'basic',
    })
    expect(global.fetch).toHaveBeenCalledWith('/api/v1/setup/', { cache: 'no-store' })
  })

  it('validates the public first-run status and forwards its signal', async () => {
    const controller = new AbortController()
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        is_first_run: true,
        auth_enabled: true,
        share_enabled: 'false',
        webdav_enabled: true,
        webdav_auth_type: 'basic',
      }),
    } as Response)

    await expect(getSetupStatus({ signal: controller.signal })).rejects.toThrow(invalidResponseMessage)
    expect(global.fetch).toHaveBeenCalledWith('/api/v1/setup/', {
      cache: 'no-store',
      signal: controller.signal,
    })
  })

  it.each([
    { success: false },
    { success: true, is_first_run: true },
  ])('rejects a malformed public setup response', async (body) => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve(body),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects unreadable public setup JSON', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.reject(new SyntaxError('bad json')),
    } as Response)

    await expect(getSetupStatus()).rejects.toThrow(invalidResponseMessage)
  })

  it('passes through legacy public setup errors', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: false,
      status: 500,
      headers: new Headers(),
      json: () => Promise.resolve({ error: 'setup status failed' }),
    } as unknown as Response)

    await expect(getSetupStatus()).rejects.toMatchObject({
      name: 'SetupError',
      message: 'setup status failed',
      status: 500,
    })
  })

  it('returns a strictly validated readiness envelope with an abort signal', async () => {
    const controller = new AbortController()
    vi.mocked(authFetch).mockResolvedValueOnce(successResponse())

    const result = await getSetupReadiness({ signal: controller.signal })

    expect(result).toEqual(createReadiness())
    expect(authFetch).toHaveBeenCalledWith('/api/v1/setup/readiness', {
      cache: 'no-store',
      signal: controller.signal,
    })
  })

  it.each([
    ['lifecycle enum', (value: SetupReadiness) => { value.lifecycle = 'waiting' as SetupReadiness['lifecycle'] }],
    ['overall status enum', (value: SetupReadiness) => { value.overall_status = 'unknown' as SetupReadiness['overall_status'] }],
    ['prompt flag', (value: SetupReadiness) => { value.prompt = 'yes' as unknown as boolean }],
    ['generated timestamp', (value: SetupReadiness) => { value.generated_at = 'yesterday' }],
    ['calendar date', (value: SetupReadiness) => { value.generated_at = '2026-02-30T01:02:03Z' }],
    ['hour', (value: SetupReadiness) => { value.generated_at = '2026-07-13T24:02:03Z' }],
    ['minute', (value: SetupReadiness) => { value.generated_at = '2026-07-13T01:60:03Z' }],
    ['second', (value: SetupReadiness) => { value.generated_at = '2026-07-13T01:02:60Z' }],
    ['time zone offset', (value: SetupReadiness) => { value.generated_at = '2026-07-13T01:02:03+24:00' }],
    ['time zone offset minute', (value: SetupReadiness) => { value.generated_at = '2026-07-13T01:02:03+08:60' }],
    ['optional timestamp', (value: SetupReadiness) => { value.completed_at = '2026-07-13' }],
    ['completion capability', (value: SetupReadiness) => { value.can_complete = 'yes' as unknown as boolean }],
    ['progress counts', (value: SetupReadiness) => { value.required = { completed: 2, total: 1 } }],
    ['progress count evidence mismatch', (value: SetupReadiness) => { value.required = { completed: 3, total: 6 } }],
    ['missing fixed check', (value: SetupReadiness) => { value.checks.pop() }],
    ['fixed check order', (value: SetupReadiness) => { [value.checks[0], value.checks[1]] = [value.checks[1], value.checks[0]] }],
    ['fixed check id', (value: SetupReadiness) => { value.checks[0].id = 'backup_job' }],
    ['check requirement', (value: SetupReadiness) => { value.checks[0].requirement = 'optional' as SetupReadiness['checks'][number]['requirement'] }],
    ['check status', (value: SetupReadiness) => { value.checks[0].status = 'pending' as SetupReadiness['checks'][number]['status'] }],
    ['check deferrable flag', (value: SetupReadiness) => { value.checks[0].deferrable = 'yes' as unknown as boolean }],
    ['check action', (value: SetupReadiness) => { value.checks[0].action = 'open_page' as SetupReadiness['checks'][number]['action'] }],
    ['check action binding', (value: SetupReadiness) => { value.checks[0].action = 'run_backup' }],
    ['summary count', (value: SetupReadiness) => { value.summary.active_admin_count = -1 }],
    ['password-change admin count', (value: SetupReadiness) => { value.summary.password_change_required_admin_count = 2 }],
    ['initial password file status', (value: SetupReadiness) => { value.summary.initial_password_file = 'unknown' as SetupReadiness['summary']['initial_password_file'] }],
    ['security status', (value: SetupReadiness) => { value.summary.security_status = 'unknown' as SetupReadiness['summary']['security_status'] }],
    ['security check ids', (value: SetupReadiness) => { value.summary.security_blocking_check_ids = [''] }],
    ['duplicate security check ids', (value: SetupReadiness) => { value.summary.security_blocking_check_ids = ['public_access', 'public_access'] }],
    ['unsorted security check ids', (value: SetupReadiness) => { value.summary.security_blocking_check_ids = ['tls', 'public_access'] }],
    ['ready status with incomplete requirements', (value: SetupReadiness) => { value.overall_status = 'ready' }],
    ['completion capability with incomplete requirements', (value: SetupReadiness) => { value.can_complete = true }],
    ['completed lifecycle without timestamp', (value: SetupReadiness) => {
      value.lifecycle = 'completed'
      value.prompt = false
      value.can_defer = false
    }],
    ['completed lifecycle prompt', (value: SetupReadiness) => {
      value.lifecycle = 'completed'
      value.completed_at = value.generated_at
      value.can_defer = false
    }],
    ['deferred lifecycle without future timestamp', (value: SetupReadiness) => {
      value.lifecycle = 'deferred'
      value.prompt = false
    }],
    ['initial password evidence mismatch', (value: SetupReadiness) => {
      value.summary.initial_password_file = 'present'
      value.summary.security_status = 'block'
      value.summary.security_blocking_check_ids = ['initial_password_file']
    }],
    ['security pass with blocking evidence', (value: SetupReadiness) => {
      value.summary.security_status = 'pass'
      value.summary.security_blocking_check_ids = ['public_access']
    }],
    ['security warning with blocking evidence', (value: SetupReadiness) => {
      value.summary.security_blocking_check_ids = ['public_access']
    }],
    ['security unavailable evidence mismatch', (value: SetupReadiness) => {
      value.summary.security_status = 'unavailable'
      value.checks[3].status = 'unavailable'
      value.required.completed = 3
      value.overall_status = 'unavailable'
      value.can_defer = false
    }],
    ['backup availability evidence mismatch', (value: SetupReadiness) => {
      value.checks[8].status = 'unavailable'
    }],
    ['backup job count evidence mismatch', (value: SetupReadiness) => {
      value.summary.enabled_backup_job_count = 1
    }],
  ])('rejects an invalid readiness %s', async (_label, mutate) => {
    const readiness = createReadiness()
    mutate(readiness)
    vi.mocked(authFetch).mockResolvedValueOnce(successResponse(readiness))

    await expect(getSetupReadiness()).rejects.toThrow(invalidResponseMessage)
  })

  it.each([
    undefined,
    null,
    {},
    { success: false, data: createReadiness() },
    { success: true },
    { success: true, data: [] },
  ])('rejects a malformed readiness envelope %#', async (body) => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve(body),
    } as Response)

    await expect(getSetupReadiness()).rejects.toThrow(invalidResponseMessage)
  })

  it('rejects unreadable readiness JSON', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.reject(new SyntaxError('bad json')),
    } as Response)

    await expect(getSetupReadiness()).rejects.toThrow(invalidResponseMessage)
  })

  it('passes through structured error metadata', async () => {
    const body = { code: 'SERVICE_UNAVAILABLE', message: 'setup readiness unavailable' }
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: false,
      status: 503,
      statusText: 'Service Unavailable',
      headers: new Headers({ 'Content-Type': 'application/json' }),
      clone: () => ({ json: () => Promise.resolve(body) }),
      json: () => Promise.resolve(body),
    } as unknown as Response)

    const error = await getSetupReadiness().catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(SetupError)
    expect(error).toMatchObject({
      message: 'setup readiness unavailable',
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
      isUnavailable: true,
    })
  })

  it('uses the readiness fallback when an error body is unreadable', async () => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: false,
      status: 500,
      headers: new Headers(),
      json: () => Promise.reject(new SyntaxError('bad json')),
    } as unknown as Response)

    await expect(getSetupReadiness()).rejects.toThrow('获取设置就绪状态失败')
  })

  it('returns readiness after acknowledging setup', async () => {
    const controller = new AbortController()
    const completed = createReadiness({
      lifecycle: 'completed',
      prompt: false,
      overall_status: 'ready',
      completed_at: '2026-07-13T01:03:00Z',
      can_complete: false,
      can_defer: false,
      required: { completed: 6, total: 6 },
      checks: createReadiness().checks.map((check) => (
        check.id === 'backup_job' || check.id === 'backup_success'
          ? { ...check, status: 'complete' as const }
          : check
      )),
      summary: {
        ...createReadiness().summary,
        enabled_backup_job_count: 1,
      },
    })
    vi.mocked(authFetch).mockResolvedValueOnce(successResponse(completed))

    await expect(acknowledgeSetup({ signal: controller.signal })).resolves.toEqual(completed)
    expect(authFetch).toHaveBeenCalledWith('/api/v1/setup/acknowledge', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
      signal: controller.signal,
    })
  })

  it.each([1, 3, 7, 30] as const)('sends an exact %i-day defer payload and returns readiness', async (days) => {
    const controller = new AbortController()
    const deferred = createReadiness({
      lifecycle: 'deferred',
      prompt: false,
      deferred_until: '2026-07-20T01:02:03Z',
    })
    vi.mocked(authFetch).mockResolvedValueOnce(successResponse(deferred))

    await expect(deferSetup({ remind_in_days: days }, { signal: controller.signal })).resolves.toEqual(deferred)
    expect(authFetch).toHaveBeenCalledWith('/api/v1/setup/defer', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ remind_in_days: days }),
      signal: controller.signal,
    })
  })

  it('rejects unsupported defer durations before sending a request', async () => {
    await expect(deferSetup({ remind_in_days: 14 as 7 })).rejects.toThrow('无效的设置提醒延期天数')
    expect(authFetch).not.toHaveBeenCalled()
  })

  it.each([
    ['acknowledge', () => acknowledgeSetup(), '完成设置确认失败'],
    ['defer', () => deferSetup({ remind_in_days: 1 }), '延期设置提醒失败'],
  ])('uses the action fallback for unreadable %s errors', async (_label, invoke, expected) => {
    vi.mocked(authFetch).mockResolvedValueOnce({
      ok: false,
      status: 500,
      headers: new Headers(),
      json: () => Promise.reject(new SyntaxError('bad json')),
    } as unknown as Response)

    await expect(invoke()).rejects.toThrow(expected)
  })
})
