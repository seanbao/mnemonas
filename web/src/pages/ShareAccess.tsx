import { useState, useEffect, useCallback } from 'react'
import { useParams } from 'react-router-dom'
import {
  Card,
  CardBody,
  Button,
  Input,
  Spinner,
  addToast,
} from '@heroui/react'
import {
  Download,
  Lock,
  AlertCircle,
  HardDrive,
  Folder,
} from 'lucide-react'
import {
  getPublicShare,
  accessShareWithPassword,
  getShareDownloadUrl,
  type PublicShareInfo,
  ShareError,
} from '@/api/share'
import { EmptyState } from '@/components/ui/EmptyState'
import { FileIcon } from '@/components/ui/FileIcon'
import { formatBytes } from '@/lib/utils'

export function ShareAccessPage() {
  const { id } = useParams<{ id: string }>()
  const [isLoading, setIsLoading] = useState(true)
  const [shareInfo, setShareInfo] = useState<PublicShareInfo | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [needsPassword, setNeedsPassword] = useState(false)
  const [password, setPassword] = useState('')
  const [isVerifying, setIsVerifying] = useState(false)
  const [isAuthenticated, setIsAuthenticated] = useState(false)

  const loadShareInfo = useCallback(async () => {
    if (!id) {
      setError('无效的分享链接')
      setIsLoading(false)
      return
    }

    setIsLoading(true)
    setError(null)
    
    try {
      const info = await getPublicShare(id)
      setShareInfo(info)
      if (info.has_password) {
        setNeedsPassword(true)
      } else {
        setIsAuthenticated(true)
      }
    } catch (err) {
      if (err instanceof ShareError) {
        setError(err.message)
      } else {
        setError('加载分享信息失败')
      }
    } finally {
      setIsLoading(false)
    }
  }, [id])

  useEffect(() => {
    loadShareInfo()
  }, [loadShareInfo])

  const handlePasswordSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!id || !password.trim()) return

    setIsVerifying(true)
    try {
      const info = await accessShareWithPassword(id, password)
      setShareInfo(info)
      setIsAuthenticated(true)
      setNeedsPassword(false)
    } catch (err) {
      if (err instanceof ShareError && err.isUnauthorized) {
        addToast({ title: '密码错误', color: 'danger' })
      } else {
        addToast({ 
          title: err instanceof Error ? err.message : '验证失败', 
          color: 'danger' 
        })
      }
    } finally {
      setIsVerifying(false)
    }
  }

  const handleDownload = () => {
    if (!id) return
    const url = getShareDownloadUrl(id, needsPassword ? password : undefined)
    window.open(url, '_blank')
  }

  // Loading state
  if (isLoading) {
    return (
      <div className="min-h-screen bg-content2 flex items-center justify-center app-shell">
        <Spinner size="lg" />
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
              无法访问分享
            </h2>
            <p className="text-default-500">{error}</p>
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
              <Input
                type="password"
                label="访问密码"
                placeholder="请输入密码"
                value={password}
                onValueChange={setPassword}
                isDisabled={isVerifying}
                variant="bordered"
                radius="lg"
                classNames={{
                  inputWrapper: "bg-default-100/50 hover:bg-default-200/50 border-transparent focus-within:!border-primary transition-colors",
                  label: "text-default-500",
                }}
              />
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
            <EmptyState
              icon={Folder}
              title="暂不支持文件夹浏览"
              description="文件夹浏览功能开发中..."
              className="py-6"
            />
          )}
        </CardBody>
      </Card>
    </div>
  )
}

