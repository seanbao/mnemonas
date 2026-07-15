import { Button, addToast } from '@heroui/react'
import { AlertCircle, CheckCircle2, Copy } from 'lucide-react'
import type {
  DirectoryAccessCheckData,
  DirectoryAccessDecision,
  DirectoryAccessReportData,
} from '@/api/settings'
import { cn, copyTextToClipboard } from '@/lib/utils'

export type ReviewSaveResult = 'saved' | 'local' | 'failed'

function accessSourceLabel(source: DirectoryAccessDecision['source']): string {
  switch (source) {
    case 'admin':
      return '管理员'
    case 'auth_disabled':
      return '未启用认证'
    case 'directory_access_rule':
      return '目录规则'
    case 'home_dir':
      return '主目录'
    case 'invalid_home_dir':
      return '主目录无效'
    case 'user_disabled':
      return '账号已停用'
    case 'user_not_found':
      return '用户不存在'
    default:
      return source
  }
}

function accessDecisionMessage(decision: DirectoryAccessDecision): string {
  if (decision.message?.trim().toLowerCase() === 'directory access rule grants read through an existing descendant') {
    return '已存在的子目录命中读取规则，因此允许查看相关路径。'
  }
  switch (decision.source) {
    case 'admin':
      return '管理员角色拥有完整访问权限。'
    case 'auth_disabled':
      return '当前未启用认证，此路径对该请求开放。'
    case 'user_not_found':
      return '用户不存在，无法访问该路径。'
    case 'user_disabled':
      return '账号已停用，无法访问该路径。'
    case 'invalid_home_dir':
      return '该用户主目录配置无效，无法判断访问范围。'
    case 'home_dir':
      return decision.allowed ? '路径位于该用户主目录内。' : '路径位于该用户主目录外。'
    case 'directory_access_rule':
      return decision.allowed
        ? `目录规则允许${decision.mode === 'write' ? '写入' : '读取'}该路径。`
        : `目录规则未授予${decision.mode === 'write' ? '写入' : '读取'}权限。`
    default:
      return decision.allowed ? '已允许访问该路径。' : '未允许访问该路径。'
  }
}

function AccessDecision({
  label,
  decision,
}: {
  label: string
  decision: DirectoryAccessDecision
}) {
  const Icon = decision.allowed ? CheckCircle2 : AlertCircle
  return (
    <div className={cn(
      'rounded-lg border px-3 py-2',
      decision.allowed
        ? 'border-success/30 bg-success/5 text-success'
        : 'border-danger/30 bg-danger/5 text-danger',
    )}>
      <div className="flex items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-2 text-sm font-semibold">
          <Icon size={16} className="shrink-0" aria-hidden="true" />
          {label}
        </span>
        <span className="rounded-full bg-background/70 px-2 py-0.5 text-xs font-medium text-foreground">
          {decision.allowed ? '允许' : '拒绝'}
        </span>
      </div>
      <p className="mt-1 break-anywhere text-xs text-foreground/70">
        {accessSourceLabel(decision.source)}
        {decision.matched_rule?.path ? ` · ${decision.matched_rule.path}` : ''}
      </p>
      <p className="mt-1 break-anywhere text-xs text-foreground/60">{accessDecisionMessage(decision)}</p>
    </div>
  )
}

export function AccessCheckResult({ result }: { result: DirectoryAccessCheckData }) {
  return (
    <div className="rounded-lg border border-divider bg-content2/40 p-3" aria-label="有效权限检查结果">
      <div className="mb-3 flex flex-wrap items-center gap-2 text-xs text-default-500">
        <span className="rounded-full bg-content1 px-2 py-1 font-mono text-foreground">{result.username}</span>
        <span className="rounded-full bg-content1 px-2 py-1">{result.role}</span>
        <span className="break-anywhere rounded-full bg-content1 px-2 py-1 font-mono text-foreground">{result.path}</span>
      </div>
      <div className="grid gap-2 sm:grid-cols-2">
        <AccessDecision label="读取" decision={result.read} />
        <AccessDecision label="写入" decision={result.write} />
      </div>
    </div>
  )
}

function shareRelationLabel(relation: string): string {
  if (relation === 'exact') return '直接分享'
  if (relation === 'covers_path') return '父级覆盖'
  if (relation === 'inside_path') return '子级分享'
  return relation
}

function formatReport(report: DirectoryAccessReportData, title: string): string {
  const lines = [
    '目录权限复核记录',
    `类型: ${report.preview ? '未保存变更预览' : title}`,
    `路径: ${report.path}`,
    `用户: ${report.summary.users}`,
    `读取: 允许 ${report.summary.read_allowed} / 拒绝 ${report.summary.read_denied}`,
    `写入: 允许 ${report.summary.write_allowed} / 拒绝 ${report.summary.write_denied}`,
    `相关分享: ${report.summary.related_shares}`,
    '',
    '用户明细:',
    ...report.users.map((entry) => (
      `- ${entry.username} (${entry.role}, home ${entry.home_dir}): 读 ${entry.read.allowed ? '允许' : '拒绝'} · ${accessSourceLabel(entry.read.source)}; 写 ${entry.write.allowed ? '允许' : '拒绝'} · ${accessSourceLabel(entry.write.source)}`
    )),
    '',
    '规则生效明细:',
    ...(report.rule_effects?.length
      ? report.rule_effects.map((entry) => `- 规则 ${entry.index + 1} ${entry.path}: 读允许 ${entry.read_allowed} / 读拒绝 ${entry.read_denied}; 写允许 ${entry.write_allowed} / 写拒绝 ${entry.write_denied}`)
      : ['- 未命中目录规则']),
    '',
    '分享影响:',
    ...(report.shares?.length
      ? report.shares.map((entry) => `- ${entry.path} (${shareRelationLabel(entry.relation)}): ${entry.active ? '可访问' : '不可访问'}`)
      : ['- 无相关分享']),
  ]
  return lines.join('\n')
}

export function ReportResult({
  report,
  title = '用户矩阵',
  ariaLabel = '目录权限用户矩阵',
  onSave,
}: {
  report: DirectoryAccessReportData
  title?: string
  ariaLabel?: string
  onSave: (report: DirectoryAccessReportData, title: string, text: string) => Promise<ReviewSaveResult>
}) {
  const copyReport = async () => {
    try {
      const text = formatReport(report, title)
      await copyTextToClipboard(text)
      const saved = await onSave(report, title, text)
      addToast(saved === 'saved'
        ? { title: '目录权限复核记录已复制并保存', color: 'success' }
        : saved === 'local'
          ? {
              title: '目录权限复核记录已复制',
              description: '服务端历史暂不可用，记录已保存在当前浏览器。',
              color: 'warning',
            }
          : {
              title: '目录权限复核记录已复制',
              description: '近期历史写入失败，报告内容已复制到剪贴板。',
              color: 'warning',
            })
    } catch {
      addToast({
        title: '复制目录权限复核记录失败',
        description: '请检查浏览器剪贴板权限。',
        color: 'danger',
      })
    }
  }

  return (
    <div className="rounded-lg border border-divider bg-content2/40 p-3" aria-label={ariaLabel}>
      <div className="mb-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="text-sm font-semibold text-foreground">{title}</div>
        <Button
          size="sm"
          variant="flat"
          className="self-start rounded-lg"
          startContent={<Copy size={14} aria-hidden="true" />}
          onPress={copyReport}
        >
          复制复核记录
        </Button>
      </div>
      <div className="mb-3 flex flex-wrap gap-2 text-xs">
        <span className="break-anywhere rounded-full bg-content1 px-2 py-1 font-mono text-foreground">{report.path}</span>
        <span className="rounded-full bg-content1 px-2 py-1">用户 {report.summary.users}</span>
        <span className="rounded-full bg-success/10 px-2 py-1 text-success">可读 {report.summary.read_allowed}</span>
        <span className="rounded-full bg-success/10 px-2 py-1 text-success">可写 {report.summary.write_allowed}</span>
        <span className="rounded-full bg-danger/10 px-2 py-1 text-danger">读拒绝 {report.summary.read_denied}</span>
        <span className="rounded-full bg-danger/10 px-2 py-1 text-danger">写拒绝 {report.summary.write_denied}</span>
        <span className="rounded-full bg-warning/10 px-2 py-1 text-warning">相关分享 {report.summary.related_shares}</span>
        <span className="rounded-full bg-content1 px-2 py-1">命中规则 {report.rule_effects?.length ?? 0}</span>
      </div>
      <div className="space-y-2" role="list" aria-label={`${title}用户明细`}>
        {report.users.map((entry) => (
          <div key={entry.user_id || entry.username} role="listitem" className="rounded-lg border border-divider bg-content1 px-3 py-2">
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <div className="break-anywhere text-sm font-semibold text-foreground">{entry.username}</div>
                <div className="break-anywhere text-xs text-default-500">{entry.role} · {entry.home_dir}</div>
              </div>
              <div className="flex flex-wrap gap-2 text-xs">
                <span className={cn('rounded-full px-2 py-1', entry.read.allowed ? 'bg-success/10 text-success' : 'bg-danger/10 text-danger')}>
                  读：{entry.read.allowed ? '允许' : '拒绝'} · {accessSourceLabel(entry.read.source)}
                </span>
                <span className={cn('rounded-full px-2 py-1', entry.write.allowed ? 'bg-success/10 text-success' : 'bg-danger/10 text-danger')}>
                  写：{entry.write.allowed ? '允许' : '拒绝'} · {accessSourceLabel(entry.write.source)}
                </span>
              </div>
            </div>
          </div>
        ))}
      </div>
      <div className="mt-3 rounded-lg border border-divider bg-content1" aria-label={`${title}规则生效明细`}>
        {report.rule_effects?.length ? report.rule_effects.map((entry) => (
          <div key={`${entry.index}:${entry.path}`} className="border-b border-divider px-3 py-2 text-xs last:border-b-0">
            <div className="break-anywhere text-sm font-semibold text-foreground">规则 {entry.index + 1} · {entry.path}</div>
            <div className="mt-1 flex flex-wrap gap-2">
              <span className="text-success">读允许 {entry.read_allowed}</span>
              <span className="text-danger">读拒绝 {entry.read_denied}</span>
              <span className="text-success">写允许 {entry.write_allowed}</span>
              <span className="text-danger">写拒绝 {entry.write_denied}</span>
            </div>
          </div>
        )) : <div className="px-3 py-2 text-sm text-default-500">未命中目录规则</div>}
      </div>
    </div>
  )
}
