import type { ElementType, ReactNode } from 'react'
import { cn } from '@/lib/utils'

interface EmptyStateProps {
  title: string
  description?: string
  icon?: ElementType
  action?: ReactNode
  className?: string
}

export function EmptyState({
  title,
  description,
  icon: Icon,
  action,
  className,
}: EmptyStateProps) {
  return (
    <div className={cn("card-mnemonas flex min-w-0 flex-col items-center justify-center border-dashed p-8 text-center", className)}>
      {Icon && (
        <div className="mb-4 flex h-14 w-14 shrink-0 items-center justify-center rounded-lg border border-divider bg-content2 text-primary">
          <Icon size={32} className="text-current" />
        </div>
      )}
      <h3 className="break-anywhere mb-1 text-lg font-semibold text-foreground">{title}</h3>
      {description && <p className="break-anywhere mb-4 text-sm text-default-500">{description}</p>}
      {action}
    </div>
  )
}
