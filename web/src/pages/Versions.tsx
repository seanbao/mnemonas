import { useEffect, useRef, useState } from 'react'
import { useCallback } from 'react'
import { useSearchParams, type SetURLSearchParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { 
  Button, 
  Input,
  Table,
  TableHeader,
  TableColumn,
  TableBody,
  TableRow,
  TableCell,
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
  Sparkles
} from 'lucide-react'
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
    <TableRow key={version.hash} className="hover:bg-content2 transition-colors">
      <TableCell>
        <div className="flex items-center gap-2">
          <div className="w-8 h-8 rounded-lg bg-accent-primary/15 flex items-center justify-center">
            <span className="font-mono text-sm font-medium text-accent-primary">v{index + 1}</span>
          </div>
          {isLatest && (
            <Chip size="sm" variant="flat" className="chip-soft">
              当前版本
            </Chip>
          )}
        </div>
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-2 text-default-400">
          <Clock size={14} />
          <span>{formatDate(version.timestamp)}</span>
        </div>
      </TableCell>
      <TableCell>
        <span className="font-medium">{formatBytes(version.size)}</span>
      </TableCell>
      <TableCell>
        <code className="text-xs bg-content2/50 border border-divider px-2 py-1 rounded-lg font-mono">
          {version.hash.substring(0, 12)}...
        </code>
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-1">
          <Button
            isIconOnly
            size="sm"
            variant="light"
            aria-label="预览此版本"
            onPress={onPreview}
            title="预览"
            className="btn-secondary btn-sm rounded-xl"
          >
            <Eye size={16} />
          </Button>
          <Button
            isIconOnly
            size="sm"
            variant="light"
            aria-label="下载此版本"
            onPress={onDownload}
            title="下载此版本"
            className="btn-secondary btn-sm rounded-xl"
          >
            <Download size={16} />
          </Button>
          {canRestore && !isLatest && (
            <Button
              isIconOnly
              size="sm"
              variant="light"
              color="warning"
              aria-label="恢复到此版本"
              onPress={onRestore}
              title="恢复到此版本"
              className="btn-secondary btn-sm rounded-xl"
            >
              <RotateCcw size={16} />
            </Button>
          )}
        </div>
      </TableCell>
    </TableRow>
  )
}

export function VersionsPage() {
  const isAdmin = useIsAdmin()
  const user = useUser()
  const [searchParams, setSearchParams] = useSearchParams()
  const initialPath = (searchParams.get('path') || '').trim()
  const normalizedInitialPath = initialPath ? (initialPath.startsWith('/') ? initialPath : `/${initialPath}`) : ''
  const { scopedHomeDir, hasInvalidHomeDir } = resolveUserHomeScope(user)
  const effectiveScopedHomeDir = !isAdmin && scopedHomeDir ? scopedHomeDir : ''

  return (
    <VersionsPageContent
      key={`${hasInvalidHomeDir ? 'invalid' : effectiveScopedHomeDir}:${normalizedInitialPath || '__empty__'}`}
      initialPath={normalizedInitialPath}
      isAdmin={isAdmin}
      scopedHomeDir={effectiveScopedHomeDir}
      hasInvalidHomeDir={hasInvalidHomeDir && !isAdmin}
      setSearchParams={setSearchParams}
    />
  )
}

interface VersionsPageContentProps {
  initialPath: string
  isAdmin: boolean
  scopedHomeDir: string
  hasInvalidHomeDir: boolean
  setSearchParams: SetURLSearchParams
}

function VersionsPageContent({ initialPath, isAdmin, scopedHomeDir, hasInvalidHomeDir, setSearchParams }: VersionsPageContentProps) {
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
      setSearchPath('')
      setSelectedPath(null)
      setSelectedVersion(null)
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
    queryKey: ['versions', selectedPath],
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
      queryClient.invalidateQueries({ queryKey: ['versions', variables.path] })
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
        queryClient.invalidateQueries({ queryKey: ['versions', variables.path] })
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
        queryClient.invalidateQueries({ queryKey: ['versions', selectedPath] })
        addToast(getMissingVersionToast('download'))
        return
      }

      addToast(getVersionsActionErrorToast(error, {
        unavailable: '下载版本暂不可用',
        failure: '下载版本失败',
      }))
    })
  }

  const handlePreview = (version: VersionInfo) => {
    if (!selectedPath) return
    const url = buildDownloadUrl(selectedPath, { version: version.hash })
    if (!openUrlInNewTab(url)) {
      addToast({ title: '浏览器拦截了新标签页，请允许弹窗后重试', color: 'warning' })
    }
  }

  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="p-6 space-y-6">
      {/* Header */}
      <PageHeader
        title="版本历史"
        subtitle={hasInvalidHomeDir ? '主目录配置无效' : isAdmin ? '查看和恢复文件历史版本' : '查看文件历史版本'}
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
      <div className="card-meridian rounded-xl p-4">
        <div className="flex items-center gap-4">
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
            className="font-medium rounded-xl"
          >
            查询版本
          </Button>
        </div>
      </div>
      )}

      {/* Version List */}
      {!hasInvalidHomeDir && selectedPath && (
        <div className="card-meridian rounded-xl p-6 space-y-4">
          <div className="flex items-center gap-2 text-default-400">
            <History size={18} />
            <span>文件路径:</span>
            <code className="bg-content2/50 border border-divider px-3 py-1 rounded-lg text-sm font-mono text-foreground">
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
              <div className="w-16 h-16 rounded-xl bg-danger/10 flex items-center justify-center mb-4">
                <AlertCircle size={32} className="text-danger" />
              </div>
              <p className="font-medium text-danger mb-1">{versionsErrorPresentation.title}</p>
              <p className="text-sm">{versionsErrorPresentation.description}</p>
              <Button variant="bordered" className="mt-4 rounded-xl" onPress={handleRefreshVersions}>
                重新加载
              </Button>
            </div>
          ) : versions && versions.length > 0 ? (
            <Table 
              aria-label="版本历史" 
              removeWrapper
              classNames={{
                th: "table-head font-medium",
                td: "py-2.5",
              }}
            >
              <TableHeader>
                <TableColumn>版本</TableColumn>
                <TableColumn>修改时间</TableColumn>
                <TableColumn>大小</TableColumn>
                <TableColumn>Hash</TableColumn>
                <TableColumn>操作</TableColumn>
              </TableHeader>
              <TableBody>
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
              </TableBody>
            </Table>
          ) : (
            <div className="flex flex-col items-center justify-center py-12 text-default-400">
              <div className="w-16 h-16 rounded-xl bg-default-100 flex items-center justify-center mb-4">
                <History size={32} className="text-default-300" />
              </div>
              <p className="font-medium mb-1">暂无版本历史</p>
              <p className="text-sm">该文件可能是新创建的或路径不存在</p>
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
            ? 'MnemoNAS 自动保留每个文件的历史版本。输入文件路径即可查看所有历史版本，支持预览、下载和一键回滚。'
            : 'MnemoNAS 自动保留主目录内文件的历史版本。输入文件路径即可查看历史版本，支持预览和下载。'}
          action={
            <div className="flex items-center gap-2 text-sm text-default-500 bg-content2/50 px-4 py-2 rounded-lg">
              <span>提示: 也可以在文件管理器中右键点击文件</span>
              <ChevronRight size={14} />
              <span className="font-medium text-primary">"版本历史"</span>
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
                <div className="bg-content2/50 border border-divider rounded-xl p-4 space-y-3">
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
            <Button variant="light" onPress={handleCloseRestoreModal} isDisabled={restoreMutation.isPending} className="rounded-xl">
              取消
            </Button>
            {isAdmin && (
              <Button 
                color="warning" 
                onPress={confirmRestore}
                isLoading={restoreMutation.isPending}
                className="rounded-xl"
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
