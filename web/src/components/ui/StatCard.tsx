import type { ElementType, ReactNode } from 'react'
import { Card, CardBody } from '@heroui/react'
import { cn } from '@/lib/utils'

type StatTone = 'default' | 'primary' | 'secondary' | 'success' | 'warning' | 'danger'

interface StatCardProps {
  title: string
  value: string | number
  subtitle?: string
  icon?: ElementType
  tone?: StatTone
  action?: ReactNode
  className?: string
}

export function StatCard({
  title,
  value,
  subtitle,
  icon: Icon,
  tone = 'default',
  action,
  className,
}: StatCardProps) {
  const toneClasses: Record<StatTone, string> = {
    default: 'bg-content2 text-foreground',
    primary: 'bg-primary/10 text-primary',
    secondary: 'bg-secondary/10 text-secondary',
    success: 'bg-success/10 text-success',
    warning: 'bg-warning/10 text-warning',
    danger: 'bg-danger/10 text-danger',
  }

  return (
    <Card className={cn("card-meridian min-w-0", className)}>
      <CardBody className="flex items-center gap-3 p-4">
        {Icon && (
          <div className={cn("flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-divider", toneClasses[tone])}>
            <Icon size={20} className="text-current" />
          </div>
        )}
        <div className="flex-1 min-w-0">
          <p className="text-xs uppercase text-default-500">{title}</p>
          <p className="break-anywhere text-2xl font-semibold leading-tight text-foreground">{value}</p>
          {subtitle && <p className="break-anywhere text-xs text-default-500">{subtitle}</p>}
        </div>
        {action && <div className="shrink-0">{action}</div>}
      </CardBody>
    </Card>
  )
}
