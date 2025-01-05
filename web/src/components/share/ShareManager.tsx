import { useState, useEffect, useCallback } from 'react'
import {
  Card,
  CardBody,
  Button,
  Chip,
  Dropdown,
  DropdownTrigger,
  DropdownMenu,
  DropdownItem,
  addToast,
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
} from '@heroui/react'
import {
  Link2,
  MoreVertical,
  Copy,
  Trash2,
  ToggleLeft,
  ToggleRight,
  Lock,
  Clock,
  Eye,
  RefreshCw,
} from 'lucide-react'
import { 
  listShares, 
  deleteShare, 
  updateShare, 
  copyShareUrl,
  formatExpiration,
  type Share,
} from '@/api/share'
import { EmptyState } from '@/components/ui/EmptyState'
import { FileIcon } from '@/components/ui/FileIcon'

interface ShareManagerProps {
  showAllShares?: boolean
}

export function ShareManager({ showAllShares = false }: ShareManagerProps) {
  const [shares, setShares] = useState<Share[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [deleteTarget, setDeleteTarget] = useState<Share | null>(null)
  const [isDeleting, setIsDeleting] = useState(false)

  const loadShares = useCallback(async () => {
    setIsLoading(true)
    try {
      const data = await listShares(showAllShares)
      setShares(data)
    } catch (err) {
      addToast({ 
        title: err instanceof Error ? err.message : '加载分享列表失败', 
        color: 'danger' 
      })
    } finally {
      setIsLoading(false)
    }
  }, [showAllShares])

  useEffect(() => {
    loadShares()
  }, [loadShares])

  const handleCopy = async (share: Share) => {
    try {
      await copyShareUrl(share)
      addToast({ title: '链接已复制', color: 'success' })
    } catch {
      addToast({ title: '复制失败', color: 'danger' })
    }
  }

  const handleToggle = async (share: Share) => {
    try {
      await updateShare(share.id, { enabled: !share.enabled })
      setShares(prev => prev.map(s => 
        s.id === share.id ? { ...s, enabled: !s.enabled } : s
      ))
      addToast({ 
        title: share.enabled ? '分享已禁用' : '分享已启用', 
        color: 'success' 
      })
    } catch (err) {
      addToast({ 
        title: err instanceof Error ? err.message : '操作失败', 
        color: 'danger' 
      })
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setIsDeleting(true)
    try {
      await deleteShare(deleteTarget.id)
      setShares(prev => prev.filter(s => s.id !== deleteTarget.id))
      addToast({ title: '分享已删除', color: 'success' })
      setDeleteTarget(null)
    } catch (err) {
      addToast({ 
        title: err instanceof Error ? err.message : '删除失败', 
        color: 'danger' 
      })
    } finally {
      setIsDeleting(false)
    }
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-center">
          <div className="w-10 h-10 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-3" />
          <p className="text-default-500 text-sm">加载分享列表...</p>
        </div>
      </div>
    )
  }

  if (shares.length === 0) {
    return (
      <EmptyState
        icon={Link2}
        title="暂无分享"
        description="在文件浏览器中选择文件或文件夹创建分享链接"
        className="py-12"
      />
    )
  }

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold text-foreground">
          我的分享 ({shares.length})
        </h2>
        <Button
          isIconOnly
          variant="flat"
          size="sm"
          onPress={loadShares}
        >
          <RefreshCw size={16} />
        </Button>
      </div>

      {/* Share list */}
      <div className="space-y-3">
        {shares.map((share) => (
          <ShareItem
            key={share.id}
            share={share}
            onCopy={() => handleCopy(share)}
            onToggle={() => handleToggle(share)}
            onDelete={() => setDeleteTarget(share)}
          />
        ))}
      </div>

      {/* Delete confirmation modal */}
      <Modal 
        isOpen={!!deleteTarget} 
        onClose={() => setDeleteTarget(null)}
        classNames={{
          base: "bg-content1 border border-divider",
        }}
      >
        <ModalContent>
          <ModalHeader>确认删除</ModalHeader>
          <ModalBody>
            <p>确定要删除此分享链接吗？已分享的链接将失效。</p>
            {deleteTarget && (
              <div className="p-3 bg-content2 rounded-lg mt-2">
                <div className="text-sm text-default-500">分享路径</div>
                <div className="font-medium truncate">{deleteTarget.path}</div>
              </div>
            )}
          </ModalBody>
          <ModalFooter>
            <Button variant="flat" onPress={() => setDeleteTarget(null)}>
              取消
            </Button>
            <Button 
              color="danger" 
              onPress={handleDelete}
              isLoading={isDeleting}
            >
              删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  )
}

interface ShareItemProps {
  share: Share
  onCopy: () => void
  onToggle: () => void
  onDelete: () => void
}

function ShareItem({ share, onCopy, onToggle, onDelete }: ShareItemProps) {
  const fileName = share.path.split('/').pop() || share.path
  const isExpired = share.expires_at && new Date(share.expires_at) < new Date()

  return (
    <Card className="card-meridian">
      <CardBody className="p-4">
        <div className="flex items-start gap-4">
          {/* Icon */}
          <FileIcon
            name={fileName}
            isDir={share.type === 'folder'}
            size={28}
          />

          {/* Content */}
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2 mb-1">
              <span className="font-medium text-foreground truncate">
                {fileName}
              </span>
              {!share.enabled && (
                <Chip size="sm" color="default" variant="flat">已禁用</Chip>
              )}
              {isExpired && (
                <Chip size="sm" color="danger" variant="flat">已过期</Chip>
              )}
            </div>
            
            <div className="text-sm text-default-500 truncate mb-2">
              {share.path}
            </div>

            {/* Stats */}
            <div className="flex flex-wrap items-center gap-3 text-xs text-default-500">
              {share.has_password && (
                <div className="flex items-center gap-1">
                  <Lock size={12} />
                  <span>密码保护</span>
                </div>
              )}
              <div className="flex items-center gap-1">
                <Clock size={12} />
                <span>{formatExpiration(share.expires_at)}</span>
              </div>
              <div className="flex items-center gap-1">
                <Eye size={12} />
                <span>
                  {share.access_count} 次访问
                  {share.max_access && share.max_access > 0 && ` / ${share.max_access}`}
                </span>
              </div>
            </div>
          </div>

          {/* Actions */}
          <div className="flex items-center gap-2">
            <Button
              isIconOnly
              variant="flat"
              size="sm"
              onPress={onCopy}
            >
              <Copy size={16} />
            </Button>
            
            <Dropdown>
              <DropdownTrigger>
                <Button isIconOnly variant="flat" size="sm">
                  <MoreVertical size={16} />
                </Button>
              </DropdownTrigger>
              <DropdownMenu aria-label="分享操作">
                <DropdownItem 
                  key="copy" 
                  startContent={<Copy size={14} />}
                  onPress={onCopy}
                >
                  复制链接
                </DropdownItem>
                <DropdownItem 
                  key="toggle"
                  startContent={share.enabled ? <ToggleLeft size={14} /> : <ToggleRight size={14} />}
                  onPress={onToggle}
                >
                  {share.enabled ? '禁用分享' : '启用分享'}
                </DropdownItem>
                <DropdownItem 
                  key="delete" 
                  className="text-danger"
                  color="danger"
                  startContent={<Trash2 size={14} />}
                  onPress={onDelete}
                >
                  删除分享
                </DropdownItem>
              </DropdownMenu>
            </Dropdown>
          </div>
        </div>
      </CardBody>
    </Card>
  )
}
