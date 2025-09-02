import { useEffect, useRef, useState } from 'react'
import { useCallback } from 'react'
import { useSearchParams, type SetURLSearchParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { 
  Button, 
  Input,
  Chip,
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  useDisclosure,
  Skeleton,
  addToast,
} from '@heroui/react'
import {
  History,
  Search,
  RotateCcw,
  Download,
  Eye,
  Clock,
  FileText,
  ChevronRight,
  AlertCircle,
  Sparkles,
  HardDrive,
  Fingerprint,
} from 'lucide-react'
import { ensureDownloadSession } from '@/api/auth'
import { ApiError, getVersions, buildDownloadUrl, downloadFile, restoreVersion, type ActionResult, type VersionInfo } from '@/api/files'
import { useIsAdmin, useUser } from '@/stores/auth'
import { formatBytes, formatDate, normalizePath, openUrlInNewTab } from '@/lib/utils'
import { getInvalidHomeDirDescription, invalidHomeDirTitle, resolveUserHomeScope } from '@/lib/userScope'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'

function pathWithinBase(basePath: string, targetPath: string): boolean {
  if (basePath === '/') {
    return targetPath.startsWith('/')
  }
  return targetPath === basePath || targetPath.startsWith(`${basePath}/`)
}

function getVersionQueryState(path: string, scopedHomeDir: string): {
  searchPath: string
  selectedPath: string | null
  isBlocked: boolean
} {
  if (!path) {
    return {
      searchPath: '',
      selectedPath: null,
      isBlocked: false,
    }
  }

  if (scopedHomeDir && !pathWithinBase(scopedHomeDir, path)) {
    return {
      searchPath: scopedHomeDir,
      selectedPath: null,
      isBlocked: true,
    }
  }

  return {
    searchPath: path,
    selectedPath: path,
    isBlocked: false,
  }
}

function getVersionsErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '版本历史暂不可用',
      description: '版本存储当前不可用，请检查系统状态或稍后重试。',
    }
  }

  return {
    title: '获取版本历史失败',
    description: error instanceof Error ? error.message : '请稍后重试',
  }
}

function getVersionsActionErrorToast(
  error: unknown,
  titles: {
    unavailable: string
    failure: string
  }
): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: titles.unavailable,
      description: '版本存储当前不可用，请检查系统状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: titles.failure,
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
}

function isMissingVersionError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404
}

function getMissingVersionToast(action: 'restore' | 'download'): {
  title: string
  description: string
  color: 'warning'
} {
  return {
    title: action === 'restore' ? '所选版本已不存在，已同步更新' : '所选版本已不存在',
    description: '该版本或目标文件已被移除，请刷新版本历史后重试。',
    color: 'warning',
  }
}

function getVersionsActionSuccessToast(result: ActionResult): {
  title: string
  color: 'success' | 'warning'
} {
  if (result.warning) {
    return {
      title: result.message ?? '恢复版本完成，但存在警告',
      color: 'warning',
    }
  }

  return {
    title: '恢复版本成功',
    color: 'success',
  }
}

interface VersionRowProps {
  version: VersionInfo
  index: number
  isLatest: boolean
  canRestore: boolean
  onPreview: () => void
  onRestore: () => void
  onDownload: () => void
}

function VersionRow({ version, index, isLatest, canRestore, onPreview, onRestore, onDownload }: VersionRowProps) {
  return (
    <div
      role="listitem"
      className="grid gap-4 border-b border-divider px-4 py-4 last:border-b-0 lg:grid-cols-[minmax(180px,1.1fr)_minmax(150px,0.75fr)_minmax(90px,0.45fr)_minmax(180px,0.8fr)_auto] lg:items-center"
    >
      <div className="flex min-w-0 items-center gap-3">
        <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg bg-accent-primary/12 text-accent-primary">
          <span className="font-mono text-sm font-semibold">v{index + 1}</span>
        </div>
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium text-foreground">
              {isLatest ? '当前版本' : '历史版本'}
            </span>
            {isLatest && (
              <Chip size="sm" variant="flat" className="chip-soft">
                当前文件
              </Chip>
            )}
          </div>
          <p className="mt-1 text-xs text-default-500">
            {isLatest ? '文件当前保存状态' : '可预览、下载或恢复'}
          </p>
        </div>
      </div>

      <div className="flex min-w-0 items-center gap-2 text-sm text-default-600">
        <Clock size={14} className="shrink-0 text-default-400" />
        <div className="min-w-0">
          <div className="text-xs text-default-400 lg:hidden">保存时间</div>
          <span className="truncate">{formatDate(version.timestamp)}</span>
        </div>
      </div>

      <div className="flex items-center gap-2 text-sm text-default-600">
        <HardDrive size={14} className="text-default-400" />
        <div>
          <div className="text-xs text-default-400 lg:hidden">大小</div>
          <span className="font-medium text-foreground">{formatBytes(version.size)}</span>
        </div>
      </div>

      <div className="flex min-w-0 items-center gap-2 text-sm text-default-600">
        <Fingerprint size={14} className="shrink-0 text-default-400" />
        <div className="min-w-0">
          <div className="text-xs text-default-400 lg:hidden">版本 ID</div>
          <code className="block truncate font-mono text-xs text-default-500">
            {version.hash}
          </code>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2 lg:justify-end">
          <Button
            size="sm"
            variant="bordered"
            aria-label="预览此版本"
            onPress={onPreview}
            title="预览"
            className="btn-secondary btn-sm rounded-lg"
            startContent={<Eye size={14} />}
          >
            预览
          </Button>
          <Button
            size="sm"
            variant="bordered"
            aria-label="下载此版本"
            onPress={onDownload}
            title="下载此版本"
            className="btn-secondary btn-sm rounded-lg"
            startContent={<Download size={14} />}
          >
            下载
          </Button>
          {canRestore && !isLatest && (
            <Button
              size="sm"
              variant="flat"
              color="warning"
              aria-label="恢复到此版本"
              onPress={onRestore}
              title="恢复到此版本"
              className="rounded-lg"
              startContent={<RotateCcw size={14} />}
            >
              恢复
            </Button>
          )}
      </div>
    </div>
  )
}

export function VersionsPage() {
  const isAdmin = useIsAdmin()
  const user = useUser()
  const authScopeKey = `${user?.id ?? 'anonymous'}:${isAdmin ? 'admin' : 'scoped'}:${!isAdmin && user?.homeDir ? user.homeDir : '/'}`
  const [searchParams, setSearchParams] = useSearchParams()
  const initialPath = (searchParams.get('path') || '').trim()
  const normalizedInitialPath = initialPath ? (initialPath.startsWith('/') ? initialPath : `/${initialPath}`) : ''
  const { scopedHomeDir, hasInvalidHomeDir } = resolveUserHomeScope(user)
  const effectiveScopedHomeDir = !isAdmin && scopedHomeDir ? scopedHomeDir : ''

  return (
    <VersionsPageContent
      key={`${hasInvalidHomeDir ? 'invalid' : effectiveScopedHomeDir}:${normalizedInitialPath || '__empty__'}`}
      authScopeKey={authScopeKey}
      initialPath={normalizedInitialPath}
      isAdmin={isAdmin}
      scopedHomeDir={effectiveScopedHomeDir}
      hasInvalidHomeDir={hasInvalidHomeDir && !isAdmin}
      setSearchParams={setSearchParams}
    />
  )
}

interface VersionsPageContentProps {
  authScopeKey: string
  initialPath: string
  isAdmin: boolean
  scopedHomeDir: string
  hasInvalidHomeDir: boolean
  setSearchParams: SetURLSearchParams
}

function VersionsPageContent({ authScopeKey, initialPath, isAdmin, scopedHomeDir, hasInvalidHomeDir, setSearchParams }: VersionsPageContentProps) {
  const initialState = hasInvalidHomeDir
    ? { searchPath: '', selectedPath: null, isBlocked: false }
    : getVersionQueryState(initialPath, scopedHomeDir)
  const [searchPath, setSearchPath] = useState(initialState.searchPath)
  const [selectedPath, setSelectedPath] = useState<string | null>(initialState.selectedPath)
  const [selectedVersion, setSelectedVersion] = useState<VersionInfo | null>(null)
  const { isOpen, onOpen, onClose } = useDisclosure()
  const queryClient = useQueryClient()
  const restoreSessionRef = useRef(0)
  const currentSelectedPathRef = useRef<string | null>(initialState.selectedPath)
  const currentSelectedVersionRef = useRef<VersionInfo | null>(null)
  const selectedPathAllowed = !hasInvalidHomeDir && (!selectedPath || !scopedHomeDir || pathWithinBase(scopedHomeDir, selectedPath))

  useEffect(() => {
    currentSelectedPathRef.current = selectedPath
  }, [selectedPath])

  useEffect(() => {
    currentSelectedVersionRef.current = selectedVersion
  }, [selectedVersion])

  useEffect(() => {
    if (hasInvalidHomeDir) {
      setSearchParams({})
      return
    }
    if (!initialState.isBlocked) {
      return
    }

    addToast({
      title: '仅可查看主目录内文件的版本历史',
      color: 'warning',
    })
    setSearchParams({})
  }, [hasInvalidHomeDir, initialState.isBlocked, setSearchParams])

  const { data: versions, isLoading, error, refetch } = useQuery({
    queryKey: ['versions', authScopeKey, selectedPath],
    queryFn: () => getVersions(selectedPath!),
    enabled: !!selectedPath && selectedPathAllowed,
  })
  const versionsErrorPresentation = getVersionsErrorPresentation(error)

  const handleRefreshVersions = async () => {
    const result = await refetch()
    if (result.error) {
      addToast(getVersionsActionErrorToast(result.error, {
        unavailable: '版本历史暂不可用',
        failure: '刷新失败',
      }))
      return
    }
    addToast({ title: '版本历史已刷新', color: 'success' })
  }

  const restoreMutation = useMutation({
    mutationFn: async ({ path, hash }: { path: string; hash: string; sessionId: number }) => {
      return restoreVersion(path, hash)
    },
    retry: false,
    onSuccess: (result, variables) => {
      queryClient.invalidateQueries({ queryKey: ['versions', authScopeKey, variables.path] })
      queryClient.invalidateQueries({ queryKey: ['files'] })
      addToast(getVersionsActionSuccessToast(result))
      if (
        restoreSessionRef.current === variables.sessionId
        && currentSelectedPathRef.current === variables.path
        && currentSelectedVersionRef.current?.hash === variables.hash
      ) {
        onClose()
      }
    },
    onError: (error: unknown, variables) => {
      if (isMissingVersionError(error)) {
        queryClient.invalidateQueries({ queryKey: ['versions', authScopeKey, variables.path] })
        queryClient.invalidateQueries({ queryKey: ['files'] })
        if (
          restoreSessionRef.current === variables.sessionId
          && currentSelectedPathRef.current === variables.path
          && currentSelectedVersionRef.current?.hash === variables.hash
        ) {
          onClose()
        }
        addToast(getMissingVersionToast('restore'))
        return
      }

      addToast(getVersionsActionErrorToast(error, {
        unavailable: '恢复版本暂不可用',
        failure: '恢复版本失败',
      }))
    },
  })

  const handleSearch = () => {
    if (hasInvalidHomeDir) {
      return
    }
    const trimmedPath = searchPath.trim()
    if (trimmedPath) {
      let normalizedPath: string
      try {
        normalizedPath = normalizePath(trimmedPath)
      } catch {
        addToast({ title: '文件路径无效', color: 'danger' })
        return
      }
      if (scopedHomeDir && !pathWithinBase(scopedHomeDir, normalizedPath)) {
        addToast({
          title: '仅可查看主目录内文件的版本历史',
          color: 'warning',
        })
        setSearchPath(scopedHomeDir)
        return
      }
      setSearchPath(normalizedPath)
      setSelectedPath(normalizedPath)
      setSearchParams({ path: normalizedPath })
    } else {
      setSearchPath('')
      setSelectedPath(null)
      setSearchParams({})
    }
  }

  const handleRestore = (version: VersionInfo) => {
    restoreSessionRef.current += 1
    setSelectedVersion(version)
    onOpen()
  }

  const confirmRestore = () => {
    if (selectedPath && selectedVersion) {
      restoreMutation.mutate({
        path: selectedPath,
        hash: selectedVersion.hash,
        sessionId: restoreSessionRef.current,
      })
    }
  }

  const handleCloseRestoreModal = useCallback(() => {
    if (restoreMutation.isPending) {
      return
    }
    onClose()
  }, [onClose, restoreMutation.isPending])

  const handleDownload = (version: VersionInfo) => {
    void downloadFile(selectedPath!, { version: version.hash }).catch((error: unknown) => {
      if (selectedPath && isMissingVersionError(error)) {
        queryClient.invalidateQueries({ queryKey: ['versions', authScopeKey, selectedPath] })
        addToast(getMissingVersionToast('download'))
        return
      }

      addToast(getVersionsActionErrorToast(error, {
        unavailable: '下载版本暂不可用',
        failure: '下载版本失败',
      }))
    })
  }

  const handlePreview = async (version: VersionInfo) => {
    if (!selectedPath) return

    const session = await ensureDownloadSession()
    if (!session.ok) {
      addToast({ title: session.message ?? '原始预览和下载会话同步失败，请稍后重试', color: 'warning' })
      return
    }

    const url = buildDownloadUrl(selectedPath, { version: version.hash })
    if (!openUrlInNewTab(url)) {
      addToast({ title: '浏览器拦截了新标签页，请允许弹窗后重试', color: 'warning' })
    }
  }

  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="space-y-6 p-4 sm:p-6">
      {/* Header */}
      <PageHeader
        title="版本历史"
        subtitle={hasInvalidHomeDir ? '主目录配置无效' : isAdmin ? '查看、下载或恢复文件版本' : '查看和下载文件版本'}
        icon={History}
      />

      {hasInvalidHomeDir && (
        <EmptyState
          icon={AlertCircle}
          title={invalidHomeDirTitle}
          description={getInvalidHomeDirDescription('查看版本历史')}
        />
      )}

      {/* Search */}
      {!hasInvalidHomeDir && (
      <div className="rounded-lg border border-divider bg-content1 p-4 shadow-[var(--shadow-soft)]">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:gap-4">
          <Input
            placeholder="输入文件路径，例如: /documents/report.pdf"
            value={searchPath}
            onValueChange={setSearchPath}
            onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
            startContent={<FileText size={18} className="text-default-400" />}
            classNames={{
              base: "flex-1",
              inputWrapper: "bg-content2/50 border border-divider hover:border-divider",
            }}
          />
          <Button
            color="primary" 
            startContent={<Search size={16} />}
            onPress={handleSearch}
            className="rounded-lg font-medium sm:shrink-0"
          >
            查询
          </Button>
        </div>
      </div>
      )}

      {/* Version List */}
      {!hasInvalidHomeDir && selectedPath && (
        <div className="space-y-4 rounded-lg border border-divider bg-content1 p-4 shadow-[var(--shadow-soft)] sm:p-6">
          <div className="flex min-w-0 flex-wrap items-center gap-2 text-default-500">
            <History size={18} />
            <span>当前文件</span>
            <code className="break-anywhere rounded-lg border border-divider bg-content2/50 px-3 py-1 font-mono text-sm text-foreground">
              {selectedPath}
            </code>
          </div>

          {isLoading ? (
            <div className="space-y-2">
              {[1, 2, 3].map((i) => (
                <Skeleton key={i} className="w-full h-14 rounded-lg" />
              ))}
            </div>
          ) : error ? (
            <div className="flex flex-col items-center justify-center py-12 text-default-400">
              <div className="w-16 h-16 rounded-lg bg-danger/10 flex items-center justify-center mb-4">
                <AlertCircle size={32} className="text-danger" />
              </div>
              <p className="font-medium text-danger mb-1">{versionsErrorPresentation.title}</p>
              <p className="text-sm">{versionsErrorPresentation.description}</p>
              <Button variant="bordered" className="mt-4 rounded-lg" onPress={handleRefreshVersions}>
                重新加载
              </Button>
            </div>
          ) : versions && versions.length > 0 ? (
            <div className="space-y-3">
              {versions.length === 1 && (
                <div className="flex items-start gap-3 rounded-lg border border-accent-primary/20 bg-accent-primary/10 px-4 py-3 text-sm">
                  <History size={18} className="mt-0.5 shrink-0 text-accent-primary" />
                  <div>
                    <p className="font-medium text-foreground">仅有当前版本</p>
                    <p className="mt-0.5 text-default-600">这个文件还没有可恢复的历史版本。文件更新后，旧版本会在这里保留。</p>
                  </div>
                </div>
              )}

              <div role="list" aria-label="版本历史" className="overflow-hidden rounded-lg border border-divider bg-content1">
                <div className="hidden border-b border-divider bg-content2/40 px-4 py-2 text-xs font-medium text-default-500 lg:grid lg:grid-cols-[minmax(180px,1.1fr)_minmax(150px,0.75fr)_minmax(90px,0.45fr)_minmax(180px,0.8fr)_auto]">
                  <span>版本</span>
                  <span>保存时间</span>
                  <span>大小</span>
                  <span>版本 ID</span>
                  <span className="text-right">操作</span>
                </div>
                {versions.map((version, index) => (
                    <VersionRow
                      key={version.hash}
                      version={version}
                      index={versions.length - index - 1}
                      isLatest={index === 0}
                      canRestore={isAdmin}
                      onPreview={() => handlePreview(version)}
                      onRestore={() => handleRestore(version)}
                      onDownload={() => handleDownload(version)}
                    />
                ))}
              </div>
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center py-12 text-default-400">
              <div className="w-16 h-16 rounded-lg bg-default-100 flex items-center justify-center mb-4">
                <History size={32} className="text-default-300" />
              </div>
              <p className="font-medium mb-1">未找到版本记录</p>
              <p className="text-sm">请确认文件路径是否正确，或稍后在文件更新后再查看。</p>
            </div>
          )}
        </div>
      )}

      {/* Empty State */}
      {!hasInvalidHomeDir && !selectedPath && (
        <EmptyState
          icon={Sparkles}
          title="查看文件版本历史"
          description={isAdmin
            ? '输入文件路径后可查看版本记录，支持预览、下载和恢复。'
            : '输入主目录内文件路径后可查看版本记录，支持预览和下载。'}
          action={
            <div className="flex flex-wrap items-center justify-center gap-2 rounded-lg bg-content2/50 px-4 py-2 text-sm text-default-500">
              <span>也可以在文件列表中打开文件菜单</span>
              <ChevronRight size={14} />
              <span className="font-medium text-primary">版本历史</span>
            </div>
          }
        />
      )}

      {/* Restore Confirmation Modal */}
      <Modal 
        isOpen={isOpen} 
        onClose={handleCloseRestoreModal}
        classNames={{
          base: "bg-content1 border border-divider",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex flex-col gap-1">
            <div className="flex items-center gap-3">
              <div className="w-10 h-10 rounded-lg bg-warning/10 flex items-center justify-center">
                <RotateCcw size={20} className="text-warning" />
              </div>
              <span>确认恢复版本</span>
            </div>
          </ModalHeader>
          <ModalBody>
            <div className="space-y-4">
              <p>确定要将文件恢复到以下版本吗？</p>
              {selectedVersion && (
                <div className="bg-content2/50 border border-divider rounded-lg p-4 space-y-3">
                  <div className="flex justify-between">
                    <span className="text-default-400">修改时间</span>
                    <span className="font-medium">{formatDate(selectedVersion.timestamp)}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-default-400">文件大小</span>
                    <span className="font-medium">{formatBytes(selectedVersion.size)}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-default-400">Hash</span>
                    <code className="text-xs font-mono">{selectedVersion.hash.substring(0, 16)}...</code>
                  </div>
                </div>
              )}
              <div className="flex items-start gap-2 text-warning text-sm bg-warning/10 border border-warning/20 rounded-lg p-3">
                <AlertCircle size={16} className="mt-0.5 shrink-0" />
                <span>当前版本将被保存为新的历史版本</span>
              </div>
            </div>
          </ModalBody>
          <ModalFooter>
            <Button variant="light" onPress={handleCloseRestoreModal} isDisabled={restoreMutation.isPending} className="rounded-lg">
              取消
            </Button>
            {isAdmin && (
              <Button 
                color="warning" 
                onPress={confirmRestore}
                isLoading={restoreMutation.isPending}
                className="rounded-lg"
              >
                确认恢复
              </Button>
            )}
          </ModalFooter>
        </ModalContent>
      </Modal>
      </div>
    </div>
  )
}
