import type { ElementType, ReactNode } from 'react'
import { cn } from '@/lib/utils'

interface PageHeaderProps {
  title: string
  subtitle?: string
  icon?: ElementType
  actions?: ReactNode
  className?: string
  iconClassName?: string
}

export function PageHeader({
  title,
  subtitle,
  icon: Icon,
  actions,
  className,
  iconClassName,
}: PageHeaderProps) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4", className)}>
      <div className="flex min-w-0 items-center gap-3">
        {Icon && (
          <div className={cn("flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-divider bg-content1 text-primary", iconClassName)}>
            <Icon size={20} className="text-current" />
          </div>
        )}
        <div className="min-w-0">
          <h1 className="page-title">{title}</h1>
          {subtitle && <p className="page-subtitle mt-0.5">{subtitle}</p>}
        </div>
      </div>
      {actions && <div className="flex min-w-0 flex-wrap items-center gap-2 sm:justify-end">{actions}</div>}
    </div>
  )
}
