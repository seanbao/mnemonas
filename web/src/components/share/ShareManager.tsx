import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
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
  AlertCircle,
} from 'lucide-react'
import { 
  listShares, 
  deleteShare, 
  updateShare, 
  copyShareUrl,
  formatExpiration,
  ShareError,
  type Share,
} from '@/api/share'
import { EmptyState } from '@/components/ui/EmptyState'
import { FileIcon } from '@/components/ui/FileIcon'

function getShareFeatureState(error: unknown): 'disabled' | 'unavailable' | null {
  if (!(error instanceof ShareError)) {
    return null
  }

  if (error.isFeatureDisabled) {
    return 'disabled'
  }

  if (error.isUnavailable) {
    return 'unavailable'
  }

  return null
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
        description: '当前服务已关闭分享功能。启用后可在此管理已创建的分享链接。',
        color: 'warning',
      }
    }

    if (error.isUnavailable) {
      return {
        title: titles.unavailable,
        description: '分享服务当前不可用，请检查设备状态或稍后重试。',
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

function getShareLoadErrorToast(error: unknown): {
  title: string
  description?: string
  color: 'warning' | 'danger'
} {
  return {
    title: '刷新分享列表失败',
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
}

function getMissingShareToast(): {
  title: string
  description: string
  color: 'warning'
} {
  return {
    title: '分享已不存在',
    description: '该分享可能已被其他操作删除，列表已同步更新。',
    color: 'warning',
  }
}

function isRiskyShare(share: Share): boolean {
  return share.enabled && !!share.risk && share.risk.level !== 'none'
}

function isHighRiskShare(share: Share): boolean {
  return share.enabled && share.risk?.level === 'high'
}

function shareHasRiskCode(share: Share, code: string): boolean {
  return share.enabled && (share.risk?.reasons?.some(reason => !reason.resolved && reason.code === code) ?? false)
}

function getRiskPresentation(level?: string): { label: string; color: 'default' | 'warning' | 'danger' | 'primary' } | null {
  switch (level) {
  case 'high':
    return { label: '需处理', color: 'danger' }
  case 'medium':
    return { label: '需关注', color: 'warning' }
  case 'low':
    return { label: '低风险', color: 'primary' }
  default:
    return null
  }
}

interface ShareManagerProps {
  showAllShares?: boolean
  featureEnabled?: boolean
}

type ShareReviewFilter = 'all' | 'review' | 'expiring' | 'passwordless' | 'broad'

export function ShareManager({ showAllShares = false, featureEnabled = true }: ShareManagerProps) {
  const [shares, setShares] = useState<Share[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [loadError, setLoadError] = useState<unknown | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Share | null>(null)
  const [isDeleting, setIsDeleting] = useState(false)
  const [reviewFilter, setReviewFilter] = useState<ShareReviewFilter>('all')
  const [isDisablingHighRisk, setIsDisablingHighRisk] = useState(false)
  const sharesRef = useRef<Share[]>([])
  const loadRequestRef = useRef(0)

  useEffect(() => () => {
    loadRequestRef.current += 1
  }, [])

  useEffect(() => {
    sharesRef.current = shares
  }, [shares])

  const loadShares = useCallback(async () => {
    const requestId = loadRequestRef.current + 1
    loadRequestRef.current = requestId
    setIsLoading(true)
    setLoadError(null)
    try {
      const data = await listShares(showAllShares)
      if (requestId !== loadRequestRef.current) {
        return
      }
      setShares(data)
    } catch (err) {
      if (requestId !== loadRequestRef.current) {
        return
      }
      const featureState = getShareFeatureState(err)
      setLoadError(err)
      if (featureState !== null) {
        setShares([])
        return
      }

      if (sharesRef.current.length > 0) {
        addToast(getShareLoadErrorToast(err))
      }
    } finally {
      if (requestId === loadRequestRef.current) {
        setIsLoading(false)
      }
    }
  }, [showAllShares])

  const loadSharesRef = useRef(loadShares)

  useEffect(() => {
    loadSharesRef.current = loadShares
  }, [loadShares])

  useEffect(() => {
    if (!featureEnabled) {
      loadRequestRef.current += 1
      let cancelled = false
      queueMicrotask(() => {
        if (cancelled) return
        setIsLoading(false)
        setLoadError(null)
        setShares([])
      })
      sharesRef.current = []
      return () => {
        cancelled = true
      }
    }

    void loadSharesRef.current()
  }, [featureEnabled])

  const riskyShares = useMemo(() => shares.filter(isRiskyShare), [shares])
  const highRiskShares = useMemo(() => shares.filter(isHighRiskShare), [shares])
  const expiringSoonShares = useMemo(() => shares.filter(share => shareHasRiskCode(share, 'expiring_soon')), [shares])
  const passwordlessShares = useMemo(() => shares.filter(share => shareHasRiskCode(share, 'no_password')), [shares])
  const broadFolderShares = useMemo(() => shares.filter(share => (
    shareHasRiskCode(share, 'root_folder') || shareHasRiskCode(share, 'broad_folder')
  )), [shares])
  const staleShares = useMemo(() => shares.filter(share => (
    shareHasRiskCode(share, 'unused_enabled') || shareHasRiskCode(share, 'stale_enabled')
  )), [shares])
  const visibleShares = useMemo(() => {
    switch (reviewFilter) {
    case 'review':
      return riskyShares
    case 'expiring':
      return expiringSoonShares
    case 'passwordless':
      return passwordlessShares
    case 'broad':
      return broadFolderShares
    default:
      return shares
    }
  }, [broadFolderShares, expiringSoonShares, passwordlessShares, reviewFilter, riskyShares, shares])

  if (!featureEnabled) {
    return (
      <EmptyState
        icon={Link2}
        title="分享功能已关闭"
        description="当前服务已关闭分享功能。启用后可在此管理已创建的分享链接。"
        className="py-12"
      />
    )
  }

  const loadFeatureState = getShareFeatureState(loadError)

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
      if (err instanceof ShareError && err.isNotFound) {
        setShares(prev => prev.filter(s => s.id !== share.id))
        addToast(getMissingShareToast())
        return
      }

      addToast(getShareActionErrorToast(err, {
        unavailable: '分享操作暂不可用',
        failure: '操作失败',
      }))
    }
  }

  const handleDisableHighRisk = async () => {
    const targets = highRiskShares
    if (targets.length === 0 || isDisablingHighRisk) {
      return
    }

    setIsDisablingHighRisk(true)
    try {
      const results = await Promise.allSettled(targets.map(target => updateShare(target.id, { enabled: false })))
      const disabledIds = new Set<string>()
      let firstFailure: unknown | null = null

      results.forEach((result, index) => {
        if (result.status === 'fulfilled') {
          disabledIds.add(targets[index].id)
          return
        }
        if (firstFailure === null) {
          firstFailure = result.reason
        }
      })

      if (disabledIds.size > 0) {
        setShares(prev => prev.map(s => (
          disabledIds.has(s.id)
            ? { ...s, enabled: false, risk: { level: 'none' } }
            : s
        )))
        addToast({
          title: `已停用 ${disabledIds.size} 个需处理分享`,
          color: 'success',
        })
      }

      if (firstFailure !== null) {
        addToast(getShareActionErrorToast(firstFailure, {
          unavailable: '分享操作暂不可用',
          failure: '部分分享停用失败',
        }))
      }
    } finally {
      setIsDisablingHighRisk(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    const target = deleteTarget
    setIsDeleting(true)
    try {
      const result = await deleteShare(target.id)
      setShares(prev => prev.filter(s => s.id !== target.id))
      addToast(result.warning
        ? { title: result.message ?? '分享已删除，但存在警告', color: 'warning' }
        : { title: '分享已删除', color: 'success' })
      setDeleteTarget(current => (current?.id === target.id ? null : current))
    } catch (err) {
      if (err instanceof ShareError && err.isNotFound) {
        setShares(prev => prev.filter(s => s.id !== target.id))
        addToast(getMissingShareToast())
        setDeleteTarget(current => (current?.id === target.id ? null : current))
        return
      }

      addToast(getShareActionErrorToast(err, {
        unavailable: '删除分享暂不可用',
        failure: '删除失败',
      }))
    } finally {
      setIsDeleting(false)
    }
  }

  const handleCloseDeleteModal = () => {
    if (isDeleting) {
      return
    }
    setDeleteTarget(null)
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

  if (loadError && shares.length === 0) {
    if (loadFeatureState === 'disabled') {
      return (
        <EmptyState
          icon={Link2}
          title="分享功能已关闭"
          description="当前服务已关闭分享功能。启用后可在此管理已创建的分享链接。"
          className="py-12"
        />
      )
    }

    if (loadFeatureState === 'unavailable') {
      return (
        <EmptyState
          icon={AlertCircle}
          title="分享功能暂不可用"
          description="分享服务当前不可用，请检查设备状态或稍后重试。"
          action={
            <Button variant="bordered" className="rounded-lg" onPress={() => loadShares()}>
              重新加载
            </Button>
          }
          className="py-12"
        />
      )
    }

    return (
      <EmptyState
        icon={AlertCircle}
        title="加载分享列表失败"
        description={loadError instanceof Error ? loadError.message : '请稍后重试'}
        action={
          <Button variant="bordered" className="rounded-lg" onPress={() => loadShares()}>
            重新加载
          </Button>
        }
        className="py-12"
      />
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
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold text-foreground">
            我的分享 ({shares.length})
          </h2>
          {riskyShares.length > 0 && (
            <Chip size="sm" color="warning" variant="flat">
              风险 {riskyShares.length}
            </Chip>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant={reviewFilter === 'all' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('all')}
            className="rounded-lg"
          >
            全部
          </Button>
          <Button
            variant={reviewFilter === 'review' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('review')}
            className="rounded-lg"
            isDisabled={riskyShares.length === 0}
          >
            需复核 ({riskyShares.length})
          </Button>
          <Button
            variant={reviewFilter === 'expiring' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('expiring')}
            className="rounded-lg"
            isDisabled={expiringSoonShares.length === 0}
          >
            即将到期 ({expiringSoonShares.length})
          </Button>
          <Button
            variant={reviewFilter === 'passwordless' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('passwordless')}
            className="rounded-lg"
            isDisabled={passwordlessShares.length === 0}
          >
            无密码 ({passwordlessShares.length})
          </Button>
          <Button
            variant={reviewFilter === 'broad' ? 'solid' : 'flat'}
            size="sm"
            onPress={() => setReviewFilter('broad')}
            className="rounded-lg"
            isDisabled={broadFolderShares.length === 0}
          >
            覆盖较大 ({broadFolderShares.length})
          </Button>
          {highRiskShares.length > 0 && (
            <Button
              color="danger"
              variant="flat"
              size="sm"
              isLoading={isDisablingHighRisk}
              onPress={handleDisableHighRisk}
              className="rounded-lg"
            >
              停用需处理 ({highRiskShares.length})
            </Button>
          )}
          <Button
            isIconOnly
            variant="flat"
            size="sm"
            onPress={loadShares}
            aria-label="刷新分享列表"
            className="rounded-lg"
          >
            <RefreshCw size={16} />
          </Button>
        </div>
      </div>

      {/* Share list */}
      <div className="space-y-3">
        {visibleShares.length === 0 ? (
          <EmptyState
            icon={AlertCircle}
            title="暂无符合条件的分享"
            description="当前筛选条件下没有需要处理的分享链接"
            className="py-10"
          />
        ) : (
          visibleShares.map((share) => (
            <ShareItem
              key={share.id}
              share={share}
              onCopy={() => handleCopy(share)}
              onToggle={() => handleToggle(share)}
              onDelete={() => setDeleteTarget(share)}
            />
          ))
        )}
      </div>

      {(riskyShares.length > 0 || expiringSoonShares.length > 0 || staleShares.length > 0) && (
        <div className="rounded-lg border border-divider bg-content1 px-4 py-3 text-sm text-default-600">
          <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
            <span>复核：{riskyShares.length}</span>
            <span>即将到期：{expiringSoonShares.length}</span>
            <span>无密码：{passwordlessShares.length}</span>
            <span>覆盖较大：{broadFolderShares.length}</span>
            <span>长期未访问：{staleShares.length}</span>
          </div>
        </div>
      )}

      {/* Delete confirmation modal */}
      <Modal 
        isOpen={!!deleteTarget} 
        onClose={handleCloseDeleteModal}
        placement="center"
        size="md"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-danger/10 text-danger flex items-center justify-center">
              <Trash2 size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">删除分享</h3>
              <p className="text-xs text-default-500 font-normal">已分享的链接将失效</p>
            </div>
          </ModalHeader>
          <ModalBody className="px-6 py-4">
            <p className="text-default-600">确定要删除此分享链接吗？</p>
            {deleteTarget && (
              <div className="p-3 bg-content2 rounded-lg mt-3 border border-divider">
                <div className="text-xs text-default-500 mb-1">分享路径</div>
                <div className="font-medium truncate text-foreground flex items-center gap-2">
                  <FileIcon name={deleteTarget.path} isDir={deleteTarget.type === 'folder'} size={16} />
                  <span className="truncate">{deleteTarget.path}</span>
                </div>
              </div>
            )}
          </ModalBody>
          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button
              variant="flat"
              onPress={handleCloseDeleteModal}
              isDisabled={isDeleting}
              className="text-default-600 rounded-lg"
            >
              取消
            </Button>
            <Button 
              color="danger" 
              onPress={handleDelete}
              isLoading={isDeleting}
              className="rounded-lg"
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
  const riskPresentation = getRiskPresentation(share.risk?.level)
  const riskReasons = share.risk?.reasons?.filter(reason => !reason.resolved) ?? []

  return (
    <Card className="card-meridian">
      <CardBody className="p-4">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
          {/* Icon */}
          <FileIcon
            name={fileName}
            isDir={share.type === 'folder'}
            size={28}
          />

          {/* Content */}
          <div className="min-w-0 flex-1">
            <div className="mb-1 flex min-w-0 flex-wrap items-center gap-2">
              <span className="font-medium text-foreground truncate">
                {fileName}
              </span>
              {!share.enabled && (
                <Chip size="sm" color="default" variant="flat">已禁用</Chip>
              )}
              {isExpired && (
                <Chip size="sm" color="danger" variant="flat">已过期</Chip>
              )}
              {riskPresentation && (
                <Chip size="sm" color={riskPresentation.color} variant="flat">
                  {riskPresentation.label}
                </Chip>
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

            {riskReasons.length > 0 && (
              <div className="mt-3 rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-default-700">
                <div className="mb-1 flex items-center gap-1 font-medium text-warning">
                  <AlertCircle size={12} />
                  <span>风险提醒</span>
                </div>
                <div className="space-y-1">
                  {riskReasons.slice(0, 3).map(reason => (
                    <div key={reason.code}>{reason.message}</div>
                  ))}
                </div>
              </div>
            )}
          </div>

          {/* Actions */}
          <div className="flex shrink-0 items-center gap-2 self-end sm:self-start">
            <Button
              isIconOnly
              variant="flat"
              size="sm"
              onPress={onCopy}
              aria-label={`${fileName} 复制分享链接`}
              className="rounded-lg"
            >
              <Copy size={16} />
            </Button>
            
            <Dropdown>
              <DropdownTrigger>
                <Button isIconOnly variant="flat" size="sm" aria-label={`${fileName} 分享操作`} className="rounded-lg">
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
