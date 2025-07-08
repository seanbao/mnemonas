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
  density?: 'default' | 'compact'
  action?: ReactNode
  onPress?: () => void
  ariaLabel?: string
  className?: string
}

function getDefaultAriaLabel(title: string, value: string | number, subtitle?: string): string {
  return [title, String(value), subtitle]
    .filter((part): part is string => typeof part === 'string' && part.trim().length > 0)
    .join('，')
}

export function StatCard({
  title,
  value,
  subtitle,
  icon: Icon,
  tone = 'default',
  density = 'default',
  action,
  onPress,
  ariaLabel,
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
  const statAccessibleName = getDefaultAriaLabel(title, value, subtitle)
  const actionAccessibleName = ariaLabel ?? statAccessibleName

  const content = (
    <>
      {Icon && (
        <div
          className={cn(
            "flex shrink-0 items-center justify-center self-start rounded-lg border border-divider",
            density === 'compact' ? 'h-8 w-8' : 'h-10 w-10',
            toneClasses[tone],
          )}
        >
          <Icon size={density === 'compact' ? 18 : 20} className="text-current" />
        </div>
      )}
      <div className="flex-1 min-w-0">
        <p className={cn("uppercase text-default-500", density === 'compact' ? 'text-[11px] leading-4' : 'text-xs')}>{title}</p>
        <p className={cn(
          "break-anywhere font-semibold leading-tight text-foreground",
          density === 'compact' ? 'text-xl' : 'text-2xl',
        )}>{value}</p>
        {subtitle && (
          <p className={cn("break-anywhere text-default-500", density === 'compact' ? 'text-[11px] leading-4' : 'text-xs')}>
            {subtitle}
          </p>
        )}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </>
  )

  return (
    <Card
      role="group"
      aria-label={statAccessibleName}
      className={cn("card-mnemonas min-w-0", onPress && "transition-colors hover:border-primary/40", className)}
    >
      <CardBody
        className={cn(
          "p-0",
          !onPress && "flex items-start gap-3",
          !onPress && (density === 'compact' ? 'min-h-[5rem] p-3' : 'min-h-[6rem] p-4'),
        )}
      >
        {onPress ? (
          <button
            type="button"
            aria-label={actionAccessibleName}
            className={cn(
              "flex w-full items-start gap-3 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary/40",
              density === 'compact' ? 'min-h-[5rem] p-3' : 'min-h-[6rem] p-4',
            )}
            onClick={onPress}
          >
            {content}
          </button>
        ) : content}
      </CardBody>
    </Card>
  )
}
