import { useState, useCallback, useMemo } from 'react'
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
import { createShare, copyShareUrl, type Share, type CreateShareRequest } from '@/api/share'

interface ShareDialogProps {
  isOpen: boolean
  onClose: () => void
  filePath: string
  isFolder?: boolean
  onShareCreated?: (share: Share) => void
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
  { value: 'read_write', label: '可编辑', icon: Users },
]

export function ShareDialog({ 
  isOpen, 
  onClose, 
  filePath, 
  isFolder = false,
  onShareCreated,
}: ShareDialogProps) {
  const [isLoading, setIsLoading] = useState(false)
  const [createdShare, setCreatedShare] = useState<Share | null>(null)
  
  // Form state
  const [usePassword, setUsePassword] = useState(false)
  const [password, setPassword] = useState('')
  const [expiresIn, setExpiresIn] = useState('')
  const [permission, setPermission] = useState<'read' | 'read_write'>('read')
  const [maxAccess, setMaxAccess] = useState('')
  const [description, setDescription] = useState('')

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
  }, [])

  const handleClose = useCallback(() => {
    resetForm()
    onClose()
  }, [onClose, resetForm])

  const handleCreate = async () => {
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
      setCreatedShare(share)
      onShareCreated?.(share)
      addToast({ title: '分享链接已创建', color: 'success' })
    } catch (err) {
      addToast({ 
        title: err instanceof Error ? err.message : '创建分享失败', 
        color: 'danger' 
      })
    } finally {
      setIsLoading(false)
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
        base: "bg-content1 border border-divider shadow-2xl rounded-2xl",
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
              <div className="flex items-center gap-2 text-status-success">
                <CheckCircle size={20} />
                <span className="font-medium">分享链接已创建</span>
              </div>
              
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
                className="w-full rounded-xl"
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
                  <Input
                    type="password"
                    placeholder="设置访问密码"
                    value={password}
                    onValueChange={setPassword}
                    classNames={{
                      inputWrapper: "bg-content2 border-divider",
                    }}
                  />
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
                  onSelectionChange={(keys) => setPermission([...keys][0] as 'read' | 'read_write' || 'read')}
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
          {createdShare ? (
            <Button onPress={handleClose} className="rounded-xl">
              关闭
            </Button>
          ) : (
            <>
              <Button variant="flat" onPress={handleClose} className="rounded-xl">
                取消
              </Button>
              <Button 
                color="primary" 
                onPress={handleCreate}
                isLoading={isLoading}
                startContent={!isLoading && <Link2 size={16} />}
                className="rounded-xl"
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
