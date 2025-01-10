import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Card, CardBody, CardHeader, Button, Chip, Progress, Divider, Table, TableHeader, TableColumn, TableBody, TableRow, TableCell } from '@heroui/react'
import { 
  ShieldCheck, 
  Play, 
  Download, 
  CheckCircle, 
  XCircle, 
  AlertCircle,
  Clock,
  Database,
  RefreshCw,
  FileWarning
} from 'lucide-react'
import { PageHeader } from '@/components/ui/PageHeader'
import { StatCard } from '@/components/ui/StatCard'
import { getScrubResult, runScrub, downloadDiagnosticsExport, type ScrubResult, type ScrubError } from '@/api/files'
import { formatBytes, formatDuration } from '@/lib/utils'

// Status chip component
function StatusChip({ status }: { status?: string }) {
  if (!status) return null
  
  const configs: Record<string, { color: 'success' | 'warning' | 'danger' | 'default'; icon: React.ReactNode; label: string }> = {
    completed: { color: 'success', icon: <CheckCircle size={14} />, label: '校验完成' },
    running: { color: 'warning', icon: <RefreshCw size={14} className="animate-spin" />, label: '校验中...' },
    failed: { color: 'danger', icon: <XCircle size={14} />, label: '校验失败' },
  }
  
  const config = configs[status] || { color: 'default', icon: <AlertCircle size={14} />, label: status }
  
  return (
    <Chip size="sm" color={config.color} variant="flat" startContent={config.icon}>
      {config.label}
    </Chip>
  )
}

// Result summary card
function ResultSummary({ result }: { result: ScrubResult }) {
  if (!result.has_result || !result.status || result.status === 'running') {
    return null
  }
  
  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mt-4">
      <StatCard
        title="总对象数"
        value={result.total_objects || 0}
        icon={Database}
        tone="primary"
      />
      <StatCard
        title="有效对象"
        value={result.valid_objects || 0}
        icon={CheckCircle}
        tone="success"
      />
      <StatCard
        title="损坏对象"
        value={result.corrupted_objects || 0}
        icon={AlertCircle}
        tone={(result.corrupted_objects || 0) > 0 ? 'danger' : 'default'}
      />
      <StatCard
        title="缺失对象"
        value={result.missing_objects || 0}
        icon={XCircle}
        tone={(result.missing_objects || 0) > 0 ? 'warning' : 'default'}
      />
    </div>
  )
}

// Error list component
function ErrorList({ errors }: { errors: ScrubError[] }) {
  if (!errors || errors.length === 0) return null
  
  return (
    <div className="mt-4">
      <h4 className="text-sm font-medium mb-2 flex items-center gap-2">
        <FileWarning size={16} className="text-danger" />
        发现的问题 ({errors.length})
      </h4>
      <Table aria-label="错误列表" isStriped>
        <TableHeader>
          <TableColumn>哈希</TableColumn>
          <TableColumn>错误类型</TableColumn>
          <TableColumn>详情</TableColumn>
        </TableHeader>
        <TableBody>
          {errors.slice(0, 100).map((error, index) => (
            <TableRow key={index}>
              <TableCell>
                <code className="text-xs">{error.hash.slice(0, 16)}...</code>
              </TableCell>
              <TableCell>
                <Chip size="sm" color="danger" variant="flat">{error.error_type}</Chip>
              </TableCell>
              <TableCell className="text-sm">{error.message}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {errors.length > 100 && (
        <p className="text-sm text-default-500 mt-2">
          仅显示前 100 条，共 {errors.length} 条错误
        </p>
      )}
    </div>
  )
}

export default function Maintenance() {
  const queryClient = useQueryClient()
  const [isExporting, setIsExporting] = useState(false)
  
  // Fetch last scrub result
  const { data: scrubResult, isLoading } = useQuery({
    queryKey: ['scrub-result'],
    queryFn: getScrubResult,
    refetchInterval: (query) => {
      // Auto-refresh while scrub is running
      const data = query.state.data
      return data?.status === 'running' ? 2000 : false
    },
  })
  
  // Run scrub mutation
  const scrubMutation = useMutation({
    mutationFn: () => runScrub(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['scrub-result'] })
    },
  })
  
  // Handle export
  const handleExport = async () => {
    setIsExporting(true)
    try {
      await downloadDiagnosticsExport()
    } catch (error) {
      console.error('Export failed:', error)
    } finally {
      setIsExporting(false)
    }
  }
  
  const isRunning = scrubResult?.status === 'running'
  
  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="p-6 space-y-6">
      <PageHeader
        title="系统维护"
        subtitle="数据校验与诊断工具"
        icon={ShieldCheck}
        actions={
          <Button
            className="btn-secondary rounded-xl"
            startContent={<Download size={18} />}
            isLoading={isExporting}
            onPress={handleExport}
          >
            导出诊断信息
          </Button>
        }
      />
      
      {/* Scrub Card */}
      <Card className="card-meridian">
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-xl bg-accent-primary/15 flex items-center justify-center">
              <ShieldCheck size={20} className="text-accent-primary" />
            </div>
            <div>
              <h3 className="font-semibold">数据完整性校验</h3>
              <p className="text-xs text-default-500">验证所有存储对象的 BLAKE3 哈希值</p>
            </div>
          </div>
          <div className="flex items-center gap-2">
            {scrubResult && <StatusChip status={scrubResult.status} />}
            <Button
              className="btn-primary shadow-md rounded-xl"
              startContent={isRunning ? <RefreshCw size={18} className="animate-spin" /> : <Play size={18} />}
              isLoading={scrubMutation.isPending}
              isDisabled={isRunning}
              onPress={() => scrubMutation.mutate()}
            >
              {isRunning ? '校验中...' : '开始校验'}
            </Button>
          </div>
        </CardHeader>
        <Divider />
        <CardBody>
          {isLoading ? (
            <div className="flex items-center justify-center py-8">
              <RefreshCw size={24} className="animate-spin text-default-400" />
            </div>
          ) : scrubResult?.has_result ? (
            <>
              {/* Meta info */}
              <div className="flex flex-wrap items-center gap-4 text-sm text-default-500">
                {scrubResult.id && (
                  <div className="flex items-center gap-1">
                    <Database size={14} />
                    <span>任务 ID: {scrubResult.id}</span>
                  </div>
                )}
                {scrubResult.start_time && (
                  <div className="flex items-center gap-1">
                    <Clock size={14} />
                    <span>开始: {new Date(scrubResult.start_time).toLocaleString('zh-CN')}</span>
                  </div>
                )}
                {scrubResult.duration_ms !== undefined && scrubResult.status !== 'running' && (
                  <div className="flex items-center gap-1">
                    <Clock size={14} />
                    <span>耗时: {formatDuration(scrubResult.duration_ms)}</span>
                  </div>
                )}
                {scrubResult.total_size !== undefined && (
                  <div className="flex items-center gap-1">
                    <Database size={14} />
                    <span>数据量: {formatBytes(scrubResult.total_size)}</span>
                  </div>
                )}
              </div>
              
              {/* Progress indicator while running */}
              {isRunning && (
                <div className="mt-4">
                  <Progress
                    size="sm"
                    isIndeterminate
                    aria-label="校验进行中"
                    className="max-w-full"
                  />
                  <p className="text-sm text-default-500 mt-2">正在校验数据完整性，这可能需要一些时间...</p>
                </div>
              )}
              
              {/* Result summary */}
              <ResultSummary result={scrubResult} />
              
              {/* Error message */}
              {scrubResult.error_message && (
                <div className="mt-4 p-3 bg-danger/10 rounded-lg border border-danger/20">
                  <p className="text-sm text-danger">{scrubResult.error_message}</p>
                </div>
              )}
              
              {/* Error list */}
              {scrubResult.errors && <ErrorList errors={scrubResult.errors} />}
            </>
          ) : (
            <div className="text-center py-8 text-default-500">
              <ShieldCheck size={48} className="mx-auto mb-4 opacity-30" />
              <p>尚未执行过数据校验</p>
              <p className="text-sm mt-1">点击"开始校验"来验证所有存储数据的完整性</p>
            </div>
          )}
        </CardBody>
      </Card>
      
      {/* Info Card */}
      <Card className="card-meridian">
        <CardBody className="text-sm text-default-600">
          <h4 className="font-medium mb-2">关于数据校验</h4>
          <ul className="list-disc list-inside space-y-1">
            <li>校验会读取每个存储块并重新计算 BLAKE3 哈希值</li>
            <li>对比计算的哈希与存储的哈希来检测数据损坏</li>
            <li>大量数据时校验可能需要较长时间</li>
            <li>建议定期执行校验以确保数据完整性</li>
          </ul>
        </CardBody>
      </Card>
      </div>
    </div>
  )
}
