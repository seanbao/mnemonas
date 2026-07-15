import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as settingsApi from '@/api/settings'
import { UserAccessView } from './UserAccessView'
import { useSettingsDraftStore } from '@/stores/settingsDraft'

const mockAddToast = vi.fn()
const currentUser = {
  id: 'admin-1',
  username: 'admin',
  role: 'admin' as const,
  homeDir: '/',
  email: '',
  mustChangePassword: false,
}

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  return {
    ...actual,
    addToast: (...args: unknown[]) => mockAddToast(...args),
  }
})

vi.mock('@/stores/auth', () => ({
  useUser: () => currentUser,
}))

vi.mock('@/api/settings', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/settings')>()
  return {
    ...actual,
    getSettings: vi.fn(),
    updateSettings: vi.fn(),
    checkDirectoryAccess: vi.fn(),
    reportDirectoryAccess: vi.fn(),
    previewDirectoryAccess: vi.fn(),
    listDirectoryAccessReviewRecords: vi.fn(),
    createDirectoryAccessReviewRecord: vi.fn(),
    clearDirectoryAccessReviewRecords: vi.fn(),
  }
})

function settingsResponse(
  quotas = [{ path: '/team', quota_bytes: 1073741824 }],
  rules = [{ path: '/team', read_groups: ['family'] }],
) {
  return {
    success: true,
    data: {
      storage: {
        root: '~/.mnemonas',
        directory_quotas: quotas,
        directory_access_rules: rules,
      },
    },
  } as Awaited<ReturnType<typeof settingsApi.getSettings>>
}

const report = {
  path: '/team/readme.txt',
  preview: false,
  summary: {
    users: 1,
    read_allowed: 1,
    read_denied: 0,
    write_allowed: 0,
    write_denied: 1,
    related_shares: 1,
    active_related_shares: 1,
    password_protected_shares: 0,
  },
  users: [{
    username: 'alice',
    user_id: 'user-1',
    role: 'user' as const,
    groups: ['family'],
    home_dir: '/alice',
    path: '/team/readme.txt',
    read: {
      mode: 'read' as const,
      allowed: true,
      source: 'directory_access_rule' as const,
      message: 'directory access rule grants read through an existing descendant',
      matched_rule: { path: '/team', read_groups: ['family'] },
    },
    write: {
      mode: 'write' as const,
      allowed: false,
      source: 'directory_access_rule' as const,
      matched_rule: { path: '/team', read_groups: ['family'] },
    },
  }],
  rule_effects: [{
    path: '/team',
    index: 0,
    read_allowed: 1,
    read_denied: 0,
    write_allowed: 0,
    write_denied: 1,
  }],
  shares: [{
    id: 'share-1',
    path: '/team',
    type: 'folder' as const,
    created_by: 'admin',
    relation: 'exact' as const,
    enabled: true,
    active: true,
    has_password: false,
    access_count: 0,
    max_access: 0,
  }],
}

function reviewRecord(
  id: string,
  title: string,
  preview: boolean,
  reviewedAt: string,
): Awaited<ReturnType<typeof settingsApi.createDirectoryAccessReviewRecord>> {
  return {
    id,
    reviewed_at: reviewedAt,
    reviewer: 'admin',
    title,
    path: report.path,
    preview,
    users: 1,
    read_allowed: 1,
    read_denied: 0,
    write_allowed: 0,
    write_denied: 1,
    related_shares: 1,
    active_related_shares: 1,
    password_protected_shares: 0,
    report_text: `目录权限复核记录 ${id}`,
  }
}

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

type ReviewKey = 'a' | 'b'
type ReviewOutcome = 'success' | 'failure'
type StoredReviewEntry = {
  id: string
  title: string
  preview: boolean
  reviewer?: string
}
type ConcurrentReviewScenario = {
  name: string
  settlementOrder: readonly ReviewKey[]
  outcomes: Record<ReviewKey, ReviewOutcome>
  expectedOrder: readonly ReviewKey[]
}

const reviewHistoryStorageKey = 'mnemonas_directory_access_review_history:admin-1'
const concurrentReviewScenarios: ConcurrentReviewScenario[] = [
  {
    name: 'replaces both temporary reviews when B succeeds before A',
    settlementOrder: ['b', 'a'],
    outcomes: { a: 'success', b: 'success' },
    expectedOrder: ['a', 'b'],
  },
  {
    name: 'replaces both temporary reviews when A succeeds before B',
    settlementOrder: ['a', 'b'],
    outcomes: { a: 'success', b: 'success' },
    expectedOrder: ['b', 'a'],
  },
  {
    name: 'retains A locally when A fails and B succeeds',
    settlementOrder: ['a', 'b'],
    outcomes: { a: 'failure', b: 'success' },
    expectedOrder: ['b', 'a'],
  },
  {
    name: 'replaces A and retains B locally when A succeeds and B fails',
    settlementOrder: ['a', 'b'],
    outcomes: { a: 'success', b: 'failure' },
    expectedOrder: ['a', 'b'],
  },
  {
    name: 'retains both temporary reviews when both requests fail',
    settlementOrder: ['a', 'b'],
    outcomes: { a: 'failure', b: 'failure' },
    expectedOrder: ['b', 'a'],
  },
]

function readStoredReviewHistory(): StoredReviewEntry[] {
  return JSON.parse(window.localStorage.getItem(reviewHistoryStorageKey) ?? '[]') as StoredReviewEntry[]
}

function renderView(queryClient = createQueryClient()) {
  return {
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <UserAccessView />
      </QueryClientProvider>,
    ),
  }
}

describe('UserAccessView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.localStorage.clear()
    useSettingsDraftStore.setState({ hasPendingChanges: false })
    vi.mocked(settingsApi.getSettings).mockResolvedValue(settingsResponse())
    vi.mocked(settingsApi.updateSettings).mockResolvedValue({ success: true, message: '' })
    vi.mocked(settingsApi.listDirectoryAccessReviewRecords).mockResolvedValue({
      items: [],
      total: 0,
      limit: 5,
      offset: 0,
    })
    vi.mocked(settingsApi.checkDirectoryAccess).mockResolvedValue(report.users[0])
    vi.mocked(settingsApi.reportDirectoryAccess).mockResolvedValue(report)
    vi.mocked(settingsApi.previewDirectoryAccess).mockResolvedValue({ ...report, preview: true })
    vi.mocked(settingsApi.createDirectoryAccessReviewRecord).mockResolvedValue(
      reviewRecord('review-1', '用户矩阵', false, '2026-07-18T08:00:00Z'),
    )
    vi.mocked(settingsApi.clearDirectoryAccessReviewRecords).mockResolvedValue({
      success: true,
      message: '',
    })
  })

  it('uses mobile-safe structure and accessible section semantics at 390px', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
    renderView()

    expect(await screen.findByLabelText('目录与访问管理')).toBeTruthy()
    expect(screen.getByRole('heading', { name: '目录配额' })).toBeTruthy()
    expect(screen.getByRole('heading', { name: '目录权限' })).toBeTruthy()
    expect(screen.getByLabelText('有效权限复核')).toBeTruthy()
    expect(screen.getByRole('group', { name: '目录权限规则 1' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '保存目录策略' })).toHaveClass('w-full', 'sm:w-auto')
    expect(screen.getByRole('button', { name: '添加规则' })).toHaveClass('w-full', 'sm:w-auto')
  })

  it('saves only the directory policy payload and refreshes settings and active stats', async () => {
    const user = userEvent.setup()
    const queryClient = createQueryClient()
    const refetchQueries = vi.spyOn(queryClient, 'refetchQueries')
    vi.mocked(settingsApi.getSettings)
      .mockResolvedValueOnce(settingsResponse())
      .mockResolvedValue(settingsResponse(
        [
          { path: '/team', quota_bytes: 2147483648 },
          { path: '/media', quota_bytes: 536870912 },
        ],
        [{ path: '/shared', read_roles: ['user'], write_roles: ['user'] }],
      ))
    renderView(queryClient)

    const quotas = await screen.findByRole('textbox', { name: '目录配额' })
    fireEvent.change(quotas, { target: { value: '/team 2 GB\n/media 512 MB' } })
    await user.click(screen.getByRole('button', { name: /全员协作/ }))
    await user.click(screen.getByRole('button', { name: '保存目录策略' }))

    await waitFor(() => {
      expect(settingsApi.updateSettings).toHaveBeenCalledWith({
        storage: {
          directory_quotas: [
            { path: '/team', quota_bytes: 2147483648 },
            { path: '/media', quota_bytes: 536870912 },
          ],
          directory_access_rules: [
            { path: '/team', read_groups: ['family'] },
            { path: '/shared', read_roles: ['user'], write_roles: ['user'] },
          ],
        },
      }, { signal: expect.any(AbortSignal) })
    })
    await waitFor(() => {
      expect(settingsApi.getSettings).toHaveBeenCalledTimes(2)
      expect(refetchQueries).toHaveBeenCalledWith({ queryKey: ['stats'], type: 'active' })
    })
  })

  it('keeps a dirty draft visible and resets to the latest refetched server baseline', async () => {
    const user = userEvent.setup()
    vi.mocked(settingsApi.getSettings)
      .mockResolvedValueOnce(settingsResponse())
      .mockResolvedValue(settingsResponse([{ path: '/server', quota_bytes: 4294967296 }], []))
    const { queryClient } = renderView()

    const quotas = await screen.findByRole('textbox', { name: '目录配额' })
    fireEvent.change(quotas, { target: { value: '/draft 3 GB' } })
    await act(async () => {
      await queryClient.refetchQueries({ queryKey: ['directory-policies'] })
    })

    await waitFor(() => expect(settingsApi.getSettings).toHaveBeenCalledTimes(2))
    expect(screen.getByRole('textbox', { name: '目录配额' })).toHaveValue('/draft 3 GB')
    expect(screen.getByText('当前有未保存的目录策略。后台刷新不会覆盖这些草稿。')).toBeTruthy()
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true)

    await user.click(screen.getByRole('button', { name: '重置草稿' }))
    expect(screen.getByRole('textbox', { name: '目录配额' })).toHaveValue('/server 4 GB')
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(false)
  })

  it('resets a dirty draft to the last server-backed policies', async () => {
    const user = userEvent.setup()
    renderView()

    fireEvent.change(await screen.findByRole('textbox', { name: '目录配额' }), { target: { value: '/draft 3 GB' } })
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true)
    expect(screen.getByRole('button', { name: '刷新' })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: '重置草稿' }))

    expect(screen.getByRole('textbox', { name: '目录配额' })).toHaveValue('/team 1 GB')
    expect(screen.queryByText('当前有未保存的目录策略。后台刷新不会覆盖这些草稿。')).toBeNull()
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(false)
  })

  it('keeps edits made during save dirty and resets them to the saved submission', async () => {
    const user = userEvent.setup()
    const save = deferred<Awaited<ReturnType<typeof settingsApi.updateSettings>>>()
    const postSaveRefetch = deferred<Awaited<ReturnType<typeof settingsApi.getSettings>>>()
    vi.mocked(settingsApi.updateSettings).mockReturnValueOnce(save.promise)
    vi.mocked(settingsApi.getSettings)
      .mockResolvedValueOnce(settingsResponse())
      .mockReturnValueOnce(postSaveRefetch.promise)
    renderView()

    const quotas = await screen.findByRole('textbox', { name: '目录配额' })
    fireEvent.change(quotas, { target: { value: '/submitted 2 GB' } })
    await user.click(screen.getByRole('button', { name: '保存目录策略' }))
    await waitFor(() => expect(settingsApi.updateSettings).toHaveBeenCalled())

    fireEvent.change(quotas, { target: { value: '/continued 3 GB' } })
    await act(async () => {
      save.resolve({ success: true, message: '' })
      await save.promise
    })

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: '目录配额' })).toHaveValue('/continued 3 GB')
      expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true)
    })
    await user.click(screen.getByRole('button', { name: '重置草稿' }))
    expect(screen.getByRole('textbox', { name: '目录配额' })).toHaveValue('/submitted 2 GB')
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(false)
  })

  it('ignores an older settings refetch that resolves after a newer save', async () => {
    const user = userEvent.setup()
    const oldRefetch = deferred<Awaited<ReturnType<typeof settingsApi.getSettings>>>()
    const postSaveRefetch = deferred<Awaited<ReturnType<typeof settingsApi.getSettings>>>()
    vi.mocked(settingsApi.getSettings)
      .mockResolvedValueOnce(settingsResponse())
      .mockReturnValueOnce(oldRefetch.promise)
      .mockReturnValueOnce(postSaveRefetch.promise)
    const { queryClient } = renderView()

    await screen.findByRole('textbox', { name: '目录配额' })
    void queryClient.refetchQueries({ queryKey: ['directory-policies'] })
    await waitFor(() => expect(settingsApi.getSettings).toHaveBeenCalledTimes(2))

    fireEvent.change(screen.getByRole('textbox', { name: '目录配额' }), {
      target: { value: '/saved 2 GB' },
    })
    await user.click(screen.getByRole('button', { name: '保存目录策略' }))
    await waitFor(() => expect(settingsApi.getSettings).toHaveBeenCalledTimes(3))

    await act(async () => {
      oldRefetch.resolve(settingsResponse([{ path: '/stale', quota_bytes: 8589934592 }], []))
      await oldRefetch.promise
    })
    fireEvent.change(screen.getByRole('textbox', { name: '目录配额' }), {
      target: { value: '/continued 3 GB' },
    })
    await user.click(screen.getByRole('button', { name: '重置草稿' }))

    expect(screen.getByRole('textbox', { name: '目录配额' })).toHaveValue('/saved 2 GB')
  })

  it('aborts an in-flight preview when resetting a dirty draft', async () => {
    const user = userEvent.setup()
    vi.mocked(settingsApi.previewDirectoryAccess).mockReturnValue(new Promise(() => {}))
    renderView()

    fireEvent.change(await screen.findByRole('textbox', { name: '目录配额' }), { target: { value: '/draft 3 GB' } })
    await user.click(screen.getByRole('button', { name: '预览变更' }))

    await waitFor(() => expect(settingsApi.previewDirectoryAccess).toHaveBeenCalled())
    const previewSignal = vi.mocked(settingsApi.previewDirectoryAccess).mock.calls[0]?.[1]?.signal
    expect(previewSignal?.aborted).toBe(false)

    await user.click(screen.getByRole('button', { name: '重置草稿' }))

    expect(previewSignal?.aborted).toBe(true)
    expect(screen.queryByLabelText('目录权限变更预览')).toBeNull()
  })

  it('validates quota and access drafts before saving', async () => {
    const user = userEvent.setup()
    renderView()

    fireEvent.change(await screen.findByRole('textbox', { name: '目录配额' }), { target: { value: 'team 1 GB' } })
    await user.click(screen.getByRole('button', { name: '保存目录策略' }))
    expect(settingsApi.updateSettings).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenLastCalledWith({
      title: '目录配额格式无效',
      description: '第 1 行路径无效',
      color: 'danger',
    })

    fireEvent.change(screen.getByRole('textbox', { name: '目录配额' }), { target: { value: '/team 1 GB' } })
    fireEvent.change(screen.getByLabelText('写角色 1'), { target: { value: 'owner' } })
    await user.click(screen.getByRole('button', { name: '保存目录策略' }))
    expect(settingsApi.updateSettings).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenLastCalledWith({
      title: '目录权限格式无效',
      description: '第 1 行角色只能是 admin、user 或 guest',
      color: 'danger',
    })
  })

  it('checks effective permissions and renders saved and preview matrices', async () => {
    const user = userEvent.setup()
    renderView()

    await user.type(await screen.findByLabelText('检查用户'), 'alice')
    fireEvent.change(screen.getByLabelText('检查路径'), { target: { value: '/team/readme.txt' } })
    await user.click(screen.getByRole('button', { name: '检查权限' }))
    const checkResult = await screen.findByLabelText('有效权限检查结果')
    expect(within(checkResult).getByText('已存在的子目录命中读取规则，因此允许查看相关路径。')).toBeTruthy()

    await user.click(screen.getByRole('button', { name: '用户矩阵' }))
    const matrix = await screen.findByLabelText('目录权限用户矩阵')
    expect(within(matrix).getByText('可读 1')).toBeTruthy()
    expect(within(matrix).getByText('规则 1 · /team')).toBeTruthy()

    await user.click(screen.getByRole('button', { name: '预览变更' }))
    expect(await screen.findByLabelText('目录权限变更预览')).toBeTruthy()
    expect(settingsApi.previewDirectoryAccess).toHaveBeenCalledWith({
      path: '/team/readme.txt',
      directory_access_rules: [{ path: '/team', read_groups: ['family'] }],
    }, { signal: expect.any(AbortSignal) })
  })

  it('loads, copies, and clears review history', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    vi.mocked(settingsApi.listDirectoryAccessReviewRecords).mockResolvedValueOnce({
      items: [{
        id: 'server-review',
        reviewed_at: '2026-07-18T08:00:00Z',
        reviewer: 'admin',
        title: '用户矩阵',
        path: '/team/readme.txt',
        preview: false,
        users: 1,
        read_allowed: 1,
        read_denied: 0,
        write_allowed: 0,
        write_denied: 1,
        related_shares: 1,
        active_related_shares: 1,
        password_protected_shares: 0,
        report_text: '目录权限复核记录\n路径: /team/readme.txt',
      }],
      total: 1,
      limit: 5,
      offset: 0,
    })
    renderView()

    const history = within(await screen.findByLabelText('目录权限近期复核历史'))
    expect(await history.findByText('/team/readme.txt')).toBeTruthy()
    expect(history.getByText('复核人 admin')).toBeTruthy()
    await user.click(history.getByRole('button', { name: '复制记录' }))
    expect(writeText).toHaveBeenCalledWith('目录权限复核记录\n路径: /team/readme.txt')

    await user.click(history.getByRole('button', { name: '清空近期记录' }))
    await waitFor(() => expect(settingsApi.clearDirectoryAccessReviewRecords).toHaveBeenCalled())
    expect(screen.getByText('暂无近期目录权限复核记录。')).toBeTruthy()
  })

  it.each(concurrentReviewScenarios)('$name', async ({
    settlementOrder,
    outcomes,
    expectedOrder,
  }) => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    const firstSave = deferred<Awaited<ReturnType<typeof settingsApi.createDirectoryAccessReviewRecord>>>()
    const secondSave = deferred<Awaited<ReturnType<typeof settingsApi.createDirectoryAccessReviewRecord>>>()
    vi.mocked(settingsApi.createDirectoryAccessReviewRecord)
      .mockReset()
      .mockReturnValueOnce(firstSave.promise)
      .mockReturnValueOnce(secondSave.promise)
    renderView()

    await user.click(await screen.findByRole('button', { name: '用户矩阵' }))
    await user.click(screen.getByRole('button', { name: '预览变更' }))
    const copyButtons = await screen.findAllByRole('button', { name: '复制复核记录' })

    await user.click(copyButtons[0])
    await waitFor(() => expect(settingsApi.createDirectoryAccessReviewRecord).toHaveBeenCalledTimes(1))
    await user.click(copyButtons[1])
    await waitFor(() => expect(settingsApi.createDirectoryAccessReviewRecord).toHaveBeenCalledTimes(2))

    const temporaryEntries = readStoredReviewHistory()
    expect(temporaryEntries).toHaveLength(2)
    const temporaryIDs: Record<ReviewKey, string> = {
      a: temporaryEntries.find((entry) => entry.title === '用户矩阵')?.id ?? '',
      b: temporaryEntries.find((entry) => entry.title === '变更预览')?.id ?? '',
    }
    expect(temporaryIDs.a).not.toBe('')
    expect(temporaryIDs.b).not.toBe('')
    expect(temporaryIDs.a).not.toBe(temporaryIDs.b)

    const saves = { a: firstSave, b: secondSave }
    const serverRecords = {
      a: reviewRecord('server-review-a', '用户矩阵', false, '2026-07-18T09:00:00Z'),
      b: reviewRecord('server-review-b', '变更预览', true, '2026-07-18T09:01:00Z'),
    }
    for (const key of settlementOrder) {
      const save = saves[key]
      await act(async () => {
        if (outcomes[key] === 'success') {
          save.resolve(serverRecords[key])
        } else {
          save.reject(new Error(`review ${key} unavailable`))
        }
        await save.promise.catch(() => undefined)
      })
    }

    await waitFor(() => {
      const stored = readStoredReviewHistory()
      const expectedIDs = expectedOrder.map((key) => (
        outcomes[key] === 'success' ? `server-review-${key}` : temporaryIDs[key]
      ))
      expect(stored.map((entry) => entry.id)).toEqual(expectedIDs)
      expect(new Set(stored.map((entry) => entry.id)).size).toBe(2)
      for (const key of ['a', 'b'] as const) {
        expect(stored.some((entry) => entry.id === temporaryIDs[key])).toBe(outcomes[key] === 'failure')
        expect(stored.some((entry) => entry.id === `server-review-${key}`)).toBe(outcomes[key] === 'success')
      }
    })

    const history = within(screen.getByLabelText('目录权限近期复核历史'))
    expect(history.getAllByRole('listitem')).toHaveLength(2)
    expect(history.getAllByText(report.path)).toHaveLength(2)
    for (const [key, label] of [['a', '用户矩阵'], ['b', '变更预览']] as const) {
      const item = history.getByText(label).closest('li')
      expect(item).not.toBeNull()
      expect(within(item as HTMLElement).queryByText('复核人 admin') !== null)
        .toBe(outcomes[key] === 'success')
    }
  })

  it('ignores damaged browser review history without crashing the access view', async () => {
    window.localStorage.setItem(
      'mnemonas_directory_access_review_history:admin-1',
      JSON.stringify([{
        id: 'damaged-review',
        recordedAt: 'not-a-date',
        reviewer: '',
        title: '用户矩阵',
        path: '/team',
        preview: false,
        users: -1,
        readAllowed: 1,
        writeAllowed: 1,
        relatedShares: 0,
        reportText: '目录权限复核记录',
      }]),
    )

    renderView()

    expect(await screen.findByLabelText('目录与访问管理')).toBeTruthy()
    const history = within(screen.getByLabelText('目录权限近期复核历史'))
    expect(history.getByText('暂无近期目录权限复核记录。')).toBeTruthy()
  })

  it('rejects malformed matrix paths before sending requests', async () => {
    const user = userEvent.setup()
    renderView()

    fireEvent.change(await screen.findByLabelText('检查路径'), { target: { value: '/team/./readme.txt' } })
    await user.click(screen.getByRole('button', { name: '用户矩阵' }))

    expect(settingsApi.reportDirectoryAccess).not.toHaveBeenCalled()
    expect(mockAddToast).toHaveBeenLastCalledWith({
      title: '权限矩阵路径无效',
      description: '路径必须是站内绝对路径，且不能包含反斜杠、?、#、控制字符、. 或 .. 路径段。',
      color: 'warning',
    })
  })

  it('aborts pending access checks and saves when unmounted', async () => {
    const user = userEvent.setup()
    vi.mocked(settingsApi.checkDirectoryAccess).mockReturnValue(new Promise(() => {}))
    vi.mocked(settingsApi.updateSettings).mockReturnValue(new Promise(() => {}))
    const { unmount } = renderView()

    await user.type(await screen.findByLabelText('检查用户'), 'alice')
    await user.click(screen.getByRole('button', { name: '检查权限' }))
    fireEvent.change(screen.getByRole('textbox', { name: '目录配额' }), { target: { value: '/team 2 GB' } })
    await user.click(screen.getByRole('button', { name: '保存目录策略' }))

    await waitFor(() => {
      expect(settingsApi.checkDirectoryAccess).toHaveBeenCalled()
      expect(settingsApi.updateSettings).toHaveBeenCalled()
    })
    const checkSignal = vi.mocked(settingsApi.checkDirectoryAccess).mock.calls[0]?.[1]?.signal
    const saveSignal = vi.mocked(settingsApi.updateSettings).mock.calls[0]?.[1]?.signal
    expect(checkSignal?.aborted).toBe(false)
    expect(saveSignal?.aborted).toBe(false)

    act(() => unmount())
    expect(checkSignal?.aborted).toBe(true)
    expect(saveSignal?.aborted).toBe(true)
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(false)
  })
})
