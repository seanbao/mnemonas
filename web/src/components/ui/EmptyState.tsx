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
    <div className={cn("flex flex-col items-center justify-center text-center card-meridian border-dashed p-8", className)}>
      {Icon && (
        <div className="w-16 h-16 rounded-2xl glass text-primary flex items-center justify-center mb-4">
          <Icon size={32} className="text-current" />
        </div>
      )}
      <h3 className="text-lg font-semibold text-foreground mb-1">{title}</h3>
      {description && <p className="text-sm text-default-500 mb-4">{description}</p>}
      {action}
    </div>
  )
}