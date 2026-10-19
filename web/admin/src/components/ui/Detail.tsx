import type {ReactNode} from 'react'
import {fmt, orUnknown} from '../../lib/format'

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
 * Progress bar for a usage-against-limit pair.
 *
 * The limit has three states and they must not look alike: null is "inherited"
 * (the caller could not resolve a value), 0 is explicitly unlimited, and a
 * positive number is the cap. A bar drawn against 0 would read as "0% of
 * nothing", which is the opposite of unlimited.
 */
export function Progress({label, used, limit, limitText}: {label: string; used: number; limit: number | null | undefined; limitText?: string}) {
  const known = limit !== null && limit !== undefined
  const capped = known && limit > 0
  const pct = capped ? Math.min(100, (used / (limit as number)) * 100) : 0
  const tone = pct >= 90 ? 'danger' : pct >= 70 ? 'warning' : 'primary'
  const shown = !known ? (limitText ?? '继承') : limit === 0 ? '不限' : fmt(limit as number)
  return (
    <div className="progress">
      <div className="progressHead">
        <span>{label}</span>
        <b>
          {fmt(used)} / {shown}
        </b>
      </div>
      <i className="progressTrack">
        <em className={`progressFill ${capped ? tone : 'muted'}`} style={{width: `${pct}%`}} />
      </i>
    </div>
  )
}
