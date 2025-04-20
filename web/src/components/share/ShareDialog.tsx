import { useState, useCallback, useMemo, useEffect, useRef } from 'react'
import {
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  Button,
  Input,
  Select,
  SelectItem,
  Switch,
  addToast,
  Snippet,
} from '@heroui/react'
import {
  Link2,
  Copy,
  Lock,
  Clock,
  Eye,
  Users,
  CheckCircle,
} from 'lucide-react'
import { createShare, copyShareUrl, ShareError, type ShareCreateResult, type CreateShareRequest } from '@/api/share'

interface ShareDialogProps {
  isOpen: boolean
  onClose: () => void
  filePath: string
  isFolder?: boolean
  onShareCreated?: (share: ShareCreateResult) => void
  featureEnabled?: boolean
  onFeatureDisabled?: () => void
}

const EXPIRATION_OPTIONS = [
  { value: '', label: '永不过期' },
  { value: '1h', label: '1 小时' },
  { value: '24h', label: '24 小时' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
  { value: '90d', label: '90 天' },
]

const PERMISSION_OPTIONS = [
  { value: 'read', label: '仅查看', icon: Eye },
]

function getShareDialogActionErrorToast(error: unknown): {
  title: string
  description?: string
  color: 'warning' | 'danger'
} {
  if (error instanceof ShareError) {
    if (error.isNotFound) {
      return {
        title: '分享目标已不存在',
        description: '该文件或文件夹可能已被移动或删除，请刷新列表后重试。',
        color: 'warning',
      }
    }

    if (error.isFeatureDisabled) {
      return {
        title: '分享功能已关闭',
        color: 'warning',
      }
    }

    if (error.isUnavailable) {
      return {
        title: '创建分享暂不可用',
        description: '分享服务当前不可用，请检查系统健康状态或稍后重试。',
        color: 'warning',
      }
    }
  }

  return {
    title: '创建分享失败',
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
}

export function ShareDialog({ 
  isOpen, 
  onClose, 
  filePath, 
  isFolder = false,
  onShareCreated,
  featureEnabled = true,
  onFeatureDisabled,
}: ShareDialogProps) {
  const [isLoading, setIsLoading] = useState(false)
  const [createdShare, setCreatedShare] = useState<ShareCreateResult | null>(null)
  const [featureDisabled, setFeatureDisabled] = useState(false)
  const createSessionRef = useRef(0)
  const currentFilePathRef = useRef(filePath)
  const currentOpenRef = useRef(isOpen)
  
  // Form state
  const [usePassword, setUsePassword] = useState(false)
  const [password, setPassword] = useState('')
  const [expiresIn, setExpiresIn] = useState('')
  const [permission, setPermission] = useState<'read'>('read')
  const [maxAccess, setMaxAccess] = useState('')
  const [description, setDescription] = useState('')

  const passwordRequiredButEmpty = usePassword && password.trim() === ''

  const shareUrl = useMemo(() => {
    if (!createdShare) return ''
    return createdShare.url.startsWith('http')
      ? createdShare.url
      : `${window.location.origin}${createdShare.url}`
  }, [createdShare])

  const resetForm = useCallback(() => {
    setUsePassword(false)
    setPassword('')
    setExpiresIn('')
    setPermission('read')
    setMaxAccess('')
    setDescription('')
    setCreatedShare(null)
    setFeatureDisabled(false)
  }, [])

  const handleClose = useCallback(() => {
    if (isLoading) {
      return
    }
    resetForm()
    onClose()
  }, [isLoading, onClose, resetForm])

  useEffect(() => {
    currentOpenRef.current = isOpen
    if (!isOpen) {
      return
    }

    createSessionRef.current += 1
    currentFilePathRef.current = filePath
    resetForm()
    setIsLoading(false)
  }, [filePath, isOpen, resetForm])

  const handleCreate = async () => {
    if (featureDisabled || !featureEnabled) return
    if (passwordRequiredButEmpty) {
      addToast({
        title: '请输入分享密码',
        description: '启用密码保护后，必须设置访问密码。',
        color: 'warning',
      })
      return
    }

    const sessionId = createSessionRef.current
    const requestPath = filePath

    setIsLoading(true)
    try {
      const req: CreateShareRequest = {
        path: filePath,
        type: isFolder ? 'folder' : 'file',
        permission,
      }
      
      if (usePassword && password) {
        req.password = password
      }
      if (expiresIn) {
        req.expires_in = expiresIn
      }
      if (maxAccess) {
        const num = parseInt(maxAccess)
        if (num > 0) req.max_access = num
      }
      if (description.trim()) {
        req.description = description.trim()
      }

      const share = await createShare(req)
      if (
        createSessionRef.current === sessionId
        && currentOpenRef.current
        && currentFilePathRef.current === requestPath
      ) {
        setCreatedShare(share)
      }
      onShareCreated?.(share)
      addToast(share.warning
        ? { title: share.message ?? '分享链接已创建，但存在警告', color: 'warning' }
        : { title: '分享链接已创建', color: 'success' })
    } catch (err) {
      if (err instanceof ShareError && err.isFeatureDisabled) {
        if (
          createSessionRef.current === sessionId
          && currentOpenRef.current
          && currentFilePathRef.current === requestPath
        ) {
          setFeatureDisabled(true)
        }
        onFeatureDisabled?.()
        addToast(getShareDialogActionErrorToast(err))
        return
      }
      addToast(getShareDialogActionErrorToast(err))
    } finally {
      if (
        createSessionRef.current === sessionId
        && currentOpenRef.current
        && currentFilePathRef.current === requestPath
      ) {
        setIsLoading(false)
      }
    }
  }

  const handleCopy = async () => {
    if (!createdShare) return
    try {
      await copyShareUrl(createdShare)
      addToast({ title: '链接已复制', color: 'success' })
    } catch {
      addToast({ title: '复制失败', color: 'danger' })
    }
  }

  return (
    <Modal 
      isOpen={isOpen} 
      onClose={handleClose}
      placement="center"
      size="lg"
      classNames={{
        base: "bg-content1 border border-divider shadow-xl rounded-lg",
        backdrop: "bg-black/60 backdrop-blur-md",
        closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        header: "border-b border-divider",
        footer: "border-t border-divider",
      }}
    >
      <ModalContent>
        <ModalHeader className="flex items-center gap-2">
          <Link2 size={20} className="text-accent-primary" />
          <span>分享 {isFolder ? '文件夹' : '文件'}</span>
        </ModalHeader>
        
        <ModalBody className="py-6">
          {/* File info */}
          <div className="p-3 bg-content2 rounded-lg border border-divider mb-4">
            <div className="text-sm text-default-500">分享路径</div>
            <div className="font-medium text-foreground truncate">{filePath}</div>
          </div>

          {createdShare ? (
            /* Share created - show link */
            <div className="space-y-4">
              <div className={`flex items-center gap-2 ${createdShare.warning ? 'text-status-warning' : 'text-status-success'}`}>
                <CheckCircle size={20} />
                <span className="font-medium">{createdShare.warning ? '分享链接已创建，但存在警告' : '分享链接已创建'}</span>
              </div>

              {createdShare.warning && createdShare.message && (
                <div className="p-3 bg-status-warning/10 border border-status-warning/30 rounded-lg text-sm text-status-warning">
                  {createdShare.message}
                </div>
              )}
              
              <Snippet 
                symbol="" 
                variant="bordered"
                classNames={{
                  base: "w-full bg-content2 border-divider",
                  pre: "text-foreground",
                }}
              >
                {shareUrl}
              </Snippet>

              <Button
                className="w-full rounded-lg"
                color="primary"
                startContent={<Copy size={16} />}
                onPress={handleCopy}
              >
                复制链接
              </Button>

              {createdShare.has_password && (
                <div className="p-3 bg-status-warning/10 border border-status-warning/30 rounded-lg">
                  <div className="flex items-center gap-2 text-status-warning text-sm">
                    <Lock size={16} />
                    <span>此链接需要密码才能访问</span>
                  </div>
                </div>
              )}
            </div>
          ) : featureDisabled || !featureEnabled ? (
            <div className="space-y-4">
              <div className="flex items-center gap-2 text-warning">
                <Lock size={20} />
                <span className="font-medium">分享功能已关闭</span>
              </div>

              <div className="p-4 bg-warning/10 border border-warning/30 rounded-lg text-sm text-default-700">
                当前服务已关闭分享功能。重新启用后，才能为文件或文件夹创建分享链接。
              </div>
            </div>
          ) : (
            /* Share form */
            <div className="space-y-6">
              {/* Password protection */}
              <div className="space-y-3">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Lock size={16} className="text-default-500" />
                    <span className="text-sm font-medium">密码保护</span>
                  </div>
                  <Switch
                    isSelected={usePassword}
                    onValueChange={setUsePassword}
                    size="sm"
                  />
                </div>
                {usePassword && (
                  <div className="space-y-2">
                    <Input
                      type="password"
                      placeholder="设置访问密码"
                      value={password}
                      onValueChange={setPassword}
                      isInvalid={passwordRequiredButEmpty}
                      errorMessage={passwordRequiredButEmpty ? '启用密码保护后必须输入密码' : undefined}
                      classNames={{
                        inputWrapper: "bg-content2 border-divider",
                      }}
                    />
                    <p className="text-xs text-default-500">启用后，访问此分享链接必须先输入密码。</p>
                  </div>
                )}
              </div>

              {/* Expiration */}
              <div className="space-y-3">
                <div className="flex items-center gap-2">
                  <Clock size={16} className="text-default-500" />
                  <span className="text-sm font-medium">有效期</span>
                </div>
                <Select
                  selectedKeys={[expiresIn]}
                  onSelectionChange={(keys) => setExpiresIn([...keys][0] as string || '')}
                  classNames={{
                    trigger: "bg-content2 border-divider",
                  }}
                >
                  {EXPIRATION_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value}>{opt.label}</SelectItem>
                  ))}
                </Select>
              </div>

              {/* Permission */}
              <div className="space-y-3">
                <div className="flex items-center gap-2">
                  <Eye size={16} className="text-default-500" />
                  <span className="text-sm font-medium">权限</span>
                </div>
                <Select
                  selectedKeys={[permission]}
                  onSelectionChange={(keys) => setPermission([...keys][0] as 'read' || 'read')}
                  classNames={{
                    trigger: "bg-content2 border-divider",
                  }}
                >
                  {PERMISSION_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} startContent={<opt.icon size={14} />}>
                      {opt.label}
                    </SelectItem>
                  ))}
                </Select>
              </div>

              {/* Max access count */}
              <div className="space-y-3">
                <div className="flex items-center gap-2">
                  <Users size={16} className="text-default-500" />
                  <span className="text-sm font-medium">访问次数限制</span>
                </div>
                <Input
                  type="number"
                  placeholder="不限制"
                  min="1"
                  value={maxAccess}
                  onValueChange={setMaxAccess}
                  classNames={{
                    inputWrapper: "bg-content2 border-divider",
                  }}
                />
              </div>

              {/* Description */}
              <div className="space-y-3">
                <span className="text-sm font-medium text-default-600">备注（可选）</span>
                <Input
                  placeholder="添加备注信息"
                  value={description}
                  onValueChange={setDescription}
                  classNames={{
                    inputWrapper: "bg-content2 border-divider",
                  }}
                />
              </div>
            </div>
          )}
        </ModalBody>

        <ModalFooter>
          {createdShare || featureDisabled || !featureEnabled ? (
            <Button onPress={handleClose} className="rounded-lg">
              关闭
            </Button>
          ) : (
            <>
              <Button variant="flat" onPress={handleClose} isDisabled={isLoading} className="rounded-lg">
                取消
              </Button>
              <Button 
                color="primary" 
                onPress={handleCreate}
                isDisabled={passwordRequiredButEmpty}
                isLoading={isLoading}
                startContent={!isLoading && <Link2 size={16} />}
                className="rounded-lg"
              >
                创建分享链接
              </Button>
            </>
          )}
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}
