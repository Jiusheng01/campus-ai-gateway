import type {TrendBucket} from '../../api'
import {fmt, ms, num} from '../../lib/format'

/**
 * One bar chart over a traffic series.
 *
 * Dependency-free and CSS-only on purpose: the console ships three dependencies
 * and adding a charting library for one panel would cost more than the panel is
 * worth, and an SVG canvas would have to be told the theme's colours a second
 * time. Bars are flex children, so they inherit every colour variable the rest
 * of the console already defines and follow the theme without being told.
 */
export type TrendChartProps = {
  points: TrendBucket[]
  /** Which field the bars represent. */
  value: (p: TrendBucket) => number
  /** Formats one bar's value for the readout. */
  format: (n: number) => string
  /** Short name of the metric, used in the readout and the aria label. */
  metric: string
  /** Draws the error count as a segment at the foot of each bar. */
  withErrors?: boolean
  /**
   * The bucket under the pointer, owned by the panel rather than by this chart.
   *
   * The three charts share one x axis, so the point worth highlighting is the
   * same in all of them; each keeping its own state would mean hovering the
   * requests chart said nothing about the latency at that same moment, which is
   * the comparison the panel exists to support.
   */
  hover: number | null
  onHover: (index: number | null) => void
}

/**
 * The readout is a fixed row above the chart rather than a floating tooltip.
 *
 * A tooltip would have to be positioned over a horizontally scrollable plot, so
 * it would clip at both ends and cover the bars nearest the pointer. A readout
 * line has neither problem, is readable at any width, and is announced by a
 * screen reader without any extra work.
 */
export function TrendChart({
  points,
  value,
  format,
  metric,
  withErrors = false,
  hover,
  onHover,
}: TrendChartProps) {
  if (!points.length) {
    return <p className="muted chartEmpty">该时间范围内没有数据。</p>
  }

  // A flat-zero series must still render a baseline rather than divide by zero.
  const peak = Math.max(1, ...points.map(value))
  const shown = hover === null ? null : points[hover]

  const totals = points.reduce(
    (acc, p) => {
      acc.requests += p.requests
      acc.tokens += p.tokens
      acc.errors += p.errors
      acc.latency += p.p95_latency_ms
      return acc
    },
    {requests: 0, tokens: 0, errors: 0, latency: 0},
  )

  return (
    <div className="chart">
      <div className="chartReadout" role="status">
        <b className="chartReadoutWhen">{shown ? shown.bucket : '区间合计'}</b>
        <span>
          请求 <b>{num(shown ? shown.requests : totals.requests)}</b>
        </span>
        <span className={shown && shown.errors > 0 ? 'danger' : undefined}>
          错误 <b>{num(shown ? shown.errors : totals.errors)}</b>
        </span>
        <span>
          Token <b>{num(shown ? shown.tokens : totals.tokens)}</b>
        </span>
        <span>
          P95 <b>{ms(shown ? shown.p95_latency_ms : totals.latency / points.length)}</b>
        </span>
      </div>

      <div className="chartScroll">
        <div className="chartPlot" role="img" aria-label={`按时间分组的${metric}`}>
          {points.map((p, i) => {
            const v = value(p)
            const height = v > 0 ? Math.max(1.5, (v / peak) * 100) : 0
            // The error segment is a share of the bar, so it is only meaningful
            // when the bar counts requests. Sizing it against tokens would
            // draw a proportion that corresponds to nothing.
            const errShare =
              withErrors && v > 0 && p.errors > 0 ? Math.min(100, (p.errors / v) * 100) : 0
            return (
              <div
                key={p.bucket}
                className={`chartCol ${hover === i ? 'on' : ''}`.trim()}
                onMouseEnter={() => onHover(i)}
                onMouseLeave={() => onHover(null)}
                title={`${p.bucket} · ${metric} ${format(v)}`}
              >
                <span className="chartTrack" aria-hidden />
                <span className="chartBar" style={{height: `${height}%`}} aria-hidden>
                  {errShare > 0 && <i className="chartBarError" style={{height: `${errShare}%`}} />}
                </span>
              </div>
            )
          })}
        </div>
      </div>

      <div className="chartAxis">
        <span>{points[0]?.bucket}</span>
        {points.length > 2 && <span>{points[Math.floor(points.length / 2)]?.bucket}</span>}
        <span>{points[points.length - 1]?.bucket}</span>
      </div>
      <div className="chartPeak">
        峰值 {format(peak)} · {points.length} 个时间点
      </div>
    </div>
  )
}

/** Formats a bar count. */
export const countFormat = (n: number) => fmt(n)
