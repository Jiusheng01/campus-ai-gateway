import type {ReactNode} from 'react'

export type Tone = 'neutral' | 'success' | 'warning' | 'danger' | 'info'

export function Badge({tone = 'neutral', children}: {tone?: Tone; children: ReactNode}) {
  return <span className={`badge ${tone}`}>{children}</span>
}

/**
 * Enable/disable badge. Colour alone never carries the meaning: the word is
 * always present, which is also what screen readers announce.
 */
export function StatusBadge({status}: {status: number}) {
  const on = status === 1
  return (
    <span className={`badge status ${on ? 'success' : 'neutral'}`}>
      <i className={`dot ${on ? 'on' : 'off'}`} aria-hidden />
      {on ? '启用' : '停用'}
    </span>
  )
}

/** HTTP status badge for request logs. */
export function HttpStatusBadge({code}: {code: number}) {
  const tone: Tone = code >= 500 ? 'danger' : code >= 400 ? 'warning' : 'success'
  return <span className={`badge http ${tone}`}>{code}</span>
}
