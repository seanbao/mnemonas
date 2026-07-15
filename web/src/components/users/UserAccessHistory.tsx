import { Button } from '@heroui/react'
import { Copy } from 'lucide-react'
import { formatDate } from '@/lib/utils'
import { reviewHistoryLimit, type ReviewHistoryEntry } from './userAccessHistory'

export function ReviewHistory({
  entries,
  onCopy,
  onClear,
}: {
  entries: ReviewHistoryEntry[]
  onCopy: (entry: ReviewHistoryEntry) => void
  onClear: () => void
}) {
  return (
    <div aria-label="目录权限近期复核历史" className="rounded-lg border border-divider bg-content1/60 p-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="text-sm font-semibold text-foreground">近期复核历史</div>
          <p className="mt-1 text-xs leading-5 text-default-500">保留最近 {reviewHistoryLimit} 条矩阵或变更预览；服务端不可用时使用当前浏览器记录。</p>
        </div>
        <Button
          size="sm"
          variant="light"
          className="self-start rounded-lg text-default-500"
          isDisabled={entries.length === 0}
          onPress={onClear}
        >
          清空近期记录
        </Button>
      </div>
      {entries.length === 0 ? (
        <div className="mt-3 rounded-lg border border-dashed border-divider bg-content2/40 px-3 py-2 text-sm text-default-500">
          暂无近期目录权限复核记录。
        </div>
      ) : (
        <ul className="mt-3 space-y-2">
          {entries.map((entry) => (
            <li key={entry.id} className="flex flex-col gap-2 rounded-lg border border-divider bg-content2/50 px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <div className="flex min-w-0 flex-wrap items-center gap-2 text-sm font-medium text-foreground">
                  <span className="break-anywhere font-mono">{entry.path}</span>
                  <span className="rounded-full bg-content1 px-2 py-0.5 text-xs text-default-600">{entry.preview ? '变更预览' : entry.title}</span>
                </div>
                <div className="mt-1 flex flex-wrap gap-2 text-xs text-default-500">
                  <time dateTime={entry.recordedAt}>{formatDate(entry.recordedAt)}</time>
                  {entry.reviewer ? <span>复核人 {entry.reviewer}</span> : null}
                  <span>用户 {entry.users}</span>
                  <span>可读 {entry.readAllowed}</span>
                  <span>可写 {entry.writeAllowed}</span>
                  <span>相关分享 {entry.relatedShares}</span>
                </div>
              </div>
              <Button
                size="sm"
                variant="flat"
                className="self-start rounded-lg"
                startContent={<Copy size={14} aria-hidden="true" />}
                onPress={() => onCopy(entry)}
              >
                复制记录
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
