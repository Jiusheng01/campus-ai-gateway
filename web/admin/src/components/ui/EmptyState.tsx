import type {ReactNode} from 'react'
import {Inbox} from 'lucide-react'

type Props = {
  icon?: ReactNode
  title: string
  description?: string
  action?: ReactNode
}

/** Shown whenever a list legitimately has nothing in it. */
export function EmptyState({icon, title, description, action}: Props) {
  return (
    <div className="emptyState">
      <span className="emptyIcon" aria-hidden>
        {icon ?? <Inbox />}
      </span>
      <b className="emptyTitle">{title}</b>
      {description && <span className="emptyText">{description}</span>}
      {action && <div className="emptyAction">{action}</div>}
    </div>
  )
}

/** Shown when a request failed; always offers a way out when retrying helps. */
export function ErrorState({message, status, onRetry}: {message: string; status?: number; onRetry?: () => void}) {
  // A 403 will not become a 200 by asking again, so offering a retry button
  // would just waste the operator's time. Say what is actually wrong.
  const denied = status === 403
  return (
    <div className={`emptyState ${denied ? 'denied' : 'error'}`}>
      <b className="emptyTitle">{denied ? '没有访问权限' : '加载失败'}</b>
      <span className="emptyText">
        {denied ? '当前账号的角色无权查看该内容。如需访问，请联系校园信息化管理部门调整角色。' : message}
      </span>
      {!denied && onRetry && (
        <div className="emptyAction">
          <button type="button" className="btn secondary sm" onClick={onRetry}>
            重新加载
          </button>
        </div>
      )}
    </div>
  )
}
