import {Loader2} from 'lucide-react'

/** Placeholder block used while a first load is in flight. */
export function Skeleton({width = '100%', height = 14}: {width?: string | number; height?: number}) {
  return <span className="skeleton" style={{width, height}} aria-hidden />
}

/** Row-shaped skeleton that mirrors a table's rhythm. */
export function SkeletonRows({rows = 5, columns = 5}: {rows?: number; columns?: number}) {
  return (
    <div className="skeletonRows" role="status" aria-label="正在加载">
      {Array.from({length: rows}).map((_, r) => (
        <div className="skeletonRow" key={r}>
          {Array.from({length: columns}).map((__, c) => (
            <Skeleton key={c} width={c === 0 ? '28%' : `${Math.max(12, 60 / columns)}%`} />
          ))}
        </div>
      ))}
    </div>
  )
}

/** Skeleton that mirrors the KPI card grid on the dashboard. */
export function SkeletonCards({count = 4}: {count?: number}) {
  return (
    <div className="skeletonCards" role="status" aria-label="正在加载">
      {Array.from({length: count}).map((_, i) => (
        <div className="skeletonCard" key={i}>
          <Skeleton width="42%" height={11} />
          <Skeleton width="60%" height={22} />
        </div>
      ))}
    </div>
  )
}

/** Inline busy indicator for refresh actions that must not blank the page. */
export function LoadingState({text = '正在加载'}: {text?: string}) {
  return (
    <span className="loadingState" role="status">
      <Loader2 className="spin" aria-hidden />
      {text}
    </span>
  )
}
