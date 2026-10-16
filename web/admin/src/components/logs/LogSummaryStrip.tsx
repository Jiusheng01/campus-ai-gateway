import type {RequestLogSummary} from '../../api'
import {fmt, ms, num, ratio} from '../../lib/format'

/**
 * The aggregate for the current filter, shown directly above the table.
 *
 * Every figure is computed over the whole filtered set, not over the page below
 * it, and the labels say so — "本页" never appears, because a number that
 * silently meant "on this page" is the one reading an operator would never
 * intend when they narrow a filter to investigate an incident.
 */
export function LogSummaryStrip({summary, loading}: {summary: RequestLogSummary | null; loading: boolean}) {
  if (loading && !summary) {
    return (
      <div className="summaryStrip">
        {['请求数', '错误数', '错误率', 'Token 总量', '平均延迟', 'P95 延迟', '故障切换'].map(k => (
          <div className="summaryCell" key={k}>
            <small>{k}</small>
            <b className="muted">—</b>
          </div>
        ))}
      </div>
    )
  }
  if (!summary) return null

  const errorRate = ratio(summary.errors, summary.requests)
  return (
    <div className="summaryStrip">
      <div className="summaryCell">
        <small>请求数</small>
        <b>{num(summary.requests)}</b>
      </div>
      <div className={`summaryCell ${summary.errors > 0 ? 'danger' : ''}`.trim()}>
        <small>错误数</small>
        <b>{num(summary.errors)}</b>
      </div>
      <div className={`summaryCell ${summary.errors > 0 ? 'danger' : ''}`.trim()}>
        <small>错误率</small>
        <b>{errorRate}</b>
      </div>
      <div className="summaryCell">
        <small>Token 总量</small>
        <b>{fmt(summary.total_tokens)}</b>
      </div>
      <div className="summaryCell">
        <small>输入 / 输出 Token</small>
        <b>
          {fmt(summary.input_tokens)} / {fmt(summary.output_tokens)}
        </b>
      </div>
      <div className="summaryCell">
        <small>平均延迟</small>
        <b>{ms(summary.avg_latency_ms)}</b>
      </div>
      <div className="summaryCell">
        <small>P95 延迟</small>
        <b>{ms(summary.p95_latency_ms)}</b>
      </div>
      <div className={`summaryCell ${summary.failover_count > 0 ? 'warning' : ''}`.trim()}>
        <small>故障切换</small>
        <b>{num(summary.failover_count)}</b>
      </div>
    </div>
  )
}
