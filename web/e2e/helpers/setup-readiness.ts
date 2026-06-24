import type { Page, Route } from '@playwright/test'

type SetupReadinessAction =
  | 'change_password'
  | 'manage_users'
  | 'create_backup'
  | 'run_backup'
  | 'run_restore_drill'
  | 'review_security'

type SetupReadinessCheck = {
  id: string
  requirement: 'required' | 'recommended'
  status: 'complete' | 'incomplete' | 'unavailable' | 'not_applicable'
  deferrable: boolean
  title: string
  message: string
  action: SetupReadinessAction
}

type SetupReadinessSummary = {
  auth_enabled: boolean
  active_admin_count: number
  password_change_required_admin_count: number
  initial_password_file: 'missing' | 'present' | 'unavailable'
  enabled_backup_job_count: number
  latest_backup_success_at?: string
  latest_restore_verification_at?: string
  security_status: 'pass' | 'warning' | 'block' | 'unavailable'
  security_blocking_check_ids: string[]
}

export type SetupReadiness = {
  lifecycle: 'pending' | 'deferred' | 'completed'
  overall_status: 'ready' | 'action_required' | 'unavailable'
  prompt: boolean
  generated_at: string
  completed_at?: string
  deferred_until?: string
  can_complete: boolean
  can_defer: boolean
  required: { completed: number; total: number }
  recommended: { completed: number; total: number }
  checks: SetupReadinessCheck[]
  summary: SetupReadinessSummary
}

type DeferSetupRequest = {
  remind_in_days: 1 | 3 | 7 | 30
}

const setupReadinessRoutePattern = /\/api\/v1\/setup\/(?:readiness|acknowledge|defer)(?:\?.*)?$/
const generatedAt = '2026-07-13T01:02:03Z'

type SetupCheckOverrides = Partial<Omit<SetupReadinessCheck, 'id'>>

export type SetupReadinessFactoryOptions = {
  lifecycle?: SetupReadiness['lifecycle']
  checkOverrides?: Record<string, SetupCheckOverrides>
  summary?: Partial<SetupReadinessSummary>
  completedAt?: string
  deferredUntil?: string
  generatedAt?: string
}

export type CapturedSetupReadinessRequest = {
  method: string
  pathname: string
  body: unknown
}

export type SetupReadinessMockReply = {
  status: number
  readiness?: SetupReadiness
  message?: string
  error?: {
    code: string
    message: string
    details?: unknown
  }
}

export type SetupReadinessRouteState = {
  current: SetupReadiness
  requests: {
    readiness: CapturedSetupReadinessRequest[]
    acknowledge: CapturedSetupReadinessRequest[]
    defer: CapturedSetupReadinessRequest[]
  }
  setReadiness: (readiness: SetupReadiness) => void
}

type SetupReadinessMutationHandler = (
  request: CapturedSetupReadinessRequest,
  state: SetupReadinessRouteState,
) => SetupReadinessMockReply | Promise<SetupReadinessMockReply>

export type SetupReadinessRouteOptions = {
  initialReadiness: SetupReadiness
  onAcknowledge?: SetupReadinessMutationHandler
  onDefer?: SetupReadinessMutationHandler
}

function check(
  id: string,
  requirement: SetupReadinessCheck['requirement'],
  title: string,
  message: string,
  action: SetupReadinessAction,
  deferrable: boolean,
): SetupReadinessCheck {
  return {
    id,
    requirement,
    status: 'complete',
    deferrable,
    title,
    message,
    action,
  }
}

const defaultChecks: SetupReadinessCheck[] = [
  check('admin_access', 'required', '管理员访问可用', '至少有一个启用中的管理员账号。', 'manage_users', false),
  check('bootstrap_credential', 'required', '初始密码已更换', '启用中的管理员均已完成密码更换。', 'change_password', false),
  check('initial_password_file', 'required', '清理初始密码文件', '服务器上没有遗留初始密码文件。', 'change_password', false),
  check('security_baseline', 'required', '满足安全基线', '安全基线没有阻断项。', 'review_security', false),
  check('backup_job', 'required', '添加独立备份', '已配置启用中的独立备份任务。', 'create_backup', true),
  check('backup_success', 'required', '完成首次备份', '已有当前有效的成功备份。', 'run_backup', true),
  check('admin_redundancy', 'recommended', '准备备用管理员', '已有备用管理员账号。', 'manage_users', false),
  check('backup_schedule', 'recommended', '启用自动备份', '至少有一个启用中的自动备份计划。', 'create_backup', false),
  check('restore_verification', 'recommended', '验证恢复能力', '已有当前有效的恢复验证记录。', 'run_restore_drill', false),
  check('security_recommendations', 'recommended', '处理安全建议', '安全自检全部通过。', 'review_security', false),
]

const defaultSummary: SetupReadinessSummary = {
  auth_enabled: true,
  active_admin_count: 2,
  password_change_required_admin_count: 0,
  initial_password_file: 'missing',
  enabled_backup_job_count: 1,
  latest_backup_success_at: '2026-07-13T00:02:03Z',
  latest_restore_verification_at: '2026-07-13T00:32:03Z',
  security_status: 'pass',
  security_blocking_check_ids: [],
}

function isComplete(checkItem: SetupReadinessCheck): boolean {
  return checkItem.status === 'complete' || checkItem.status === 'not_applicable'
}

function deriveReadinessGates(checks: SetupReadinessCheck[]) {
  const requiredChecks = checks.filter(checkItem => checkItem.requirement === 'required')
  const recommendedChecks = checks.filter(checkItem => checkItem.requirement === 'recommended')
  const requiredUnavailable = requiredChecks.some(checkItem => checkItem.status === 'unavailable')
  const nonDeferrableReady = requiredChecks
    .filter(checkItem => !checkItem.deferrable)
    .every(isComplete)
  const deferrableIncomplete = requiredChecks
    .filter(checkItem => checkItem.deferrable)
    .some(checkItem => checkItem.status === 'incomplete')
  const deferrableUnavailable = requiredChecks
    .filter(checkItem => checkItem.deferrable)
    .some(checkItem => checkItem.status === 'unavailable')
  const canComplete = requiredChecks.every(isComplete)

  return {
    overallStatus: requiredUnavailable
      ? 'unavailable' as const
      : canComplete
        ? 'ready' as const
        : 'action_required' as const,
    canComplete,
    canDefer: nonDeferrableReady && deferrableIncomplete && !deferrableUnavailable,
    required: {
      completed: requiredChecks.filter(isComplete).length,
      total: requiredChecks.length,
    },
    recommended: {
      completed: recommendedChecks.filter(isComplete).length,
      total: recommendedChecks.length,
    },
  }
}

function cloneReadiness(readiness: SetupReadiness): SetupReadiness {
  return JSON.parse(JSON.stringify(readiness)) as SetupReadiness
}

export function createSetupReadiness(options: SetupReadinessFactoryOptions = {}): SetupReadiness {
  const checks = defaultChecks.map(checkItem => ({
    ...checkItem,
    ...options.checkOverrides?.[checkItem.id],
  }))
  const gates = deriveReadinessGates(checks)
  const lifecycle = options.lifecycle ?? 'pending'

  return {
    lifecycle,
    overall_status: gates.overallStatus,
    prompt: lifecycle === 'pending',
    generated_at: options.generatedAt ?? generatedAt,
    ...(options.completedAt ? { completed_at: options.completedAt } : {}),
    ...(options.deferredUntil ? { deferred_until: options.deferredUntil } : {}),
    can_complete: lifecycle === 'completed' ? false : gates.canComplete,
    can_defer: lifecycle === 'completed' ? false : gates.canDefer,
    required: gates.required,
    recommended: gates.recommended,
    checks,
    summary: {
      ...defaultSummary,
      ...options.summary,
    },
  }
}

export function completedSetupReadiness(readiness: SetupReadiness): SetupReadiness {
  return {
    ...cloneReadiness(readiness),
    lifecycle: 'completed',
    prompt: false,
    completed_at: readiness.generated_at,
    deferred_until: undefined,
    can_complete: false,
    can_defer: false,
  }
}

export function deferredSetupReadiness(readiness: SetupReadiness, remindInDays: number): SetupReadiness {
  const deferredUntil = new Date(readiness.generated_at)
  deferredUntil.setUTCDate(deferredUntil.getUTCDate() + remindInDays)
  return {
    ...cloneReadiness(readiness),
    lifecycle: 'deferred',
    prompt: false,
    completed_at: undefined,
    deferred_until: deferredUntil.toISOString(),
  }
}

function requestBody(route: Route): unknown {
  const rawBody = route.request().postData()
  if (!rawBody) {
    return null
  }
  try {
    return JSON.parse(rawBody) as unknown
  } catch {
    return rawBody
  }
}

function capturedRequest(route: Route): CapturedSetupReadinessRequest {
  const request = route.request()
  return {
    method: request.method(),
    pathname: new URL(request.url()).pathname,
    body: requestBody(route),
  }
}

async function fulfillSuccess(route: Route, readiness: SetupReadiness, message?: string) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      success: true,
      data: cloneReadiness(readiness),
      ...(message ? { message } : {}),
      timestamp: generatedAt,
    }),
  })
}

async function fulfillReply(
  route: Route,
  state: SetupReadinessRouteState,
  reply: SetupReadinessMockReply,
) {
  if (reply.status >= 200 && reply.status < 300 && reply.readiness) {
    state.setReadiness(reply.readiness)
    await fulfillSuccess(route, state.current, reply.message)
    return
  }

  const error = reply.error ?? {
    code: 'SETUP_MOCK_ERROR',
    message: 'setup readiness mock rejected the request',
  }
  await route.fulfill({
    status: reply.status,
    contentType: 'application/json',
    body: JSON.stringify({
      ...error,
      timestamp: generatedAt,
    }),
  })
}

export async function routeSetupReadiness(
  page: Page,
  options: SetupReadinessRouteOptions,
): Promise<SetupReadinessRouteState> {
  const state: SetupReadinessRouteState = {
    current: cloneReadiness(options.initialReadiness),
    requests: {
      readiness: [],
      acknowledge: [],
      defer: [],
    },
    setReadiness(readiness) {
      state.current = cloneReadiness(readiness)
    },
  }

  await page.route(setupReadinessRoutePattern, async (route) => {
    const request = capturedRequest(route)

    if (request.pathname.endsWith('/readiness') && request.method === 'GET') {
      state.requests.readiness.push(request)
      await fulfillSuccess(route, state.current)
      return
    }

    if (request.pathname.endsWith('/acknowledge') && request.method === 'POST') {
      state.requests.acknowledge.push(request)
      const reply = options.onAcknowledge
        ? await options.onAcknowledge(request, state)
        : {
            status: 200,
            readiness: completedSetupReadiness(state.current),
            message: 'setup completed',
          }
      await fulfillReply(route, state, reply)
      return
    }

    if (request.pathname.endsWith('/defer') && request.method === 'POST') {
      state.requests.defer.push(request)
      const body = request.body as Partial<DeferSetupRequest> | null
      const remindInDays = typeof body?.remind_in_days === 'number' ? body.remind_in_days : 7
      const reply = options.onDefer
        ? await options.onDefer(request, state)
        : {
            status: 200,
            readiness: deferredSetupReadiness(state.current, remindInDays),
            message: 'setup deferred',
          }
      await fulfillReply(route, state, reply)
      return
    }

    await route.fulfill({ status: 405, body: '' })
  })

  return state
}
