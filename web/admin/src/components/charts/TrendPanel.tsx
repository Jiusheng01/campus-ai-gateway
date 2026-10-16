import {useCallback, useState} from 'react'
import {TrendChart} from './TrendChart'
import {fetchTrend} from '../../api'
import {Panel} from '../ui/Panel'
import {ErrorState} from '../ui/EmptyState'
import {Skeleton} from '../ui/Skeleton'
import {useResource} from '../../lib/useResource'
import {fmt, ms, num} from '../../lib/format'

/**
 * The three windows the dashboard offers.
 *
 * Each names its own bucket width rather than deriving one from the span: a
 * week of hourly points shows the daily rhythm, while a month of hourly points
 * would be 720 bars too thin to read and too much to aggregate cheaply. Thirty
 * days is therefore daily, and the label says so.
 */
const RANGES = [
  {id: '24h', label: '近 24 小时', hours: 24, bucket: 'hour' as const},
  {id: '7d', label: '近 7 天', hours: 24 * 7, bucket: 'hour' as const},
  {id: '30d', label: '近 30 天', hours: 24 * 30, bucket: 'day' as const},
]

type RangeId = (typeof RANGES)[number]['id']

/**
 * Traffic over time, one chart per metric.
 *
 * Three separate charts rather than one with three series: requests, tokens and
 * latency differ by orders of magnitude, and a shared axis would flatten two of
 * them into the baseline. Each keeps its own scale while the x axis is shared,
 * so the shapes can still be compared point by point.
 *
 * This is the one part of the dashboard that is not scoped to today, so it
 * carries its own range picker. The KPI cards above stay fixed on today, which
 * is what "今日" in their labels promises.
 */
export function TrendPanel() {
  const [rangeId, setRangeId] = useState<RangeId>('24h')
  const [hover, setHover] = useState<number | null>(null)
  const range = RANGES.find(r => r.id === rangeId) || RANGES[0]
  const bucketWord = range.bucket === 'day' ? '天' : '小时'

  const loader = useCallback(() => {
    // The window is computed at load time, not captured at mount: a range
    // anchored to "now" once would drift stale behind a refresh button that
    // appears to do nothing.
    const to = new Date()
    const from = new Date(to.getTime() - range.hours * 3600 * 1000)
    return fetchTrend(from, to, range.bucket)
  }, [range.hours, range.bucket])

  const {data, loading, error, errorStatus, reload} = useResource(loader, [rangeId])
  const points = data?.data || []

  // The highlighted bucket is owned here, not by each chart, so the three
  // charts mark the same instant: the point of stacking them on a shared axis
  // is to read the requests, the tokens and the latency of one moment together.
  const pickRange = (id: RangeId) => {
    setHover(null) // an index into the old series means nothing in the new one
    setRangeId(id)
  }

  return (
    <Panel
      title="流量趋势"
      sub={`按${bucketWord}聚合的请求量、Token 与延迟。时间边界按本机时区计算，无流量${bucketWord}按 0 补齐。`}
      actions={
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
      }
    >
      {error ? (
        <ErrorState message={error} status={errorStatus} onRetry={reload} />
      ) : loading && !data ? (
        <div className="chartStack">
          {[0, 1, 2].map(i => (
            <div className="chartBlock" key={i}>
              <Skeleton width="30%" height={12} />
              <Skeleton width="100%" height={120} />
            </div>
          ))}
        </div>
      ) : (
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
              format={fmt}
              metric="Token 用量"
              hover={hover}
              onHover={setHover}
            />
          </div>
          <div className="chartBlock">
            <div className="chartBlockHead">
              <b>P95 延迟</b>
              <small>慢尾响应，取每个{bucketWord}的 95 分位</small>
            </div>
            <TrendChart
              points={points}
              value={p => p.p95_latency_ms}
              format={ms}
              metric="P95 延迟"
              hover={hover}
              onHover={setHover}
            />
          </div>
        </div>
      )}
    </Panel>
  )
}
