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
import { formatBytes, formatDate } from '@/lib/utils'
import { getFolderPathAfterShareAuth } from './shareAccessUtils'

function hasAuthorizedShareContent(info: PublicShareInfo): boolean {
  return info.file_name !== undefined || info.file_size !== undefined || info.folder_items !== undefined
}

function getShareAccessErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ShareError) {
    if (error.isFeatureDisabled) {
      return {
        title: '分享功能已关闭',
        description: '当前服务已关闭分享功能，公开分享链接暂不可访问。',
      }
    }

    if (error.isExpired) {
      return {
        title: '分享已失效',
        description: error.message,
      }
    }

    if (error.isUnavailable) {
      return {
        title: '分享内容暂不可用',
        description: '分享内容当前不可访问，请检查系统状态或稍后重试。',
      }
    }
  }

  return {
    title: '无法访问分享',
    description: error instanceof Error ? error.message : '加载分享信息失败',
  }
}

function getShareListErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ShareError && error.isUnavailable) {
    return {
      title: '文件夹内容暂不可用',
      description: '分享目录当前不可访问，请检查系统状态或稍后重试。',
    }
  }

  return {
    title: '加载文件夹失败',
    description: error instanceof Error ? error.message : '请稍后重试',
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
  if (error instanceof ShareError) {
    if (error.isFeatureDisabled) {
      return {
        title: '分享功能已关闭',
        description: '当前服务已关闭分享功能，公开分享链接暂不可访问。',
        color: 'warning',
      }
    }

    if (error.isExpired) {
      return {
        title: '分享已失效',
        description: error.message,
        color: 'warning',
      }
    }

    if (error.isUnavailable) {
      return {
        title: titles.unavailable,
        description: '分享内容当前不可访问，请检查系统状态或稍后重试。',
        color: 'warning',
      }
    }
  }

  return {
    title: titles.failure,
    description: error instanceof Error ? error.message : '请稍后重试',
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
  const shareInfoRequestRef = useRef(0)
  const folderListRequestRef = useRef(0)
  const errorPresentation = getShareAccessErrorPresentation(error)
  const listErrorPresentation = getShareListErrorPresentation(listError)

  useEffect(() => () => {
    shareInfoRequestRef.current += 1
    folderListRequestRef.current += 1
  }, [])

  const loadShareInfo = useCallback(async (options?: { notify?: boolean }) => {
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
    setIsLoading(true)
    setError(null)
    setNeedsPassword(false)
    setIsAuthenticated(false)
    setPassword('')
    
    try {
      const info = await getPublicShare(id)
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
      if (requestId === shareInfoRequestRef.current) {
        setIsLoading(false)
      }
    }
  }, [id])

  useEffect(() => {
    loadShareInfo()
  }, [loadShareInfo])

  const handlePasswordSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!id) return
    if (!password.trim()) {
      addToast({ title: '请输入访问密码', color: 'warning' })
      return
    }

    setIsVerifying(true)
    try {
      const info = await accessShareWithPassword(id, password)
      setShareInfo(info)
      setFolderItems([])
      setListError(null)
      setFolderPath((currentFolderPath) => getFolderPathAfterShareAuth(currentFolderPath, info))
      setIsAuthenticated(true)
      setNeedsPassword(false)
      setPassword('')
    } catch (err) {
      if (err instanceof ShareError && err.isUnauthorized) {
        addToast({ title: '密码错误', color: 'danger' })
      } else {
        addToast(getShareActionErrorToast(err, {
          unavailable: '验证暂不可用',
          failure: '验证失败',
        }))
      }
    } finally {
      setIsVerifying(false)
    }
  }

  const handleDownload = async () => {
    if (!id) return

    try {
      await downloadShare(id, { filename: shareInfo?.file_name })
    } catch (err) {
      if (err instanceof ShareError && err.isUnauthorized) {
        setIsAuthenticated(false)
        setNeedsPassword(true)
        setPassword('')
        addToast({ title: '访问凭证已失效，请重新输入密码', color: 'warning' })
        return
      }
      addToast(getShareActionErrorToast(err, {
        unavailable: '下载暂不可用',
        failure: '下载失败',
      }))
    }
  }

  const handleDownloadItem = async (itemPath: string) => {
    if (!id) return

    const item = folderItems.find((folderItem) => folderItem.path === itemPath)
    try {
      await downloadShare(id, { filePath: itemPath, filename: item?.name })
    } catch (err) {
      if (err instanceof ShareError && err.isUnauthorized) {
        setIsAuthenticated(false)
        setNeedsPassword(true)
        setPassword('')
        addToast({ title: '访问凭证已失效，请重新输入密码', color: 'warning' })
        return
      }
      addToast(getShareActionErrorToast(err, {
        unavailable: '下载暂不可用',
        failure: '下载失败',
      }))
    }
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
    if (!id || !shareInfo || shareInfo.type !== 'folder' || !isAuthenticated) return

    const requestId = folderListRequestRef.current + 1
    folderListRequestRef.current = requestId
    setIsListing(true)
    setListError(null)
    setFolderItems([])
    try {
      const data = await getPublicShareItems(id, {
        path: folderPath || undefined,
      })
      if (requestId !== folderListRequestRef.current) {
        return
      }
      setFolderItems(data.items)
    } catch (err) {
      if (requestId !== folderListRequestRef.current) {
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
      if (requestId === folderListRequestRef.current) {
        setIsListing(false)
      }
    }
  }, [id, shareInfo, isAuthenticated, folderPath])

  useEffect(() => {
    loadFolderItems()
  }, [loadFolderItems])

  // Loading state
  if (isLoading) {
    return (
      <div className="min-h-screen bg-background flex items-center justify-center app-shell">
        <div className="text-center">
          <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-default-500">加载分享信息...</p>
        </div>
      </div>
    )
  }

  // Error state
  if (error) {
    return (
      <div className="min-h-screen relative flex items-center justify-center p-4 bg-background overflow-hidden app-shell">
        <div className="absolute inset-0 z-0 overflow-hidden pointer-events-none">
          <div className="absolute -top-[20%] -left-[10%] w-[50%] h-[50%] rounded-full bg-danger/5 blur-[120px]" />
        </div>
        <Card className="w-full max-w-md card-meridian backdrop-blur-xl border border-divider/60 shadow-2xl relative z-10">
          <CardBody className="p-8 text-center">
            <div className="w-16 h-16 mx-auto rounded-full bg-danger/10 flex items-center justify-center mb-4">
              <AlertCircle size={32} className="text-danger" />
            </div>
            <h2 className="text-xl font-semibold text-foreground mb-2">
              {errorPresentation.title}
            </h2>
            <p className="text-default-500">{errorPresentation.description}</p>
            <Button className="mt-4" variant="bordered" onPress={() => { void loadShareInfo({ notify: true }) }}>
              重新加载
            </Button>
          </CardBody>
        </Card>
      </div>
    )
  }

  // Password required state
  if (needsPassword && !isAuthenticated) {
    return (
      <div className="min-h-screen relative flex items-center justify-center p-4 bg-background overflow-hidden app-shell">
        {/* Background decoration */}
        <div className="absolute inset-0 z-0 overflow-hidden pointer-events-none">
          <div className="absolute -top-[20%] -left-[10%] w-[50%] h-[50%] rounded-full bg-primary/5 blur-[120px]" />
          <div className="absolute -bottom-[20%] -right-[10%] w-[50%] h-[50%] rounded-full bg-secondary/5 blur-[120px]" />
        </div>

        <Card className="w-full max-w-md card-meridian backdrop-blur-xl border border-divider/60 shadow-2xl relative z-10">
          <CardBody className="p-8">
            {/* Logo */}
            <div className="text-center mb-8">
              <div className="w-16 h-16 mx-auto rounded-2xl bg-gradient-to-br from-primary to-secondary flex items-center justify-center mb-4 shadow-lg logo-glow">
                <Lock size={32} className="text-white" />
              </div>
              <h2 className="text-xl font-semibold text-foreground">
                此分享需要密码
              </h2>
              <p className="text-default-500 mt-1">请输入密码以访问内容</p>
            </div>

            <form onSubmit={handlePasswordSubmit} className="space-y-4">
              <div>
                <label className="text-sm font-medium text-default-600 mb-1.5 block">访问密码</label>
                <Input
                  type="password"
                  placeholder="请输入密码"
                  value={password}
                  onValueChange={setPassword}
                  isDisabled={isVerifying}
                  variant="bordered"
                  radius="lg"
                  classNames={{
                    inputWrapper: "bg-default-100/50 hover:bg-default-200/50 border-transparent focus-within:!border-primary transition-colors",
                  }}
                />
              </div>
              <Button
                type="submit"
                className="w-full font-medium shadow-lg shadow-primary/20"
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
    <div className="min-h-screen relative flex items-center justify-center p-4 bg-background overflow-hidden app-shell">
      {/* Background decoration */}
      <div className="absolute inset-0 z-0 overflow-hidden pointer-events-none">
        <div className="absolute -top-[20%] -left-[10%] w-[50%] h-[50%] rounded-full bg-primary/5 blur-[120px]" />
        <div className="absolute -bottom-[20%] -right-[10%] w-[50%] h-[50%] rounded-full bg-secondary/5 blur-[120px]" />
      </div>

        <Card className="w-full max-w-lg card-meridian backdrop-blur-xl border border-divider/60 shadow-2xl relative z-10">
        <CardBody className="p-8">
          {/* Logo */}
          <div className="text-center mb-6">
            <div className="w-12 h-12 mx-auto rounded-xl bg-gradient-to-br from-primary to-secondary flex items-center justify-center mb-3 shadow-lg logo-glow">
              <HardDrive size={24} className="text-white" />
            </div>
            <p className="text-sm text-default-500">MnemoNAS 文件分享</p>
          </div>

          {/* File info */}
          {shareInfo && (
            <div className="p-6 glass rounded-xl border border-divider/50 mb-6">
              <div className="flex items-center gap-4">
                <FileIcon
                  name={shareInfo.file_name || '分享内容'}
                  isDir={shareInfo.type === 'folder'}
                  size={46}
                  variant="tile"
                />
                <div className="flex-1 min-w-0">
                  <div className="font-semibold text-foreground text-lg truncate">
                    {shareInfo.file_name || '分享内容'}
                  </div>
                  <div className="flex items-center gap-2 mt-1">
                    {shareInfo.file_size !== undefined && (
                      <span className="text-xs px-2 py-0.5 rounded-full bg-default-100 text-default-500">
                        {formatBytes(shareInfo.file_size)}
                      </span>
                    )}
                    {shareInfo.folder_items !== undefined && (
                      <span className="text-xs px-2 py-0.5 rounded-full bg-default-100 text-default-500">
                        {shareInfo.folder_items} 个项目
                      </span>
                    )}
                  </div>
                </div>
              </div>

              {shareInfo.description && (
                <div className="mt-4 pt-4 border-t border-divider/50">
                  <p className="text-sm text-default-600 leading-relaxed">{shareInfo.description}</p>
                </div>
              )}
            </div>
          )}

          {/* Download button */}
          {shareInfo?.type === 'file' && (
            <Button
              className="w-full font-medium shadow-lg shadow-primary/20"
              color="primary"
              size="lg"
              radius="lg"
              startContent={<Download size={20} />}
              onPress={handleDownload}
            >
              下载文件
            </Button>
          )}

          {shareInfo?.type === 'folder' && (
            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2 text-default-500">
                  <Folder size={16} />
                  <span className="text-sm">
                    {folderPath ? `/${folderPath}` : '根目录'}
                  </span>
                </div>
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

              {isListing && (
                <div className="text-sm text-default-500">加载文件夹内容...</div>
              )}
              {Boolean(listError) && (
                <div className="space-y-2">
                  <div className="space-y-1">
                    <div className="text-sm font-medium text-danger">{listErrorPresentation.title}</div>
                    <div className="text-sm text-danger/80">{listErrorPresentation.description}</div>
                  </div>
                  <Button size="sm" variant="bordered" onPress={loadFolderItems}>
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
                      className="flex items-center justify-between rounded-lg border border-divider/60 bg-content2/40 px-3 py-2"
                    >
                      <button
                        type="button"
                        className="flex items-center gap-3 text-left min-w-0"
                        onClick={() => handleEnterFolder(item)}
                        disabled={!item.is_dir}
                      >
                        <FileIcon name={item.name} isDir={item.is_dir} size={36} variant="tile" />
                        <div className="min-w-0">
                          <div className="text-sm font-medium text-foreground truncate">{item.name}</div>
                          <div className="text-xs text-default-500">
                            {item.is_dir ? '文件夹' : formatBytes(item.size)}
                            {item.mod_time && !item.is_dir && ` · ${formatDate(item.mod_time)}`}
                          </div>
                        </div>
                      </button>
                      {!item.is_dir && (
                        <Button
                          size="sm"
                          variant="flat"
                          onPress={() => handleDownloadItem(item.path)}
                          startContent={<Download size={16} />}
                        >
                          下载
                        </Button>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </CardBody>
      </Card>
    </div>
  )
}

