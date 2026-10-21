import type {QuotaDimension} from '../../api'
import {fmt} from '../../lib/format'
import {formatPercent, quotaTone} from '../../lib/quota'

/**
 * One quota band, shared by the administrator request-log section and the
 * personal usage page.
 *
 * A band with no configured limit renders as 未设, never as 0%: the two are
 * opposite facts, and a reader who sees 0% concludes the quota is untouched
 * when there is in fact no quota to be touched.
 *
 * `format` keeps each page's number convention: the log page headlines with the
 * compact form, the personal usage page shows full precision.
 */
export function QuotaBand({
  label,
  q,
  format = fmt,
}: {
  label: string
  q: QuotaDimension | null
  format?: (n: number | null | undefined) => string
}) {
  if (!q) {
    return (
      <div className="quotaCell">
        <small>{label}</small>
        <b className="quotaValue muted">未设</b>
        <span className="quotaMeta">未配置限额</span>
      </div>
    )
  }
  const tone = quotaTone(q.pct)
  return (
    <div className={`quotaCell ${tone}`.trim()}>
      <small>{label}</small>
      <b className="quotaValue">{formatPercent(q.pct)}</b>
      <i className="progressTrack">
        <em className={`progressFill ${tone}`.trim()} style={{width: `${Math.min(100, q.pct)}%`}} />
      </i>
      <span className="quotaMeta">
        {format(q.used)} / {format(q.limit)}
      </span>
    </div>
  )
}
