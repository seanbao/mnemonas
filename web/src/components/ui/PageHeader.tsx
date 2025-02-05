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
    <div className={cn("flex items-start justify-between gap-4", className)}>
      <div className="flex items-center gap-3 min-w-0">
        {Icon && (
          <div className={cn("w-10 h-10 rounded-lg glass text-primary flex items-center justify-center", iconClassName)}>
            <Icon size={20} className="text-current" />
          </div>
        )}
        <div className="min-w-0">
          <h1 className="page-title text-gradient-meridian">{title}</h1>
          {subtitle && <p className="page-subtitle mt-0.5">{subtitle}</p>}
        </div>
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}