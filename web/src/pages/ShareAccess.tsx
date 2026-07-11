import { useState, useEffect, useCallback, useRef } from 'react'
import { useParams } from 'react-router-dom'
import {
  Card,
  CardBody,
  Button,
  Input,
  addToast,
} from '@heroui/react'
import {
  Download,
  Lock,
  AlertCircle,
  HardDrive,
  Folder,
  ChevronLeft,
} from 'lucide-react'
import {
  getPublicShare,
  accessShareWithPassword,
  downloadShare,
  getPublicShareItems,
  type PublicShareInfo,
  type PublicShareItem,
  ShareError,
} from '@/api/share'
import { EmptyState } from '@/components/ui/EmptyState'
import { FileIcon } from '@/components/ui/FileIcon'
import { ensureZipExtension, formatBytes, formatDate } from '@/lib/utils'
import { GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { getBrowserDownloadCapacityErrorToast } from '@/lib/fileActionErrors'
import { getFolderPathAfterShareAuth } from './shareAccessUtils'

const SHARE_ACCESS_PASSWORD_INPUT_ID = 'share-access-password'

function hasAuthorizedShareContent(info: PublicShareInfo): boolean {
  return info.file_name !== undefined || info.file_size !== undefined || info.folder_items !== undefined
}

function getShareArchiveFilename(name: string | undefined): string {
  return ensureZipExtension(name || 'share')
}

function isTerminalShareError(error: unknown): error is ShareError {
  return error instanceof ShareError
    && (error.isExpired
      || error.isDisabled
      || error.isAccessLimitReached
      || error.isNotFound
      || error.isFeatureDisabled)
}

function getGoneSharePresentation(error: ShareError): { title: string; description: string } | null {
  if (error.isDisabled) {
    return {
      title: '分享已停用',
      description: '该分享已被停用，当前不可访问。',
    }
  }

  if (error.isAccessLimitReached) {
    return {
      title: '分享下载次数已用尽',
      description: '该分享已达到下载次数上限，当前不可访问。',
    }
  }

  if (error.isExpired) {
    return {
      title: '分享已失效',
      description: error.code === 'SHARE_EXPIRED' ? '该分享已过期，当前不可访问。' : '该分享已失效，当前不可访问。',
    }
  }

  return null
}

function getMissingShareContentPresentation(error: ShareError): { title: string; description: string } | null {
  if (error.code !== 'FILE_NOT_FOUND') {
    return null
  }

  return {
    title: '分享内容已不存在',
    description: '该分享指向的文件或文件夹已被移动或删除，请联系分享创建者。',
  }
}

function getShareAccessErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ShareError) {
    if (error.isFeatureDisabled) {
      return {
        title: '分享功能已关闭',
        description: '当前服务已关闭分享功能，公开分享链接暂不可访问。',
      }
    }

    const missingContentPresentation = getMissingShareContentPresentation(error)
    if (missingContentPresentation) {
      return missingContentPresentation
    }

    if (error.isNotFound) {
      return {
        title: '分享不存在或已失效',
        description: '该分享链接不存在、已被移除，或当前不可访问。',
      }
    }

    const gonePresentation = getGoneSharePresentation(error)
    if (gonePresentation) {
      return gonePresentation
    }

    if (error.isUnavailable) {
      return {
        title: '分享内容暂不可用',
        description: '分享内容当前不可访问，请检查设备状态或稍后重试。',
      }
    }
  }

  if (error instanceof Error && error.message === '无效的分享链接') {
    return {
      title: '无法访问分享',
      description: '无效的分享链接',
    }
  }

  return {
    title: '无法访问分享',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
  }
}

function getShareListErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ShareError) {
    const missingContentPresentation = getMissingShareContentPresentation(error)
    if (missingContentPresentation) {
      return missingContentPresentation
    }

    if (error.isUnavailable) {
      return {
        title: '文件夹内容暂不可用',
        description: '分享目录当前不可访问，请检查设备状态或稍后重试。',
      }
    }
  }

  return {
    title: '加载文件夹失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
  }
}

function getShareActionErrorToast(
  error: unknown,
  titles: {
    unavailable: string
    failure: string
  }
): {
  title: string
  description?: string
  color: 'warning' | 'danger'
} {
  const browserCapacityToast = getBrowserDownloadCapacityErrorToast(error)
  if (browserCapacityToast) {
    return browserCapacityToast
  }

  if (error instanceof ShareError) {
    if (error.isFeatureDisabled) {
      return {
        title: '分享功能已关闭',
        description: '当前服务已关闭分享功能，公开分享链接暂不可访问。',
        color: 'warning',
      }
    }

    const missingContentPresentation = getMissingShareContentPresentation(error)
    if (missingContentPresentation) {
      return {
        ...missingContentPresentation,
        color: 'warning',
      }
    }

    const gonePresentation = getGoneSharePresentation(error)
    if (gonePresentation) {
      return {
        ...gonePresentation,
        color: 'warning',
      }
    }

    if (error.isUnavailable) {
      return {
        title: titles.unavailable,
        description: '分享内容当前不可访问，请检查设备状态或稍后重试。',
        color: 'warning',
      }
    }

    return {
      title: error.isRateLimited ? titles.unavailable : titles.failure,
      description: error.message,
      color: error.isRateLimited ? 'warning' : 'danger',
    }
  }

  return {
    title: titles.failure,
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}

export function ShareAccessPage() {
  const { id } = useParams<{ id: string }>()
  const [isLoading, setIsLoading] = useState(true)
  const [shareInfo, setShareInfo] = useState<PublicShareInfo | null>(null)
  const [error, setError] = useState<unknown | null>(null)
  const [needsPassword, setNeedsPassword] = useState(false)
  const [password, setPassword] = useState('')
  const [isVerifying, setIsVerifying] = useState(false)
  const [isAuthenticated, setIsAuthenticated] = useState(false)
  const [folderItems, setFolderItems] = useState<PublicShareItem[]>([])
  const [folderPath, setFolderPath] = useState('')
  const [isListing, setIsListing] = useState(false)
  const [listError, setListError] = useState<unknown | null>(null)
  const [downloadTarget, setDownloadTarget] = useState<string | null>(null)
  const shareInfoRequestRef = useRef(0)
  const folderListRequestRef = useRef(0)
  const shareInfoAbortControllerRef = useRef<AbortController | null>(null)
  const folderListAbortControllerRef = useRef<AbortController | null>(null)
  const downloadAbortControllerRef = useRef<AbortController | null>(null)
  const errorHeadingRef = useRef<HTMLHeadingElement | null>(null)
  const listErrorHeadingRef = useRef<HTMLHeadingElement | null>(null)
  const passwordInputRef = useRef<HTMLInputElement | null>(null)
  const errorPresentation = getShareAccessErrorPresentation(error)
  const listErrorPresentation = getShareListErrorPresentation(listError)

  useEffect(() => {
    if (error) {
      errorHeadingRef.current?.focus()
    }
  }, [error])

  useEffect(() => {
    if (listError) {
      listErrorHeadingRef.current?.focus()
    }
  }, [listError])

  useEffect(() => {
    if (needsPassword && !isAuthenticated) {
      passwordInputRef.current?.focus()
    }
  }, [isAuthenticated, needsPassword])

  const presentTerminalShareError = useCallback((terminalError: unknown): boolean => {
    if (!isTerminalShareError(terminalError)) {
      return false
    }

    shareInfoRequestRef.current += 1
    folderListRequestRef.current += 1
    shareInfoAbortControllerRef.current?.abort()
    folderListAbortControllerRef.current?.abort()
    downloadAbortControllerRef.current?.abort()
    shareInfoAbortControllerRef.current = null
    folderListAbortControllerRef.current = null
    downloadAbortControllerRef.current = null
    setError(terminalError)
    setShareInfo(null)
    setNeedsPassword(false)
    setPassword('')
    setIsVerifying(false)
    setIsAuthenticated(false)
    setFolderItems([])
    setFolderPath('')
    setIsListing(false)
    setListError(null)
    setDownloadTarget(null)
    setIsLoading(false)
    return true
  }, [])

  useEffect(() => () => {
    shareInfoRequestRef.current += 1
    folderListRequestRef.current += 1
    shareInfoAbortControllerRef.current?.abort()
    folderListAbortControllerRef.current?.abort()
    downloadAbortControllerRef.current?.abort()
    shareInfoAbortControllerRef.current = null
    folderListAbortControllerRef.current = null
    downloadAbortControllerRef.current = null
  }, [])

  const loadShareInfo = useCallback(async (options?: { notify?: boolean }) => {
    shareInfoAbortControllerRef.current?.abort()
    folderListAbortControllerRef.current?.abort()
    downloadAbortControllerRef.current?.abort()
    shareInfoAbortControllerRef.current = null
    folderListAbortControllerRef.current = null
    downloadAbortControllerRef.current = null
    setDownloadTarget(null)
    if (!id) {
      setError(new Error('无效的分享链接'))
      setIsLoading(false)
      if (options?.notify) {
        addToast({ title: '刷新失败', description: '无效的分享链接', color: 'danger' })
      }
      return
    }

    const requestId = shareInfoRequestRef.current + 1
    shareInfoRequestRef.current = requestId
    folderListRequestRef.current += 1
    const controller = new AbortController()
    shareInfoAbortControllerRef.current = controller
    setIsVerifying(false)
    setIsLoading(true)
    setError(null)
    setNeedsPassword(false)
    setIsAuthenticated(false)
    setPassword('')
    setShareInfo(null)
    setFolderItems([])
    setListError(null)
    
    try {
      const info = await getPublicShare(id, { signal: controller.signal })
      if (requestId !== shareInfoRequestRef.current) {
        return
      }
      setShareInfo(info)
      setFolderItems([])
      setListError(null)
      const hasAccess = !info.has_password || hasAuthorizedShareContent(info)
      if (info.type === 'folder') {
        setFolderPath('')
      }
      if (!hasAccess) {
        setNeedsPassword(true)
      } else {
        setIsAuthenticated(true)
      }
      if (options?.notify) {
        addToast({ title: '分享信息已刷新', color: 'success' })
      }
    } catch (err) {
      if (controller.signal.aborted) {
        return
      }
      if (requestId !== shareInfoRequestRef.current) {
        return
      }
      if (err instanceof ShareError) {
        setError(err)
      } else {
        setError(new Error('加载分享信息失败'))
      }
      if (options?.notify) {
        addToast(getShareActionErrorToast(err, {
          unavailable: '分享内容暂不可用',
          failure: '刷新失败',
        }))
      }
    } finally {
      if (shareInfoAbortControllerRef.current === controller) {
        shareInfoAbortControllerRef.current = null
      }
      if (requestId === shareInfoRequestRef.current) {
        setIsLoading(false)
      }
    }
  }, [id])

  useEffect(() => {
    let cancelled = false
    queueMicrotask(() => {
      if (!cancelled) {
        void loadShareInfo()
      }
    })

    return () => {
      cancelled = true
    }
  }, [loadShareInfo])

  const handlePasswordSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!id) return
    if (!password.trim()) {
      addToast({ title: '请输入访问密码', color: 'warning' })
      return
    }

    const requestId = shareInfoRequestRef.current + 1
    shareInfoRequestRef.current = requestId
    shareInfoAbortControllerRef.current?.abort()
    folderListAbortControllerRef.current?.abort()
    downloadAbortControllerRef.current?.abort()
    folderListAbortControllerRef.current = null
    downloadAbortControllerRef.current = null
    setDownloadTarget(null)
    const controller = new AbortController()
    shareInfoAbortControllerRef.current = controller
    setIsVerifying(true)
    try {
      const info = await accessShareWithPassword(id, password, { signal: controller.signal })
      if (requestId !== shareInfoRequestRef.current) {
        return
      }
      setShareInfo(info)
      setFolderItems([])
      setListError(null)
      setFolderPath((currentFolderPath) => getFolderPathAfterShareAuth(currentFolderPath, info))
      setIsAuthenticated(true)
      setNeedsPassword(false)
      setPassword('')
    } catch (err) {
      if (controller.signal.aborted) {
        return
      }
      if (requestId !== shareInfoRequestRef.current) {
        return
      }
      if (presentTerminalShareError(err)) {
        return
      }
      if (err instanceof ShareError && err.isUnauthorized) {
        addToast({ title: '密码错误', color: 'danger' })
      } else {
        addToast(getShareActionErrorToast(err, {
          unavailable: '验证暂不可用',
          failure: '验证失败',
        }))
      }
    } finally {
      if (shareInfoAbortControllerRef.current === controller) {
        shareInfoAbortControllerRef.current = null
      }
      if (requestId === shareInfoRequestRef.current) {
        setIsVerifying(false)
      }
    }
  }

  const runShareDownload = async (target: string, operation: (signal: AbortSignal) => Promise<void>) => {
    if (!id || downloadAbortControllerRef.current) return

    const requestId = shareInfoRequestRef.current
    const controller = new AbortController()
    downloadAbortControllerRef.current = controller
    setDownloadTarget(target)

    try {
      await operation(controller.signal)
    } catch (err) {
      if (controller.signal.aborted) {
        return
      }
      if (requestId !== shareInfoRequestRef.current) {
        return
      }
      if (presentTerminalShareError(err)) {
        return
      }
      if (err instanceof ShareError && err.isUnauthorized) {
        setIsAuthenticated(false)
        setNeedsPassword(true)
        setPassword('')
        setFolderItems([])
        setListError(null)
        addToast({ title: '访问凭证已失效，请重新输入密码', color: 'warning' })
        return
      }
      addToast(getShareActionErrorToast(err, {
        unavailable: '下载暂不可用',
        failure: '下载失败',
      }))
    } finally {
      if (downloadAbortControllerRef.current === controller) {
        downloadAbortControllerRef.current = null
        setDownloadTarget(null)
      }
    }
  }

  const handleDownload = async () => {
    if (!id || !shareInfo) return

    const target = shareInfo.type === 'folder' ? `folder:${folderPath}` : 'shared-file'
    await runShareDownload(target, async (signal) => {
      if (shareInfo.type === 'folder') {
        const currentFolderName = folderPath
          ? folderPath.split('/').filter(Boolean).pop()
          : shareInfo.file_name
        await downloadShare(id, {
          filePath: folderPath || undefined,
          archive: 'zip',
          filename: getShareArchiveFilename(currentFolderName),
          signal,
        })
      } else {
        await downloadShare(id, { filename: shareInfo.file_name, signal })
      }
    })
  }

  const handleDownloadItem = async (itemPath: string) => {
    if (!id) return

    const item = folderItems.find((folderItem) => folderItem.path === itemPath)
    if (!item) return

    await runShareDownload(`item:${itemPath}`, async (signal) => {
      if (item.is_dir) {
        await downloadShare(id, {
          filePath: itemPath,
          filename: getShareArchiveFilename(item.name),
          archive: 'zip',
          signal,
        })
      } else {
        await downloadShare(id, { filePath: itemPath, filename: item.name, signal })
      }
    })
  }

  const handleEnterFolder = (item: PublicShareItem) => {
    if (!item.is_dir) return
    setFolderPath(item.path)
  }

  const handleNavigateUp = () => {
    if (!folderPath) return
    const segments = folderPath.split('/').filter(Boolean)
    segments.pop()
    setFolderPath(segments.join('/'))
  }

  const loadFolderItems = useCallback(async () => {
    folderListAbortControllerRef.current?.abort()
    folderListAbortControllerRef.current = null
    if (!id || !shareInfo || shareInfo.type !== 'folder' || !isAuthenticated) return

    const requestId = folderListRequestRef.current + 1
    folderListRequestRef.current = requestId
    const controller = new AbortController()
    folderListAbortControllerRef.current = controller
    setIsListing(true)
    setListError(null)
    setFolderItems([])
    try {
      const data = await getPublicShareItems(id, {
        path: folderPath || undefined,
        signal: controller.signal,
      })
      if (requestId !== folderListRequestRef.current) {
        return
      }
      setFolderItems(data.items)
    } catch (err) {
      if (controller.signal.aborted) {
        return
      }
      if (requestId !== folderListRequestRef.current) {
        return
      }
      if (presentTerminalShareError(err)) {
        return
      }
      if (err instanceof ShareError && err.isUnauthorized) {
        setIsAuthenticated(false)
        setNeedsPassword(true)
        setPassword('')
        setListError(null)
      } else {
        setListError(err)
      }
    } finally {
      if (folderListAbortControllerRef.current === controller) {
        folderListAbortControllerRef.current = null
      }
      if (requestId === folderListRequestRef.current) {
        setIsListing(false)
      }
    }
  }, [id, shareInfo, isAuthenticated, folderPath, presentTerminalShareError])

  useEffect(() => {
    let cancelled = false
    queueMicrotask(() => {
      if (!cancelled) {
        void loadFolderItems()
      }
    })

    return () => {
      cancelled = true
    }
  }, [loadFolderItems])

  const isDownloading = downloadTarget !== null
  const sharedFileName = shareInfo?.file_name || '分享内容'
  const currentFolderName = folderPath
    ? folderPath.split('/').filter(Boolean).pop() || '当前文件夹'
    : sharedFileName
  const currentFolderDownloadTarget = `folder:${folderPath}`

  // Loading state
  if (isLoading) {
    return (
      <div className="app-shell flex min-h-[100svh] items-center justify-center bg-background px-4">
        <div className="text-center">
          <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-default-500">加载分享信息…</p>
        </div>
      </div>
    )
  }

  // Error state
  if (error) {
    return (
      <div className="app-shell flex min-h-[100svh] items-center justify-center bg-background px-4 py-10">
        <Card className="w-full max-w-md rounded-lg border border-divider bg-content1 shadow-sm">
          <CardBody className="p-6 sm:p-8">
            <div className="text-center" role="alert" aria-live="assertive" aria-atomic="true">
              <div className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-lg bg-danger/10">
                <AlertCircle size={28} className="text-danger" />
              </div>
              <h2 ref={errorHeadingRef} tabIndex={-1} className="mb-2 text-xl font-semibold text-foreground">
                {errorPresentation.title}
              </h2>
              <p className="text-sm leading-6 text-default-500">{errorPresentation.description}</p>
              <Button className="mt-4" variant="bordered" onPress={() => { void loadShareInfo({ notify: true }) }}>
                重新加载
              </Button>
            </div>
          </CardBody>
        </Card>
      </div>
    )
  }

  // Password required state
  if (needsPassword && !isAuthenticated) {
    return (
      <div className="app-shell flex min-h-[100svh] items-center justify-center bg-background px-4 py-10">
        <Card className="w-full max-w-md rounded-lg border border-divider bg-content1 shadow-sm">
          <CardBody className="p-6 sm:p-8">
            <div className="mb-8 text-center">
              <div className="gradient-mnemonas mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-lg">
                <Lock size={28} className="text-white" />
              </div>
              <h2 className="text-xl font-semibold text-foreground">
                此分享需要密码
              </h2>
              <p className="text-default-500 mt-1">请输入密码以访问内容</p>
            </div>

            <form onSubmit={handlePasswordSubmit} className="space-y-4">
              <div>
                <label
                  htmlFor={SHARE_ACCESS_PASSWORD_INPUT_ID}
                  className="text-sm font-medium text-default-600 mb-1.5 block"
                >
                  访问密码
                </label>
                <Input
                  ref={passwordInputRef}
                  id={SHARE_ACCESS_PASSWORD_INPUT_ID}
                  aria-label="访问密码"
                  type="password"
                  placeholder="请输入密码"
                  value={password}
                  onValueChange={setPassword}
                  isDisabled={isVerifying}
                  variant="bordered"
                  radius="lg"
                  classNames={{
                    inputWrapper: "bg-content1 border-divider focus-within:!border-accent-primary transition-colors",
                  }}
                />
              </div>
              <Button
                type="submit"
                className="w-full font-medium"
                color="primary"
                size="lg"
                radius="lg"
                isLoading={isVerifying}
              >
                验证密码
              </Button>
            </form>
          </CardBody>
        </Card>
      </div>
    )
  }

  // Share content
  return (
    <div className="app-shell min-h-[100svh] bg-background px-4 py-8 sm:py-12">
      <main className="mx-auto w-full max-w-2xl">
        <div className="mb-5 text-center">
          <div className="gradient-mnemonas mx-auto mb-3 flex h-11 w-11 items-center justify-center rounded-lg">
            <HardDrive size={22} className="text-white" />
          </div>
          <p className="text-sm font-medium text-default-500">MnemoNAS 文件分享</p>
        </div>

        <Card className="w-full rounded-lg border border-divider bg-content1 shadow-sm">
          <CardBody className="p-5 sm:p-7">
            {shareInfo && (
              <div className="mb-6 rounded-lg border border-divider bg-content2/40 p-4 sm:p-5">
                <div className="flex items-center gap-4">
                  <FileIcon
                    name={shareInfo.file_name || '分享内容'}
                    isDir={shareInfo.type === 'folder'}
                    size={46}
                    variant="tile"
                  />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-lg font-semibold text-foreground">
                      {shareInfo.file_name || '分享内容'}
                    </div>
                    <div className="mt-2 flex flex-wrap items-center gap-2">
                      {shareInfo.file_size !== undefined && (
                        <span className="rounded-md border border-divider bg-content1 px-2 py-0.5 text-xs text-default-500">
                          {formatBytes(shareInfo.file_size)}
                        </span>
                      )}
                      {shareInfo.folder_items !== undefined && (
                        <span className="rounded-md border border-divider bg-content1 px-2 py-0.5 text-xs text-default-500">
                          {shareInfo.folder_items} 个项目
                        </span>
                      )}
                    </div>
                  </div>
                </div>

                {shareInfo.description && (
                  <div className="mt-4 border-t border-divider pt-4">
                    <p className="text-sm leading-6 text-default-600">{shareInfo.description}</p>
                  </div>
                )}
              </div>
            )}

            {shareInfo?.type === 'file' && (
              <Button
                aria-label={`下载文件 ${sharedFileName}`}
                className="w-full font-medium"
                color="primary"
                size="lg"
                radius="lg"
                startContent={<Download size={20} />}
                isDisabled={isDownloading}
                isLoading={downloadTarget === 'shared-file'}
                onPress={handleDownload}
              >
                下载文件
              </Button>
            )}

            {shareInfo?.type === 'folder' && (
              <div className="space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="flex min-w-0 items-center gap-2 text-default-500">
                    <Folder size={16} className="shrink-0" />
                    <span className="truncate text-sm">
                      {folderPath ? `/${folderPath}` : '根目录'}
                    </span>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      aria-label={`下载文件夹 ${currentFolderName} 为 ZIP`}
                      size="sm"
                      variant="flat"
                      onPress={handleDownload}
                      startContent={<Download size={16} />}
                      isDisabled={isDownloading}
                      isLoading={downloadTarget === currentFolderDownloadTarget}
                    >
                      下载为 ZIP
                    </Button>
                    {folderPath && (
                      <Button
                        size="sm"
                        variant="flat"
                        onPress={handleNavigateUp}
                        startContent={<ChevronLeft size={16} />}
                      >
                        返回上级
                      </Button>
                    )}
                  </div>
                </div>

                {isListing && (
                  <div className="rounded-lg border border-divider bg-content2/40 px-4 py-3 text-sm text-default-500">加载文件夹内容…</div>
                )}
                {Boolean(listError) && (
                  <div
                    className="rounded-lg border border-danger/30 bg-danger/10 p-4"
                    role="alert"
                    aria-live="assertive"
                    aria-atomic="true"
                  >
                    <div className="space-y-1">
                      <h3
                        ref={listErrorHeadingRef}
                        tabIndex={-1}
                        className="text-sm font-medium text-danger"
                      >
                        {listErrorPresentation.title}
                      </h3>
                      <div className="text-sm text-danger/80">{listErrorPresentation.description}</div>
                    </div>
                    <Button className="mt-3" size="sm" variant="bordered" onPress={loadFolderItems}>
                      重试加载
                    </Button>
                  </div>
                )}

                {!isListing && !listError && folderItems.length === 0 && (
                  <EmptyState
                    icon={Folder}
                    title="文件夹为空"
                    description="当前目录没有可分享的内容"
                    className="py-6"
                  />
                )}

                {!isListing && !listError && folderItems.length > 0 && (
                  <div className="space-y-2">
                    {folderItems.map((item) => (
                      <div
                        key={item.path}
                        className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-divider bg-content2/40 px-3 py-3 sm:flex-nowrap"
                      >
                        {item.is_dir ? (
                          <button
                            type="button"
                            aria-label={`打开文件夹 ${item.name}`}
                            className="flex min-w-0 flex-1 items-center gap-3 text-left"
                            onClick={() => handleEnterFolder(item)}
                          >
                            <FileIcon name={item.name} isDir size={36} variant="tile" />
                            <div className="min-w-0">
                              <div className="truncate text-sm font-medium text-foreground">{item.name}</div>
                              <div className="text-xs text-default-500">文件夹</div>
                            </div>
                          </button>
                        ) : (
                          <div className="flex min-w-0 flex-1 items-center gap-3 text-left">
                            <FileIcon name={item.name} isDir={false} size={36} variant="tile" />
                            <div className="min-w-0">
                              <div className="truncate text-sm font-medium text-foreground">{item.name}</div>
                              <div className="text-xs text-default-500">
                                {formatBytes(item.size)}
                                {item.mod_time && ` · ${formatDate(item.mod_time)}`}
                              </div>
                            </div>
                          </div>
                        )}
                        <Button
                          aria-label={`${item.is_dir ? '下载文件夹' : '下载文件'} ${item.name}${item.is_dir ? ' 为 ZIP' : ''}`}
                          size="sm"
                          variant="flat"
                          onPress={() => handleDownloadItem(item.path)}
                          startContent={<Download size={16} />}
                          isDisabled={isDownloading}
                          isLoading={downloadTarget === `item:${item.path}`}
                        >
                          {item.is_dir ? '下载为 ZIP' : '下载'}
                        </Button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </CardBody>
        </Card>
      </main>
    </div>
  )
}
