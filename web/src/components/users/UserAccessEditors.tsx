import { useState } from 'react'
import { Button, Card, CardBody, CardHeader, Input } from '@heroui/react'
import { Plus, Trash2 } from 'lucide-react'
import type { DirectoryAccessRule, DirectoryQuota } from '@/api/settings'
import { cn, formatBytes } from '@/lib/utils'
import {
  formatLogicalPathLineToken,
  normalizeLogicalPathInput,
  parseDirectoryAccessRuleLines,
  parseDirectoryQuotaLines,
} from './userAccessDraft'

export function AccessSection({
  title,
  description,
  icon: Icon,
  children,
}: {
  title: string
  description: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  children: React.ReactNode
}) {
  const headingID = `user-access-${title === '目录配额' ? 'quota' : 'rules'}`
  return (
    <Card className="card-mnemonas" as="section" aria-labelledby={headingID}>
      <CardHeader className="flex min-w-0 gap-3 pb-2 sm:gap-4">
        <div className="gradient-mnemonas shrink-0 rounded-lg p-2.5 shadow-sm">
          <Icon size={20} className="text-white" aria-hidden="true" />
        </div>
        <div className="min-w-0 flex-1">
          <h2 id={headingID} className="break-anywhere text-base font-semibold text-foreground">{title}</h2>
          <p className="break-anywhere mt-0.5 text-xs leading-5 text-default-500">{description}</p>
        </div>
      </CardHeader>
      <CardBody className="pt-2">{children}</CardBody>
    </Card>
  )
}

type QuotaReviewItem = {
  kind: 'added' | 'changed' | 'removed'
  path: string
  before?: DirectoryQuota
  after?: DirectoryQuota
}

function buildQuotaReview(saved: DirectoryQuota[], draft: DirectoryQuota[]): QuotaReviewItem[] {
  const savedByPath = new Map(saved.map((quota) => [quota.path, quota]))
  const draftByPath = new Map(draft.map((quota) => [quota.path, quota]))
  const review: QuotaReviewItem[] = []
  for (const quota of draft) {
    const previous = savedByPath.get(quota.path)
    if (!previous) {
      review.push({ kind: 'added', path: quota.path, after: quota })
    } else if (previous.quota_bytes !== quota.quota_bytes) {
      review.push({ kind: 'changed', path: quota.path, before: previous, after: quota })
    }
  }
  for (const quota of saved) {
    if (!draftByPath.has(quota.path)) {
      review.push({ kind: 'removed', path: quota.path, before: quota })
    }
  }
  return review
}

function quotaReviewDescription(item: QuotaReviewItem): string {
  if (item.kind === 'changed' && item.before && item.after) {
    return `配额从 ${formatBytes(item.before.quota_bytes)} 调整为 ${formatBytes(item.after.quota_bytes)}`
  }
  const quota = item.after ?? item.before
  return quota ? `容量 ${formatBytes(quota.quota_bytes)}` : ''
}

export function DirectoryQuotaChangeReview({
  saved,
  draftValue,
}: {
  saved: DirectoryQuota[]
  draftValue: string
}) {
  const parsed = parseDirectoryQuotaLines(draftValue)
  if (parsed.error) {
    return (
      <div className="rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-warning">
        目录配额变更复核暂不可用：{parsed.error}
      </div>
    )
  }

  const review = buildQuotaReview(saved, parsed.quotas)
  const counts = {
    added: review.filter((item) => item.kind === 'added').length,
    changed: review.filter((item) => item.kind === 'changed').length,
    removed: review.filter((item) => item.kind === 'removed').length,
  }

  return (
    <div className="rounded-lg border border-divider bg-content1/60 p-3" aria-label="目录配额变更复核">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="text-sm font-semibold text-foreground">目录配额变更复核</div>
          <div className="mt-1 text-xs text-default-500">比较已保存配置和当前草稿。</div>
        </div>
        <div className="flex flex-wrap gap-2 text-xs">
          <span className="rounded-full bg-success/10 px-2 py-1 text-success">新增 {counts.added}</span>
          <span className="rounded-full bg-warning/10 px-2 py-1 text-warning">修改 {counts.changed}</span>
          <span className="rounded-full bg-danger/10 px-2 py-1 text-danger">删除 {counts.removed}</span>
        </div>
      </div>
      {review.length === 0 ? (
        <div className="mt-3 rounded-lg border border-divider bg-content2/40 px-3 py-2 text-sm text-default-500">
          目录配额与已保存配置一致。
        </div>
      ) : (
        <ul className="mt-3 space-y-2">
          {review.map((item) => (
            <li key={`${item.kind}:${item.path}`} className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
              <div className="flex flex-wrap items-center gap-2">
                <span className={cn(
                  'rounded-full border px-2 py-0.5 text-xs font-medium',
                  item.kind === 'added'
                    ? 'border-success/30 bg-success/5 text-success'
                    : item.kind === 'changed'
                      ? 'border-warning/30 bg-warning/5 text-warning'
                      : 'border-danger/30 bg-danger/5 text-danger',
                )}>
                  {item.kind === 'added' ? '新增' : item.kind === 'changed' ? '修改' : '删除'}
                </span>
                <span className="break-anywhere font-mono text-sm font-semibold text-foreground">{item.path}</span>
              </div>
              <div className="mt-1 text-xs text-default-500">{quotaReviewDescription(item)}</div>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

interface AccessRuleDraft {
  path: string
  readUsers: string
  writeUsers: string
  readGroups: string
  writeGroups: string
  readRoles: string
  writeRoles: string
}

type AccessRuleDraftField = keyof AccessRuleDraft

function emptyAccessRuleDraft(): AccessRuleDraft {
  return {
    path: '',
    readUsers: '',
    writeUsers: '',
    readGroups: '',
    writeGroups: '',
    readRoles: '',
    writeRoles: '',
  }
}

const accessRulePresets: Array<{
  label: string
  description: string
  draft: AccessRuleDraft
}> = [
  {
    label: '全员协作',
    description: '普通用户可读写 /shared。',
    draft: { ...emptyAccessRuleDraft(), path: '/shared', readRoles: 'user', writeRoles: 'user' },
  },
  {
    label: '全员只读',
    description: '普通用户可读取 /library，只有管理员可写入。',
    draft: { ...emptyAccessRuleDraft(), path: '/library', readRoles: 'user', writeRoles: 'admin' },
  },
  {
    label: '管理员归档',
    description: '仅管理员可读写 /archive。',
    draft: { ...emptyAccessRuleDraft(), path: '/archive', readRoles: 'admin', writeRoles: 'admin' },
  },
]

function accessRuleToDraft(rule: DirectoryAccessRule): AccessRuleDraft {
  return {
    path: rule.path,
    readUsers: (rule.read_users ?? []).join(', '),
    writeUsers: (rule.write_users ?? []).join(', '),
    readGroups: (rule.read_groups ?? []).join(', '),
    writeGroups: (rule.write_groups ?? []).join(', '),
    readRoles: (rule.read_roles ?? []).join(', '),
    writeRoles: (rule.write_roles ?? []).join(', '),
  }
}

function draftsFromText(value: string): AccessRuleDraft[] {
  const parsed = parseDirectoryAccessRuleLines(value)
  if (!parsed.error) {
    return parsed.rules.length > 0 ? parsed.rules.map(accessRuleToDraft) : [emptyAccessRuleDraft()]
  }
  return [emptyAccessRuleDraft()]
}

function draftList(value: string): string[] {
  return value.split(',').map((entry) => entry.trim()).filter(Boolean)
}

function formatAccessRuleDrafts(drafts: AccessRuleDraft[]): string {
  return drafts
    .map((draft) => {
      const parts = [draft.path.trim() ? formatLogicalPathLineToken(draft.path) : '']
      const fields: Array<[string, string]> = [
        ['read_users', draft.readUsers],
        ['write_users', draft.writeUsers],
        ['read_groups', draft.readGroups],
        ['write_groups', draft.writeGroups],
        ['read_roles', draft.readRoles],
        ['write_roles', draft.writeRoles],
      ]
      for (const [key, value] of fields) {
        const values = draftList(value)
        if (values.length > 0) {
          parts.push(`${key}=${values.join(',')}`)
        }
      }
      return parts.filter(Boolean).join(' ')
    })
    .filter(Boolean)
    .join('\n')
}

export function AccessRuleEditor({
  value,
  onChange,
}: {
  value: string
  onChange: (value: string) => void
}) {
  const [state, setState] = useState(() => ({ source: value, drafts: draftsFromText(value) }))
  const drafts = state.source === value ? state.drafts : draftsFromText(value)

  const commit = (nextDrafts: AccessRuleDraft[]) => {
    const normalizedDrafts = nextDrafts.length > 0 ? nextDrafts : [emptyAccessRuleDraft()]
    const source = formatAccessRuleDrafts(normalizedDrafts)
    setState({ source, drafts: normalizedDrafts })
    onChange(source)
  }

  const updateDraft = (index: number, field: AccessRuleDraftField, nextValue: string) => {
    commit(drafts.map((draft, currentIndex) => (
      currentIndex === index ? { ...draft, [field]: nextValue } : draft
    )))
  }

  const applyPreset = (preset: (typeof accessRulePresets)[number]) => {
    const path = normalizeLogicalPathInput(preset.draft.path)
    const existingIndex = drafts.findIndex((draft) => normalizeLogicalPathInput(draft.path) === path)
    const current = drafts.length === 1 && Object.values(drafts[0]).every((entry) => !entry.trim())
      ? []
      : drafts
    commit(existingIndex >= 0
      ? current.map((draft, index) => (index === existingIndex ? { ...preset.draft } : draft))
      : [...current, { ...preset.draft }])
  }

  return (
    <div className="space-y-3">
      <div className="rounded-lg border border-divider bg-content1/60 p-3">
        <div className="text-sm font-semibold text-foreground">权限策略预设</div>
        <p className="mt-1 text-xs leading-5 text-default-500">选择常用边界后，可继续按用户或用户组调整。</p>
        <div className="mt-3 grid gap-2 md:grid-cols-3">
          {accessRulePresets.map((preset) => (
            <Button
              key={preset.label}
              variant="flat"
              className="h-auto min-w-0 justify-start rounded-lg px-3 py-2 text-left"
              onPress={() => applyPreset(preset)}
            >
              <span className="min-w-0">
                <span className="block text-sm font-medium">{preset.label}</span>
                <span className="mt-1 block whitespace-normal text-xs leading-5 text-default-500">{preset.description}</span>
              </span>
            </Button>
          ))}
        </div>
      </div>
      {drafts.map((draft, index) => {
        const number = index + 1
        return (
          <fieldset key={index} className="rounded-lg border border-divider bg-content1/60 p-3">
            <legend className="sr-only">目录权限规则 {number}</legend>
            <div className="mb-3 flex items-center justify-between gap-3">
              <div className="text-sm font-semibold text-foreground">规则 {number}</div>
              <Button
                isIconOnly
                variant="light"
                color="danger"
                className="rounded-lg"
                aria-label={`删除目录权限规则 ${number}`}
                onPress={() => commit(drafts.filter((_, currentIndex) => currentIndex !== index))}
                isDisabled={drafts.length === 1 && !value.trim()}
              >
                <Trash2 size={16} aria-hidden="true" />
              </Button>
            </div>
            <div className="grid min-w-0 gap-3 lg:grid-cols-3">
              <Input
                label="路径"
                aria-label={`目录权限路径 ${number}`}
                value={draft.path}
                onValueChange={(next) => updateDraft(index, 'path', next)}
                placeholder="/team"
                className="input-shell min-w-0 lg:col-span-3"
              />
              {([
                ['读用户', 'readUsers', 'alice,bob'],
                ['写用户', 'writeUsers', 'alice'],
                ['读组', 'readGroups', 'family'],
                ['写组', 'writeGroups', 'editors'],
                ['读角色', 'readRoles', 'user'],
                ['写角色', 'writeRoles', 'admin'],
              ] as Array<[string, AccessRuleDraftField, string]>).map(([label, field, placeholder]) => (
                <Input
                  key={field}
                  label={label}
                  aria-label={`${label} ${number}`}
                  value={draft[field]}
                  onValueChange={(next) => updateDraft(index, field, next)}
                  placeholder={placeholder}
                  className="input-shell min-w-0"
                />
              ))}
            </div>
          </fieldset>
        )
      })}
      <Button
        variant="bordered"
        className="w-full rounded-lg sm:w-auto"
        onPress={() => commit([...drafts, emptyAccessRuleDraft()])}
        startContent={<Plus size={16} aria-hidden="true" />}
      >
        添加规则
      </Button>
    </div>
  )
}

const ruleFields: Array<{
  key: keyof Omit<DirectoryAccessRule, 'path'>
  label: string
}> = [
  { key: 'read_users', label: '读用户' },
  { key: 'write_users', label: '写用户' },
  { key: 'read_groups', label: '读组' },
  { key: 'write_groups', label: '写组' },
  { key: 'read_roles', label: '读角色' },
  { key: 'write_roles', label: '写角色' },
]

function normalizedRuleValues(values: string[] | undefined): string[] {
  return Array.from(new Set((values ?? []).map((value) => value.trim().toLowerCase()).filter(Boolean))).sort()
}

function accessRuleSummary(rule: DirectoryAccessRule): string {
  const parts = ruleFields.map(({ key, label }) => {
    const values = normalizedRuleValues(rule[key])
    return values.length > 0 ? `${label}: ${values.join(', ')}` : ''
  }).filter(Boolean)
  return parts.length > 0 ? parts.join(' · ') : '未配置主体'
}

function changedRuleFields(before: DirectoryAccessRule, after: DirectoryAccessRule): string[] {
  return ruleFields.filter(({ key }) => {
    const left = normalizedRuleValues(before[key])
    const right = normalizedRuleValues(after[key])
    return left.length !== right.length || left.some((value, index) => value !== right[index])
  }).map(({ label }) => label)
}

type AccessRuleReviewItem = {
  kind: 'added' | 'changed' | 'removed'
  path: string
  active: DirectoryAccessRule
  fields: string[]
}

function buildAccessRuleReview(
  saved: DirectoryAccessRule[],
  draft: DirectoryAccessRule[],
): AccessRuleReviewItem[] {
  const savedByPath = new Map(saved.map((rule) => [rule.path, rule]))
  const draftByPath = new Map(draft.map((rule) => [rule.path, rule]))
  const review: AccessRuleReviewItem[] = []
  for (const rule of draft) {
    const before = savedByPath.get(rule.path)
    if (!before) {
      review.push({ kind: 'added', path: rule.path, active: rule, fields: [] })
      continue
    }
    const fields = changedRuleFields(before, rule)
    if (fields.length > 0) {
      review.push({ kind: 'changed', path: rule.path, active: rule, fields })
    }
  }
  for (const rule of saved) {
    if (!draftByPath.has(rule.path)) {
      review.push({ kind: 'removed', path: rule.path, active: rule, fields: [] })
    }
  }
  return review
}

export function AccessRuleChangeReview({
  saved,
  draftValue,
}: {
  saved: DirectoryAccessRule[]
  draftValue: string
}) {
  const parsed = parseDirectoryAccessRuleLines(draftValue)
  if (parsed.error) {
    return (
      <div className="rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-warning">
        目录权限变更复核暂不可用：{parsed.error}
      </div>
    )
  }
  const review = buildAccessRuleReview(saved, parsed.rules)
  const counts = {
    added: review.filter((item) => item.kind === 'added').length,
    changed: review.filter((item) => item.kind === 'changed').length,
    removed: review.filter((item) => item.kind === 'removed').length,
  }

  return (
    <div className="rounded-lg border border-divider bg-content1/60 p-3" aria-label="目录权限变更复核">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="text-sm font-semibold text-foreground">目录权限变更复核</div>
          <div className="mt-1 text-xs text-default-500">比较已保存规则和当前草稿。</div>
        </div>
        <div className="flex flex-wrap gap-2 text-xs">
          <span className="rounded-full bg-success/10 px-2 py-1 text-success">新增 {counts.added}</span>
          <span className="rounded-full bg-warning/10 px-2 py-1 text-warning">修改 {counts.changed}</span>
          <span className="rounded-full bg-danger/10 px-2 py-1 text-danger">删除 {counts.removed}</span>
        </div>
      </div>
      {review.length === 0 ? (
        <div className="mt-3 rounded-lg border border-divider bg-content2/40 px-3 py-2 text-sm text-default-500">
          目录权限与已保存配置一致。
        </div>
      ) : (
        <ul className="mt-3 space-y-2">
          {review.map((item) => (
            <li key={`${item.kind}:${item.path}`} className="rounded-lg border border-divider bg-content2/40 px-3 py-2">
              <div className="flex flex-wrap items-center gap-2">
                <span className={cn(
                  'rounded-full border px-2 py-0.5 text-xs font-medium',
                  item.kind === 'added'
                    ? 'border-success/30 bg-success/5 text-success'
                    : item.kind === 'changed'
                      ? 'border-warning/30 bg-warning/5 text-warning'
                      : 'border-danger/30 bg-danger/5 text-danger',
                )}>
                  {item.kind === 'added' ? '新增' : item.kind === 'changed' ? '修改' : '删除'}
                </span>
                <span className="break-anywhere font-mono text-sm font-semibold text-foreground">{item.path}</span>
                {item.fields.length > 0 && (
                  <span className="text-xs text-default-500">变更字段：{item.fields.join('、')}</span>
                )}
              </div>
              <div className="mt-1 break-anywhere text-xs text-default-500">{accessRuleSummary(item.active)}</div>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function addPrincipals(target: Set<string>, type: string, values: string[] | undefined) {
  normalizedRuleValues(values).forEach((value) => target.add(`${type}:${value}`))
}

export function AccessCoverageSummary({ draftValue }: { draftValue: string }) {
  const parsed = parseDirectoryAccessRuleLines(draftValue)
  if (parsed.error) {
    return (
      <div className="rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-warning">
        目录权限覆盖摘要暂不可用：{parsed.error}
      </div>
    )
  }
  if (parsed.rules.length === 0) {
    return (
      <div aria-label="目录权限覆盖摘要" className="rounded-lg border border-dashed border-divider bg-content2/40 px-3 py-3 text-sm text-default-500">
        暂无目录权限规则。未命中规则的路径继续受用户主目录边界限制。
      </div>
    )
  }

  const readers = new Set<string>()
  const writers = new Set<string>()
  for (const rule of parsed.rules) {
    addPrincipals(readers, 'user', rule.read_users)
    addPrincipals(readers, 'group', rule.read_groups)
    addPrincipals(readers, 'role', rule.read_roles)
    addPrincipals(readers, 'user', rule.write_users)
    addPrincipals(readers, 'group', rule.write_groups)
    addPrincipals(readers, 'role', rule.write_roles)
    addPrincipals(writers, 'user', rule.write_users)
    addPrincipals(writers, 'group', rule.write_groups)
    addPrincipals(writers, 'role', rule.write_roles)
  }
  const attention = parsed.rules.filter((rule) => (
    rule.path === '/'
    || normalizedRuleValues(rule.write_roles).includes('guest')
    || normalizedRuleValues(rule.write_roles).includes('user')
    || normalizedRuleValues(rule.read_roles).includes('guest')
  ))

  return (
    <div aria-label="目录权限覆盖摘要" className="rounded-lg border border-divider bg-content1/60 p-3">
      <div className="text-sm font-semibold text-foreground">目录权限覆盖摘要</div>
      <div className="mt-1 text-xs text-default-500">根据当前草稿估算；保存后可用权限检查和用户矩阵复核。</div>
      <dl className="mt-3 grid grid-cols-2 gap-2 lg:grid-cols-4">
        {[
          ['规则总数', `${parsed.rules.length} 条`],
          ['有效可读主体', `${readers.size} 个`],
          ['可写主体', `${writers.size} 个`],
          ['写权限路径', `${parsed.rules.filter((rule) => (
            rule.write_users?.length || rule.write_groups?.length || rule.write_roles?.length
          )).length} 个`],
        ].map(([label, value]) => (
          <div key={label} className="rounded-lg border border-default-200 bg-content2/40 px-3 py-2">
            <dt className="text-xs text-default-500">{label}</dt>
            <dd className="mt-1 text-base font-semibold text-foreground">{value}</dd>
          </div>
        ))}
      </dl>
      <div className={cn(
        'mt-3 rounded-lg border px-3 py-2 text-xs',
        attention.length > 0
          ? 'border-warning/25 bg-warning/5 text-warning'
          : 'border-success/20 bg-success/5 text-success',
      )}>
        {attention.length > 0
          ? `发现 ${attention.length} 条根路径、访客或普通用户宽权限规则，请重点复核。`
          : '未发现根路径授权、访客授权或普通用户宽写入规则。'}
      </div>
    </div>
  )
}
