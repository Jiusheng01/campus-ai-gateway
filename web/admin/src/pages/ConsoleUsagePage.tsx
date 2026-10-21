import {useCallback, useState} from 'react'
import {Building2, CircleDollarSign, LineChart} from 'lucide-react'
import {fetchConsoleUsage, type QuotaDimension} from '../api'
import {PageBanner} from '../components/ui/PageBanner'
import {Panel} from '../components/ui/Panel'
import {Table} from '../components/ui/Table'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {StatCard, StatGrid} from '../components/ui/StatCard'
import {TrendChart} from '../components/charts/TrendChart'
import {num} from '../lib/format'
import {formatPercent, quotaTone} from '../lib/quota'
import {useResource} from '../lib/useResource'

/**
 * The three windows the page offers. Each states its own bucket width for the
 * same reason the dashboard does: a week of hourly points shows the daily
 * rhythm, while a month of hourly points is too thin to read and too much to
 * aggregate for one chart.
 */
const RANGES = [
  {id: '24h', label: '近 24 小时', hours: 24, bucket: 'hour' as const},
  {id: '7d', label: '近 7 天', hours: 24 * 7, bucket: 'hour' as const},
  {id: '30d', label: '近 30 天', hours: 24 * 30, bucket: 'day' as const},
]

type RangeId = (typeof RANGES)[number]['id']


/**
 * The personal usage page.
 *
 * Every figure comes from /api/console/usage, whose owner predicate is the
 * signed-in Account. The page never sends a Tenant or key id, and the tenant
 * quota beside it is an aggregate of the shared budget rather than a second
 * owner's view. There is no currency anywhere: the gateway records tokens, so
 * the page says cost is unavailable instead of dividing tokens by a guessed
 * price.
 */
export function ConsoleUsagePage() {
  const [rangeId, setRangeId] = useState<RangeId>('24h')
  const [hover, setHover] = useState<number | null>(null)
  const range = RANGES.find(r => r.id === rangeId) || RANGES[0]
  const bucketWord = range.bucket === 'day' ? '天' : '小时'

  const loader = useCallback(() => {
    // Anchored to "now" at load time, not at mount: a window captured once
    // would drift stale behind the reload button.
    const to = new Date()
    const from = new Date(to.getTime() - range.hours * 3600 * 1000)
    return fetchConsoleUsage(from, to, range.bucket)
  }, [range.hours, range.bucket])

  const {data, loading, error, errorStatus, reload} = useResource(loader, [rangeId])
  const totals = data?.totals
  const points = data?.series || []
  const models = data?.by_model || []
  const quota = data?.tenant_quota || null

  const pickRange = (id: RangeId) => {
    setHover(null) // an index into the old series means nothing in the new one
    setRangeId(id)
  }

  const periodPicker = (
    <div className="segmented" role="group" aria-label="时间范围">
      {RANGES.map(r => (
        <button
          key={r.id}
          type="button"
          className={`segmentedItem ${r.id === rangeId ? 'on' : ''}`.trim()}
          aria-pressed={r.id === rangeId}
          onClick={() => pickRange(r.id)}
        >
          {r.label}
        </button>
      ))}
    </div>
  )

  return (
    <div className="stack">
      <PageBanner
        title="用量"
        subtitle="你在所选时间段内的请求、成功与失败、输入与输出 Tokens，以及按模型分组的用量。"
        illustration={false}
      />

      {error ? (
        <Panel title="用量">
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        </Panel>
      ) : loading && !data ? (
        <Panel title="用量">
          <SkeletonRows rows={5} columns={4} />
        </Panel>
      ) : data && totals ? (
        <>
          <Panel
            title="用量概览"
            sub={`所选时间段内的个人用量。时间边界按本机时区计算，无流量${bucketWord}按 0 补齐。`}
            actions={periodPicker}
          >
            <div className="panelPad">
              <StatGrid columns={4}>
                <StatCard label="请求数" value={num(totals.requests)} tone="info" />
                <StatCard label="成功" value={num(totals.successes)} tone="success" />
                <StatCard
                  label="失败"
                  value={num(totals.failures)}
                  tone={totals.failures > 0 ? 'danger' : 'default'}
                />
                <StatCard label="错误率" value={formatPercent(totals.error_rate)} />
                <StatCard label="输入 Token" value={num(totals.input_tokens)} />
                <StatCard label="输出 Token" value={num(totals.output_tokens)} />
                <StatCard label="总 Token" value={num(totals.total_tokens)} tone="purple" />
              </StatGrid>
            </div>
          </Panel>

          <Panel title="调用趋势" sub={`按${bucketWord}聚合的请求数与 Token 用量。`}>
            <div className="chartStack">
              <div className="chartBlock">
                <div className="chartBlockHead">
                  <b>请求数</b>
                  <small>红色段为失败请求</small>
                </div>
                <TrendChart
                  points={points}
                  value={p => p.requests}
                  format={num}
                  metric="请求数"
                  withErrors
                  hover={hover}
                  onHover={setHover}
                />
              </div>
              <div className="chartBlock">
                <div className="chartBlockHead">
                  <b>Token 用量</b>
                  <small>输入与输出合计</small>
                </div>
                <TrendChart
                  points={points}
                  value={p => p.tokens}
                  format={num}
                  metric="Token 用量"
                  hover={hover}
                  onHover={setHover}
                />
              </div>
            </div>
          </Panel>

          <Panel title="按模型" sub="同一个人用量按你请求的模型别名分组。">
            {models.length ? (
              <Table heads={['模型', '请求数', '输入 Token', '输出 Token', '总 Token']} minWidth={620}>
                {models.map(m => (
                  <tr key={m.alias || 'unknown'}>
                    <td>{m.alias ? <code className="mono">{m.alias}</code> : '—'}</td>
                    <td className="numeric">{num(m.requests)}</td>
                    <td className="numeric">{num(m.input_tokens)}</td>
                    <td className="numeric">{num(m.output_tokens)}</td>
                    <td className="numeric">{num(m.total_tokens)}</td>
                  </tr>
                ))}
              </Table>
            ) : (
              <EmptyState
                icon={<LineChart />}
                title="所选时间段内没有可归因的请求"
                description="换一个时间段，或确认你的应用已经开始使用个人 API Key 发起请求。"
              />
            )}
          </Panel>

          <Panel
            title="租户共享配额"
            sub="配额属于整个租户，是成员共同消耗的共享预算；这里只展示汇总，不包含其他成员的明细。"
          >
            {quota ? (
              <div className="panelPad">
                <p className="hint">
                  所属租户：<b>{quota.tenant_name || quota.tenant_id}</b>。以下为只读汇总，每日与每月用量由网关按租户统计。
                </p>
                <div className="quotaStrip">
                  <QuotaBand label="每日 Token 配额" q={quota.daily_token} />
                  <QuotaBand label="每月 Token 配额" q={quota.monthly_token} />
                  <QuotaBand label="每月请求配额" q={quota.monthly_request} />
                </div>
              </div>
            ) : (
              <EmptyState
                icon={<Building2 />}
                title="尚未分配租户"
                description="你的账号还没有加入任何租户，因此没有可展示的共享配额。请联系管理员在「用户」中为你的账号分配租户。"
              />
            )}
          </Panel>

          <Panel title="费用">
            <div className="panelPad">
              <div className="accountNotice">
                <CircleDollarSign aria-hidden />
                <div>
                  <b>费用暂不可用。</b>
                  <p>
                    网关只记录请求数与 Token 用量，不记录价格，因此本页不提供金额，也不会用 Token 推算货币费用。
                    如需核算成本，请以所用模型的实际计费为准。
                  </p>
                </div>
              </div>
            </div>
          </Panel>
        </>
      ) : null}
    </div>
  )
}

/**
 * One shared-quota band.
 *
 * A band with no configured limit renders as 未设, never 0%: the two are
 * opposite facts, and a member who reads 0% concludes the budget is untouched.
 */
function QuotaBand({label, q}: {label: string; q: QuotaDimension | null}) {
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
        {num(q.used)} / {num(q.limit)}
      </span>
    </div>
  )
}
