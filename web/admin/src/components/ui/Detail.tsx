import type {ReactNode} from 'react'
import {fmt, orUnknown} from '../../lib/format'
import {quotaState, quotaText} from '../../lib/quota'

/** Grouped key/value block used by every detail drawer. */
export function Detail({title, rows}: {title: string; rows: [string, ReactNode][]}) {
  return (
    <section className="detailBox">
      <h3>{title}</h3>
      {rows.map(([k, v], i) => (
        <div className="detailRow" key={`${k}-${i}`}>
          <span className="detailKey">{k}</span>
          <b className={`detailValue ${typeof v === 'string' && v.length > 34 ? 'mono wrap' : ''}`.trim()}>
            {v === null || v === undefined ? <span className="unknown">未知</span> : v}
          </b>
        </div>
      ))}
    </section>
  )
}

/**
 * Detail row for telemetry values that may legitimately be missing. Zero and
 * false are real values; only an absent value reads as 未知.
 */
export function TelemetryRow(label: string, value: unknown): [string, ReactNode] {
  return [label, <span key={label} className={value === undefined || value === null || value === '' ? 'unknown' : undefined}>{orUnknown(value)}</span>]
}

/**
 * Usage against a tenant limit.
 *
 * The denominator goes through `quotaText` because a configured 0 (无限制) and
 * an absent limit (未设置) are different promises. Only a configured, non-zero
 * limit gets a proportion: dividing by zero would draw a bar that asserts an
 * enforcement the gateway does not apply.
 *
 * The bar's width stops at 100% because that is all the geometry can show, so
 * the text carries the two facts the width cannot: usage above the limit, and
 * how far above it. A window with no usage says so rather than drawing an
 * empty bar, which would look like a measurement of zero rather than an
 * absence of activity.
 */
export function Progress({label, used, limit}: {label: string; used: number; limit?: number | null}) {
  const state = quotaState(limit)
  const limited = state === 'set'
  const limitValue = limited ? (limit as number) : 0
  const pct = limited ? (used / limitValue) * 100 : 0
  const over = limited && used > limitValue
  const tone = over || pct >= 90 ? 'danger' : pct >= 70 ? 'warning' : 'primary'
  // Rounding a real but tiny share to 0% would read as "nothing was used" while
  // the numerator beside it says otherwise. Keep the direction of the fact.
  const share = pct > 0 && pct < 0.1 ? '<0.1%' : `${pct.toFixed(0)}%`
  return (
    <div className="progress">
      <div className="progressHead">
        <span>{label}</span>
        <b>
          {fmt(used)} / {quotaText(limit)}
        </b>
      </div>
      <i className="progressTrack">
        <em className={`progressFill ${limited ? tone : 'muted'}`} style={{width: `${Math.min(100, pct)}%`}} />
      </i>
      <p className="progressNote">
        {over
          ? `已超出 ${fmt(used - limitValue)}（${share}）`
          : limited
            ? used === 0
              ? '本周期暂无使用'
              : `已用 ${share}`
            : state === 'unlimited'
              ? '无限制，不计算占比'
              : '未设置上限，不计算占比'}
      </p>
    </div>
  )
}
