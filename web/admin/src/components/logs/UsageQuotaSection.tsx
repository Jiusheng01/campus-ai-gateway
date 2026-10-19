import type {QuotaDimension, RequestLogUsage} from '../../api'
import {fmt, num} from '../../lib/format'
import {Badge} from '../ui/Badge'
import {Table} from '../ui/Table'
import {LogSummaryStrip} from './LogSummaryStrip'

/** Percent with a fixed shape, so two bands can be compared at a glance. */
function pct(v: number): string {
  return `${v.toFixed(2)}%`
}

/** Traffic-light tone for a share that is on its way to the limit. */
function tone(v: number): string {
  return v >= 90 ? 'danger' : v >= 70 ? 'warning' : ''
}

/**
 * One quota band.
 *
 * A band with no limit renders as 未设, never as 0%: the two are opposite facts,
 * and an operator who reads 0% concludes the quota is untouched when there is in
 * fact no quota to be touched.
 */
function QuotaCell({label, q}: {label: string; q: QuotaDimension | null}) {
  if (!q) {
    return (
      <div className="quotaCell">
        <small>{label}</small>
        <b className="quotaValue muted">未设</b>
        <span className="quotaMeta">未配置限额</span>
      </div>
    )
  }
  return (
    <div className={`quotaCell ${tone(q.pct)}`.trim()}>
      <small>{label}</small>
      <b className="quotaValue">{pct(q.pct)}</b>
      <i className="progressTrack">
        <em className={`progressFill ${tone(q.pct)}`.trim()} style={{width: `${Math.min(100, q.pct)}%`}} />
      </i>
      <span className="quotaMeta">
        {fmt(q.used)} / {fmt(q.limit)}
      </span>
    </div>
  )
}

/** Placeholder cells for the first load, so an unset quota is not shown before the answer arrives. */
function QuotaSkeleton() {
  return (
    <div className="quotaStrip">
      {['每日 Token 配额', '每月 Token 配额', '每月请求配额'].map(label => (
        <div className="quotaCell" key={label}>
          <small>{label}</small>
          <b className="quotaValue muted">—</b>
          <span className="quotaMeta">加载中</span>
        </div>
      ))}
    </div>
  )
}

/**
 * The "用量与配额" section at the top of the request-log page.
 *
 * It answers three questions about one filter selection, all from the same
 * request over the same predicate: how much traffic the filter covers, how much
 * of the tenant's quota that is, and which API keys are consuming it. Clicking a
 * key row drills the whole page into that key's requests.
 *
 * Nothing here is a second source of truth: the totals are the summary's own,
 * and the quota and per-key figures come from the usage endpoint that takes the
 * table's query string verbatim.
 */
export function UsageQuotaSection({
  usage,
  loading,
  activeKeyID,
  onSelectKey,
}: {
  usage: RequestLogUsage | null
  loading: boolean
  activeKeyID: string
  onSelectKey: (apiKeyID: string) => void
}) {
  // With no data and no load in flight the fetch failed; the page renders the
  // error below this section, and an invented "未设" here would only confuse it.
  if (!usage && !loading) return null

  const quota = usage?.totals.quota
  const keys = usage?.by_key || []
  const firstLoad = loading && !usage

  return (
    <section className="usageSection">
      <div className="usageHead">
        <h3>用量与配额</h3>
        <span>
          与下方明细同一口径，覆盖当前筛选的全部请求而非本页；配额按筛选所属的单一租户计算，未配置限额或筛选跨多个租户时显示「未设」。
        </span>
      </div>

      <LogSummaryStrip summary={usage?.totals || null} loading={loading} />

      {firstLoad ? (
        <QuotaSkeleton />
      ) : (
        <div className="quotaStrip">
          <QuotaCell label="每日 Token 配额" q={quota?.daily_token || null} />
          <QuotaCell label="每月 Token 配额" q={quota?.monthly_token || null} />
          <QuotaCell label="每月请求配额" q={quota?.monthly_request || null} />
        </div>
      )}

      <div className="usageKeysHead">
        <h4>按 API Key</h4>
        <span>占比 = 该密钥 Token / 所属租户月 Token 限额（未配置时显示未设）；点任意一行下钻到它的请求明细。</span>
      </div>

      {keys.length ? (
        <Table heads={['API 密钥', '请求数', 'Tokens', '配额占比']} minWidth={640}>
          {keys.map(k => {
            const clickable = k.api_key_id !== ''
            const rowClass = [
              clickable ? 'clickRow' : '',
              clickable && k.api_key_id === activeKeyID ? 'drilledRow' : '',
            ]
              .filter(Boolean)
              .join(' ')
            return (
              <tr
                key={k.api_key_id || 'no-key'}
                className={rowClass || undefined}
                title={clickable ? '下钻到该密钥的请求明细' : '这条记录没有关联 API 密钥'}
                onClick={() => clickable && onSelectKey(k.api_key_id)}
              >
                <td>
                  <span className="usageKeyCell">
                    <b>{k.name || (clickable ? '未命名密钥' : '未关联密钥')}</b>
                    {k.key_prefix && <span className="mono muted">{k.key_prefix}</span>}
                    {clickable && k.api_key_id === activeKeyID && <Badge tone="info">当前筛选</Badge>}
                  </span>
                </td>
                <td className="numeric">{num(k.requests)}</td>
                <td className="numeric">{fmt(k.tokens)}</td>
                <td className="numeric">
                  {k.quota_share == null ? <span className="muted">未设</span> : pct(k.quota_share)}
                </td>
              </tr>
            )
          })}
        </Table>
      ) : (
        <p className="usageEmpty">
          {firstLoad ? '正在汇总按密钥用量…' : '当前筛选条件下没有可归因到 API Key 的用量。'}
        </p>
      )}
    </section>
  )
}
