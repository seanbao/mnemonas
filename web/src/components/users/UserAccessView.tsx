import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Input, addToast } from '@heroui/react'
import {
  HardDrive,
  RefreshCw,
  Save,
  Shield,
} from 'lucide-react'
import {
  SettingsError,
  checkDirectoryAccess,
  clearDirectoryAccessReviewRecords,
  createDirectoryAccessReviewRecord,
  getSettings,
  listDirectoryAccessReviewRecords,
  previewDirectoryAccess,
  reportDirectoryAccess,
  updateSettings,
  type DirectoryAccessReportData,
  type DirectoryAccessRule,
  type DirectoryQuota,
} from '@/api/settings'
import { useUser } from '@/stores/auth'
import { useSettingsDraftStore } from '@/stores/settingsDraft'
import { copyTextToClipboard } from '@/lib/utils'
import { getUserFacingErrorDescription } from '@/lib/apiMessages'
import {
  formatDirectoryAccessRuleLines,
  formatDirectoryQuotaLines,
  logicalPathInputErrorDescription,
  normalizeLogicalPathInput,
  parseDirectoryAccessRuleLines,
  parseDirectoryQuotaLines,
  serializeDirectoryPolicies,
} from './userAccessDraft'
import {
  AccessCoverageSummary,
  AccessRuleChangeReview,
  AccessRuleEditor,
  AccessSection,
  DirectoryQuotaChangeReview,
} from './UserAccessEditors'
import {
  getHistoryStorageKey,
  historyRequest,
  loadHistory,
  mergeHistory,
  reviewHistoryLimit,
  saveHistory,
  serverRecordToHistory,
  type ReviewHistoryEntry,
} from './userAccessHistory'
import { ReviewHistory } from './UserAccessHistory'
import {
  AccessCheckResult,
  ReportResult,
  type ReviewSaveResult,
} from './UserAccessReport'

type AccessDraft = {
  directoryQuotas: string
  directoryAccessRules: string
}

type SettingsSnapshot = {
  response: Awaited<ReturnType<typeof getSettings>>
  generation: number
}

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

function getActionErrorToast(
  error: unknown,
  titles: { unavailable: string; failure: string },
): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: titles.unavailable,
      description: '目录访问配置当前不可用，请检查设备状态或稍后重试。',
      color: 'warning',
    }
  }
  return {
    title: titles.failure,
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

export function UserAccessView() {
  const user = useUser()
  const queryClient = useQueryClient()
  const setHasPendingChanges = useSettingsDraftStore((state) => state.setHasPendingChanges)
  const userID = user?.id ?? 'anonymous'
  const directoryPoliciesQueryKey = useMemo(() => ['directory-policies', userID] as const, [userID])
  const settingsCacheKey = useMemo(() => ['settings', userID] as const, [userID])
  const historyStorageKey = useMemo(() => getHistoryStorageKey(user?.id), [user?.id])
  const [draft, setDraft] = useState<AccessDraft>({ directoryQuotas: '', directoryAccessRules: '' })
  const [savedQuotas, setSavedQuotas] = useState<DirectoryQuota[]>([])
  const [savedRules, setSavedRules] = useState<DirectoryAccessRule[]>([])
  const [isDirty, setIsDirty] = useState(false)
  const draftRef = useRef(draft)
  const isDirtyRef = useRef(isDirty)
  const [checkUsername, setCheckUsername] = useState('')
  const [checkPath, setCheckPath] = useState('/')
  const [history, setHistory] = useState<ReviewHistoryEntry[]>(() => loadHistory(historyStorageKey))
  const historyRef = useRef(history)
  const saveControllerRef = useRef<AbortController | null>(null)
  const checkControllerRef = useRef<AbortController | null>(null)
  const reportControllerRef = useRef<AbortController | null>(null)
  const previewControllerRef = useRef<AbortController | null>(null)
  const operationGenerationRef = useRef(0)
  const baselineGenerationRef = useRef(0)
  const latestSaveGenerationRef = useRef(0)

  useLayoutEffect(() => {
    draftRef.current = draft
    isDirtyRef.current = isDirty
  }, [draft, isDirty])

  const settingsQuery = useQuery({
    queryKey: directoryPoliciesQueryKey,
    queryFn: async ({ signal }): Promise<SettingsSnapshot> => {
      const generation = ++operationGenerationRef.current
      return {
        response: await getSettings({ signal }),
        generation,
      }
    },
  })
  const historyQuery = useQuery({
    queryKey: ['directory-access-review-records', userID],
    queryFn: ({ signal }) => listDirectoryAccessReviewRecords({ limit: reviewHistoryLimit, signal }),
    retry: false,
  })

  const serverHistory = useMemo(
    () => historyQuery.data?.items.map(serverRecordToHistory) ?? [],
    [historyQuery.data?.items],
  )

  useEffect(() => {
    const snapshot = settingsQuery.data
    if (!snapshot?.response.data || snapshot.generation <= baselineGenerationRef.current) return
    baselineGenerationRef.current = snapshot.generation
    const quotas = snapshot.response.data.storage.directory_quotas ?? []
    const rules = snapshot.response.data.storage.directory_access_rules ?? []
    const nextDraft = {
      directoryQuotas: formatDirectoryQuotaLines(quotas),
      directoryAccessRules: formatDirectoryAccessRuleLines(rules),
    }
    setSavedQuotas(quotas)
    setSavedRules(rules)
    if (isDirtyRef.current) return
    setDraft(nextDraft)
    draftRef.current = nextDraft
  }, [settingsQuery.data])

  useEffect(() => {
    const merged = mergeHistory(serverHistory, loadHistory(historyStorageKey))
    historyRef.current = merged
    let cancelled = false
    queueMicrotask(() => {
      if (!cancelled) setHistory(merged)
    })
    return () => {
      cancelled = true
    }
  }, [historyStorageKey, serverHistory])

  useEffect(() => () => {
    saveControllerRef.current?.abort()
    checkControllerRef.current?.abort()
    reportControllerRef.current?.abort()
    previewControllerRef.current?.abort()
    setHasPendingChanges(false)
  }, [setHasPendingChanges])

  const updateDraft = useCallback((update: (current: AccessDraft) => AccessDraft) => {
    setDraft((current) => {
      const next = update(current)
      draftRef.current = next
      return next
    })
    isDirtyRef.current = true
    setIsDirty(true)
  }, [])

  const saveMutation = useMutation({
    mutationFn: (variables: {
      quotas: DirectoryQuota[]
      rules: DirectoryAccessRule[]
      submittedSignature: string
      generation: number
      signal: AbortSignal
    }) => updateSettings({
      storage: {
        directory_quotas: variables.quotas,
        directory_access_rules: variables.rules,
      },
    }, { signal: variables.signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted || variables.generation !== latestSaveGenerationRef.current) return
      const baselineGeneration = ++operationGenerationRef.current
      baselineGenerationRef.current = baselineGeneration
      const currentQuotas = parseDirectoryQuotaLines(draftRef.current.directoryQuotas)
      const currentRules = parseDirectoryAccessRuleLines(draftRef.current.directoryAccessRules)
      const currentSignature = currentQuotas.error || currentRules.error
        ? ''
        : serializeDirectoryPolicies(currentQuotas.quotas, currentRules.rules)
      setSavedQuotas(variables.quotas)
      setSavedRules(variables.rules)
      queryClient.setQueryData<Awaited<ReturnType<typeof getSettings>>>(settingsCacheKey, (current) => (
        current?.data
          ? {
              ...current,
              data: {
                ...current.data,
                storage: {
                  ...current.data.storage,
                  directory_quotas: variables.quotas,
                  directory_access_rules: variables.rules,
                },
              },
            }
          : current
      ))
      void queryClient.invalidateQueries({ queryKey: settingsCacheKey, exact: true })
      if (currentSignature === variables.submittedSignature) {
        isDirtyRef.current = false
        setIsDirty(false)
        const normalizedDraft = {
          directoryQuotas: formatDirectoryQuotaLines(variables.quotas),
          directoryAccessRules: formatDirectoryAccessRuleLines(variables.rules),
        }
        draftRef.current = normalizedDraft
        setDraft(normalizedDraft)
      }
      addToast(result.warning
        ? {
            title: '目录策略已保存，但存在警告',
            description: result.message || undefined,
            color: 'warning',
          }
        : { title: '目录策略已保存', color: 'success' })
      void Promise.all([
        settingsQuery.refetch(),
        queryClient.refetchQueries({ queryKey: ['stats'], type: 'active' }),
      ])
    },
    onError: (error, variables) => {
      if (variables.signal.aborted || isAbortError(error)) return
      addToast(getActionErrorToast(error, {
        unavailable: '目录策略保存暂不可用',
        failure: '保存目录策略失败',
      }))
    },
    onSettled: (_data, _error, variables) => {
      if (saveControllerRef.current?.signal === variables.signal) saveControllerRef.current = null
    },
  })

  useEffect(() => {
    setHasPendingChanges(isDirty || saveMutation.isPending)
  }, [isDirty, saveMutation.isPending, setHasPendingChanges])

  const checkMutation = useMutation({
    mutationFn: ({ username, path, signal }: { username: string; path: string; signal: AbortSignal }) => (
      checkDirectoryAccess({ username, path }, { signal })
    ),
    onError: (error, variables) => {
      if (!variables.signal.aborted && !isAbortError(error)) {
        addToast(getActionErrorToast(error, { unavailable: '权限检查不可用', failure: '权限检查失败' }))
      }
    },
  })
  const reportMutation = useMutation({
    mutationFn: ({ path, signal }: { path: string; signal: AbortSignal }) => (
      reportDirectoryAccess({ path }, { signal })
    ),
    onError: (error, variables) => {
      if (!variables.signal.aborted && !isAbortError(error)) {
        addToast(getActionErrorToast(error, { unavailable: '权限矩阵不可用', failure: '权限矩阵生成失败' }))
      }
    },
  })
  const previewMutation = useMutation({
    mutationFn: ({
      path,
      rules,
      signal,
    }: {
      path: string
      rules: DirectoryAccessRule[]
      signal: AbortSignal
    }) => previewDirectoryAccess({ path, directory_access_rules: rules }, { signal }),
    onError: (error, variables) => {
      if (!variables.signal.aborted && !isAbortError(error)) {
        addToast(getActionErrorToast(error, { unavailable: '权限预览不可用', failure: '权限预览失败' }))
      }
    },
  })

  const handleSave = () => {
    const quotas = parseDirectoryQuotaLines(draft.directoryQuotas)
    if (quotas.error) {
      addToast({ title: '目录配额格式无效', description: quotas.error, color: 'danger' })
      return
    }
    const rules = parseDirectoryAccessRuleLines(draft.directoryAccessRules)
    if (rules.error) {
      addToast({ title: '目录权限格式无效', description: rules.error, color: 'danger' })
      return
    }
    saveControllerRef.current?.abort()
    const controller = new AbortController()
    const generation = ++operationGenerationRef.current
    latestSaveGenerationRef.current = generation
    saveControllerRef.current = controller
    saveMutation.mutate({
      quotas: quotas.quotas,
      rules: rules.rules,
      submittedSignature: serializeDirectoryPolicies(quotas.quotas, rules.rules),
      generation,
      signal: controller.signal,
    })
  }

  const handleReset = () => {
    checkControllerRef.current?.abort()
    reportControllerRef.current?.abort()
    previewControllerRef.current?.abort()
    checkControllerRef.current = null
    reportControllerRef.current = null
    previewControllerRef.current = null
    const nextDraft = {
      directoryQuotas: formatDirectoryQuotaLines(savedQuotas),
      directoryAccessRules: formatDirectoryAccessRuleLines(savedRules),
    }
    draftRef.current = nextDraft
    isDirtyRef.current = false
    setDraft(nextDraft)
    setIsDirty(false)
    checkMutation.reset()
    reportMutation.reset()
    previewMutation.reset()
    addToast({ title: '目录策略草稿已重置', color: 'success' })
  }

  const normalizedCheckPath = (title: string): string | null => {
    const normalized = normalizeLogicalPathInput(checkPath)
    if (!normalized) {
      addToast({ title, description: logicalPathInputErrorDescription, color: 'warning' })
    }
    return normalized
  }

  const handleCheck = () => {
    const username = checkUsername.trim()
    if (!username || !checkPath.trim()) {
      addToast({ title: '权限检查信息不完整', description: '请输入用户名和路径。', color: 'warning' })
      return
    }
    const path = normalizedCheckPath('权限检查路径无效')
    if (!path) return
    checkControllerRef.current?.abort()
    const controller = new AbortController()
    checkControllerRef.current = controller
    checkMutation.mutate({ username, path, signal: controller.signal })
  }

  const handleReport = () => {
    if (!checkPath.trim()) {
      addToast({ title: '权限矩阵路径为空', description: '请输入需要检查的路径。', color: 'warning' })
      return
    }
    const path = normalizedCheckPath('权限矩阵路径无效')
    if (!path) return
    reportControllerRef.current?.abort()
    const controller = new AbortController()
    reportControllerRef.current = controller
    reportMutation.mutate({ path, signal: controller.signal })
  }

  const handlePreview = () => {
    if (!checkPath.trim()) {
      addToast({ title: '权限预览路径为空', description: '请输入需要预览的路径。', color: 'warning' })
      return
    }
    const path = normalizedCheckPath('权限预览路径无效')
    if (!path) return
    const rules = parseDirectoryAccessRuleLines(draft.directoryAccessRules)
    if (rules.error) {
      addToast({ title: '目录权限格式无效', description: rules.error, color: 'danger' })
      return
    }
    previewControllerRef.current?.abort()
    const controller = new AbortController()
    previewControllerRef.current = controller
    previewMutation.mutate({ path, rules: rules.rules, signal: controller.signal })
  }

  const saveReview = useCallback(async (
    report: DirectoryAccessReportData,
    title: string,
    reportText: string,
  ): Promise<ReviewSaveResult> => {
    const entry: ReviewHistoryEntry = {
      id: typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(36).slice(2)}`,
      recordedAt: new Date().toISOString(),
      title,
      path: report.path,
      preview: report.preview === true,
      users: report.summary.users,
      readAllowed: report.summary.read_allowed,
      writeAllowed: report.summary.write_allowed,
      relatedShares: report.summary.related_shares,
      reportText,
    }
    const next = mergeHistory([entry], historyRef.current)
    if (!saveHistory(historyStorageKey, next)) return 'failed'
    historyRef.current = next
    setHistory(next)
    try {
      const saved = serverRecordToHistory(await createDirectoryAccessReviewRecord(historyRequest(report, title, reportText)))
      const merged = mergeHistory(
        [saved],
        historyRef.current.filter((candidate) => candidate.id !== entry.id),
      )
      saveHistory(historyStorageKey, merged)
      historyRef.current = merged
      setHistory(merged)
      void historyQuery.refetch()
      return 'saved'
    } catch {
      return 'local'
    }
  }, [historyQuery, historyStorageKey])

  const copyHistory = useCallback(async (entry: ReviewHistoryEntry) => {
    try {
      await copyTextToClipboard(entry.reportText)
      addToast({ title: '目录权限历史记录已复制', color: 'success' })
    } catch {
      addToast({
        title: '复制目录权限历史记录失败',
        description: '请检查浏览器剪贴板权限。',
        color: 'danger',
      })
    }
  }, [])

  const clearHistory = useCallback(async () => {
    try {
      window.localStorage.removeItem(historyStorageKey)
    } catch {
      addToast({
        title: '清空目录权限历史失败',
        description: '请检查浏览器本地存储权限。',
        color: 'danger',
      })
      return
    }
    try {
      await clearDirectoryAccessReviewRecords()
      historyRef.current = []
      setHistory([])
      void historyQuery.refetch()
      addToast({ title: '目录权限近期复核历史已清空', color: 'success' })
    } catch {
      if (serverHistory.length > 0) {
        historyRef.current = serverHistory
        setHistory(serverHistory)
        addToast({
          title: '本机目录权限历史已清空',
          description: '服务端历史清空失败，仍保留已持久化记录。',
          color: 'warning',
        })
      } else {
        historyRef.current = []
        setHistory([])
        addToast({ title: '目录权限近期复核历史已清空', color: 'success' })
      }
    }
  }, [historyQuery, historyStorageKey, serverHistory])

  if (settingsQuery.isLoading) {
    return (
      <div role="status" aria-label="加载目录访问策略" aria-busy="true" className="rounded-lg border border-divider bg-content1 p-6 text-sm text-default-500">
        正在加载目录访问策略…
      </div>
    )
  }

  if (settingsQuery.error && !settingsQuery.data) {
    return (
      <div role="alert" className="rounded-lg border border-danger/25 bg-danger/5 p-4">
        <div className="font-medium text-foreground">目录访问策略加载失败</div>
        <p className="mt-1 text-sm text-default-600">{getUserFacingErrorDescription(settingsQuery.error)}</p>
        <Button
          className="mt-3 rounded-lg"
          variant="bordered"
          startContent={<RefreshCw size={16} aria-hidden="true" />}
          onPress={() => void settingsQuery.refetch()}
        >
          重新加载
        </Button>
      </div>
    )
  }

  return (
    <div className="min-w-0 space-y-6" aria-label="目录与访问管理">
      <div className="flex flex-col gap-3 rounded-lg border border-divider bg-content1 px-4 py-4 shadow-[var(--shadow-soft)] sm:flex-row sm:items-center sm:justify-between sm:px-5">
        <div className="min-w-0">
          <h1 className="text-lg font-semibold text-foreground">目录与访问</h1>
          <p className="mt-1 text-sm leading-6 text-default-500">集中管理目录容量边界、共享目录权限和有效权限复核。</p>
        </div>
        <div className="flex w-full flex-col gap-2 sm:w-auto sm:flex-row">
          <Button
            variant="light"
            className="w-full rounded-lg sm:w-auto"
            startContent={<RefreshCw size={16} aria-hidden="true" />}
            isLoading={settingsQuery.isRefetching}
            isDisabled={isDirty || saveMutation.isPending}
            onPress={() => void settingsQuery.refetch()}
          >
            刷新
          </Button>
          <Button
            variant="bordered"
            className="w-full rounded-lg sm:w-auto"
            isDisabled={!isDirty || saveMutation.isPending}
            onPress={handleReset}
          >
            重置草稿
          </Button>
          <Button
            color="primary"
            className="w-full rounded-lg sm:w-auto"
            startContent={<Save size={16} aria-hidden="true" />}
            isDisabled={!isDirty}
            isLoading={saveMutation.isPending}
            onPress={handleSave}
          >
            保存目录策略
          </Button>
        </div>
      </div>

      {isDirty && (
        <div role="status" className="rounded-lg border border-warning/25 bg-warning/5 px-4 py-3 text-sm text-warning">
          当前有未保存的目录策略。后台刷新不会覆盖这些草稿。
        </div>
      )}

      <AccessSection
        title="目录配额"
        description="限制逻辑目录的当前文件总量；保存后用于 API 与 WebDAV 写入。"
        icon={HardDrive}
      >
        <div className="space-y-3">
          <textarea
            aria-label="目录配额"
            value={draft.directoryQuotas}
            onChange={(event) => updateDraft((current) => ({ ...current, directoryQuotas: event.target.value }))}
            rows={4}
            placeholder={'/team 1 TB\n"/Family Photos" 500 GB'}
            className="input-shell w-full min-w-0 rounded-medium border border-transparent bg-transparent px-3 py-2 font-mono text-sm outline-none focus:border-accent-primary"
          />
          <DirectoryQuotaChangeReview saved={savedQuotas} draftValue={draft.directoryQuotas} />
          <div className="grid gap-2 text-xs leading-5 text-default-500 sm:grid-cols-2">
            <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
              每行一个目录，例如 <span className="font-mono text-foreground">/team 1 TB</span>；路径含空格或双引号时使用双引号。
            </div>
            <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
              配额覆盖上传、复制、移动、恢复和 WebDAV 写入。
            </div>
          </div>
        </div>
      </AccessSection>

      <AccessSection
        title="目录权限"
        description="为共享目录授予读写权限；最具体的路径规则优先。"
        icon={Shield}
      >
        <div className="space-y-3">
          <AccessRuleEditor
            value={draft.directoryAccessRules}
            onChange={(value) => updateDraft((current) => ({ ...current, directoryAccessRules: value }))}
          />
          <AccessRuleChangeReview saved={savedRules} draftValue={draft.directoryAccessRules} />
          <AccessCoverageSummary draftValue={draft.directoryAccessRules} />
          <div className="grid gap-2 text-xs leading-5 text-default-500 sm:grid-cols-2">
            <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
              路径填写 MnemoNAS 逻辑路径；路径中有空格或双引号时无需额外加引号。
            </div>
            <div className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
              用户、用户组和角色支持多个值，使用英文逗号分隔。
            </div>
          </div>
          <div className="rounded-lg border border-divider bg-content1/60 p-3" aria-label="有效权限复核">
            <div className="grid min-w-0 gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto_auto]">
              <Input
                label="检查用户"
                value={checkUsername}
                onValueChange={setCheckUsername}
                placeholder="alice"
                className="input-shell min-w-0"
              />
              <Input
                label="检查路径"
                value={checkPath}
                onValueChange={setCheckPath}
                placeholder="/team/readme.txt"
                className="input-shell min-w-0"
              />
              <Button color="primary" className="w-full self-end rounded-lg" onPress={handleCheck} isLoading={checkMutation.isPending}>
                检查权限
              </Button>
              <Button variant="bordered" className="w-full self-end rounded-lg" onPress={handleReport} isLoading={reportMutation.isPending}>
                用户矩阵
              </Button>
              <Button variant="bordered" className="w-full self-end rounded-lg" onPress={handlePreview} isLoading={previewMutation.isPending}>
                预览变更
              </Button>
            </div>
            <p className="mt-2 text-xs leading-5 text-default-500">用户矩阵使用已保存配置；预览变更使用当前草稿。</p>
          </div>
          {checkMutation.data && <AccessCheckResult result={checkMutation.data} />}
          {reportMutation.data && <ReportResult report={reportMutation.data} onSave={saveReview} />}
          {previewMutation.data && (
            <ReportResult
              report={previewMutation.data}
              title="变更预览"
              ariaLabel="目录权限变更预览"
              onSave={saveReview}
            />
          )}
          <ReviewHistory entries={history} onCopy={copyHistory} onClear={clearHistory} />
        </div>
      </AccessSection>
    </div>
  )
}
