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
  FileText,
  Folder,
  Download,
  Lock,
  AlertCircle,
  HardDrive,
} from 'lucide-react'
import {
  getPublicShare,
  accessShareWithPassword,
  getShareDownloadUrl,
  type PublicShareInfo,
  ShareError,
} from '@/api/share'

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
      <div className="min-h-screen bg-bg-primary flex items-center justify-center">
        <Spinner size="lg" />
      </div>
    )
  }

  // Error state
  if (error) {
    return (
      <div className="min-h-screen bg-bg-primary flex items-center justify-center p-4">
        <Card className="w-full max-w-md bg-bg-card border border-divider">
          <CardBody className="p-8 text-center">
            <AlertCircle size={48} className="mx-auto text-status-error mb-4" />
            <h2 className="text-xl font-semibold text-text-primary mb-2">
              无法访问分享
            </h2>
            <p className="text-text-muted">{error}</p>
          </CardBody>
        </Card>
      </div>
    )
  }

  // Password required state
  if (needsPassword && !isAuthenticated) {
    return (
      <div className="min-h-screen bg-bg-primary flex items-center justify-center p-4">
        <Card className="w-full max-w-md bg-bg-card border border-divider">
          <CardBody className="p-8">
            {/* Logo */}
            <div className="text-center mb-8">
              <div className="w-16 h-16 mx-auto rounded-2xl bg-gradient-to-br from-accent-primary to-accent-dark flex items-center justify-center mb-4">
                <Lock size={32} className="text-white" />
              </div>
              <h2 className="text-xl font-semibold text-text-primary">
                此分享需要密码
              </h2>
              <p className="text-text-muted mt-1">请输入密码以访问内容</p>
            </div>

            <form onSubmit={handlePasswordSubmit} className="space-y-4">
              <Input
                type="password"
                label="访问密码"
                placeholder="请输入密码"
                value={password}
                onValueChange={setPassword}
                isDisabled={isVerifying}
                classNames={{
                  inputWrapper: "bg-bg-secondary border-divider",
                }}
              />
              <Button
                type="submit"
                className="w-full"
                color="primary"
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
    <div className="min-h-screen bg-bg-primary flex items-center justify-center p-4">
      <Card className="w-full max-w-lg bg-bg-card border border-divider">
        <CardBody className="p-8">
          {/* Logo */}
          <div className="text-center mb-6">
            <div className="w-12 h-12 mx-auto rounded-xl bg-gradient-to-br from-accent-primary to-accent-dark flex items-center justify-center mb-3">
              <HardDrive size={24} className="text-white" />
            </div>
            <p className="text-sm text-text-muted">MnemoNAS 文件分享</p>
          </div>

          {/* File info */}
          {shareInfo && (
            <div className="p-6 bg-bg-secondary rounded-xl border border-divider mb-6">
              <div className="flex items-center gap-4">
                <div className={`
                  p-3 rounded-lg 
                  ${shareInfo.type === 'folder' ? 'bg-status-warning/10' : 'bg-accent-primary/10'}
                `}>
                  {shareInfo.type === 'folder' 
                    ? <Folder size={32} className="text-status-warning" />
                    : <FileText size={32} className="text-accent-primary" />
                  }
                </div>
                <div className="flex-1 min-w-0">
                  <div className="font-semibold text-text-primary text-lg">
                    {shareInfo.file_name || '分享内容'}
                  </div>
                  {shareInfo.file_size !== undefined && (
                    <div className="text-sm text-text-muted">
                      {formatFileSize(shareInfo.file_size)}
                    </div>
                  )}
                  {shareInfo.folder_items !== undefined && (
                    <div className="text-sm text-text-muted">
                      {shareInfo.folder_items} 个项目
                    </div>
                  )}
                </div>
              </div>

              {shareInfo.description && (
                <div className="mt-4 pt-4 border-t border-divider">
                  <p className="text-sm text-text-secondary">{shareInfo.description}</p>
                </div>
              )}
            </div>
          )}

          {/* Download button */}
          {shareInfo?.type === 'file' && (
            <Button
              className="w-full"
              color="primary"
              size="lg"
              startContent={<Download size={20} />}
              onPress={handleDownload}
            >
              下载文件
            </Button>
          )}

          {shareInfo?.type === 'folder' && (
            <div className="text-center text-text-muted">
              文件夹浏览功能开发中...
            </div>
          )}
        </CardBody>
      </Card>
    </div>
  )
}

function formatFileSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
}
