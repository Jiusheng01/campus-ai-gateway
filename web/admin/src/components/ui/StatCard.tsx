import {type ReactNode} from 'react'

export type StatCardProps = {
  label: ReactNode
  value: ReactNode
  icon?: ReactNode
  badge?: ReactNode
  subtext?: ReactNode
  trend?: {
    value: string
    isPositive?: boolean
    neutral?: boolean
  }
  sparkline?: ReactNode
  tone?: 'default' | 'success' | 'warning' | 'danger' | 'info' | 'purple'
  className?: string
  onClick?: () => void
}

export function StatCard({
  label,
  value,
  icon,
  badge,
  subtext,
  trend,
  sparkline,
  tone = 'default',
  className = '',
  onClick,
}: StatCardProps) {
  return (
    <div
      className={`statCard ${tone} ${onClick ? 'interactive' : ''} ${className}`.trim()}
      onClick={onClick}
      role={onClick ? 'button' : undefined}
      tabIndex={onClick ? 0 : undefined}
    >
      <div className="statCardTop">
        <div className="statCardLabelGroup">
          <span className="statCardLabel">{label}</span>
          {badge && <span className="statCardBadge">{badge}</span>}
        </div>
        {icon && <span className="statCardIcon" aria-hidden>{icon}</span>}
      </div>

      <div className="statCardMain">
        <div className="statCardValueRow">
          <span className="statCardValue">{value}</span>
          {sparkline && <div className="statCardSparkline">{sparkline}</div>}
        </div>

        {(subtext || trend) && (
          <div className="statCardFoot">
            {trend && (
              <span
                className={`statCardTrend ${trend.neutral ? 'neutral' : trend.isPositive ? 'up' : 'down'}`}
              >
                {trend.isPositive ? '↑' : trend.neutral ? '—' : '↓'} {trend.value}
              </span>
            )}
            {subtext && <span className="statCardSub">{subtext}</span>}
          </div>
        )}
      </div>
    </div>
  )
}

export function StatGrid({
  children,
  columns = 4,
  className = '',
}: {
  children: ReactNode
  columns?: 2 | 3 | 4
  className?: string
}) {
  return <div className={`statGrid cols-${columns} ${className}`.trim()}>{children}</div>
}
