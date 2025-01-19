import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
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
  Skeleton
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
import { getVersions, buildDownloadUrl, restoreVersion, type VersionInfo } from '@/api/files'
import { formatBytes, formatDate } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'

interface VersionRowProps {
  version: VersionInfo
  index: number
  isLatest: boolean
  onPreview: () => void
  onRestore: () => void
  onDownload: () => void
}

function VersionRow({ version, index, isLatest, onPreview, onRestore, onDownload }: VersionRowProps) {
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
            onPress={onDownload}
            title="下载此版本"
            className="btn-secondary btn-sm rounded-xl"
          >
            <Download size={16} />
          </Button>
          {!isLatest && (
            <Button
              isIconOnly
              size="sm"
              variant="light"
              color="warning"
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
  const [searchParams, setSearchParams] = useSearchParams()
  const [searchPath, setSearchPath] = useState('')
  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const [selectedVersion, setSelectedVersion] = useState<VersionInfo | null>(null)
  const { isOpen, onOpen, onClose } = useDisclosure()

  useEffect(() => {
    const paramPath = searchParams.get('path') || ''
    const normalizedPath = paramPath ? (paramPath.startsWith('/') ? paramPath : `/${paramPath}`) : ''
    setSearchPath(normalizedPath)
    setSelectedPath(normalizedPath || null)
  }, [searchParams])
  const queryClient = useQueryClient()

  const { data: versions, isLoading, error } = useQuery({
    queryKey: ['versions', selectedPath],
    queryFn: () => getVersions(selectedPath!),
    enabled: !!selectedPath,
  })

  const restoreMutation = useMutation({
    mutationFn: async ({ path, hash }: { path: string; hash: string }) => {
      await restoreVersion(path, hash)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['versions', selectedPath] })
      queryClient.invalidateQueries({ queryKey: ['files'] })
      onClose()
    },
  })

  const handleSearch = () => {
    if (searchPath.trim()) {
      const normalizedPath = searchPath.startsWith('/') ? searchPath : `/${searchPath}`
      setSelectedPath(normalizedPath)
      setSearchParams({ path: normalizedPath })
    } else {
      setSelectedPath(null)
      setSearchParams({})
    }
  }

  const handleRestore = (version: VersionInfo) => {
    setSelectedVersion(version)
    onOpen()
  }

  const confirmRestore = () => {
    if (selectedPath && selectedVersion) {
      restoreMutation.mutate({ path: selectedPath, hash: selectedVersion.hash })
    }
  }

  const handleDownload = (version: VersionInfo) => {
    const url = buildDownloadUrl(selectedPath!, { version: version.hash, download: true })
    window.open(url, '_blank')
  }

  const handlePreview = (version: VersionInfo) => {
    if (!selectedPath) return
    const url = buildDownloadUrl(selectedPath, { version: version.hash })
    window.open(url, '_blank')
  }

  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="p-6 space-y-6">
      {/* Header */}
      <PageHeader
        title="版本历史"
        subtitle="查看和恢复文件历史版本"
        icon={History}
      />

      {/* Search */}
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

      {/* Version List */}
      {selectedPath && (
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
              <p className="font-medium text-danger mb-1">获取版本历史失败</p>
              <p className="text-sm">{(error as Error).message}</p>
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
      {!selectedPath && (
        <EmptyState
          icon={Sparkles}
          title="查看文件版本历史"
          description="MnemoNAS 自动保留每个文件的历史版本。输入文件路径即可查看所有历史版本，支持预览、下载和一键回滚。"
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
        onClose={onClose}
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
            <Button variant="light" onPress={onClose} className="rounded-xl">
              取消
            </Button>
            <Button 
              color="warning" 
              onPress={confirmRestore}
              isLoading={restoreMutation.isPending}
              className="rounded-xl"
            >
              确认恢复
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      </div>
    </div>
  )
}
